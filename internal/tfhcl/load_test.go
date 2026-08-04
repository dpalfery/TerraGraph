package tfhcl

import (
	"testing"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const fixture = "../../testdata/repo"

func load(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := Load(fixture)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return g
}

// parseTraversal compiles a bare expression and returns its single traversal, so address
// extraction can be tested without a whole fixture file per shape.
func parseTraversal(t *testing.T, expr string) hcl.Traversal {
	t.Helper()
	e, diags := hclsyntax.ParseExpression([]byte(expr), "test.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatalf("ParseExpression(%q): %s", expr, diags.Error())
	}
	vars := e.Variables()
	if len(vars) != 1 {
		t.Fatalf("ParseExpression(%q) yielded %d traversals, want 1", expr, len(vars))
	}
	return vars[0]
}

// TestReferencesAreTraversalsNotText is the test the whole tool exists to pass.
//
// testdata names aws_s3_bucket.logs five times in stacks/prod: twice in a comment, once
// inside a quoted ARN, and twice as a real traversal. grep sees five. An index built on
// expression traversals must see exactly two — anything else means it is a slower grep.
func TestReferencesAreTraversalsNotText(t *testing.T) {
	g := load(t)
	key := graph.NodeKey("stacks/prod", "aws_s3_bucket.logs")

	if g.ByKey(key) == nil {
		t.Fatalf("fixture missing node %s", key)
	}

	var refs, moved int
	for _, e := range g.Incoming(key) {
		switch e.Kind {
		case graph.EdgeReferences:
			refs++
		case graph.EdgeMovedFrom:
			moved++
		}
	}

	if refs != 2 {
		t.Errorf("REFERENCES into aws_s3_bucket.logs = %d, want 2 "+
			"(aws_s3_bucket_policy.logs and output.logs_bucket_id). "+
			"A count of 4 or 5 means comment or string text leaked into the graph.", refs)
		for _, e := range g.Incoming(key) {
			t.Logf("  %s from %s at %s:%d (%s)", e.Kind, e.From, e.File, e.Line, e.Traversal)
		}
	}
	if moved != 1 {
		t.Errorf("MOVED_FROM into aws_s3_bucket.logs = %d, want 1", moved)
	}
}

// TestModuleScopeIsolation proves addresses do not leak across module directories. Both
// stacks declare aws_s3_bucket.logs; a reference in prod must never resolve into dev.
func TestModuleScopeIsolation(t *testing.T) {
	g := load(t)

	if got := len(g.ByAddress("aws_s3_bucket.logs")); got != 2 {
		t.Fatalf("aws_s3_bucket.logs declared in %d scopes, want 2", got)
	}

	devKey := graph.NodeKey("stacks/dev", "aws_s3_bucket.logs")
	if n := len(g.Incoming(devKey)); n != 0 {
		t.Errorf("dev's aws_s3_bucket.logs has %d incoming edges, want 0 — "+
			"prod's references must not resolve across scopes", n)
	}
}

func TestTraversalAddressShapes(t *testing.T) {
	// Exercised through a synthetic module so the table stays readable.
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"variable", "var.environment", "var.environment"},
		{"local", "local.common_tags", "local.common_tags"},
		{"local indexed", `local.common_tags["Environment"]`, "local.common_tags"},
		{"resource attr", "aws_s3_bucket.logs.id", "aws_s3_bucket.logs"},
		{"resource indexed", "aws_instance.web[0].id", "aws_instance.web"},
		{"data source", "data.aws_caller_identity.current.account_id", "data.aws_caller_identity.current"},
		{"module output", "module.vpc.subnet_ids", "module.vpc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := parseTraversal(t, tc.expr)
			got, ok := traversalAddress(tr)
			if !ok {
				t.Fatalf("traversalAddress(%q) not ok", tc.expr)
			}
			if got != tc.want {
				t.Errorf("traversalAddress(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

func TestBuiltinScopesAreNotReferences(t *testing.T) {
	for _, expr := range []string{
		"each.value", "count.index", "self.id", "path.module", "terraform.workspace",
	} {
		tr := parseTraversal(t, expr)
		if !builtinScopes[tr.RootName()] {
			t.Errorf("%q rooted at %q is not recognised as a builtin scope", expr, tr.RootName())
		}
	}
}

// TestDynamicIteratorIsNotAResource guards the noisiest possible false positive: HCL sees
// `dynamic "rule"` as an ordinary block, so `rule.value.id` inside it looks exactly like a
// reference to a resource type named "rule".
func TestDynamicIteratorIsNotAResource(t *testing.T) {
	g := load(t)
	for _, e := range g.Edges() {
		if e.Unresolved == "rule.value" || e.Unresolved == "rule.id" || e.Unresolved == "rule.days" {
			t.Errorf("dynamic iterator leaked as a reference: %+v", e)
		}
	}
}

func TestRootDiscovery(t *testing.T) {
	g := load(t)
	want := map[string]bool{"stacks/prod": true, "stacks/dev": true, "examples/simple": true}

	if len(g.Roots) != len(want) {
		t.Errorf("roots = %v, want %d entries", g.Roots, len(want))
	}
	for _, r := range g.Roots {
		if !want[r] {
			t.Errorf("unexpected root %q", r)
		}
	}
	// modules/bucket is called by three directories, so it is a child, never a root.
	for _, r := range g.Roots {
		if r == "modules/bucket" {
			t.Error("modules/bucket classified as a root, but three stacks call it")
		}
	}
}

// TestSharedModuleHasNoStack proves the loader refuses to guess. modules/bucket is called
// from prod, dev and examples, so attributing it to any one of them would make every
// stack-scoped answer about it quietly wrong.
func TestSharedModuleHasNoStack(t *testing.T) {
	g := load(t)
	n := g.ByKey(graph.NodeKey("modules/bucket", "aws_s3_bucket.this"))
	if n == nil {
		t.Fatal("fixture missing modules/bucket aws_s3_bucket.this")
	}
	if n.Stack != "" {
		t.Errorf("shared module node claims stack %q, want empty", n.Stack)
	}
}

func TestModuleVersionSkew(t *testing.T) {
	g := load(t)

	var versions []string
	for _, n := range g.Nodes() {
		if n.Kind == graph.KindModuleSource && n.Source == "terraform-aws-modules/vpc/aws" {
			versions = append(versions, n.Version)
		}
	}
	if len(versions) != 2 {
		t.Fatalf("vpc module_source nodes = %v, want two distinct versions", versions)
	}
	if versions[0] == versions[1] {
		t.Errorf("both vpc sources at %q; the skew fixture is not being distinguished", versions[0])
	}
}

func TestModuleInputsWireToChildVariables(t *testing.T) {
	g := load(t)
	// modules/bucket's var.purpose is fed by all three callers.
	key := graph.NodeKey("modules/bucket", "var.purpose")

	var inputs int
	for _, e := range g.Incoming(key) {
		if e.Kind == graph.EdgeInputs {
			inputs++
		}
	}
	if inputs != 4 {
		t.Errorf("INPUTS into modules/bucket var.purpose = %d, want 4 "+
			"(prod artifacts, prod backups, dev artifacts, examples simple)", inputs)
		for _, e := range g.Incoming(key) {
			t.Logf("  %s from %s", e.Kind, e.From)
		}
	}
}

func TestOrphanVariableHasNoReferences(t *testing.T) {
	g := load(t)
	key := graph.NodeKey("modules/bucket", "var.unused_legacy_flag")
	if g.ByKey(key) == nil {
		t.Fatal("fixture missing var.unused_legacy_flag")
	}
	if n := len(g.Incoming(key)); n != 0 {
		t.Errorf("var.unused_legacy_flag has %d incoming edges, want 0", n)
	}
}

func TestMovedBlockMarksAddressDeprecated(t *testing.T) {
	g := load(t)
	// aws_s3_bucket.audit is only named by the moved block, so it has no node of its own.
	// What must be true is that the moved edge exists and names it.
	var found bool
	for _, e := range g.Edges() {
		if e.Kind == graph.EdgeMovedFrom && e.Traversal == "aws_s3_bucket.audit → aws_s3_bucket.logs" {
			found = true
		}
	}
	if !found {
		t.Error("moved block did not produce a MOVED_FROM edge naming both addresses")
	}
}

func TestAliasedProviderIsAddressable(t *testing.T) {
	g := load(t)
	if n := g.ByKey(graph.NodeKey("stacks/prod", "provider.aws.replica")); n == nil {
		t.Error("aliased provider not addressable as provider.aws.replica")
	}
	if n := g.ByKey(graph.NodeKey("stacks/prod", "provider.aws")); n == nil {
		t.Error("default provider not addressable as provider.aws")
	}
}

// TestProviderAssignmentResolves covers the one address space that does not follow the
// resource rules: `provider = aws.replica` names provider.aws.replica.
func TestProviderAssignmentResolves(t *testing.T) {
	g := load(t)
	aliasKey := graph.NodeKey("stacks/prod", "provider.aws.replica")

	var provides int
	for _, e := range g.Incoming(aliasKey) {
		if e.Kind == graph.EdgeProvides {
			provides++
		}
	}
	if provides != 1 {
		t.Errorf("PROVIDES into provider.aws.replica = %d, want 1 (aws_s3_bucket.replica)", provides)
	}

	if u := g.UnresolvedCount(); u != 0 {
		t.Errorf("unresolved references = %d, want 0 in a fixture with no remote module outputs", u)
		for _, e := range g.Edges() {
			if e.To == "" {
				t.Logf("  %s %q from %s", e.Kind, e.Unresolved, e.From)
			}
		}
	}
}

func TestLeadingCommentsBecomeDoc(t *testing.T) {
	g := load(t)
	n := g.ByKey(graph.NodeKey("modules/bucket", "var.purpose"))
	if n == nil {
		t.Fatal("fixture missing modules/bucket var.purpose")
	}
	// The description is inside the block; the doc comes from above it. Here there is no
	// leading comment, so Doc must be empty rather than picking up an unrelated line.
	if n.Doc != "" {
		t.Errorf("var.purpose Doc = %q, want empty", n.Doc)
	}

	m := g.ByKey(graph.NodeKey("stacks/prod", "module.vpc"))
	if m == nil {
		t.Fatal("fixture missing stacks/prod module.vpc")
	}
	if m.Doc == "" {
		t.Error("module.vpc has a leading comment in the fixture but Doc is empty")
	}
}

func TestNoParseErrorsInFixture(t *testing.T) {
	g := load(t)
	if len(g.ParseErrors) != 0 {
		t.Errorf("fixture failed to parse cleanly: %v", g.ParseErrors)
	}
}
