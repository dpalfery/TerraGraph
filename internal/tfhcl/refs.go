package tfhcl

import (
	"strings"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// builtinScopes are traversal roots that name Terraform's own runtime, not a block in the
// configuration. They are not references and must never become edges — an edge to
// `each.value` would be noise in every for_each'd resource in the repository.
var builtinScopes = map[string]bool{
	"each":      true,
	"count":     true,
	"self":      true,
	"path":      true,
	"terraform": true,
}

// metaArgs are handled explicitly rather than as ordinary references, because each carries
// a different edge kind.
var metaArgs = map[string]bool{
	"depends_on": true,
	"providers":  true,
	"provider":   true,
	"source":     true,
	"version":    true,
}

// resolveAll turns every deferred expression into edges. It runs after all nodes exist,
// so a forward reference resolves as readily as a backward one.
func (l *loader) resolveAll() {
	index := make(map[string]*graph.Node, len(l.nodes))
	for _, n := range l.nodes {
		index[n.Key()] = n
	}

	for _, p := range l.pending {
		l.resolveBlock(index, p)
	}
	l.resolveModuleWiring(index)
	l.resolveModuleOutputs(index)
	l.resolveMoved(index)
}

// resolveModuleOutputs threads a reference through a module call to the output it selects.
//
// `module.artifacts_bucket.bucket_arn` resolves to the module *call*, because that is the
// address in the calling scope. The output name survives only in the traversal, so without
// this pass every output in every child module looks unconsumed — which would make the
// orphan report confidently wrong about the one thing it exists to detect.
func (l *loader) resolveModuleOutputs(index map[string]*graph.Node) {
	var extra []*graph.Edge

	for _, e := range l.edges {
		if e.Kind != graph.EdgeReferences || e.To == "" {
			continue
		}
		call := index[e.To]
		if call == nil || call.Kind != graph.KindModuleCall {
			continue
		}
		dir, ok := l.layout.ResolveLocalSource(call.ModuleDir, call.Source)
		if !ok {
			// A remote module's outputs are not indexed without an init, so the reference
			// stops at the call. That is a coverage limit, not a resolution failure.
			continue
		}

		parts := strings.Split(e.Traversal, ".")
		if len(parts) < 3 {
			// A bare `module.x` with no attribute selects the whole module object.
			continue
		}
		name := strings.TrimSuffix(parts[2], "[…]")

		out, found := index[graph.NodeKey(dir, "output."+name)]
		if !found {
			continue
		}
		extra = append(extra, &graph.Edge{
			Kind: graph.EdgeReferences, From: e.From, To: out.Key(),
			Traversal: e.Traversal, File: e.File, Line: e.Line,
		})
	}

	l.edges = append(l.edges, extra...)
}

func (l *loader) resolveBlock(index map[string]*graph.Node, p pendingRefs) {
	if p.body == nil {
		return
	}
	l.walkBody(index, p.owner, p.dir, p.file, p.body, map[string]bool{})
}

// walkBody descends a block body collecting references. `bound` carries the iterator names
// introduced by enclosing `dynamic` blocks: HCL sees `dynamic "ingress"` as an ordinary
// block, so a reference to `ingress.value` inside its content looks exactly like a
// reference to a resource type named `ingress`. Tracking the binding is what stops every
// dynamic block in the repository from emitting a phantom unresolved edge.
func (l *loader) walkBody(
	index map[string]*graph.Node,
	owner *graph.Node,
	dir, file string,
	body *hclsyntax.Body,
	bound map[string]bool,
) {
	for _, attr := range sortedAttrs(body) {
		switch {
		case isLifecycleBlock(owner.Kind) && (attr.Name == "from" || attr.Name == "to"):
			// resolveMoved emits these as MOVED_FROM. Letting them fall through to the
			// default arm as well would double-count: an agent asking what references
			// aws_s3_bucket.logs would be told a `moved` block does, which is true only in
			// the sense that a redirect "references" its destination.

		case attr.Name == "depends_on":
			l.emitAll(index, owner, dir, file, attr, graph.EdgeDependsOn, bound)
		case attr.Name == "provider" || attr.Name == "providers":
			l.emitAll(index, owner, dir, file, attr, graph.EdgeProvides, bound)
		case attr.Name == "replace_triggered_by":
			l.emitAll(index, owner, dir, file, attr, graph.EdgeReplaceTriggeredBy, bound)
		case metaArgs[attr.Name]:
			// source and version are literals already captured on the node.
		default:
			l.emitAll(index, owner, dir, file, attr, graph.EdgeReferences, bound)
		}
	}

	for _, b := range body.Blocks {
		inner := bound
		if b.Type == "dynamic" && len(b.Labels) > 0 {
			inner = cloneBound(bound)
			inner[b.Labels[0]] = true
			// An explicit `iterator = name` renames the binding.
			if it := literalString(b.Body, "iterator"); it != "" {
				inner[it] = true
			}
		}
		l.walkBody(index, owner, dir, file, b.Body, inner)
	}
}

// isLifecycleBlock reports whether a node is a moved/removed/import block, whose `from`
// and `to` arguments are state-management addresses rather than data dependencies.
func isLifecycleBlock(k graph.Kind) bool {
	return k == graph.KindMoved || k == graph.KindRemoved || k == graph.KindImport
}

func cloneBound(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k := range in {
		out[k] = true
	}
	return out
}

// emitAll converts every traversal in one attribute into an edge.
//
// Expression.Variables() is the whole reason this tool is not a regex. It returns only
// real traversals — a resource address written inside a comment or a quoted string is not
// one, and so produces nothing here. That is precisely the distinction grep cannot make.
func (l *loader) emitAll(
	index map[string]*graph.Node,
	owner *graph.Node,
	dir, file string,
	attr *hclsyntax.Attribute,
	kind graph.EdgeKind,
	bound map[string]bool,
) {
	for _, tr := range attr.Expr.Variables() {
		if len(tr) == 0 {
			continue
		}
		root := tr.RootName()
		if builtinScopes[root] || bound[root] {
			continue
		}

		// A provider argument uses its own address space: `provider = aws.replica` names
		// provider.aws.replica, not a resource of type "aws". Resolving it with the
		// ordinary rules leaves every aliased provider assignment permanently unresolved.
		var addr string
		var ok bool
		if kind == graph.EdgeProvides {
			addr, ok = providerAddress(tr)
		} else {
			addr, ok = traversalAddress(tr)
		}
		if !ok {
			continue
		}

		e := &graph.Edge{
			Kind:      kind,
			From:      owner.Key(),
			Traversal: traversalString(tr),
			File:      file,
			Line:      tr.SourceRange().Start.Line,
		}

		if target, found := index[graph.NodeKey(dir, addr)]; found {
			e.To = target.Key()
		} else {
			e.Unresolved = addr
		}
		l.edges = append(l.edges, e)
	}
}

// traversalAddress converts an HCL traversal into the Terraform address it names.
//
// The shapes are fixed by Terraform's own addressing rules, which is what makes this a
// resolution rather than a guess: `var.x` has exactly one attribute after the root,
// `data.type.name` has two, and a bare `type.name` is a managed resource.
func traversalAddress(tr hcl.Traversal) (string, bool) {
	root := tr.RootName()

	switch root {
	case "var", "local", "module":
		if name, ok := attrAt(tr, 1); ok {
			return root + "." + name, true
		}
		return "", false

	case "data":
		t, ok1 := attrAt(tr, 1)
		n, ok2 := attrAt(tr, 2)
		if ok1 && ok2 {
			return "data." + t + "." + n, true
		}
		return "", false

	default:
		// A managed resource: type.name. A single-element traversal is a bare identifier,
		// not an address, so it is not a reference to anything addressable.
		if name, ok := attrAt(tr, 1); ok {
			return root + "." + name, true
		}
		return "", false
	}
}

// providerAddress converts a provider argument's traversal into a provider node address.
// `aws` is the default provider for that type; `aws.replica` is the aliased one. Unlike a
// resource traversal, a single-element traversal here is meaningful rather than a bare
// identifier, which is why this cannot share traversalAddress.
func providerAddress(tr hcl.Traversal) (string, bool) {
	root := tr.RootName()
	if root == "" {
		return "", false
	}
	if alias, ok := attrAt(tr, 1); ok {
		return "provider." + root + "." + alias, true
	}
	return "provider." + root, true
}

func attrAt(tr hcl.Traversal, i int) (string, bool) {
	if i >= len(tr) {
		return "", false
	}
	a, ok := tr[i].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}
	return a.Name, true
}

// traversalString renders a traversal the way it was written, so the caller sees which
// attribute was selected — information the node key necessarily discards.
func traversalString(tr hcl.Traversal) string {
	if len(tr) == 0 {
		return ""
	}
	out := tr.RootName()
	for _, t := range tr[1:] {
		switch v := t.(type) {
		case hcl.TraverseAttr:
			out += "." + v.Name
		case hcl.TraverseIndex:
			out += "[…]"
		}
	}
	return out
}

// resolveModuleWiring connects module calls to their source and, for local sources, to the
// variables they actually feed.
//
// The INPUTS edge is what makes "who passes anything to var.instance_type" answerable.
// Without it a child module's variables look unreferenced from inside the child, because
// every real caller lives in a different directory.
func (l *loader) resolveModuleWiring(index map[string]*graph.Node) {
	for _, n := range l.nodes {
		if n.Kind != graph.KindModuleCall || n.Source == "" {
			continue
		}

		if src, ok := l.srcNodes[graph.ModuleSourceAddress(n.Source, n.Version)]; ok {
			l.edges = append(l.edges, &graph.Edge{
				Kind: graph.EdgeCalls, From: n.Key(), To: src.Key(),
				Traversal: n.Source, File: n.File, Line: n.Line,
			})
		}

		target, ok := l.layout.ResolveLocalSource(n.ModuleDir, n.Source)
		if !ok {
			continue
		}

		for _, arg := range l.moduleArgs(n) {
			if v, found := index[graph.NodeKey(target, "var."+arg.name)]; found {
				l.edges = append(l.edges, &graph.Edge{
					Kind: graph.EdgeInputs, From: n.Key(), To: v.Key(),
					Traversal: "module." + n.Name + " → var." + arg.name,
					File:      n.File, Line: arg.line,
				})
			}
		}
	}
}

type moduleArg struct {
	name string
	line int
}

// moduleArgs re-reads a module call's own attributes. The block body is not retained on
// the node, so this works from the recorded pending entry for that node.
func (l *loader) moduleArgs(n *graph.Node) []moduleArg {
	for _, p := range l.pending {
		if p.owner != n || p.body == nil {
			continue
		}
		var out []moduleArg
		for _, attr := range sortedAttrs(p.body) {
			if metaArgs[attr.Name] || attr.Name == "count" || attr.Name == "for_each" {
				continue
			}
			out = append(out, moduleArg{name: attr.Name, line: attr.NameRange.Start.Line})
		}
		return out
	}
	return nil
}

// resolveMoved marks vacated addresses.
//
// A `moved` block is the closest thing Terraform has to DocGraph's `superseded` status: it
// says, in the configuration itself, that an address is no longer where the thing lives.
// Ranking should know that, so an agent asking about the old name still finds it but is
// never handed it first.
func (l *loader) resolveMoved(index map[string]*graph.Node) {
	for _, p := range l.pending {
		if p.owner.Kind != graph.KindMoved || p.body == nil {
			continue
		}
		from, fok := singleTraversalAddress(p.body, "from")
		to, tok := singleTraversalAddress(p.body, "to")

		// Rename the node from its placeholder to what it is actually about. The loader
		// cannot do this at parse time because the addresses live in the block's body, and
		// a synthetic key built from file:line is unreadable in every output that shows it.
		// Safe here only because a moved block has no outgoing edges — walkBody skips its
		// from/to — so no earlier edge holds the old key.
		if fok {
			p.owner.Address = "moved." + from
			p.owner.Name = from
		}

		if fok {
			if old, found := index[graph.NodeKey(p.dir, from)]; found {
				old.Deprecated = true
			}
		}
		if fok && tok {
			target, found := index[graph.NodeKey(p.dir, to)]
			e := &graph.Edge{
				Kind: graph.EdgeMovedFrom, From: p.owner.Key(),
				Traversal: from + " → " + to, File: p.file, Line: p.owner.Line,
			}
			if found {
				e.To = target.Key()
			} else {
				e.Unresolved = to
			}
			l.edges = append(l.edges, e)
		}
	}
}

func singleTraversalAddress(b *hclsyntax.Body, name string) (string, bool) {
	attr, ok := b.Attributes[name]
	if !ok {
		return "", false
	}
	vars := attr.Expr.Variables()
	if len(vars) != 1 {
		return "", false
	}
	return traversalAddress(vars[0])
}
