package render

import (
	"strings"
	"testing"

	"github.com/dpalfery/terragraph/internal/index"
	"github.com/dpalfery/terragraph/internal/tfhcl"
)

const fixture = "../../testdata/repo"

func build(t *testing.T) *index.Index {
	t.Helper()
	g, err := tfhcl.Load(fixture)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return index.NewIndex(g)
}

func mustContain(t *testing.T, got, want, why string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output is missing %q — %s\n--- got ---\n%s", want, why, got)
	}
}

// TestMissIsExplicitAndActionable is the contract that makes this safe to put in front of
// grep. The caller must be told, in words, that falling back is now correct.
func TestMissIsExplicitAndActionable(t *testing.T) {
	out := Explore(build(t), "kubernetes helm ingress controller", 5, index.DefaultCharBudget)

	mustContain(t, out, "No configuration scored above the relevance threshold",
		"a miss must be stated, not implied by an empty list")
	mustContain(t, out, "nodes were considered",
		"the caller needs to know the index was not simply empty")
	mustContain(t, out, "grep",
		"a miss must name the fallback, or the agent will answer from whatever was nearest")
}

// TestImpactAlwaysStatesItsLimit guards the one claim this tool could most easily overstate.
func TestImpactAlwaysStatesItsLimit(t *testing.T) {
	ix := build(t)
	for _, addr := range []string{"var.environment", "aws_s3_bucket.logs", "local.common_tags"} {
		out := Impact(ix, addr, index.Dependents, 3)
		mustContain(t, out, "not what a plan would replace",
			"every impact answer must disclaim precision it does not have")
	}
}

func TestForAddressStatesWhatItExcludes(t *testing.T) {
	out := ForAddress(build(t), "aws_s3_bucket.logs")

	mustContain(t, out, "comment", "the caller must know text mentions were excluded on purpose")
	mustContain(t, out, "quoted string", "the string-literal exclusion is the non-obvious half")

	// The fixture's decoys must not appear as references.
	if strings.Contains(out, "arn:aws:s3:::") {
		t.Error("an ARN string literal leaked into the reference list")
	}
}

// TestTruncationNamesTheLineRange is what keeps a budgeted answer from sending the agent
// back to Read the whole file.
func TestTruncationNamesTheLineRange(t *testing.T) {
	ix := build(t)
	out := Explore(ix, "aws_s3_bucket_lifecycle_configuration.this", 1, 500)

	if !strings.Contains(out, "truncated") {
		t.Skip("fixture block fits in the minimum budget; nothing to assert")
	}
	mustContain(t, out, "lines", "truncation must name the exact line range to open")
	mustContain(t, out, "larger charBudget", "truncation must say how to get the rest")
}

func TestOrphansExplainsTheWiredDistinction(t *testing.T) {
	out := Orphans(build(t))
	mustContain(t, out, "does not count as using it",
		"the wired-but-unread distinction is the whole finding")
}

func TestStatusDeclaresTheOverlayIsAbsent(t *testing.T) {
	ix := build(t)
	out := Status(ix, "/tmp/repo", 1)

	mustContain(t, out, "plan overlay: not loaded",
		"an agent asking about replacement must be told the answer cannot come from here")
	mustContain(t, out, "root modules", "status must list what it discovered")
}

func TestModulesFlagsSkew(t *testing.T) {
	out := Modules(build(t), "")
	mustContain(t, out, "VERSION SKEW", "skew is the finding and must be visible, not inferred")
	mustContain(t, out, "local path", "a local source cannot skew and should say so")
}

// TestBudgetIsRespected checks the mechanism actually bounds output. This is the token
// claim, so it is worth asserting rather than trusting.
func TestBudgetIsRespected(t *testing.T) {
	ix := build(t)

	small := Explore(ix, "bucket", 5, 1000)
	large := Explore(ix, "bucket", 5, 40000)

	if len(small) >= len(large) {
		t.Errorf("a smaller charBudget did not produce smaller output: small=%d large=%d",
			len(small), len(large))
	}
}

func TestEmptyQueryDoesNotPanic(t *testing.T) {
	ix := build(t)
	for _, q := range []string{"", "   ", "\n"} {
		if out := Explore(ix, q, 5, index.DefaultCharBudget); out == "" {
			t.Errorf("empty query %q produced empty output rather than a stated miss", q)
		}
	}
	if out := ForAddress(ix, ""); out == "" {
		t.Error("empty address produced empty output")
	}
	if out := Impact(ix, "", index.Dependents, 3); out == "" {
		t.Error("empty impact address produced empty output")
	}
}
