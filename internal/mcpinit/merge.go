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
		// json.Unmarshal leaves the destination nil for a JSON null. Treat that as an empty
		// object so a file that is literally `null` does not panic on the first write.
		if root == nil {
			root = map[string]json.RawMessage{}
		}
	}

	inner := map[string]json.RawMessage{}
	if raw, ok := root[container]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, false, fmt.Errorf("%q is not an object", container)
		}
		// The same null trap at the container level: {"mcpServers": null} is valid JSON and
		// a reasonable "clear this key" shape, but assigning into a nil map panics.
		if inner == nil {
			inner = map[string]json.RawMessage{}
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

// tomlServersTable is the table Codex keeps its MCP servers under.
const tomlServersTable = "mcp_servers"

// tomlSectionHeader is the section Codex reads for one server, in the spelling this tool
// writes. It is for rendering and display only — never for finding a section, because the
// same table has many equally valid spellings. Use tomlKeyEqual against a parsed header.
func tomlSectionHeader(name string) string {
	return "[" + tomlServersTable + "." + name + "]"
}

// mergeTOML adds or replaces one [mcp_servers.<name>] section.
//
// It is a line-scanning edit rather than a parse-and-re-emit, and deliberately so. There is
// no TOML library in this module, and adding one to write six lines would be a poor trade;
// more importantly a real TOML round-trip would reformat and de-comment a file that is
// otherwise none of TerraGraph's business. Replacing exactly one section leaves the rest
// byte-identical.
//
// What that costs is the header match, which has to be a small parser rather than a string
// comparison. TOML spells one table many ways — a trailing comment, spaces inside the
// brackets, a quoted segment — and a scanner that recognises only its own spelling fails to
// find a section that is right there and appends a second copy. Two tables with the same key
// is a parse error, so the result is not an untidy file, it is a config Codex stops loading.
// For the same reason a file that already contains several copies is collapsed back to one.
//
// Only command and args belong to TerraGraph. Anything else inside the section — foreign
// keys, comments, and sub-tables such as [mcp_servers.<name>.env] — is carried across, in
// keeping with the rest of this file: we edit our own entry, not the user's config.
func mergeTOML(existing []byte, name, command string, args []string) (out []byte, changed bool, err error) {
	desired := renderTOMLSection(name, command, args)

	if len(bytes.TrimSpace(existing)) == 0 {
		return []byte(desired), true, nil
	}

	lines := strings.Split(string(existing), "\n")
	ours := []string{tomlServersTable, name}
	spans := findTOMLSections(lines, ours)

	if len(spans) == 0 {
		body := strings.TrimRight(string(existing), "\n")
		return []byte(body + "\n\n" + desired), true, nil
	}

	// The header line is left exactly as written. Whatever spelling or trailing comment it
	// carries is the user's, and it already names the right table.
	section := lines[spans[0].start] + "\n" + strings.TrimRight(renderTOMLBody(command, args), "\n")
	if kept := keepTOMLSectionExtras(lines[spans[0].start+1 : spans[0].end]); len(kept) > 0 {
		section += "\n" + strings.Join(kept, "\n")
	}

	if len(spans) == 1 {
		current := strings.Join(lines[spans[0].start:spans[0].end], "\n")
		if strings.TrimSpace(current) == strings.TrimSpace(section) {
			return existing, false, nil
		}
	}

	// The first occurrence is rewritten in place; any later duplicate is dropped, since a
	// file holding two of them cannot be parsed as it stands.
	var rebuilt []string
	next := 0
	for i := 0; i < len(lines); i++ {
		if next < len(spans) && i == spans[next].start {
			if next == 0 {
				rebuilt = append(rebuilt, strings.Split(section, "\n")...)
			}
			i = spans[next].end - 1
			next++
			continue
		}
		rebuilt = append(rebuilt, lines[i])
	}

	joined := strings.TrimRight(strings.Join(rebuilt, "\n"), "\n")
	return []byte(joined + "\n"), true, nil
}

// tomlSpan is one [mcp_servers.<name>] section: its header line, and every line after it up
// to (but not including) end.
type tomlSpan struct{ start, end int }

// findTOMLSections locates every section naming key, in order. More than one is possible only
// in a file that is already invalid, which is exactly the file this needs to be able to fix.
func findTOMLSections(lines, key []string) []tomlSpan {
	var spans []tomlSpan
	for i := 0; i < len(lines); i++ {
		got, ok := parseTOMLTableHeader(lines[i])
		if !ok || !tomlKeyEqual(got, key) {
			continue
		}
		// The section runs until the next table header that is not below ours: a sub-table
		// such as [mcp_servers.<name>.env] configures this same server and travels with it.
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			sub, ok := parseTOMLTableHeader(lines[j])
			if ok && !isTOMLSubKey(sub, key) {
				end = j
				break
			}
		}
		spans = append(spans, tomlSpan{start: i, end: end})
		i = end - 1
	}
	return spans
}

// keepTOMLSectionExtras returns the lines of a section body that TerraGraph does not own, so
// re-running init does not quietly discard a hand-added env table or timeout.
func keepTOMLSectionExtras(body []string) []string {
	var kept []string
	for i := 0; i < len(body); i++ {
		if _, ok := parseTOMLTableHeader(body[i]); ok {
			// A sub-table of our own server. It and everything after it within the span is
			// the user's; nothing this tool writes belongs down here.
			kept = append(kept, body[i:]...)
			break
		}
		if k, ok := parseTOMLKeyOfAssignment(body[i]); ok && len(k) == 1 && (k[0] == "command" || k[0] == "args") {
			i = endOfTOMLValue(body, i)
			continue
		}
		kept = append(kept, body[i])
	}
	// Blank lines are kept exactly as found. They are the user's paragraph breaks between
	// sections, and re-running init should not slowly compact the file.
	return kept
}

// endOfTOMLValue returns the index of the last line of the value beginning on line i, so that
// a multi-line array is dropped whole rather than leaving its tail behind as loose syntax.
func endOfTOMLValue(body []string, i int) int {
	depth := tomlBracketDepth(body[i])
	for depth > 0 && i+1 < len(body) {
		i++
		depth += tomlBracketDepth(body[i])
	}
	return i
}

// tomlBracketDepth counts brackets that are syntax, ignoring any inside a string or a comment.
func tomlBracketDepth(line string) int {
	depth := 0
	for i := 0; i < len(line); {
		switch line[i] {
		case '"', '\'':
			j, ok := skipTOMLQuoted(line, i)
			if !ok {
				return depth
			}
			i = j
		case '#':
			return depth
		case '[':
			depth++
			i++
		case ']':
			depth--
			i++
		default:
			i++
		}
	}
	return depth
}

// parseTOMLTableHeader parses a table header line — "[a.b]", "[ a . b ]", `[a."b"]`, or the
// array-of-tables form "[[a.b]]", each with an optional trailing comment — into its dotted
// key. ok is false when the line is not a table header at all.
func parseTOMLTableHeader(line string) (key []string, ok bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	s = s[1:]
	s = strings.TrimPrefix(s, "[") // an array of tables names a key just the same

	inner, rest, ok := scanToTOMLBracket(s)
	if !ok {
		return nil, false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "]"))
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, false
	}
	return splitTOMLKey(inner)
}

// parseTOMLKeyOfAssignment parses the dotted key of a "key = value" line.
func parseTOMLKeyOfAssignment(line string) (key []string, ok bool) {
	for i := 0; i < len(line); {
		switch line[i] {
		case '"', '\'':
			j, ok := skipTOMLQuoted(line, i)
			if !ok {
				return nil, false
			}
			i = j
		case '#':
			return nil, false
		case '=':
			return splitTOMLKey(line[:i])
		default:
			i++
		}
	}
	return nil, false
}

// scanToTOMLBracket splits s at the first "]" that is not inside a quoted key segment.
func scanToTOMLBracket(s string) (inner, rest string, ok bool) {
	for i := 0; i < len(s); {
		switch s[i] {
		case '"', '\'':
			j, ok := skipTOMLQuoted(s, i)
			if !ok {
				return "", "", false
			}
			i = j
		case ']':
			return s[:i], s[i+1:], true
		default:
			i++
		}
	}
	return "", "", false
}

// skipTOMLQuoted returns the index just past the string starting at i. A literal string in
// single quotes has no escapes at all; a basic string in double quotes does.
func skipTOMLQuoted(s string, i int) (int, bool) {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		if quote == '"' && s[j] == '\\' {
			j++ // an escaped character cannot end the string
			continue
		}
		if s[j] == quote {
			return j + 1, true
		}
	}
	return 0, false
}

// splitTOMLKey splits a dotted key on the dots that are not inside quotes, and unquotes each
// segment, so that every spelling of one key compares equal.
func splitTOMLKey(s string) (key []string, ok bool) {
	start := 0
	flush := func(end int) bool {
		seg, ok := tomlKeySegment(s[start:end])
		if !ok {
			return false
		}
		key = append(key, seg)
		start = end + 1
		return true
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '"', '\'':
			j, ok := skipTOMLQuoted(s, i)
			if !ok {
				return nil, false
			}
			i = j
		case '.':
			if !flush(i) {
				return nil, false
			}
			i++
		default:
			i++
		}
	}
	if !flush(len(s)) {
		return nil, false
	}
	return key, true
}

// tomlKeySegment unquotes one segment of a dotted key. A segment this does not recognise is
// reported as not-a-key, which costs nothing: it only means the line is not our section.
func tomlKeySegment(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	switch s[0] {
	case '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", false
		}
		return s[1 : len(s)-1], true
	case '"':
		// TOML basic strings and JSON strings share their escapes closely enough for a key,
		// and anything exotic enough to differ is not a name this tool writes.
		var v string
		if json.Unmarshal([]byte(s), &v) != nil {
			return "", false
		}
		return v, true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		bare := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !bare {
			return "", false
		}
	}
	return s, true
}

func tomlKeyEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isTOMLSubKey reports whether key names a table strictly below parent.
func isTOMLSubKey(key, parent []string) bool {
	if len(key) <= len(parent) {
		return false
	}
	return tomlKeyEqual(key[:len(parent)], parent)
}

func renderTOMLSection(name, command string, args []string) string {
	return tomlSectionHeader(name) + "\n" + renderTOMLBody(command, args)
}

// renderTOMLBody renders only the keys TerraGraph owns, so a merge can keep the header line
// the user already wrote.
func renderTOMLBody(command string, args []string) string {
	var b strings.Builder
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

// jsonString quotes a JSON string, for the same reason tomlString exists: a path interpolated
// into a snippet has to survive being read back as a literal.
func jsonString(s string) string {
	buf, err := json.Marshal(s)
	if err != nil {
		return `"` + s + `"`
	}
	return string(buf)
}

// tomlString quotes a basic TOML string. Paths on Windows contain backslashes, which are
// escapes in a TOML basic string and would otherwise corrupt the command. Control characters
// (including CR) are also forbidden unescaped, so a "paste this verbatim" snippet has to
// encode the whole U+0000–U+001F / U+007F range — not just the four escapes that Windows
// paths usually hit.
func tomlString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
