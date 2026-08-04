package index

import (
	"sort"
	"strings"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/overlay"
)

// Reference is one edge presented from the perspective of the node being asked about.
type Reference struct {
	Edge *graph.Edge
	Peer *graph.Node
}

// AddressResult is the reverse lookup: who declares an address, and who actually uses it.
type AddressResult struct {
	// Declarations is every node with this address. Two stacks legitimately declare the
	// same one, and both are shown rather than one being silently chosen.
	Declarations []*graph.Node

	// ReferencedBy holds only resolved expression traversals. A mention in a comment or
	// inside a quoted string is not here, because it is not a reference — that distinction
	// is the reason this query exists instead of a grep.
	ReferencedBy map[string][]Reference

	// References is what each declaration itself depends on.
	References map[string][]Reference
}

// ForAddress answers "what actually uses this?".
//
// It accepts a full address (aws_s3_bucket.logs), a bare local name (logs), or a type
// (aws_s3_bucket), because a caller who remembers only part of a name should not have to
// guess which part the index wanted.
func (ix *Index) ForAddress(query string) AddressResult {
	q := strings.TrimSpace(query)
	res := AddressResult{
		ReferencedBy: map[string][]Reference{},
		References:   map[string][]Reference{},
	}
	if q == "" {
		return res
	}

	seen := map[string]bool{}
	appendUnique := func(nodes []*graph.Node) {
		for _, n := range nodes {
			if !seen[n.Key()] {
				seen[n.Key()] = true
				res.Declarations = append(res.Declarations, n)
			}
		}
	}

	appendUnique(ix.graph.ByAddress(q))
	if len(res.Declarations) == 0 {
		appendUnique(ix.graph.ByName(q))
	}
	if len(res.Declarations) == 0 {
		appendUnique(ix.graph.ByType(q))
	}
	graph.SortNodes(res.Declarations)

	for _, n := range res.Declarations {
		key := n.Key()
		res.ReferencedBy[key] = ix.refs(ix.graph.Incoming(key), func(e *graph.Edge) string { return e.From })
		res.References[key] = ix.refs(ix.graph.Outgoing(key), func(e *graph.Edge) string { return e.To })
	}
	return res
}

func (ix *Index) refs(edges []*graph.Edge, peerOf func(*graph.Edge) string) []Reference {
	out := make([]Reference, 0, len(edges))
	for _, e := range edges {
		out = append(out, Reference{Edge: e, Peer: ix.graph.ByKey(peerOf(e))})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Edge.File != out[j].Edge.File {
			return out[i].Edge.File < out[j].Edge.File
		}
		return out[i].Edge.Line < out[j].Edge.Line
	})
	return out
}

// Direction selects which way Impact walks the graph.
type Direction string

const (
	// Dependents is the blast radius: everything that would be affected by changing this.
	Dependents Direction = "dependents"

	// Dependencies is what this node needs in order to exist.
	Dependencies Direction = "dependencies"
)

// ImpactNode is one node in a reachability set, with the distance that reached it.
type ImpactNode struct {
	Node  *graph.Node
	Depth int

	// Via is the edge that first reached this node, so a caller can see the chain rather
	// than a flat list.
	Via *graph.Edge

	// Instances is the overlay's answer for this node, empty without one.
	Instances []NodeInstance
}

// Replaces reports whether any instance of this node is destroyed and recreated.
func (n ImpactNode) Replaces() bool {
	for _, i := range n.Instances {
		if i.Replaces() {
			return true
		}
	}
	return false
}

// ImpactResult is a reachability set, and an honest statement of what it is not.
type ImpactResult struct {
	Origin []*graph.Node
	Nodes  []ImpactNode

	// Truncated says the walk hit the depth limit with frontier left over.
	Truncated bool

	// ExpandingBlocks names nodes in the set that use count or for_each. Without an
	// overlay, static parsing cannot say how many instances those become, so the set is a
	// lower bound on the real one.
	ExpandingBlocks []string

	// OverlayAvailable says whether the counts below came from a plan or a state rather
	// than from guessing.
	OverlayAvailable bool

	// OverlayKind is what the overlay can answer: a plan knows replacement, a state does
	// not. Reporting "no replacements" from a state overlay would be a lie of omission.
	OverlayKind overlay.Kind

	// InstanceTotal is how many real instances the set covers, when an overlay is loaded.
	InstanceTotal int

	// Replacing names the nodes a plan intends to destroy and recreate. This is the
	// question static configuration cannot answer at all.
	Replacing []string
}

// Impact walks the reference graph transitively.
//
// It reports what is *connected*, which is not the same as what a plan would replace.
// Static configuration knows that aws_s3_bucket_policy.logs reads aws_s3_bucket.logs.id;
// only a plan knows whether changing the bucket forces the policy to be recreated. The
// result says so rather than implying a precision it does not have.
func (ix *Index) Impact(address string, dir Direction, maxDepth int) ImpactResult {
	if maxDepth <= 0 {
		maxDepth = 3
	}

	start := ix.ForAddress(address).Declarations
	res := ImpactResult{Origin: start}
	if len(start) == 0 {
		return res
	}

	visited := map[string]bool{}
	frontier := make([]*graph.Node, 0, len(start))
	for _, n := range start {
		visited[n.Key()] = true
		frontier = append(frontier, n)
	}

	for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
		var next []*graph.Node

		for _, n := range frontier {
			var edges []*graph.Edge
			if dir == Dependencies {
				edges = ix.graph.Outgoing(n.Key())
			} else {
				edges = ix.graph.Incoming(n.Key())
			}

			for _, e := range edges {
				peerKey := e.From
				if dir == Dependencies {
					peerKey = e.To
				}
				if peerKey == "" || visited[peerKey] {
					continue
				}
				peer := ix.graph.ByKey(peerKey)
				if peer == nil {
					continue
				}
				visited[peerKey] = true
				res.Nodes = append(res.Nodes, ImpactNode{
					Node: peer, Depth: depth, Via: e, Instances: ix.InstancesOf(peer),
				})
				next = append(next, peer)
			}
		}
		frontier = next
	}
	res.Truncated = len(frontier) > 0

	res.OverlayAvailable = ix.overlay.IsAvailable()

	// The origin is part of the blast radius too: changing a resource replaces that
	// resource. Leaving it out of the replacement list was the obvious thing to get wrong.
	scan := make([]ImpactNode, 0, len(res.Nodes)+len(res.Origin))
	for _, o := range res.Origin {
		scan = append(scan, ImpactNode{Node: o, Instances: ix.InstancesOf(o)})
	}
	scan = append(scan, res.Nodes...)

	seenReplacing := map[string]bool{}
	for _, in := range scan {
		if in.Node.HasCount || in.Node.HasForEach {
			res.ExpandingBlocks = append(res.ExpandingBlocks, in.Node.Address)
		}
		res.InstanceTotal += len(in.Instances)

		if in.Replaces() && !seenReplacing[in.Node.Key()] {
			seenReplacing[in.Node.Key()] = true
			res.Replacing = append(res.Replacing, in.Node.Address)
		}
		for _, i := range in.Instances {
			if res.OverlayKind == "" {
				res.OverlayKind = ix.overlay.KindFor(i.Stack)
			}
		}
	}

	sort.Strings(res.ExpandingBlocks)
	res.ExpandingBlocks = dedupeStrings(res.ExpandingBlocks)
	sort.Strings(res.Replacing)

	sort.SliceStable(res.Nodes, func(i, j int) bool {
		if res.Nodes[i].Depth != res.Nodes[j].Depth {
			return res.Nodes[i].Depth < res.Nodes[j].Depth
		}
		if res.Nodes[i].Node.ModuleDir != res.Nodes[j].Node.ModuleDir {
			return res.Nodes[i].Node.ModuleDir < res.Nodes[j].Node.ModuleDir
		}
		return res.Nodes[i].Node.Address < res.Nodes[j].Node.Address
	})
	return res
}

// CallSite is one place a module source is used.
type CallSite struct {
	Call    *graph.Node
	Version string
}

// ModuleUsage groups every call of one source across the repository.
type ModuleUsage struct {
	Source string

	// Versions maps a version string to the calls pinned at it. More than one entry is
	// version skew — the thing grep genuinely cannot find, because the source and the
	// version live on different lines of different files in different directories.
	Versions map[string][]CallSite

	// Local is true for a path source, which carries no version and cannot skew.
	Local bool
}

// Modules inventories module usage. An empty filter returns every source.
func (ix *Index) Modules(filter string) []ModuleUsage {
	filter = strings.ToLower(strings.TrimSpace(filter))

	bySource := map[string]*ModuleUsage{}
	for _, n := range ix.graph.Nodes() {
		if n.Kind != graph.KindModuleCall || n.Source == "" {
			continue
		}
		if filter != "" &&
			!strings.Contains(strings.ToLower(n.Source), filter) &&
			!strings.Contains(strings.ToLower(n.Name), filter) {
			continue
		}

		u, ok := bySource[n.Source]
		if !ok {
			u = &ModuleUsage{
				Source:   n.Source,
				Versions: map[string][]CallSite{},
				Local:    graph.IsLocalSource(n.Source),
			}
			bySource[n.Source] = u
		}
		v := n.Version
		if v == "" {
			v = "(unpinned)"
		}
		u.Versions[v] = append(u.Versions[v], CallSite{Call: n, Version: n.Version})
	}

	out := make([]ModuleUsage, 0, len(bySource))
	for _, u := range bySource {
		for _, sites := range u.Versions {
			sort.SliceStable(sites, func(i, j int) bool {
				return sites[i].Call.Key() < sites[j].Call.Key()
			})
		}
		out = append(out, *u)
	}

	// Skewed sources first — that is the finding, and burying it under an alphabetical
	// list of healthy ones would make the caller scan for it.
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := len(out[i].Versions), len(out[j].Versions)
		if si != sj {
			return si > sj
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// Orphan is one piece of configuration nothing uses.
type Orphan struct {
	Node   *graph.Node
	Reason string
}

// Orphans finds declarations with no consumer.
//
// The distinction that makes this more than a linter run is that a caller passing a value
// does not count as using it. A child module variable every stack dutifully wires up, and
// nothing inside the module ever reads, is dead configuration with four live call sites —
// which is precisely the shape that survives code review for years.
func (ix *Index) Orphans() []Orphan {
	var out []Orphan

	for _, n := range ix.graph.Nodes() {
		switch n.Kind {
		case graph.KindVariable, graph.KindLocal, graph.KindOutput:
		default:
			continue
		}

		var reads, wiredBy int
		for _, e := range ix.graph.Incoming(n.Key()) {
			switch e.Kind {
			case graph.EdgeReferences:
				reads++
			case graph.EdgeInputs:
				wiredBy++
			}
		}
		if reads > 0 {
			continue
		}

		switch n.Kind {
		case graph.KindVariable:
			if wiredBy > 0 {
				out = append(out, Orphan{n, "declared and wired up by callers, but never read inside this module"})
			} else {
				out = append(out, Orphan{n, "declared but never referenced"})
			}
		case graph.KindLocal:
			out = append(out, Orphan{n, "computed but never referenced"})
		case graph.KindOutput:
			if isRootModule(ix.graph, n.ModuleDir) {
				// A root module's outputs are the stack's public surface. Nothing inside
				// the repository consumes them by design, so calling them orphans would
				// be a false positive on every well-formed stack.
				continue
			}
			out = append(out, Orphan{n, "exported by a child module but consumed by no caller"})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node.ModuleDir != out[j].Node.ModuleDir {
			return out[i].Node.ModuleDir < out[j].Node.ModuleDir
		}
		return out[i].Node.Address < out[j].Node.Address
	})
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func isRootModule(g *graph.Graph, dir string) bool {
	for _, r := range g.Roots {
		if r == dir {
			return true
		}
	}
	return false
}
