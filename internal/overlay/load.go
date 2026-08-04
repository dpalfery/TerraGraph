package overlay

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// The subset of `terraform show -json` this package reads.
//
// Only the fields that answer expansion and replacement are modelled. Terraform's plan
// document also carries every attribute value of every resource, before and after — for a
// real stack that is megabytes of provider state, most of it credentials-adjacent, and
// none of it is needed to say how many instances a block has or whether it survives.
// Decoding narrowly keeps that data out of memory and out of any output.
type showDocument struct {
	FormatVersion    string `json:"format_version"`
	TerraformVersion string `json:"terraform_version"`

	// ResourceChanges is present in a plan document.
	ResourceChanges []resourceChange `json:"resource_changes"`

	// Values is present in a state document.
	Values *stateValues `json:"values"`
}

type resourceChange struct {
	Address       string `json:"address"`
	ModuleAddress string `json:"module_address"`
	Mode          string `json:"mode"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	Index         any    `json:"index"`
	ActionReason  string `json:"action_reason"`
	Change        struct {
		Actions []string `json:"actions"`
	} `json:"change"`
}

type stateValues struct {
	RootModule stateModule `json:"root_module"`
}

type stateModule struct {
	Address      string          `json:"address"`
	Resources    []stateResource `json:"resources"`
	ChildModules []stateModule   `json:"child_modules"`
}

type stateResource struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Index   any    `json:"index"`
}

// LoadFile reads one `terraform show -json` document and attaches it to a stack.
func LoadFile(stack, path string) (*Overlay, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(stack, path, raw)
}

// Parse decodes a show document. A plan and a state are told apart by which section is
// populated rather than by the file's name, because both are conventionally written to
// files called some variation of "plan.json".
func Parse(stack, source string, raw []byte) (*Overlay, error) {
	var doc showDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: not a terraform show -json document: %w", source, err)
	}

	o := &Overlay{
		Stack:            stack,
		Source:           source,
		TerraformVersion: doc.TerraformVersion,
		instances:        map[string][]Instance{},
		seenModule:       map[string]bool{},
	}

	switch {
	case len(doc.ResourceChanges) > 0:
		o.Kind = KindPlan
		for _, rc := range doc.ResourceChanges {
			o.add(rc.ModuleAddress, configAddress(rc.Mode, rc.Type, rc.Name), Instance{
				Address:      rc.Address,
				IndexKey:     formatIndex(rc.Index),
				Actions:      rc.Change.Actions,
				ActionReason: rc.ActionReason,
			})
		}

	case doc.Values != nil:
		o.Kind = KindState
		collectState(o, doc.Values.RootModule)

	default:
		return nil, fmt.Errorf(
			"%s: has neither resource_changes nor values; it is not a plan or state document", source)
	}

	if len(o.instances) == 0 {
		// An empty document is legitimate — a stack with nothing in it — but it is worth
		// distinguishing from a parse that silently matched nothing.
		o.Kind = kindOrNone(o.Kind)
	}
	return o, nil
}

func kindOrNone(k Kind) Kind {
	if k == "" {
		return KindNone
	}
	return k
}

func collectState(o *Overlay, m stateModule) {
	for _, r := range m.Resources {
		o.add(m.Address, configAddress(r.Mode, r.Type, r.Name), Instance{
			Address:  r.Address,
			IndexKey: formatIndex(r.Index),
		})
	}
	for _, child := range m.ChildModules {
		collectState(o, child)
	}
}

// add records one resource instance, and the module instances implied by where it lives.
//
// The module address is normalised because Terraform embeds the module's own expansion
// into it: a resource inside a for_each'd module reports `module.fleet["eu"]`, not
// `module.fleet`. A static parse of the HCL can only ever produce the latter, so matching
// the raw string makes every resource inside every expanded module invisible to the
// overlay — silently, and precisely in the case the overlay exists to explain.
func (o *Overlay) add(moduleAddress, configAddr string, inst Instance) {
	if configAddr == "" {
		return
	}
	normalized := normalizeModuleAddress(moduleAddress)
	k := instanceKey(normalized, configAddr)
	o.instances[k] = append(o.instances[k], inst)

	o.addModuleInstances(moduleAddress)
}

// addModuleInstances registers each module call in a chain as an addressable thing with
// instances of its own, so "how many does module.fleet expand to" is answerable.
func (o *Overlay) addModuleInstances(moduleAddress string) {
	segments := splitModuleSegments(moduleAddress)

	var parent, full string
	for _, seg := range segments {
		base, key := splitIndex(seg)
		if full == "" {
			full = seg
		} else {
			full = full + "." + seg
		}

		k := instanceKey(parent, base)
		if !o.seenModule[k+"|"+full] {
			o.seenModule[k+"|"+full] = true
			o.instances[k] = append(o.instances[k], Instance{Address: full, IndexKey: key})
		}
		parent = normalizeModuleAddress(full)
	}
}

// splitModuleSegments turns `module.a[0].module.b["x"]` into its module segments. It
// cannot split naively on '.' because a for_each key is a quoted string that may itself
// contain dots — `module.fleet["eu.west"]` is a legal address.
func splitModuleSegments(addr string) []string {
	if addr == "" {
		return nil
	}

	var parts []string
	var depth int
	var inQuote bool
	start := 0

	for i := 0; i < len(addr); i++ {
		switch c := addr[i]; {
		case c == '"':
			inQuote = !inQuote
		case inQuote:
			// Everything inside a key is opaque.
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == '.' && depth == 0:
			parts = append(parts, addr[start:i])
			start = i + 1
		}
	}
	parts = append(parts, addr[start:])

	// parts alternates "module" and the call name: ["module", "a[0]", "module", `b["x"]`].
	var segments []string
	for i := 0; i+1 < len(parts); i += 2 {
		if parts[i] != "module" {
			return nil
		}
		segments = append(segments, parts[i]+"."+parts[i+1])
	}
	return segments
}

// normalizeModuleAddress strips instance keys, producing the form a static parse yields.
func normalizeModuleAddress(addr string) string {
	segments := splitModuleSegments(addr)
	if len(segments) == 0 {
		return ""
	}
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		base, _ := splitIndex(seg)
		out = append(out, base)
	}
	return strings.Join(out, ".")
}

// splitIndex separates `module.fleet["eu"]` into `module.fleet` and `eu`.
func splitIndex(seg string) (base, key string) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		return seg, ""
	}
	closing := strings.LastIndexByte(seg, ']')
	if closing < open {
		return seg, ""
	}
	return seg[:open], strings.Trim(seg[open+1:closing], `"`)
}

// configAddress rebuilds the address as written in the configuration — without the module
// prefix and without the instance index — which is the only form a static parse of the HCL
// can produce, and therefore the only form the two sides can be joined on.
func configAddress(mode, typ, name string) string {
	if typ == "" || name == "" {
		return ""
	}
	if mode == "data" {
		return "data." + typ + "." + name
	}
	return typ + "." + name
}

// formatIndex renders count and for_each keys uniformly.
//
// JSON has one number type, so a count index arrives as a float64 and would render as
// "0" or "1" only by accident. for_each keys arrive as strings. Both are normalised here
// so nothing downstream has to care which kind of expansion produced an instance.
func formatIndex(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(t)
	}
}
