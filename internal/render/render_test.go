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
		out := Impact(ix, addr, index.Dependents, 3, index.DefaultCharBudget)
		mustContain(t, out, "not what a plan would replace",
			"every impact answer must disclaim precision it does not have")
	}
}

func TestForAddressStatesWhatItExcludes(t *testing.T) {
	out := ForAddress(build(t), "aws_s3_bucket.logs", index.DefaultCharBudget)

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
	out := Orphans(build(t), index.DefaultCharBudget)
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
	out := Modules(build(t), "", index.DefaultCharBudget)
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

// TestEveryToolIsBounded is the regression for the defect the token benchmark found.
//
// Only Explore was ever budgeted. On a 29-stack repository, terra_for_address on a
// variable declared once per stack returned 3,814 tokens where the grep it replaces cost
// 1,438 — a retrieval tool costing more than the search it exists to avoid. Every tool now
// takes a budget, and each must actually honour it.
func TestEveryToolIsBounded(t *testing.T) {
	ix := build(t)

	cases := []struct {
		name string
		fn   func(budget int) string
	}{
		{"Explore", func(b int) string { return Explore(ix, "bucket", 20, b) }},
		{"ForAddress", func(b int) string { return ForAddress(ix, "aws_s3_bucket", b) }},
		{"Impact", func(b int) string { return Impact(ix, "var.environment", index.Dependents, 5, b) }},
		{"Modules", func(b int) string { return Modules(ix, "", b) }},
		{"Orphans", func(b int) string { return Orphans(ix, b) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			small, large := tc.fn(1000), tc.fn(60000)

			if len(small) > len(large) {
				t.Errorf("a smaller budget produced more output: 1000→%d, 60000→%d",
					len(small), len(large))
			}

			// The header, the caveat and the omission note are fixed overhead that must
			// always be emitted, so the cap is the budget plus a bounded preamble rather
			// than the budget exactly. What must not happen is output scaling with the
			// repository instead of with the budget.
			if len(small) > 4000 {
				t.Errorf("budget 1000 produced %d characters — the tool is not bounded",
					len(small))
			}
		})
	}
}

// TestTruncatedListsStateTheirTrueTotal is what makes a bounded answer safe. A caller
// shown three of twenty-five declarations must be told there are twenty-five, or it will
// reason from a partial list as though it were complete.
func TestTruncatedListsStateTheirTrueTotal(t *testing.T) {
	ix := build(t)

	out := ForAddress(ix, "aws_s3_bucket", 600)
	mustContain(t, out, "declaration(s)", "the true declaration count must survive truncation")

	orphans := Orphans(ix, 600)
	mustContain(t, orphans, "unreferenced declaration(s)", "the true orphan count must survive truncation")
	if strings.Contains(orphans, "omitted for space") {
		mustContain(t, orphans, "larger charBudget", "an omission must say how to get the rest")
	}
}

func TestEmptyQueryDoesNotPanic(t *testing.T) {
	ix := build(t)
	for _, q := range []string{"", "   ", "\n"} {
		if out := Explore(ix, q, 5, index.DefaultCharBudget); out == "" {
			t.Errorf("empty query %q produced empty output rather than a stated miss", q)
		}
	}
	if out := ForAddress(ix, "", index.DefaultCharBudget); out == "" {
		t.Error("empty address produced empty output")
	}
	if out := Impact(ix, "", index.Dependents, 3, index.DefaultCharBudget); out == "" {
		t.Error("empty impact address produced empty output")
	}
}
