// Package overlay resolves what static HCL cannot: how many instances a block expands
// into, and whether a change replaces a resource or updates it in place.
//
// The whole package is optional, in the same sense that CodeGraph is optional to
// kyber-weave's DocGraph. Without it every tool still works completely — only expansion
// and replace-vs-update degrade, and both say so rather than guessing. That is why the
// interface carries IsAvailable and UnavailableReason instead of returning errors: an
// absent overlay is a normal state of the world, not a failure.
package overlay

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is where an overlay's facts came from, which decides what it can answer.
type Kind string

const (
	// KindPlan is `terraform show -json <planfile>`: knows both expansion and pending
	// actions, so it can answer replace-vs-update.
	KindPlan Kind = "plan"

	// KindState is `terraform show -json`: knows expansion as it exists now, but nothing
	// about what a change would do.
	KindState Kind = "state"

	KindNone Kind = "none"
)

// Instance is one resolved instance of a configuration block.
type Instance struct {
	// Address is the full Terraform address including any index:
	// `module.child.terraform_data.inner[0]`.
	Address string

	// IndexKey is the count index or for_each key as written, empty when the block does
	// not expand.
	IndexKey string

	// Actions is the planned change, from a plan overlay only. Terraform writes a
	// two-element list for a replacement.
	Actions []string

	// ActionReason is Terraform's own explanation, e.g. replace_because_cannot_update.
	ActionReason string
}

// Replaces reports whether this instance is destroyed and recreated.
//
// Terraform expresses replacement as a pair of actions rather than a distinct verb, and
// the order encodes create_before_destroy. Both orderings mean the resource does not
// survive, which is the thing a caller is actually asking about.
func (i Instance) Replaces() bool {
	var del, create bool
	for _, a := range i.Actions {
		switch a {
		case "delete":
			del = true
		case "create":
			create = true
		}
	}
	return del && create
}

// Changes reports whether Terraform intends to do anything at all to this instance.
func (i Instance) Changes() bool {
	for _, a := range i.Actions {
		if a != "no-op" && a != "read" {
			return true
		}
	}
	return false
}

// ActionSummary renders the pending action in the terms a reader thinks in.
func (i Instance) ActionSummary() string {
	switch {
	case len(i.Actions) == 0:
		return ""
	case i.Replaces():
		return "replace"
	case len(i.Actions) == 1:
		return i.Actions[0]
	default:
		return strings.Join(i.Actions, "+")
	}
}

// Resolver answers instance and change questions for one repository.
//
// It deliberately mirrors kyber-weave's ICodeGraphResolver: an implementation that could
// not load reports IsAvailable false with a reason, and every method then returns empty
// rather than erroring, so callers never branch on availability just to avoid a panic.
type Resolver interface {
	// IsAvailable is true when at least one overlay loaded.
	IsAvailable() bool

	// UnavailableReason explains an absent or partial overlay, for status output.
	UnavailableReason() string

	// Instances returns the resolved instances of a configuration address inside a
	// module. stack is the root module directory the plan was produced from;
	// moduleAddress is the Terraform module path within it ("" for the root itself).
	Instances(stack, moduleAddress, configAddress string) []Instance

	// Stacks lists the root module directories that have an overlay loaded.
	Stacks() []string

	// SourceFor names the file a stack's overlay came from.
	SourceFor(stack string) string

	// KindFor is what that overlay can answer.
	KindFor(stack string) Kind
}

// none is the resolver used when no overlay was found.
type none struct{ reason string }

// None returns an unavailable resolver carrying an explanation.
func None(reason string) Resolver { return none{reason: reason} }

func (n none) IsAvailable() bool                   { return false }
func (n none) UnavailableReason() string           { return n.reason }
func (n none) Instances(_, _, _ string) []Instance { return nil }
func (n none) Stacks() []string                    { return nil }
func (n none) SourceFor(string) string             { return "" }
func (n none) KindFor(string) Kind                 { return KindNone }

// Set is a Resolver backed by one loaded overlay per stack.
type Set struct {
	// byStack maps a root module directory to its loaded overlay.
	byStack map[string]*Overlay

	// partial records stacks whose overlay file could not be read. A broken file is not
	// the same as an absent one and must not be silently equivalent to it.
	partial []string
}

// Overlay is the facts loaded from one `terraform show -json` document.
type Overlay struct {
	Stack  string
	Source string
	Kind   Kind

	// TerraformVersion is reported in status, because an overlay produced by a different
	// Terraform than the one in use is a plausible explanation for a surprising answer.
	TerraformVersion string

	// instances is keyed by moduleAddress + "|" + configAddress.
	instances map[string][]Instance

	// seenModule deduplicates module-instance registration, since every resource inside
	// a module re-derives the same chain.
	seenModule map[string]bool
}

// NewSet assembles a resolver from loaded overlays.
func NewSet(overlays []*Overlay, partial []string) *Set {
	s := &Set{byStack: make(map[string]*Overlay, len(overlays)), partial: partial}
	for _, o := range overlays {
		s.byStack[o.Stack] = o
	}
	return s
}

func (s *Set) IsAvailable() bool { return len(s.byStack) > 0 }

func (s *Set) UnavailableReason() string {
	if len(s.partial) > 0 {
		return fmt.Sprintf("%d overlay file(s) could not be read: %s",
			len(s.partial), strings.Join(s.partial, "; "))
	}
	if len(s.byStack) == 0 {
		return "no plan or state JSON found"
	}
	return ""
}

func (s *Set) Instances(stack, moduleAddress, configAddress string) []Instance {
	o, ok := s.byStack[stack]
	if !ok {
		return nil
	}
	return o.instances[instanceKey(moduleAddress, configAddress)]
}

func (s *Set) Stacks() []string {
	out := make([]string, 0, len(s.byStack))
	for k := range s.byStack {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Set) SourceFor(stack string) string {
	if o, ok := s.byStack[stack]; ok {
		return o.Source
	}
	return ""
}

func (s *Set) KindFor(stack string) Kind {
	if o, ok := s.byStack[stack]; ok {
		return o.Kind
	}
	return KindNone
}

// TerraformVersionFor is surfaced in status output.
func (s *Set) TerraformVersionFor(stack string) string {
	if o, ok := s.byStack[stack]; ok {
		return o.TerraformVersion
	}
	return ""
}

// Partial lists overlay files that failed to load.
func (s *Set) Partial() []string { return s.partial }

func instanceKey(moduleAddress, configAddress string) string {
	return moduleAddress + "|" + configAddress
}
