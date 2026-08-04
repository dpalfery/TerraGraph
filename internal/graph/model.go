// Package graph holds TerraGraph's node and edge model.
//
// The model is deliberately thin. Terraform already supplies the thing a code index
// normally has to invent: every block has a formal, unambiguous address, and every
// reference to one is a parseable traversal rather than a string that happens to match.
// So nodes are keyed by their Terraform address and nothing here synthesises an identity
// that the configuration did not already declare.
package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is the closed set of node shapes. Adding a member is a change to the model, not an
// authoring decision.
type Kind string

const (
	KindResource   Kind = "resource"
	KindData       Kind = "data"
	KindModuleCall Kind = "module"
	KindVariable   Kind = "variable"
	KindOutput     Kind = "output"
	KindLocal      Kind = "local"
	KindProvider   Kind = "provider"
	KindMoved      Kind = "moved"
	KindRemoved    Kind = "removed"
	KindImport     Kind = "import"

	// KindStack is synthetic: one per discovered root module. It gives whole-directory
	// questions ("what is in the prod stack") something to rank and gives every other
	// node an owner.
	KindStack Kind = "stack"

	// KindModuleSource is synthetic: one per distinct source+version pair. Version-skew
	// queries group by it, which is the whole reason it exists as a node rather than an
	// attribute — "which stacks call vpc at which version" is an edge query, not a scan.
	KindModuleSource Kind = "module_source"
)

// EdgeKind is the closed set of relationships between nodes.
type EdgeKind string

const (
	// EdgeReferences is the product. It is emitted only from a resolved expression
	// traversal, never from a textual match, which is the distinction grep collapses.
	EdgeReferences EdgeKind = "REFERENCES"

	EdgeDependsOn          EdgeKind = "DEPENDS_ON"
	EdgeCalls              EdgeKind = "CALLS"
	EdgeProvides           EdgeKind = "PROVIDES"
	EdgeInputs             EdgeKind = "INPUTS"
	EdgeMovedFrom          EdgeKind = "MOVED_FROM"
	EdgeReplaceTriggeredBy EdgeKind = "REPLACE_TRIGGERED_BY"
	EdgeContains           EdgeKind = "CONTAINS"
)

// Node is one addressable block of Terraform configuration.
type Node struct {
	Kind Kind

	// Address is the Terraform address exactly as Terraform would write it:
	// "aws_s3_bucket.logs", "data.aws_ami.ubuntu", "var.environment", "module.vpc".
	// Synthetic kinds use a prefixed form that cannot collide with a real address
	// ("stack.infra/prod", "source.terraform-aws-modules/vpc/aws@5.1.0").
	Address string

	// Type is the resource or data source type ("aws_s3_bucket"), or the provider name
	// for a provider node. Empty for variables, outputs and locals.
	Type string

	// Name is the local name ("logs"). For a resource this is the second label; it is
	// what a person types when they do not remember the full address.
	Name string

	// ModuleDir is the repo-relative directory whose scope this block lives in. It is the
	// resolution scope: `var.x` inside ModuleDir resolves to a variable in ModuleDir and
	// nowhere else.
	ModuleDir string

	// Stack is the repo-relative directory of the owning root module. Empty means either
	// "no single owner" or "the root module is the repository itself" — Shared is what
	// tells those apart.
	Stack string

	// Shared marks a node in a child module called from more than one place, which has no
	// single owning stack. Without this flag an empty Stack is ambiguous, and a
	// single-root repository gets reported as sharing everything with nobody.
	Shared bool

	File    string
	Line    int
	EndLine int

	// Body is the block's own source text, used for excerpting. Held in memory because
	// retrieval returns configuration, not paths.
	Body string

	// Doc is the contiguous comment block immediately above the declaration. Terraform
	// has no docstring convention, so leading comments are the only prose a block carries.
	Doc string

	// Source and Version are set on module calls and module_source nodes.
	Source  string
	Version string

	// ProviderAlias is set on aliased provider blocks and on nodes with an explicit
	// `provider =` argument.
	ProviderAlias string

	// HasCount and HasForEach record that this block expands. Static parsing cannot say
	// into how many instances, and callers are told so rather than left to assume one.
	HasCount   bool
	HasForEach bool

	// Deprecated marks an address a `moved` block has vacated. It is the closest thing
	// Terraform has to DocGraph's "superseded", and it feeds the authority demotion.
	Deprecated bool

	// Sensitive is set on variables and outputs declared sensitive.
	Sensitive bool
}

// Key uniquely identifies a node across the whole repository. Addresses are unique only
// within a module scope — two stacks may each declare aws_s3_bucket.logs — so the module
// directory qualifies it.
func (n *Node) Key() string { return n.ModuleDir + "|" + n.Address }

// NodeKey builds the same key from parts, for lookups during resolution.
func NodeKey(moduleDir, address string) string { return moduleDir + "|" + address }

// Location renders the node as a clickable file:line, the form callers act on.
func (n *Node) Location() string { return fmt.Sprintf("%s:%d", n.File, n.Line) }

// Edge is one directed relationship. Every edge carries the position of the expression
// that produced it, so a caller can be sent to the reference rather than to the block.
type Edge struct {
	Kind EdgeKind

	// From and To are node keys. To is empty when the traversal did not resolve.
	From string
	To   string

	// Traversal is the reference as authored ("aws_s3_bucket.logs.arn"). It is retained
	// even when resolution succeeds, because the attribute it selected is information the
	// node key has thrown away.
	Traversal string

	// Unresolved explains why To is empty. A reference into a module the loader could not
	// see is a fact about coverage, not a parse failure, and is reported rather than
	// dropped.
	Unresolved string

	File string
	Line int
}

// StackPath is one way a module directory is reachable from a root module.
//
// It exists to join two different addressing schemes. TerraGraph keys nodes by directory,
// because that is the scope HCL resolves references in and it is stable no matter who
// calls the module. Terraform keys everything by module path from a root, because that is
// what a plan is produced against. A shared module has one directory and several paths —
// `modules/bucket` is `module.artifacts_bucket` in one stack and `module.backups_bucket`
// in another — so the mapping is genuinely one-to-many and cannot be collapsed to a field
// on the node.
type StackPath struct {
	// Stack is the root module directory.
	Stack string

	// ModuleAddress is the Terraform module path within that root
	// ("module.a.module.b"), empty for the root module itself.
	ModuleAddress string
}

// TerraformAddress renders a node's address as Terraform would write it along this path.
func (p StackPath) TerraformAddress(nodeAddress string) string {
	if p.ModuleAddress == "" {
		return nodeAddress
	}
	return p.ModuleAddress + "." + nodeAddress
}

// Graph is an immutable snapshot of one repository's configuration.
//
// Nothing mutates a Graph after Build returns. Staleness is handled by discarding the
// whole snapshot and building another, so a query in flight always sees one coherent view.
type Graph struct {
	nodes []*Node
	edges []*Edge

	byKey     map[string]*Node
	byAddress map[string][]*Node
	byName    map[string][]*Node
	byType    map[string][]*Node

	outgoing map[string][]*Edge
	incoming map[string][]*Edge

	// pathsByDir maps a module directory to every way a root module reaches it.
	pathsByDir map[string][]StackPath

	// Roots is every discovered root module, repo-relative.
	Roots []string

	// RepoRoot is the absolute path the snapshot was built over.
	RepoRoot string

	// Skipped counts files that could not be parsed. A graph built over a repo with a
	// syntax error is still useful; pretending it is complete is not.
	ParseErrors []string
}

// Build assembles the lookup tables once. Callers hand over ownership of the slices.
func Build(
	repoRoot string,
	roots []string,
	nodes []*Node,
	edges []*Edge,
	pathsByDir map[string][]StackPath,
	parseErrors []string,
) *Graph {
	if pathsByDir == nil {
		pathsByDir = map[string][]StackPath{}
	}
	g := &Graph{
		nodes:       nodes,
		edges:       edges,
		byKey:       make(map[string]*Node, len(nodes)),
		byAddress:   make(map[string][]*Node),
		byName:      make(map[string][]*Node),
		byType:      make(map[string][]*Node),
		outgoing:    make(map[string][]*Edge),
		incoming:    make(map[string][]*Edge),
		pathsByDir:  pathsByDir,
		Roots:       roots,
		RepoRoot:    repoRoot,
		ParseErrors: parseErrors,
	}

	for _, n := range nodes {
		g.byKey[n.Key()] = n
		g.byAddress[n.Address] = append(g.byAddress[n.Address], n)
		if n.Name != "" {
			g.byName[n.Name] = append(g.byName[n.Name], n)
		}
		if n.Type != "" {
			g.byType[n.Type] = append(g.byType[n.Type], n)
		}
	}

	for _, e := range edges {
		g.outgoing[e.From] = append(g.outgoing[e.From], e)
		if e.To != "" {
			g.incoming[e.To] = append(g.incoming[e.To], e)
		}
	}

	return g
}

func (g *Graph) Nodes() []*Node { return g.nodes }
func (g *Graph) Edges() []*Edge { return g.edges }

func (g *Graph) NodeCount() int { return len(g.nodes) }
func (g *Graph) EdgeCount() int { return len(g.edges) }

// ByKey returns the node with an exact key, or nil.
func (g *Graph) ByKey(key string) *Node { return g.byKey[key] }

// ByAddress returns every node declaring this address, across all module scopes. Two
// stacks legitimately declare the same address, and the caller is shown both rather than
// having one silently chosen.
func (g *Graph) ByAddress(address string) []*Node { return g.byAddress[address] }

// ByName returns every node whose local name matches, which is what a caller types when
// they remember "logs" but not "aws_s3_bucket.logs".
func (g *Graph) ByName(name string) []*Node { return g.byName[name] }

// ByType returns every node of a resource or data source type — the whole family.
func (g *Graph) ByType(t string) []*Node { return g.byType[t] }

// Outgoing returns the edges leaving a node: what it references.
func (g *Graph) Outgoing(key string) []*Edge { return g.outgoing[key] }

// Incoming returns the edges arriving at a node: what references it. This is the reverse
// lookup that justifies an index over grep.
func (g *Graph) Incoming(key string) []*Edge { return g.incoming[key] }

// PathsFor returns every way a root module reaches this directory. A directory called
// from three stacks has three paths, and a caller joining to plan data must consider all
// of them — the same block genuinely has different instances in each stack.
func (g *Graph) PathsFor(moduleDir string) []StackPath { return g.pathsByDir[moduleDir] }

// CountsByKind is the shape of the graph, for status reporting.
func (g *Graph) CountsByKind() map[Kind]int {
	out := make(map[Kind]int)
	for _, n := range g.nodes {
		out[n.Kind]++
	}
	return out
}

// CountsByEdgeKind is the shape of the relationships, for status reporting.
func (g *Graph) CountsByEdgeKind() map[EdgeKind]int {
	out := make(map[EdgeKind]int)
	for _, e := range g.edges {
		out[e.Kind]++
	}
	return out
}

// UnresolvedCount is how many references pointed at something the loader could not see.
// A high number means the graph is less complete than its node count suggests, so it is
// surfaced rather than buried.
func (g *Graph) UnresolvedCount() int {
	n := 0
	for _, e := range g.edges {
		if e.To == "" {
			n++
		}
	}
	return n
}

// SortNodes orders nodes stably for deterministic output: by module, then address.
func SortNodes(nodes []*Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].ModuleDir != nodes[j].ModuleDir {
			return nodes[i].ModuleDir < nodes[j].ModuleDir
		}
		return nodes[i].Address < nodes[j].Address
	})
}

// ModuleSourceAddress is the synthetic address for a module source at a version. Local
// paths carry no version, so they collapse to the path alone.
func ModuleSourceAddress(source, version string) string {
	if version == "" {
		return "source." + source
	}
	return "source." + source + "@" + version
}

// IsLocalSource reports whether a module source is a path within the repository rather
// than a registry or git reference. Only local sources can be resolved without an init.
func IsLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}
