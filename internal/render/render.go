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
)

// refCap bounds how many references are listed for one node. A variable with two hundred
// consumers should say so, not print them.
const refCap = 40

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
		writeExcerpt(&b, h.Node, h.Excerpt)
	}
	return b.String()
}

// ForAddress renders the reverse lookup.
func ForAddress(ix *index.Index, address string) string {
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

	for _, n := range res.Declarations {
		b.WriteString("\n")
		writeIdentity(&b, n)

		writeRefs(&b, "referenced by", res.ReferencedBy[n.Key()], true)
		writeRefs(&b, "references", res.References[n.Key()], false)
	}
	return b.String()
}

// Impact renders the reachability set, and is explicit about what it is not.
func Impact(ix *index.Index, address string, dir index.Direction, depth int) string {
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

	for _, d := range depths {
		fmt.Fprintf(&b, "\ndepth %d:\n", d)
		for _, n := range byDepth[d] {
			fmt.Fprintf(&b, "  %-44s %s", n.Node.Address, n.Node.Location())
			if n.Via != nil && n.Via.Kind != graph.EdgeReferences {
				fmt.Fprintf(&b, "  [%s]", n.Via.Kind)
			}
			b.WriteString("\n")
		}
	}

	// The honest caveat, stated every time rather than buried in documentation.
	b.WriteString("\nThis is what is *connected*, not what a plan would replace. Static " +
		"configuration knows\nwhich expressions read which addresses; only `terraform plan` " +
		"knows which changes force\nreplacement.\n")

	if len(res.ExpandingBlocks) > 0 {
		fmt.Fprintf(&b, "\nLower bound: %d block(s) in this set use count or for_each, so the real\n"+
			"instance count is higher than the node count: %s\n",
			len(res.ExpandingBlocks), strings.Join(res.ExpandingBlocks, ", "))
	}
	if res.Truncated {
		fmt.Fprintf(&b, "\nTruncated at depth %d — there is more beyond it. Ask again with a larger depth.\n", depth)
	}
	return b.String()
}

// Modules renders the module inventory, skew first.
func Modules(ix *index.Index, filter string) string {
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

	for _, u := range usages {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s", u.Source)
		if u.Local {
			b.WriteString("  (local path — no version to skew)")
		} else if len(u.Versions) > 1 {
			b.WriteString("  ← VERSION SKEW")
		}
		b.WriteString("\n")

		versions := make([]string, 0, len(u.Versions))
		for v := range u.Versions {
			versions = append(versions, v)
		}
		sort.Strings(versions)

		for _, v := range versions {
			sites := u.Versions[v]
			fmt.Fprintf(&b, "  %-12s %d call site(s)\n", v, len(sites))
			for _, s := range sites {
				// The calling directory is the answer to "which stack", which is what the
				// question is actually about — the call's own address repeats across them.
				stack := s.Call.Stack
				if stack == "" {
					stack = s.Call.ModuleDir
				}
				fmt.Fprintf(&b, "    %-24s in %-18s %s\n", s.Call.Address, stack, s.Call.Location())
			}
		}
	}
	return b.String()
}

// Orphans renders dead configuration.
func Orphans(ix *index.Index) string {
	orphans := ix.Orphans()
	if len(orphans) == 0 {
		return "No unreferenced variables, locals or child-module outputs. " +
			"Root module outputs are excluded by design — they are the stack's public surface."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d unreferenced declaration(s).\n", len(orphans))
	b.WriteString("A caller passing a value does not count as using it: a variable every stack wires\n" +
		"up and nothing inside ever reads is dead configuration with live call sites.\n")

	current := "\x00"
	for _, o := range orphans {
		if o.Node.ModuleDir != current {
			current = o.Node.ModuleDir
			dir := current
			if dir == "" {
				dir = "."
			}
			fmt.Fprintf(&b, "\n%s\n", dir)
		}
		fmt.Fprintf(&b, "  %-34s %-22s %s\n", o.Node.Address, o.Node.Location(), o.Reason)
	}
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

	// The overlay is not built yet. Saying so is more useful than silence, because an
	// agent asking about replacement behaviour needs to know the answer cannot come from
	// here.
	b.WriteString("\nplan overlay: not loaded (static HCL only).\n" +
		"count/for_each expansion and replace-vs-update are therefore not available.\n")

	if len(g.ParseErrors) > 0 {
		fmt.Fprintf(&b, "\nparse errors (%d):\n", len(g.ParseErrors))
		for _, e := range g.ParseErrors {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return b.String()
}

func writeIdentity(b *strings.Builder, n *graph.Node) {
	fmt.Fprintf(b, "### %s\n", n.Address)
	fmt.Fprintf(b, "kind: %-14s at: %s\n", n.Kind, n.Location())

	scope := n.ModuleDir
	if scope == "" {
		scope = "."
	}
	fmt.Fprintf(b, "module: %s", scope)
	if n.Stack != "" && n.Stack != n.ModuleDir {
		fmt.Fprintf(b, "   stack: %s", n.Stack)
	} else if n.Stack == "" && n.Kind != graph.KindModuleSource && n.Kind != graph.KindStack {
		b.WriteString("   stack: (shared by several roots)")
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

func writeRefs(b *strings.Builder, label string, refs []index.Reference, incoming bool) {
	if len(refs) == 0 {
		fmt.Fprintf(b, "%s: none\n", label)
		return
	}

	fmt.Fprintf(b, "%s (%d):\n", label, len(refs))
	for i, r := range refs {
		if i >= refCap {
			fmt.Fprintf(b, "  … %d more.\n", len(refs)-refCap)
			break
		}

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
