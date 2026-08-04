package tfhcl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Load walks repoRoot, parses every .tf file, and returns an immutable graph.
//
// It runs in two passes because root classification depends on the module calls, and
// stack ownership depends on root classification. Trying to do it in one pass means
// guessing at the first thing you see, which is how a shared module ends up attributed to
// whichever root happened to be walked first.
func Load(repoRoot string) (*graph.Graph, error) {
	layout, err := Discover(repoRoot)
	if err != nil {
		return nil, err
	}

	l := &loader{
		layout:   layout,
		parser:   hclparse.NewParser(),
		sources:  make(map[string][]byte),
		byDir:    make(map[string][]*graph.Node),
		callers:  make(map[string][]string),
		explicit: make(map[string]bool),
		srcNodes: make(map[string]*graph.Node),
	}

	// Pass one: every block becomes a node, and module calls are recorded so roots can be
	// classified before anything is attributed to a stack.
	for _, dir := range layout.Dirs {
		for _, file := range layout.FilesByDir[dir] {
			l.loadFile(dir, file)
		}
	}

	layout.ComputeRoots(l.explicit)
	l.assignStacks()
	l.addStackNodes()

	// Pass two: expressions become edges, now that every node exists to resolve against.
	l.resolveAll()

	graph.SortNodes(l.nodes)
	return graph.Build(
		layout.RepoRoot, layout.Roots, l.nodes, l.edges, l.computePaths(), l.parseErrors), nil
}

// maxModulePathDepth bounds the module-path walk.
//
// Terraform rejects a module cycle, but TerraGraph reads configuration that may not be
// valid — that is much of the point of reading it statically. A cycle produces paths that
// grow without bound rather than repeating, so a visited-set alone does not terminate.
const maxModulePathDepth = 32

// computePaths works out every way each module directory is reached from a root.
//
// The result is what lets a node keyed by directory be looked up in a plan keyed by module
// path. A directory called from three stacks gets three paths, which is not redundancy: the
// same block really does have separate instances under each caller.
func (l *loader) computePaths() map[string][]graph.StackPath {
	type call struct{ name, target string }

	callsFrom := map[string][]call{}
	for _, n := range l.nodes {
		if n.Kind != graph.KindModuleCall || n.Source == "" {
			continue
		}
		if target, ok := l.layout.ResolveLocalSource(n.ModuleDir, n.Source); ok {
			callsFrom[n.ModuleDir] = append(callsFrom[n.ModuleDir], call{n.Name, target})
		}
	}

	paths := map[string][]graph.StackPath{}

	for _, root := range l.layout.Roots {
		type item struct {
			dir, prefix string
			depth       int
		}
		queue := []item{{dir: root}}
		seen := map[string]bool{}

		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]

			key := cur.dir + "|" + cur.prefix
			if seen[key] || cur.depth > maxModulePathDepth {
				continue
			}
			seen[key] = true

			paths[cur.dir] = append(paths[cur.dir],
				graph.StackPath{Stack: root, ModuleAddress: cur.prefix})

			for _, c := range callsFrom[cur.dir] {
				next := "module." + c.name
				if cur.prefix != "" {
					next = cur.prefix + "." + next
				}
				queue = append(queue, item{dir: c.target, prefix: next, depth: cur.depth + 1})
			}
		}
	}
	return paths
}

type loader struct {
	layout  *Layout
	parser  *hclparse.Parser
	sources map[string][]byte

	nodes []*graph.Node
	edges []*graph.Edge

	// byDir indexes nodes by their module scope, which is the scope references resolve in.
	byDir map[string][]*graph.Node

	// pending holds the expression work deferred to pass two.
	pending []pendingRefs

	// callers maps a child module directory to the directories calling it.
	callers map[string][]string

	// explicit marks directories carrying a positive root signal (backend, cloud, tfvars).
	explicit map[string]bool

	// srcNodes deduplicates synthetic module_source nodes.
	srcNodes map[string]*graph.Node

	parseErrors []string
}

// pendingRefs is one block's expressions, held until every node exists.
type pendingRefs struct {
	owner *graph.Node
	dir   string
	body  *hclsyntax.Body
	file  string
}

func (l *loader) loadFile(dir, relFile string) {
	abs := filepath.Join(l.layout.RepoRoot, relFile)
	src, err := os.ReadFile(abs)
	if err != nil {
		l.parseErrors = append(l.parseErrors, fmt.Sprintf("%s: %v", relFile, err))
		return
	}
	l.sources[relFile] = src

	f, diags := l.parser.ParseHCL(src, relFile)
	if diags.HasErrors() {
		l.parseErrors = append(l.parseErrors, fmt.Sprintf("%s: %s", relFile, diags.Error()))
		// A file with a syntax error may still have parsed blocks. Keep whatever survived
		// rather than dropping a whole file because of one bad line.
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok || body == nil {
		return
	}

	for _, b := range body.Blocks {
		l.loadBlock(dir, relFile, src, b)
	}
}

func (l *loader) loadBlock(dir, file string, src []byte, b *hclsyntax.Block) {
	switch b.Type {
	case "resource":
		if len(b.Labels) < 2 {
			return
		}
		n := l.newNode(graph.KindResource, b.Labels[0]+"."+b.Labels[1], dir, file, src, b)
		n.Type, n.Name = b.Labels[0], b.Labels[1]
		l.add(n, dir, file, b.Body)

	case "data":
		if len(b.Labels) < 2 {
			return
		}
		n := l.newNode(graph.KindData, "data."+b.Labels[0]+"."+b.Labels[1], dir, file, src, b)
		n.Type, n.Name = b.Labels[0], b.Labels[1]
		l.add(n, dir, file, b.Body)

	case "module":
		if len(b.Labels) < 1 {
			return
		}
		n := l.newNode(graph.KindModuleCall, "module."+b.Labels[0], dir, file, src, b)
		n.Name = b.Labels[0]
		n.Source = literalString(b.Body, "source")
		n.Version = literalString(b.Body, "version")
		if target, ok := l.layout.ResolveLocalSource(dir, n.Source); ok {
			l.layout.MarkCalled(target)
			l.callers[target] = append(l.callers[target], dir)
		}
		l.add(n, dir, file, b.Body)

	case "variable":
		if len(b.Labels) < 1 {
			return
		}
		n := l.newNode(graph.KindVariable, "var."+b.Labels[0], dir, file, src, b)
		n.Name = b.Labels[0]
		n.Sensitive = literalBool(b.Body, "sensitive")
		l.add(n, dir, file, b.Body)

	case "output":
		if len(b.Labels) < 1 {
			return
		}
		n := l.newNode(graph.KindOutput, "output."+b.Labels[0], dir, file, src, b)
		n.Name = b.Labels[0]
		n.Sensitive = literalBool(b.Body, "sensitive")
		l.add(n, dir, file, b.Body)

	case "locals":
		// A locals block is a container, not a node. Each attribute is separately
		// addressable as local.<name>, so that is what gets indexed.
		for _, attr := range sortedAttrs(b.Body) {
			n := &graph.Node{
				Kind:      graph.KindLocal,
				Address:   "local." + attr.Name,
				Name:      attr.Name,
				ModuleDir: dir,
				File:      file,
				Line:      attr.NameRange.Start.Line,
				EndLine:   attr.SrcRange.End.Line,
				Body:      sliceRange(src, attr.SrcRange),
				Doc:       leadingComments(src, attr.SrcRange.Start.Line),
			}
			l.nodes = append(l.nodes, n)
			l.byDir[dir] = append(l.byDir[dir], n)
			l.pending = append(l.pending, pendingRefs{owner: n, dir: dir, file: file, body: exprBody(attr)})
		}

	case "provider":
		if len(b.Labels) < 1 {
			return
		}
		alias := literalString(b.Body, "alias")
		addr := "provider." + b.Labels[0]
		if alias != "" {
			addr += "." + alias
		}
		n := l.newNode(graph.KindProvider, addr, dir, file, src, b)
		n.Type, n.Name, n.ProviderAlias = b.Labels[0], b.Labels[0], alias
		l.add(n, dir, file, b.Body)

	case "moved", "removed", "import":
		kind := map[string]graph.Kind{
			"moved": graph.KindMoved, "removed": graph.KindRemoved, "import": graph.KindImport,
		}[b.Type]
		addr := fmt.Sprintf("%s.%s:%d", b.Type, file, b.Range().Start.Line)
		n := l.newNode(kind, addr, dir, file, src, b)
		l.add(n, dir, file, b.Body)

	case "terraform":
		// Not a node — settings, not configuration. But a backend or cloud block is the
		// strongest positive signal that this directory is applied directly.
		for _, inner := range b.Body.Blocks {
			if inner.Type == "backend" || inner.Type == "cloud" {
				l.explicit[dir] = true
			}
		}
	}
}

// newNode fills the fields every block-backed node shares.
func (l *loader) newNode(kind graph.Kind, addr, dir, file string, src []byte, b *hclsyntax.Block) *graph.Node {
	r := b.Range()
	_, hasCount := b.Body.Attributes["count"]
	_, hasForEach := b.Body.Attributes["for_each"]
	return &graph.Node{
		Kind:       kind,
		Address:    addr,
		ModuleDir:  dir,
		File:       file,
		Line:       r.Start.Line,
		EndLine:    r.End.Line,
		Body:       sliceRange(src, r),
		Doc:        leadingComments(src, r.Start.Line),
		HasCount:   hasCount,
		HasForEach: hasForEach,
	}
}

func (l *loader) add(n *graph.Node, dir, file string, body *hclsyntax.Body) {
	l.nodes = append(l.nodes, n)
	l.byDir[dir] = append(l.byDir[dir], n)
	l.pending = append(l.pending, pendingRefs{owner: n, dir: dir, file: file, body: body})
}

// assignStacks attributes each node to the root module that owns it, once roots are known.
func (l *loader) assignStacks() {
	stackByDir := make(map[string]string, len(l.layout.Dirs))
	sharedByDir := make(map[string]bool, len(l.layout.Dirs))
	for _, dir := range l.layout.Dirs {
		if HasTfvars(l.layout.RepoRoot, dir) {
			l.explicit[dir] = true
		}
		stackByDir[dir], sharedByDir[dir] = l.layout.OwningStack(dir, l.callers)
	}
	for _, n := range l.nodes {
		n.Stack = stackByDir[n.ModuleDir]
		n.Shared = sharedByDir[n.ModuleDir]
	}
}

// addStackNodes creates the synthetic per-root and per-module-source nodes.
func (l *loader) addStackNodes() {
	for _, root := range l.layout.Roots {
		name := root
		if name == "" {
			name = "."
		}
		l.nodes = append(l.nodes, &graph.Node{
			Kind:      graph.KindStack,
			Address:   "stack." + name,
			Name:      filepath.Base(name),
			ModuleDir: root,
			Stack:     root,
			File:      root,
			Line:      1,
			Doc:       fmt.Sprintf("Root module at %s.", name),
		})
	}

	// One node per distinct source+version, so version skew is an edge query rather than
	// a scan over module calls.
	for _, n := range l.nodes {
		if n.Kind != graph.KindModuleCall || n.Source == "" {
			continue
		}
		addr := graph.ModuleSourceAddress(n.Source, n.Version)
		if _, seen := l.srcNodes[addr]; seen {
			continue
		}
		sn := &graph.Node{
			Kind:      graph.KindModuleSource,
			Address:   addr,
			Name:      filepath.Base(n.Source),
			Source:    n.Source,
			Version:   n.Version,
			ModuleDir: "",
			File:      n.File,
			Line:      n.Line,
		}
		if graph.IsLocalSource(n.Source) {
			sn.Doc = "Local module source."
		} else {
			sn.Doc = "Remote module source; its internals are not indexed without an init."
		}
		l.srcNodes[addr] = sn
		l.nodes = append(l.nodes, sn)
	}
}

func sortedAttrs(b *hclsyntax.Body) []*hclsyntax.Attribute {
	if b == nil {
		return nil
	}
	out := make([]*hclsyntax.Attribute, 0, len(b.Attributes))
	for _, a := range b.Attributes {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SrcRange.Start.Byte < out[j].SrcRange.Start.Byte })
	return out
}

// exprBody wraps a single attribute so the reference walker can treat it like a body.
func exprBody(attr *hclsyntax.Attribute) *hclsyntax.Body {
	return &hclsyntax.Body{Attributes: hclsyntax.Attributes{attr.Name: attr}}
}

func sliceRange(src []byte, r hcl.Range) string {
	if r.Start.Byte < 0 || r.End.Byte > len(src) || r.Start.Byte >= r.End.Byte {
		return ""
	}
	return string(src[r.Start.Byte:r.End.Byte])
}

// leadingComments returns the contiguous comment block immediately above a declaration.
// Terraform has no docstring convention, so this is the only prose a block carries and it
// is worth having in the body vector — a comment saying "bucket for CloudTrail logs" is
// often the only place the word "cloudtrail" appears near the resource.
func leadingComments(src []byte, startLine int) string {
	lines := strings.Split(string(src), "\n")
	if startLine < 2 || startLine > len(lines)+1 {
		return ""
	}
	var collected []string
	for i := startLine - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "#") {
			collected = append(collected, strings.TrimSpace(strings.TrimPrefix(t, "#")))
			continue
		}
		if strings.HasPrefix(t, "//") {
			collected = append(collected, strings.TrimSpace(strings.TrimPrefix(t, "//")))
			continue
		}
		break
	}
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	return strings.Join(collected, " ")
}

// literalString reads an attribute that must be a constant, such as a module source or a
// provider alias. Anything requiring evaluation returns "", because static parsing cannot
// honestly resolve it and guessing would put a wrong version on a module_source node.
func literalString(b *hclsyntax.Body, name string) string {
	if b == nil {
		return ""
	}
	attr, ok := b.Attributes[name]
	if !ok {
		return ""
	}
	v, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

func literalBool(b *hclsyntax.Body, name string) bool {
	if b == nil {
		return false
	}
	attr, ok := b.Attributes[name]
	if !ok {
		return false
	}
	v, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.Bool {
		return false
	}
	return v.True()
}
