// Package render turns index results into the text an agent reads.
//
// It is shared by the CLI and the MCP server so the two cannot drift. Every function here
// is budget-aware and states its own omissions: a retrieval tool that silently truncates
// sends the caller straight back to Read, which is the cost this whole tool exists to
// avoid.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/index"
	"github.com/dpalfery/terragraph/internal/overlay"
)

// refCap bounds how many references are listed for one node. A variable with two hundred
// consumers should say so, not print them.
const refCap = 40

// budget bounds an answer's size and remembers what it had to drop.
//
// Every tool needs one, not just retrieval. A benchmark against a 29-stack repository found
// terra_for_address returning 3,814 tokens where grep cost 1,438: `var.remote_state_bucket`
// is declared once per stack, and printing every declaration with every reference produced
// an answer more expensive than the thing it replaced. A tool whose whole purpose is to
// spend fewer tokens than grep must be bounded by construction, not by the shape of the
// repository it happens to be pointed at.
type budget struct {
	limit   int
	spent   int
	dropped int
}

func newBudget(limit int) *budget {
	if limit <= 0 {
		limit = index.DefaultCharBudget
	}
	return &budget{limit: limit}
}

// allow reserves room for a chunk, or records that it was dropped.
func (bd *budget) allow(cost int) bool {
	if bd.spent+cost > bd.limit {
		bd.dropped++
		return false
	}
	bd.spent += cost
	return true
}

// exhausted is true once anything has been dropped, so an emitter can stop scanning.
func (bd *budget) exhausted() bool { return bd.dropped > 0 }

// note writes the standard "what was left out" line. Naming the omission is what lets a
// caller ask again deliberately instead of assuming it saw everything.
func (bd *budget) note(b *strings.Builder, unit string) {
	if bd.dropped == 0 {
		return
	}
	fmt.Fprintf(b, "\n[%d %s omitted for space — ask again with a larger charBudget]\n",
		bd.dropped, unit)
}

// Explore renders ranked retrieval, or an explicit miss.
func Explore(ix *index.Index, query string, maxNodes, charBudget int) string {
	res := ix.Explore(query, maxNodes, charBudget)
	hits := res.Hits

	if len(hits) == 0 {
		// Saying so plainly is the whole point of the relevance floor. A caller told to
		// try this before grepping needs an unambiguous signal that it may now grep.
		return fmt.Sprintf(`No configuration scored above the relevance threshold for %q.
%d nodes were considered across %d root modules.

This is a real miss, not an empty index. Either the subject is not in this repository's
Terraform, or the question uses vocabulary the configuration does not. Try naming a
resource type (aws_s3_bucket), an address (module.vpc), a variable (var.environment), or
a module source — or fall back to grep.`,
			query, ix.NodeCount(), len(ix.Graph().Roots))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Top %d of %d nodes, best first (relevance %.2f … %.2f).\n",
		len(hits), res.Considered, hits[0].Score, hits[len(hits)-1].Score)

	// Distinguishing "the budget cut it" from "nothing else was relevant" is what tells
	// the caller whether asking again would gain anything.
	if res.DroppedForBudget > 0 {
		fmt.Fprintf(&b, "%d more node(s) cleared the relevance threshold but did not fit the "+
			"character budget — ask again with a larger charBudget to see them.\n",
			res.DroppedForBudget)
	} else if res.AboveThreshold > len(hits) {
		fmt.Fprintf(&b, "%d more node(s) cleared the threshold; raise maxNodes to see them.\n",
			res.AboveThreshold-len(hits))
	}

	for _, h := range hits {
		b.WriteString("\n")
		writeIdentity(&b, h.Node)
		fmt.Fprintf(&b, "referenced by: %d   references: %d\n", h.RefByCount, h.RefCount)
		writeInstances(&b, h.Instances)
		writeExcerpt(&b, h.Node, h.Excerpt)
	}
	return b.String()
}

// writeInstances reports what the overlay resolved. A block with for_each is one node and
// any number of instances, and an agent reasoning about cost, blast radius or naming needs
// the second number — which static HCL cannot supply at all.
func writeInstances(b *strings.Builder, instances []index.NodeInstance) {
	if len(instances) == 0 {
		return
	}

	byStack := map[string][]index.NodeInstance{}
	var order []string
	for _, i := range instances {
		if _, seen := byStack[i.Stack]; !seen {
			order = append(order, i.Stack)
		}
		byStack[i.Stack] = append(byStack[i.Stack], i)
	}

	for _, stack := range order {
		group := byStack[stack]
		name := stack
		if name == "" {
			name = "."
		}
		fmt.Fprintf(b, "instances in %s: %d", name, len(group))

		var actions []string
		for _, i := range group {
			if s := i.ActionSummary(); s != "" && s != "no-op" {
				actions = append(actions, s)
			}
		}
		if len(actions) > 0 {
			fmt.Fprintf(b, "   planned: %s", strings.Join(dedupe(actions), ", "))
		}
		b.WriteString("\n")

		// Full addresses, not bare keys. A block inside an expanded module has repeating
		// keys — `0, 1, 0, 1` for two instances of a module with count = 2 — which is
		// both useless and actively misleading. The full address is what distinguishes
		// them, and it is the string a caller pastes into `terraform state show`.
		var addrs []string
		for _, i := range group {
			if i.Address != "" {
				addrs = append(addrs, i.Address)
			}
		}
		if len(addrs) > 0 {
			shown := addrs
			suffix := ""
			if len(shown) > 8 {
				shown, suffix = shown[:8], fmt.Sprintf("\n  … %d more", len(addrs)-8)
			}
			fmt.Fprintf(b, "  %s%s\n", strings.Join(shown, "\n  "), suffix)
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// ForAddress renders the reverse lookup, bounded by charBudget.
func ForAddress(ix *index.Index, address string, charBudget int) string {
	res := ix.ForAddress(address)

	if len(res.Declarations) == 0 {
		return fmt.Sprintf(`Nothing in this repository declares %q.
%d nodes were considered.

The address may belong to a remote module whose internals are not indexed, or it may not
exist. Try the bare local name, or the resource type.`, address, ix.NodeCount())
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d declaration(s) of %q.\n", len(res.Declarations), address)
	b.WriteString("Only resolved expression references are listed. A mention in a comment or " +
		"inside a quoted string is not a reference and is deliberately absent.\n")

	// The budget is split across declarations rather than spent first-come. A variable
	// declared once per stack in a 29-stack repository would otherwise render the first
	// three in full and silently drop the other twenty-six.
	bd := newBudget(charBudget)
	perDeclaration := bd.limit / len(res.Declarations)
	if perDeclaration < 400 {
		perDeclaration = 400
	}

	for _, n := range res.Declarations {
		var d strings.Builder
		writeIdentity(&d, n)
		writeRefsBounded(&d, "referenced by", res.ReferencedBy[n.Key()], true, perDeclaration)
		writeRefsBounded(&d, "references", res.References[n.Key()], false, perDeclaration)

		if !bd.allow(d.Len() + 1) {
			continue
		}
		b.WriteString("\n")
		b.WriteString(d.String())
	}

	bd.note(&b, "declaration(s)")
	return b.String()
}

// Impact renders the reachability set, bounded, and explicit about what it is not.
func Impact(ix *index.Index, address string, dir index.Direction, depth, charBudget int) string {
	res := ix.Impact(address, dir, depth)

	if len(res.Origin) == 0 {
		return fmt.Sprintf("Nothing in this repository declares %q, so it has no impact set.", address)
	}

	var b strings.Builder
	word := "affected by changing"
	if dir == index.Dependencies {
		word = "required by"
	}

	fmt.Fprintf(&b, "%d node(s) %s %s (depth %d).\n", len(res.Nodes), word, address, depth)
	for _, o := range res.Origin {
		fmt.Fprintf(&b, "  origin: %s  %s\n", o.Address, o.Location())
	}

	if len(res.Nodes) == 0 {
		b.WriteString("\nNothing reaches it. For a variable or output this usually means it is dead " +
			"configuration; check terra_orphans.\n")
		return b.String()
	}

	byDepth := map[int][]index.ImpactNode{}
	for _, n := range res.Nodes {
		byDepth[n.Depth] = append(byDepth[n.Depth], n)
	}
	depths := make([]int, 0, len(byDepth))
	for d := range byDepth {
		depths = append(depths, d)
	}
	sort.Ints(depths)

	// Reserve room for the caveat and the replacement list, which are the parts a caller
	// most needs. Spending the whole budget on a flat node listing and then truncating the
	// "what gets destroyed" section would be the worst possible thing to drop.
	bd := newBudget(charBudget)
	bd.limit = bd.limit * 3 / 4

	for _, d := range depths {
		if bd.exhausted() {
			break
		}
		header := fmt.Sprintf("\ndepth %d:\n", d)
		if !bd.allow(len(header)) {
			break
		}
		b.WriteString(header)

		for _, n := range byDepth[d] {
			var line strings.Builder
			fmt.Fprintf(&line, "  %-44s %s", n.Node.Address, n.Node.Location())
			if n.Via != nil && n.Via.Kind != graph.EdgeReferences {
				fmt.Fprintf(&line, "  [%s]", n.Via.Kind)
			}
			// Marked inline as well as summarised below, because a reader scanning the
			// list should not have to cross-reference to find the destructive entries.
			if n.Replaces() {
				line.WriteString("  ** REPLACED **")
			} else if c := len(n.Instances); c > 1 {
				fmt.Fprintf(&line, "  (%d instances)", c)
			}
			line.WriteString("\n")

			if !bd.allow(line.Len()) {
				continue
			}
			b.WriteString(line.String())
		}
	}
	bd.note(&b, "node(s)")

	writeImpactCaveat(&b, res)

	if res.Truncated {
		fmt.Fprintf(&b, "\nTruncated at depth %d — there is more beyond it. Ask again with a larger depth.\n", depth)
	}
	return b.String()
}

// writeImpactCaveat states exactly how much this answer knows.
//
// The three cases are genuinely different and collapsing them would be dishonest in one
// direction or the other. Without an overlay the set is connectivity and a lower bound. A
// state overlay makes the instance count real but still cannot see a future change. Only a
// plan can say what gets replaced — and when it can, it should say so plainly instead of
// repeating a disclaimer it has outgrown.
func writeImpactCaveat(b *strings.Builder, res index.ImpactResult) {
	switch {
	case res.OverlayKind == overlay.KindPlan:
		fmt.Fprintf(b, "\nResolved against a plan: %d real instance(s) across this set.\n",
			res.InstanceTotal)
		if len(res.Replacing) > 0 {
			fmt.Fprintf(b, "\nDESTROYED AND RECREATED by this plan (%d):\n", len(res.Replacing))
			for _, a := range res.Replacing {
				fmt.Fprintf(b, "  %s\n", a)
			}
		} else {
			b.WriteString("Nothing in this set is replaced by the current plan; " +
				"changes are in-place updates.\n")
		}
		b.WriteString("\nThe plan is a snapshot. Re-plan after editing configuration or this " +
			"answer goes stale.\n")

	case res.OverlayKind == overlay.KindState:
		fmt.Fprintf(b, "\nResolved against state: %d real instance(s) across this set.\n",
			res.InstanceTotal)
		b.WriteString("State knows what exists, not what a change would do. For " +
			"replace-vs-update, supply a\nplan file rather than a state file.\n")

	default:
		b.WriteString("\nThis is what is *connected*, not what a plan would replace. Static " +
			"configuration knows\nwhich expressions read which addresses; only `terraform plan` " +
			"knows which changes force\nreplacement. Supply one with --plan to resolve this.\n")

		if len(res.ExpandingBlocks) > 0 {
			fmt.Fprintf(b, "\nLower bound: %d block(s) in this set use count or for_each, so the real\n"+
				"instance count is higher than the node count: %s\n",
				len(res.ExpandingBlocks), strings.Join(res.ExpandingBlocks, ", "))
		}
	}
}

// Modules renders the module inventory, skew first, bounded by charBudget.
func Modules(ix *index.Index, filter string, charBudget int) string {
	usages := ix.Modules(filter)

	if len(usages) == 0 {
		if filter == "" {
			return "This repository contains no module calls."
		}
		return fmt.Sprintf("No module source or call name matches %q.", filter)
	}

	var b strings.Builder
	skewed := 0
	for _, u := range usages {
		if len(u.Versions) > 1 {
			skewed++
		}
	}
	fmt.Fprintf(&b, "%d module source(s); %d pinned at more than one version.\n", len(usages), skewed)

	// Sources are already ordered skew-first, so a budget that runs out drops the healthy
	// ones — which is the right thing to lose.
	bd := newBudget(charBudget)

	for _, u := range usages {
		var s strings.Builder
		s.WriteString("\n")
		fmt.Fprintf(&s, "%s", u.Source)
		if u.Local {
			s.WriteString("  (local path — no version to skew)")
		} else if len(u.Versions) > 1 {
			s.WriteString("  ← VERSION SKEW")
		}
		s.WriteString("\n")

		versions := make([]string, 0, len(u.Versions))
		for v := range u.Versions {
			versions = append(versions, v)
		}
		sort.Strings(versions)

		for _, v := range versions {
			sites := u.Versions[v]
			fmt.Fprintf(&s, "  %-12s %d call site(s)\n", v, len(sites))

			// Call sites repeat heavily in a large repository. The count above is the
			// answer; the individual sites are detail, so only the first few are named.
			shown := sites
			if len(shown) > 4 {
				shown = shown[:4]
			}
			for _, site := range shown {
				// The calling directory is the answer to "which stack", which is what the
				// question is actually about — the call's own address repeats across them.
				stack := site.Call.Stack
				if stack == "" {
					stack = site.Call.ModuleDir
				}
				fmt.Fprintf(&s, "    %-24s in %-18s %s\n", site.Call.Address, stack, site.Call.Location())
			}
			if len(sites) > len(shown) {
				fmt.Fprintf(&s, "    … %d more call site(s)\n", len(sites)-len(shown))
			}
		}

		if !bd.allow(s.Len()) {
			continue
		}
		b.WriteString(s.String())
	}

	bd.note(&b, "module source(s)")
	return b.String()
}

// Orphans renders dead configuration, bounded by charBudget.
func Orphans(ix *index.Index, charBudget int) string {
	orphans := ix.Orphans()
	if len(orphans) == 0 {
		return "No unreferenced variables, locals or child-module outputs. " +
			"Root module outputs are excluded by design — they are the stack's public surface."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d unreferenced declaration(s).\n", len(orphans))
	b.WriteString("A caller passing a value does not count as using it: a variable every stack wires\n" +
		"up and nothing inside ever reads is dead configuration with live call sites.\n")

	bd := newBudget(charBudget)
	current := "\x00"

	for _, o := range orphans {
		var s strings.Builder
		if o.Node.ModuleDir != current {
			dir := o.Node.ModuleDir
			if dir == "" {
				dir = "."
			}
			fmt.Fprintf(&s, "\n%s\n", dir)
		}
		fmt.Fprintf(&s, "  %-34s %-22s %s\n", o.Node.Address, o.Node.Location(), o.Reason)

		if !bd.allow(s.Len()) {
			continue
		}
		current = o.Node.ModuleDir
		b.WriteString(s.String())
	}

	bd.note(&b, "orphan(s)")
	return b.String()
}

// Status describes the snapshot, so a caller can decide how far to trust the other tools.
func Status(ix *index.Index, repoRoot string, builds int) string {
	g := ix.Graph()

	var b strings.Builder
	fmt.Fprintf(&b, "repo:      %s\n", repoRoot)
	fmt.Fprintf(&b, "nodes:     %d\n", g.NodeCount())
	fmt.Fprintf(&b, "edges:     %d\n", g.EdgeCount())
	fmt.Fprintf(&b, "roots:     %d\n", len(g.Roots))
	fmt.Fprintf(&b, "snapshots: %d (rebuilt on any .tf change)\n", builds)

	b.WriteString("\nroot modules:\n")
	for _, r := range g.Roots {
		name := r
		if name == "" {
			name = "."
		}
		fmt.Fprintf(&b, "  %s\n", name)
	}

	b.WriteString("\nnodes by kind:\n")
	writeCounts(&b, kindCounts(g))
	b.WriteString("\nedges by kind:\n")
	writeCounts(&b, edgeCounts(g))

	if u := g.UnresolvedCount(); u > 0 {
		fmt.Fprintf(&b, "\nunresolved references: %d of %d — these point into remote modules or a\n"+
			"scope this repository does not contain. Impact sets are incomplete by that much.\n",
			u, g.EdgeCount())
	}

	writeOverlayStatus(&b, ix)

	if len(g.ParseErrors) > 0 {
		fmt.Fprintf(&b, "\nparse errors (%d):\n", len(g.ParseErrors))
		for _, e := range g.ParseErrors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return b.String()
}

// writeOverlayStatus says what the overlay can and cannot answer.
//
// An agent that asks about replacement and gets silence will assume the answer is "no
// replacement". Naming the absence, per stack, is the difference between a limit and a
// wrong answer.
func writeOverlayStatus(b *strings.Builder, ix *index.Index) {
	ov := ix.Overlay()

	if !ov.IsAvailable() {
		fmt.Fprintf(b, "\nplan overlay: not loaded — %s.\n", ov.UnavailableReason())
		b.WriteString("Static HCL only: count/for_each expansion and replace-vs-update are not\n" +
			"available. Produce one with:\n" +
			"  terraform plan -out=tf.plan && terraform show -json tf.plan > tfplan.json\n" +
			"left in the root module directory, or pass --plan <root>=<path>.\n")
		return
	}

	b.WriteString("\nplan overlay:\n")
	covered := map[string]bool{}
	for _, stack := range ov.Stacks() {
		covered[stack] = true
		name := stack
		if name == "" {
			name = "."
		}
		fmt.Fprintf(b, "  %-24s %-6s %s\n", name, ov.KindFor(stack), ov.SourceFor(stack))
	}

	// A partly covered repository is the dangerous state: the tool looks equipped, and is
	// silently blind on whichever stack the question happens to be about.
	var uncovered []string
	for _, r := range ix.Graph().Roots {
		if !covered[r] {
			name := r
			if name == "" {
				name = "."
			}
			uncovered = append(uncovered, name)
		}
	}
	if len(uncovered) > 0 {
		fmt.Fprintf(b, "  no overlay for %d root(s): %s\n", len(uncovered), strings.Join(uncovered, ", "))
		b.WriteString("  answers about those stacks are static-only.\n")
	}
	if reason := ov.UnavailableReason(); reason != "" {
		fmt.Fprintf(b, "  warning: %s\n", reason)
	}
}

func writeIdentity(b *strings.Builder, n *graph.Node) {
	fmt.Fprintf(b, "### %s\n", n.Address)
	fmt.Fprintf(b, "kind: %-14s at: %s\n", n.Kind, n.Location())

	scope := n.ModuleDir
	if scope == "" {
		scope = "."
	}
	fmt.Fprintf(b, "module: %s", scope)
	switch {
	case n.Shared:
		b.WriteString("   stack: (shared by several roots)")
	case n.Stack != "" && n.Stack != n.ModuleDir:
		fmt.Fprintf(b, "   stack: %s", n.Stack)
	}
	b.WriteString("\n")

	var flags []string
	if n.HasCount {
		flags = append(flags, "count")
	}
	if n.HasForEach {
		flags = append(flags, "for_each")
	}
	if n.Deprecated {
		flags = append(flags, "moved away — no longer the live address")
	}
	if n.Sensitive {
		flags = append(flags, "sensitive")
	}
	if len(flags) > 0 {
		fmt.Fprintf(b, "flags: %s\n", strings.Join(flags, ", "))
	}
	if n.Source != "" {
		fmt.Fprintf(b, "source: %s", n.Source)
		if n.Version != "" {
			fmt.Fprintf(b, "  version: %s", n.Version)
		}
		b.WriteString("\n")
	}
	if n.Doc != "" {
		fmt.Fprintf(b, "comment: %s\n", n.Doc)
	}
}

func writeExcerpt(b *strings.Builder, n *graph.Node, e index.Excerpt) {
	if e.Text == "" {
		return
	}
	b.WriteString(e.Text)
	if !strings.HasSuffix(e.Text, "\n") {
		b.WriteString("\n")
	}
	if e.Truncated {
		// Name the exact lines rather than saying "truncated". The caller can then open
		// precisely the right range instead of the whole file.
		fmt.Fprintf(b, "[truncated at %d of %d characters — the full block is %s lines %d-%d; "+
			"ask again with a larger charBudget]\n",
			len(e.Text), e.FullLen, n.File, n.Line, n.EndLine)
	}
}

// writeRefsBounded lists references within a character allowance, always stating the true
// total first so a truncated list is never mistaken for a complete one.
func writeRefsBounded(b *strings.Builder, label string, refs []index.Reference, incoming bool, allowance int) {
	if len(refs) == 0 {
		fmt.Fprintf(b, "%s: none\n", label)
		return
	}

	fmt.Fprintf(b, "%s (%d):\n", label, len(refs))

	spent := 0
	for i, r := range refs {
		if i >= refCap || spent >= allowance {
			fmt.Fprintf(b, "  … %d more.\n", len(refs)-i)
			return
		}
		before := b.Len()

		peer := "(unresolved)"
		if r.Peer != nil {
			peer = r.Peer.Address
		} else if r.Edge.Unresolved != "" {
			peer = r.Edge.Unresolved + " (not in this repository)"
		}

		fmt.Fprintf(b, "  %-40s %s:%d", peer, r.Edge.File, r.Edge.Line)
		if r.Edge.Kind != graph.EdgeReferences {
			fmt.Fprintf(b, "  [%s]", r.Edge.Kind)
		}
		if incoming && r.Edge.Traversal != "" {
			fmt.Fprintf(b, "  as %s", r.Edge.Traversal)
		}
		b.WriteString("\n")
		spent += b.Len() - before
	}
}

func kindCounts(g *graph.Graph) map[string]int {
	out := map[string]int{}
	for k, v := range g.CountsByKind() {
		out[string(k)] = v
	}
	return out
}

func edgeCounts(g *graph.Graph) map[string]int {
	out := map[string]int{}
	for k, v := range g.CountsByEdgeKind() {
		out[string(k)] = v
	}
	return out
}

func writeCounts(b *strings.Builder, m map[string]int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Fprintf(b, "  %-22s %d\n", k, m[k])
	}
}
