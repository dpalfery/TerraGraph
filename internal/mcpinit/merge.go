package mcpinit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// exec_LookPath is aliased so the resolver stays testable and the import is obvious.
var exec_LookPath = exec.LookPath

// mergeJSON adds or replaces one server entry, leaving everything else byte-identical.
//
// Every level is decoded into map[string]json.RawMessage rather than a typed struct, so
// keys this tool has never heard of survive untouched. That matters more than it sounds:
// these files hold other people's servers, editor settings and schema references, and a
// round-trip through a struct would quietly delete all of them.
//
// Key order does change — Go sorts map keys — but the values do not.
func mergeJSON(existing []byte, container, name string, entry any) (out []byte, changed bool, err error) {
	root := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, false, fmt.Errorf("not strict JSON (%v)", cleanJSONError(err))
		}
	}

	inner := map[string]json.RawMessage{}
	if raw, ok := root[container]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, false, fmt.Errorf("%q is not an object", container)
		}
	}

	next, err := json.Marshal(entry)
	if err != nil {
		return nil, false, err
	}

	if prev, ok := inner[name]; ok && jsonEqual(prev, next) {
		// Already correct. Reporting "no change" is better than rewriting the file and
		// producing a diff that is entirely key reordering.
		return existing, false, nil
	}

	inner[name] = next
	innerRaw, err := json.Marshal(inner)
	if err != nil {
		return nil, false, err
	}
	root[container] = innerRaw

	buf, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(buf, '\n'), true, nil
}

// jsonEqual compares two encodings semantically, so whitespace or key order in a
// hand-written file is not mistaken for a difference.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}

// snippetJSON renders just the fragment a person needs to paste when the file cannot be
// rewritten automatically.
func snippetJSON(container, name string, entry any) (string, error) {
	doc := map[string]any{container: map[string]any{name: entry}}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func cleanJSONError(err error) string {
	msg := err.Error()
	// The stock message names a byte offset, which is not actionable in a file the user
	// is about to open by hand.
	if i := strings.Index(msg, " (offset"); i > 0 {
		msg = msg[:i]
	}
	return msg
}

// tomlSectionHeader is the section Codex reads for one server.
func tomlSectionHeader(name string) string {
	return "[mcp_servers." + name + "]"
}

// mergeTOML adds or replaces one [mcp_servers.<name>] section.
//
// It is a line-scanning edit rather than a parse-and-re-emit, and deliberately so. There is
// no TOML library in this module, and adding one to write six lines would be a poor trade;
// more importantly a real TOML round-trip would reformat and de-comment a file that is
// otherwise none of TerraGraph's business. Replacing exactly one section leaves the rest
// byte-identical.
func mergeTOML(existing []byte, name, command string, args []string) (out []byte, changed bool, err error) {
	desired := renderTOMLSection(name, command, args)

	if len(bytes.TrimSpace(existing)) == 0 {
		return []byte(desired), true, nil
	}

	lines := strings.Split(string(existing), "\n")
	header := tomlSectionHeader(name)

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == header {
			start = i
			break
		}
	}

	if start < 0 {
		body := strings.TrimRight(string(existing), "\n")
		return []byte(body + "\n\n" + desired), true, nil
	}

	// The section runs until the next top-level table header. A sub-table of our own
	// server — [mcp_servers.terragraph.env] — belongs to us and is replaced with it.
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && !strings.HasPrefix(t, header+".") {
			end = i
			break
		}
	}

	current := strings.Join(lines[start:end], "\n")
	if strings.TrimSpace(current) == strings.TrimSpace(desired) {
		return existing, false, nil
	}

	rebuilt := append([]string{}, lines[:start]...)
	rebuilt = append(rebuilt, strings.TrimRight(desired, "\n"))
	if end < len(lines) {
		rebuilt = append(rebuilt, "")
		rebuilt = append(rebuilt, lines[end:]...)
	}
	joined := strings.TrimRight(strings.Join(rebuilt, "\n"), "\n")
	return []byte(joined + "\n"), true, nil
}

func renderTOMLSection(name, command string, args []string) string {
	var b strings.Builder
	b.WriteString(tomlSectionHeader(name))
	b.WriteString("\n")
	fmt.Fprintf(&b, "command = %s\n", tomlString(command))
	if len(args) > 0 {
		quoted := make([]string, 0, len(args))
		for _, a := range args {
			quoted = append(quoted, tomlString(a))
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
	}
	return b.String()
}

// tomlString quotes a basic TOML string. Paths on Windows contain backslashes, which are
// escapes in a TOML basic string and would otherwise corrupt the command.
func tomlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}
