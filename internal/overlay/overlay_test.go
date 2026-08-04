package overlay

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureDir = "../../testdata/overlay"

// The fixtures are genuine `terraform show -json` output from Terraform 1.15.6, not
// hand-written JSON. That matters: the schema has fields whose shape is easy to guess
// wrong — `index` is a number for count and a string for for_each, `module_address` is
// absent rather than empty at the root — and a fixture written from the documentation
// would agree with a parser written from the same documentation while both were wrong.

func loadPlan(t *testing.T) *Overlay {
	t.Helper()
	o, err := LoadFile("", filepath.Join(fixtureDir, "tfplan.json"))
	if err != nil {
		t.Fatalf("LoadFile plan: %v", err)
	}
	return o
}

func TestParsePlanKindAndVersion(t *testing.T) {
	o := loadPlan(t)
	if o.Kind != KindPlan {
		t.Errorf("Kind = %q, want %q", o.Kind, KindPlan)
	}
	if o.TerraformVersion == "" {
		t.Error("TerraformVersion not captured")
	}
}

func TestPlanResolvesForEachKeys(t *testing.T) {
	o := loadPlan(t)
	got := o.instances[instanceKey("", "terraform_data.fleet")]

	if len(got) != 3 {
		t.Fatalf("fleet instances = %d, want 3", len(got))
	}
	want := map[string]bool{"alpha": true, "beta": true, "gamma": true}
	for _, i := range got {
		if !want[i.IndexKey] {
			t.Errorf("unexpected for_each key %q", i.IndexKey)
		}
	}
}

// TestPlanResolvesCountIndices guards the JSON-number trap: a count index arrives as a
// float64 and renders as "0" only if something deliberately makes it.
func TestPlanResolvesCountIndices(t *testing.T) {
	o := loadPlan(t)
	got := o.instances[instanceKey("", "terraform_data.counted")]

	if len(got) != 2 {
		t.Fatalf("counted instances = %d, want 2", len(got))
	}
	for _, i := range got {
		if i.IndexKey != "0" && i.IndexKey != "1" {
			t.Errorf("count index rendered as %q, want \"0\" or \"1\" — "+
				"a float64 leaked through formatIndex", i.IndexKey)
		}
	}
}

// TestReplaceIsDetected is the fact static HCL cannot produce at all.
func TestReplaceIsDetected(t *testing.T) {
	o := loadPlan(t)
	got := o.instances[instanceKey("", "terraform_data.single")]

	if len(got) != 1 {
		t.Fatalf("single instances = %d, want 1", len(got))
	}
	if !got[0].Replaces() {
		t.Errorf("actions %v not recognised as a replacement", got[0].Actions)
	}
	if got[0].ActionSummary() != "replace" {
		t.Errorf("ActionSummary = %q, want \"replace\"", got[0].ActionSummary())
	}
	if got[0].ActionReason == "" {
		t.Error("Terraform's action_reason was dropped")
	}
}

func TestNoOpIsNotAChange(t *testing.T) {
	o := loadPlan(t)
	for _, i := range o.instances[instanceKey("", "terraform_data.fleet")] {
		if i.Changes() {
			t.Errorf("no-op instance %s reported as changing (%v)", i.Address, i.Actions)
		}
		if i.Replaces() {
			t.Errorf("no-op instance %s reported as replaced", i.Address)
		}
	}
}

// TestChildModuleAddressing checks the join key. A child module's resources are keyed by
// module_address in the plan and by directory in the graph; getting this wrong makes every
// shared module invisible to the overlay.
func TestChildModuleAddressing(t *testing.T) {
	o := loadPlan(t)
	got := o.instances[instanceKey("module.child", "terraform_data.inner")]

	if len(got) != 2 {
		t.Fatalf("module.child inner instances = %d, want 2", len(got))
	}
	if got[0].ActionSummary() != "update" {
		t.Errorf("child action = %q, want update", got[0].ActionSummary())
	}
}

func TestParseState(t *testing.T) {
	o, err := LoadFile("", filepath.Join(fixtureDir, "state.json"))
	if err != nil {
		t.Fatalf("LoadFile state: %v", err)
	}
	if o.Kind != KindState {
		t.Errorf("Kind = %q, want %q", o.Kind, KindState)
	}

	if got := len(o.instances[instanceKey("", "terraform_data.fleet")]); got != 3 {
		t.Errorf("state fleet instances = %d, want 3", got)
	}
	// A state file walks child_modules recursively rather than carrying module_address on
	// each resource, so this is a different code path from the plan.
	if got := len(o.instances[instanceKey("module.child", "terraform_data.inner")]); got != 2 {
		t.Errorf("state child instances = %d, want 2", got)
	}
	// State says nothing about pending change.
	for _, i := range o.instances[instanceKey("", "terraform_data.single")] {
		if len(i.Actions) != 0 {
			t.Errorf("state instance carries actions %v; state cannot know them", i.Actions)
		}
	}
}

func TestDiscoveryPrefersPlanOverState(t *testing.T) {
	// The fixture directory holds both. A plan can answer replace-vs-update and a state
	// cannot, so silently choosing the state would quietly lose the better answer.
	found := Discover("../../testdata", []string{"overlay"})
	if len(found) != 1 {
		t.Fatalf("Discover found %d overlays, want 1", len(found))
	}
	if filepath.Base(found[0].Path) != "tfplan.json" {
		t.Errorf("discovered %s, want tfplan.json to win over state.json", found[0].Path)
	}
}

func TestDiscoveryFindsNothingWhenAbsent(t *testing.T) {
	if found := Discover("../../testdata", []string{"repo/stacks/prod"}); len(found) != 0 {
		t.Errorf("Discover returned %v for a directory with no overlay", found)
	}
}

func TestParseExplicitForms(t *testing.T) {
	t.Run("bare path with one root", func(t *testing.T) {
		got, err := ParseExplicit("plan.json", []string{"stacks/prod"})
		if err != nil {
			t.Fatalf("ParseExplicit: %v", err)
		}
		if len(got) != 1 || got[0].Stack != "stacks/prod" || got[0].Path != "plan.json" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("bare path with several roots is refused", func(t *testing.T) {
		_, err := ParseExplicit("plan.json", []string{"a", "b"})
		if err == nil {
			t.Error("a bare path with two roots must not be silently attributed to one of them")
		}
	})

	t.Run("explicit pairs", func(t *testing.T) {
		got, err := ParseExplicit("stacks/prod=p.json, stacks/dev=d.json", []string{"stacks/prod", "stacks/dev"})
		if err != nil {
			t.Fatalf("ParseExplicit: %v", err)
		}
		if len(got) != 2 || got[1].Stack != "stacks/dev" || got[1].Path != "d.json" {
			t.Errorf("got %+v", got)
		}
	})
}

// TestUnavailableResolverIsSafe covers the degradation contract: every method must answer
// on an absent overlay rather than panic, so callers never guard just to stay alive.
func TestUnavailableResolverIsSafe(t *testing.T) {
	r := None("nothing here")

	if r.IsAvailable() {
		t.Error("None reports available")
	}
	if r.UnavailableReason() == "" {
		t.Error("None must carry a reason")
	}
	if got := r.Instances("a", "b", "c"); got != nil {
		t.Errorf("Instances = %v, want nil", got)
	}
	if got := r.Stacks(); got != nil {
		t.Errorf("Stacks = %v, want nil", got)
	}
	if r.SourceFor("a") != "" || r.KindFor("a") != KindNone {
		t.Error("None must answer SourceFor and KindFor without a source")
	}
}

// TestBrokenOverlayIsPartialNotFatal checks that one unreadable file does not discard the
// others, and is reported rather than equated with having no overlay.
func TestBrokenOverlayIsPartialNotFatal(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "tfplan.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := Build([]Found{
		{Stack: "good", Path: filepath.Join(fixtureDir, "tfplan.json")},
		{Stack: "bad", Path: bad},
	})

	if !r.IsAvailable() {
		t.Fatal("one broken file discarded a working overlay")
	}
	if len(r.Stacks()) != 1 || r.Stacks()[0] != "good" {
		t.Errorf("Stacks = %v, want [good]", r.Stacks())
	}
	if r.UnavailableReason() == "" {
		t.Error("a partial load must report the failure, not hide it behind availability")
	}
}

func TestNonShowDocumentIsRejected(t *testing.T) {
	if _, err := Parse("s", "x.json", []byte(`{"hello":"world"}`)); err == nil {
		t.Error("a JSON document that is not a show output was accepted")
	}
}
