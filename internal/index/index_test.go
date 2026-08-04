package index

import (
	"strings"
	"testing"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/tfhcl"
)

const fixture = "../../testdata/repo"

func build(t *testing.T) *Index {
	t.Helper()
	g, err := tfhcl.Load(fixture)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return NewIndex(g)
}

// TestExplicitMiss is the behaviour that makes the tool safe to put in front of grep. A
// question this repository has no answer to must come back empty, not as three confident
// results the caller will then answer from.
func TestExplicitMiss(t *testing.T) {
	ix := build(t)
	for _, q := range []string{
		"how do I make a sandwich",
		"kubernetes ingress controller helm chart",
		"why does the user keep getting logged out",
	} {
		if hits := ix.Explore(q, 5, DefaultCharBudget).Hits; len(hits) != 0 {
			t.Errorf("Explore(%q) returned %d hits, want 0", q, len(hits))
			for _, h := range hits {
				t.Logf("  %.2f %s %s", h.Score, h.Node.Address, h.Node.File)
			}
		}
	}
}

// TestNaturalQuestionFindsTheResource is the regression that motivated stemming and
// dropping module `source` from identity coverage.
//
// Before both, "where does the audit log bucket live" returned three module calls tied at
// the same score: they each matched "bucket" via their source path, while the resource
// actually named `logs` failed to match "log" for want of a plural fold.
func TestNaturalQuestionFindsTheResource(t *testing.T) {
	ix := build(t)
	hits := ix.Explore("where does the audit log bucket live", 3, DefaultCharBudget).Hits

	if len(hits) == 0 {
		t.Fatal("no hits for a question the fixture answers")
	}

	var rank = -1
	for i, h := range hits {
		if h.Node.Address == "aws_s3_bucket.logs" && h.Node.ModuleDir == "stacks/prod" {
			rank = i
		}
	}
	if rank < 0 {
		t.Errorf("prod's aws_s3_bucket.logs absent from top 3: %v", addresses(hits))
	}

	for _, h := range hits {
		if h.Node.Kind == graph.KindModuleCall {
			t.Errorf("a module call (%s) outranked the resource; its source path is "+
				"leaking into identity coverage again", h.Node.Address)
		}
	}
}

func TestExactAddressRanksFirst(t *testing.T) {
	ix := build(t)
	hits := ix.Explore("aws_s3_bucket.logs", 5, DefaultCharBudget).Hits
	if len(hits) == 0 {
		t.Fatal("no hits for an exact address")
	}
	if hits[0].Node.Address != "aws_s3_bucket.logs" {
		t.Errorf("top hit = %s, want aws_s3_bucket.logs", hits[0].Node.Address)
	}
}

// TestCompoundNameReach checks the tokenizer earns its keep: nobody types
// "aws_s3_bucket_versioning", they type "bucket versioning".
func TestCompoundNameReach(t *testing.T) {
	ix := build(t)
	hits := ix.Explore("bucket versioning", 5, DefaultCharBudget).Hits
	if len(hits) == 0 {
		t.Fatal("no hits for 'bucket versioning'")
	}
	var found bool
	for _, h := range hits {
		if strings.Contains(h.Node.Address, "versioning") {
			found = true
		}
	}
	if !found {
		t.Errorf("'bucket versioning' did not surface a versioning node; got %v", addresses(hits))
	}
}

// TestBudgetIsSharedAcrossHits proves the one knob does depth and breadth: narrowing
// maxNodes must deepen each result, not merely shorten the list.
func TestBudgetIsSharedAcrossHits(t *testing.T) {
	ix := build(t)

	wide := ix.Explore("bucket", 8, 4000).Hits
	narrow := ix.Explore("bucket", 1, 4000).Hits

	if len(wide) < 2 {
		t.Skip("fixture too small to exercise budget splitting")
	}
	if len(narrow) != 1 {
		t.Fatalf("narrow returned %d hits, want 1", len(narrow))
	}

	widestShare := 0
	for _, h := range wide {
		if len(h.Excerpt.Text) > widestShare {
			widestShare = len(h.Excerpt.Text)
		}
	}
	if narrow[0].Excerpt.Truncated && widestShare >= len(narrow[0].Excerpt.Text) {
		t.Errorf("narrowing maxNodes did not deepen the excerpt: narrow=%d widest-of-wide=%d",
			len(narrow[0].Excerpt.Text), widestShare)
	}
}

// TestBudgetBoundsNodeCount is the regression for a knob that named a limit it did not
// enforce. Flooring every node's share without also capping how many nodes there are let
// a charBudget of 1000 return five nodes at the 600-char floor — three times what was asked
// for, silently.
func TestBudgetBoundsNodeCount(t *testing.T) {
	ix := build(t)

	tight := ix.Explore("bucket", 8, 1000)
	if len(tight.Hits) > 1000/minPerNodeBudget {
		t.Errorf("charBudget=1000 returned %d nodes; the floor of %d allows at most %d",
			len(tight.Hits), minPerNodeBudget, 1000/minPerNodeBudget)
	}

	// And the caller must be told the budget is what cut the list, not a thin index.
	if tight.AboveThreshold > len(tight.Hits) && tight.DroppedForBudget == 0 {
		t.Error("nodes were dropped but DroppedForBudget is zero, so the caller cannot tell " +
			"a tight budget from an exhausted index")
	}

	roomy := ix.Explore("bucket", 8, 40000)
	if len(roomy.Hits) <= len(tight.Hits) {
		t.Errorf("a larger budget did not return more nodes: tight=%d roomy=%d",
			len(tight.Hits), len(roomy.Hits))
	}
	if roomy.DroppedForBudget != 0 {
		t.Errorf("a 40000-char budget still dropped %d nodes", roomy.DroppedForBudget)
	}
}

func TestTruncationIsDeclared(t *testing.T) {
	ix := build(t)
	hits := ix.Explore("aws_s3_bucket.logs", 1, minCharBudget).Hits
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	e := hits[0].Excerpt
	if e.Truncated && e.FullLen <= len(e.Text) {
		t.Error("excerpt claims truncation but reports no omitted content")
	}
	if !e.Truncated && e.FullLen != len(e.Text) {
		t.Error("excerpt claims completeness but FullLen disagrees with Text")
	}
}

// TestExamplesAreDemoted checks that illustrative configuration loses to operative
// configuration on an otherwise equal query — but is still reachable.
func TestExamplesAreDemoted(t *testing.T) {
	ix := build(t)
	hits := ix.Explore("bucket module", 10, DefaultCharBudget).Hits

	var exampleRank, prodRank = -1, -1
	for i, h := range hits {
		if strings.HasPrefix(h.Node.File, "examples/") && exampleRank < 0 {
			exampleRank = i
		}
		if strings.HasPrefix(h.Node.File, "stacks/prod") && prodRank < 0 {
			prodRank = i
		}
	}
	if exampleRank >= 0 && prodRank >= 0 && exampleRank < prodRank {
		t.Errorf("example ranked above operative config (example=%d prod=%d): %v",
			exampleRank, prodRank, addresses(hits))
	}
}

// TestForAddressExcludesTextMentions is the reverse lookup's version of the loader test:
// what the caller is shown must be uses, never mentions.
func TestForAddressExcludesTextMentions(t *testing.T) {
	ix := build(t)
	res := ix.ForAddress("aws_s3_bucket.logs")

	if len(res.Declarations) != 2 {
		t.Fatalf("declarations = %d, want 2 (prod and dev)", len(res.Declarations))
	}

	var prodKey string
	for _, d := range res.Declarations {
		if d.ModuleDir == "stacks/prod" {
			prodKey = d.Key()
		}
	}
	var refs int
	for _, r := range res.ReferencedBy[prodKey] {
		if r.Edge.Kind == graph.EdgeReferences {
			refs++
		}
	}
	if refs != 2 {
		t.Errorf("prod aws_s3_bucket.logs referenced by %d expressions, want 2", refs)
	}
}

func TestForAddressAcceptsBareName(t *testing.T) {
	ix := build(t)
	if res := ix.ForAddress("logs"); len(res.Declarations) == 0 {
		t.Error("bare local name 'logs' resolved to nothing")
	}
	if res := ix.ForAddress("aws_s3_bucket"); len(res.Declarations) == 0 {
		t.Error("type 'aws_s3_bucket' resolved to nothing")
	}
}

func TestImpactWalksDependents(t *testing.T) {
	ix := build(t)
	res := ix.Impact("var.environment", Dependents, 3)

	if len(res.Origin) == 0 {
		t.Fatal("no origin for var.environment")
	}
	if len(res.Nodes) == 0 {
		t.Fatal("var.environment has no dependents, but locals and module calls read it")
	}

	// prod's local.common_tags reads var.environment directly.
	var foundTags bool
	for _, n := range res.Nodes {
		if n.Node.Address == "local.common_tags" {
			foundTags = true
		}
	}
	if !foundTags {
		t.Errorf("local.common_tags missing from var.environment dependents: %v", impactAddrs(res))
	}
}

func TestImpactDependenciesIsTheOtherDirection(t *testing.T) {
	ix := build(t)
	down := ix.Impact("aws_s3_bucket_policy.logs", Dependencies, 2)

	var found bool
	for _, n := range down.Nodes {
		if n.Node.Address == "aws_s3_bucket.logs" {
			found = true
		}
	}
	if !found {
		t.Errorf("aws_s3_bucket_policy.logs does not depend on aws_s3_bucket.logs: %v", impactAddrs(down))
	}
}

// TestModulesFindsVersionSkew is the query grep genuinely cannot do: the source and the
// version sit on different lines of different files in different directories.
func TestModulesFindsVersionSkew(t *testing.T) {
	ix := build(t)
	usages := ix.Modules("vpc")

	if len(usages) == 0 {
		t.Fatal("no module usage found for 'vpc'")
	}
	u := usages[0]
	if u.Source != "terraform-aws-modules/vpc/aws" {
		t.Fatalf("first usage = %s, want the vpc registry module", u.Source)
	}
	if len(u.Versions) != 2 {
		t.Errorf("vpc pinned at %d versions, want 2 (5.1.0 in prod, 4.0.2 in dev)", len(u.Versions))
	}
	if _, ok := u.Versions["5.1.0"]; !ok {
		t.Error("missing prod's 5.1.0 pin")
	}
	if _, ok := u.Versions["4.0.2"]; !ok {
		t.Error("missing dev's 4.0.2 pin")
	}
}

func TestModulesSkewFirst(t *testing.T) {
	ix := build(t)
	usages := ix.Modules("")
	if len(usages) < 2 {
		t.Skip("need at least two sources")
	}
	if len(usages[0].Versions) < len(usages[len(usages)-1].Versions) {
		t.Error("skewed sources are not sorted first")
	}
}

// TestOrphansDistinguishWiredFromUnwired is the finding a linter misses: a variable every
// caller dutifully passes and nothing inside ever reads.
func TestOrphansDistinguishWiredFromUnwired(t *testing.T) {
	ix := build(t)
	orphans := ix.Orphans()

	byAddr := map[string]Orphan{}
	for _, o := range orphans {
		byAddr[o.Node.ModuleDir+"|"+o.Node.Address] = o
	}

	if _, ok := byAddr["modules/bucket|var.unused_legacy_flag"]; !ok {
		t.Errorf("var.unused_legacy_flag not reported as an orphan; got %v", orphanAddrs(orphans))
	}
	if _, ok := byAddr["modules/bucket|output.versioning_status"]; !ok {
		t.Errorf("output.versioning_status not reported as an orphan; got %v", orphanAddrs(orphans))
	}

	// A root module's outputs are its public surface and must never be called orphans.
	for _, o := range orphans {
		if o.Node.Kind == graph.KindOutput && o.Node.ModuleDir == "stacks/prod" {
			t.Errorf("root output %s wrongly reported as an orphan", o.Node.Address)
		}
	}
}

func TestOrphansExcludeConsumedOutputs(t *testing.T) {
	ix := build(t)
	for _, o := range ix.Orphans() {
		if o.Node.ModuleDir == "modules/bucket" && o.Node.Address == "output.bucket_arn" {
			t.Error("output.bucket_arn is consumed by stacks/prod but reported as an orphan — " +
				"module output references are not being threaded through the call")
		}
	}
}

func TestAuthorityDemotions(t *testing.T) {
	deprecated := &graph.Node{Kind: graph.KindResource, File: "stacks/prod/main.tf", Deprecated: true}
	example := &graph.Node{Kind: graph.KindResource, File: "examples/simple/main.tf"}
	plain := &graph.Node{Kind: graph.KindResource, File: "stacks/prod/main.tf"}

	if Authority(plain) != 1.0 {
		t.Errorf("operative config authority = %v, want 1.0", Authority(plain))
	}
	if Authority(deprecated) >= Authority(plain) {
		t.Error("a moved-away address is not demoted")
	}
	if Authority(example) >= Authority(plain) {
		t.Error("example config is not demoted")
	}
}

func addresses(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Node.Address)
	}
	return out
}

func impactAddrs(r ImpactResult) []string {
	out := make([]string, 0, len(r.Nodes))
	for _, n := range r.Nodes {
		out = append(out, n.Node.Address)
	}
	return out
}

func orphanAddrs(os []Orphan) []string {
	out := make([]string, 0, len(os))
	for _, o := range os {
		out = append(out, o.Node.ModuleDir+"|"+o.Node.Address)
	}
	return out
}
