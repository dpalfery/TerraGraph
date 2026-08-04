package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/overlay"
)

func buildAt(t *testing.T, dir string) *Index {
	t.Helper()
	h := NewHost(dir)
	ix, err := h.Current()
	if err != nil {
		t.Fatalf("Current(%s): %v", dir, err)
	}
	return ix
}

func TestOverlayIsDiscoveredAndUsed(t *testing.T) {
	ix := buildAt(t, "../../testdata/overlay")

	if !ix.Overlay().IsAvailable() {
		t.Fatalf("no overlay discovered: %s", ix.Overlay().UnavailableReason())
	}
	if got := ix.Overlay().KindFor(""); got != overlay.KindPlan {
		t.Errorf("overlay kind = %q, want plan (a plan must win over the state file beside it)", got)
	}
}

// TestExpandedModuleInstancesResolve is the regression for the bug that made the overlay
// useless exactly where it matters most.
//
// Terraform reports a resource inside a for_each'd module as living in
// `module.fleet["eu"]`. A static parse can only ever produce `module.fleet`. Matching the
// raw string silently returned nothing for every resource in every expanded module.
func TestExpandedModuleInstancesResolve(t *testing.T) {
	ix := buildAt(t, "../../testdata/expanded")

	n := ix.Graph().ByKey(graph.NodeKey("child", "terraform_data.inner"))
	if n == nil {
		t.Fatal("fixture missing child/terraform_data.inner")
	}

	got := ix.InstancesOf(n)
	if len(got) != 4 {
		t.Fatalf("instances = %d, want 4 (module for_each of 2 x resource count of 2); "+
			"0 means module address normalisation regressed", len(got))
	}

	seen := map[string]bool{}
	for _, i := range got {
		seen[i.Address] = true
	}
	for _, want := range []string{
		`module.fleet["eu"].terraform_data.inner[0]`,
		`module.fleet["us"].terraform_data.inner[1]`,
	} {
		if !seen[want] {
			t.Errorf("missing instance %s; got %v", want, keysOfSet(seen))
		}
	}
}

func TestModuleCallInstancesResolve(t *testing.T) {
	ix := buildAt(t, "../../testdata/expanded")

	n := ix.Graph().ByKey(graph.NodeKey("", "module.fleet"))
	if n == nil {
		t.Fatal("fixture missing module.fleet")
	}
	got := ix.InstancesOf(n)
	if len(got) != 2 {
		t.Errorf("module.fleet instances = %d, want 2 (eu and us)", len(got))
	}
}

// TestImpactReportsReplacement is the question static configuration cannot answer at all.
func TestImpactReportsReplacement(t *testing.T) {
	ix := buildAt(t, "../../testdata/overlay")
	res := ix.Impact("terraform_data.single", Dependents, 3)

	if !res.OverlayAvailable {
		t.Fatal("impact did not see the overlay")
	}
	if res.OverlayKind != overlay.KindPlan {
		t.Errorf("OverlayKind = %q, want plan", res.OverlayKind)
	}
	if len(res.Replacing) != 1 || res.Replacing[0] != "terraform_data.single" {
		t.Errorf("Replacing = %v, want [terraform_data.single] — the origin is part of its "+
			"own blast radius", res.Replacing)
	}
}

func TestImpactReportsNoReplacementWhenPlanIsClean(t *testing.T) {
	ix := buildAt(t, "../../testdata/expanded")
	res := ix.Impact("terraform_data.inner", Dependents, 2)

	if len(res.Replacing) != 0 {
		t.Errorf("Replacing = %v, want empty for an all-create plan", res.Replacing)
	}
	if res.InstanceTotal == 0 {
		t.Error("InstanceTotal is 0 despite a loaded plan")
	}
}

// TestEverythingWorksWithoutAnOverlay is the degradation contract. The overlay is
// optional in the same sense CodeGraph is optional to DocGraph: without it every tool
// still answers, only less precisely, and says so.
func TestEverythingWorksWithoutAnOverlay(t *testing.T) {
	ix := build(t) // testdata/repo has no plan file

	if ix.Overlay().IsAvailable() {
		t.Fatal("testdata/repo unexpectedly has an overlay")
	}
	if ix.Overlay().UnavailableReason() == "" {
		t.Error("an absent overlay must explain itself")
	}

	if hits := ix.Explore("bucket", 3, DefaultCharBudget).Hits; len(hits) == 0 {
		t.Error("Explore stopped working without an overlay")
	}
	if res := ix.ForAddress("aws_s3_bucket.logs"); len(res.Declarations) == 0 {
		t.Error("ForAddress stopped working without an overlay")
	}
	if res := ix.Impact("var.environment", Dependents, 2); len(res.Nodes) == 0 {
		t.Error("Impact stopped working without an overlay")
	} else {
		if res.OverlayAvailable {
			t.Error("Impact claims an overlay it does not have")
		}
		if len(res.ExpandingBlocks) == 0 && res.InstanceTotal != 0 {
			t.Error("Impact invented instance counts without an overlay")
		}
	}
	if len(ix.Orphans()) == 0 {
		t.Error("Orphans stopped working without an overlay")
	}

	// A node that expands must still be reported as unresolved rather than as one instance.
	n := ix.Graph().ByKey(graph.NodeKey("modules/bucket", "aws_s3_bucket_lifecycle_configuration.this"))
	if n != nil && len(ix.InstancesOf(n)) != 0 {
		t.Error("InstancesOf returned instances with no overlay loaded")
	}
}

// TestTwoClockRebuild is why the host tracks its inputs separately: a `terraform plan` in
// an active session rewrites the overlay constantly, and re-parsing every .tf file to pick
// up an instance count would make the expensive half hostage to the cheap one.
func TestTwoClockRebuild(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, "../../testdata/overlay", dir)

	h := NewHost(dir)
	if _, err := h.Current(); err != nil {
		t.Fatalf("first build: %v", err)
	}
	configBuilds, overlayBuilds := h.Builds(), h.OverlayBuilds()

	// Nothing changed: neither half rebuilds.
	if _, err := h.Current(); err != nil {
		t.Fatal(err)
	}
	if h.Builds() != configBuilds || h.OverlayBuilds() != overlayBuilds {
		t.Errorf("an unchanged repository rebuilt: config %d→%d overlay %d→%d",
			configBuilds, h.Builds(), overlayBuilds, h.OverlayBuilds())
	}

	// Touch only the plan. The overlay must reload; the corpus must not.
	planPath := filepath.Join(dir, "tfplan.json")
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(planPath, future, future); err != nil {
		t.Fatal(err)
	}

	if _, err := h.Current(); err != nil {
		t.Fatal(err)
	}
	if h.Builds() != configBuilds {
		t.Errorf("re-planning forced a full config re-parse: %d→%d", configBuilds, h.Builds())
	}
	if h.OverlayBuilds() == overlayBuilds {
		t.Error("a rewritten plan file did not reload the overlay")
	}
}

// TestExplicitPlanFlag covers the path an agent uses when the file is not where discovery
// looks.
func TestExplicitPlanFlag(t *testing.T) {
	abs, err := filepath.Abs("../../testdata/overlay/tfplan.json")
	if err != nil {
		t.Fatal(err)
	}

	h := NewHost("../../testdata/expanded").WithPlan("=" + abs)
	ix, err := h.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !ix.Overlay().IsAvailable() {
		t.Errorf("explicit --plan did not load: %s", ix.Overlay().UnavailableReason())
	}
}

func TestMalformedPlanFlagIsReported(t *testing.T) {
	// Two roots and a bare path: unattributable, and must not be silently assigned.
	h := NewHost("../../testdata/repo").WithPlan("somewhere.json")
	if _, err := h.Current(); err != nil {
		t.Fatal(err)
	}
	if h.ExplicitPlanError() == nil {
		t.Error("an unattributable --plan was accepted silently")
	}
}

func keysOfSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	})
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
}
