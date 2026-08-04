package mcpinit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entryJSON renders one target's server entry for comparison.
func entryJSON(t *testing.T, target Target) map[string]any {
	t.Helper()
	raw, err := json.Marshal(target.Entry("terragraph-mcp", []string{"--repo", "."}))
	if err != nil {
		t.Fatalf("marshal %s: %v", target.ID, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTargetShapesMatchVendorDocs pins each tool's schema.
//
// These are not interchangeable and the differences are not cosmetic. A config written in
// the wrong shape does not error — the agent simply never sees the tools, which is the
// worst failure mode available: silent, and indistinguishable from the server being broken.
func TestTargetShapesMatchVendorDocs(t *testing.T) {
	byID := map[string]Target{}
	for _, x := range Targets() {
		byID[x.ID] = x
	}

	t.Run("claude uses bare command and args", func(t *testing.T) {
		e := entryJSON(t, byID["claude"])
		if e["command"] != "terragraph-mcp" {
			t.Errorf("command = %v", e["command"])
		}
		if _, has := e["type"]; has {
			t.Error("Claude Code entry should not carry a transport type")
		}
		if byID["claude"].Container != "mcpServers" {
			t.Errorf("container = %q, want mcpServers", byID["claude"].Container)
		}
	})

	// The single most likely thing to get wrong: VS Code's key is "servers", and a file
	// using the common "mcpServers" is ignored without complaint.
	t.Run("copilot vscode uses servers and stdio", func(t *testing.T) {
		x := byID["copilot"]
		if x.Container != "servers" {
			t.Errorf("container = %q, want servers — VS Code does not read mcpServers", x.Container)
		}
		if got := entryJSON(t, x)["type"]; got != "stdio" {
			t.Errorf("type = %v, want stdio", got)
		}
		if x.Paths[0] != ".vscode/mcp.json" {
			t.Errorf("path = %q", x.Paths[0])
		}
	})

	t.Run("copilot cli is a separate surface", func(t *testing.T) {
		x := byID["copilot-cli"]
		if x.Container != "mcpServers" {
			t.Errorf("container = %q, want mcpServers", x.Container)
		}
		if x.Paths[0] == byID["copilot"].Paths[0] {
			t.Error("the CLI and the extension must not share a file; the CLI does not read .vscode/mcp.json")
		}
		e := entryJSON(t, x)
		if e["type"] != "local" {
			t.Errorf("type = %v, want local", e["type"])
		}
		if _, has := e["tools"]; !has {
			t.Error("Copilot CLI expects a tools list")
		}
	})

	// OpenCode and Kilo take one array, not command plus args. Splitting them produces a
	// server that never starts.
	for _, id := range []string{"opencode", "kilo"} {
		t.Run(id+" uses a command array", func(t *testing.T) {
			x := byID[id]
			if x.Container != "mcp" {
				t.Errorf("container = %q, want mcp", x.Container)
			}
			e := entryJSON(t, x)
			cmd, ok := e["command"].([]any)
			if !ok {
				t.Fatalf("command = %#v, want an array", e["command"])
			}
			if len(cmd) != 3 || cmd[0] != "terragraph-mcp" || cmd[1] != "--repo" {
				t.Errorf("command = %v, want the executable and its args in one array", cmd)
			}
			if _, has := e["args"]; has {
				t.Error("args must be folded into command, not sent separately")
			}
		})
	}

	t.Run("codex is TOML", func(t *testing.T) {
		if byID["codex"].Format != FormatTOML {
			t.Error("Codex config is TOML, not JSON")
		}
		if byID["codex"].Note == "" {
			t.Error("Codex silently ignores project config for untrusted projects; that must be reported")
		}
	})
}

func TestLookupAcceptsAliases(t *testing.T) {
	for _, name := range []string{"claude", "claude-code", "CLAUDE", " cursor ", "kilocode", "vscode"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("Lookup(%q) failed", name)
		}
	}
	if _, ok := Lookup("emacs"); ok {
		t.Error("Lookup accepted an unknown tool")
	}
}

// TestMergePreservesEverythingElse is the contract that makes this safe to run on a
// repository that already has configuration in it.
func TestMergePreservesEverythingElse(t *testing.T) {
	existing := []byte(`{
  "inputs": [{"id": "api-key", "type": "promptString"}],
  "servers": {
    "playwright": {"type": "stdio", "command": "npx", "args": ["@playwright/mcp@latest"]}
  }
}`)

	out, changed, err := mergeJSON(existing, "servers", "terragraph",
		typedStdioEntry{Type: "stdio", Command: "terragraph-mcp"})
	if err != nil || !changed {
		t.Fatalf("merge: changed=%v err=%v", changed, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if _, ok := doc["inputs"]; !ok {
		t.Error("an unrelated top-level key was dropped")
	}
	servers := doc["servers"].(map[string]any)
	if _, ok := servers["playwright"]; !ok {
		t.Error("someone else's server was dropped")
	}
	if _, ok := servers["terragraph"]; !ok {
		t.Error("our own server was not added")
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	entry := stdioEntry{Command: "terragraph-mcp", Args: []string{"--repo", "."}}

	first, changed, err := mergeJSON(nil, "mcpServers", "terragraph", entry)
	if err != nil || !changed {
		t.Fatalf("first merge: changed=%v err=%v", changed, err)
	}

	second, changed, err := mergeJSON(first, "mcpServers", "terragraph", entry)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("re-running reported a change when the entry was already correct")
	}
	if string(second) != string(first) {
		t.Error("re-running rewrote the file")
	}
}

// TestMergeRefusesJSONC guards against the destructive path: re-encoding a commented file
// would silently delete every comment in it.
func TestMergeRefusesJSONC(t *testing.T) {
	commented := []byte("{\n  // load-bearing comment\n  \"mcp\": {}\n}")

	if _, _, err := mergeJSON(commented, "mcp", "terragraph", stdioEntry{Command: "x"}); err == nil {
		t.Fatal("a commented JSONC file was parsed and would have been rewritten without its comments")
	}
}

func TestMergeTOMLAppendsAndReplaces(t *testing.T) {
	base := []byte(`model = "gpt-5-codex"

[mcp_servers.playwright]
command = "npx"
args = ["@playwright/mcp@latest"]
`)

	added, changed, err := mergeTOML(base, "terragraph", "terragraph-mcp", []string{"--repo", "."})
	if err != nil || !changed {
		t.Fatalf("append: changed=%v err=%v", changed, err)
	}
	s := string(added)
	if !strings.Contains(s, `model = "gpt-5-codex"`) || !strings.Contains(s, "[mcp_servers.playwright]") {
		t.Error("existing TOML content was lost")
	}
	if strings.Count(s, "[mcp_servers.terragraph]") != 1 {
		t.Errorf("expected exactly one section, got %d", strings.Count(s, "[mcp_servers.terragraph]"))
	}

	// Re-running with a different command must replace the section, never append a second.
	replaced, changed, err := mergeTOML(added, "terragraph", "/opt/terragraph-mcp", []string{"--repo", "."})
	if err != nil || !changed {
		t.Fatalf("replace: changed=%v err=%v", changed, err)
	}
	r := string(replaced)
	if n := strings.Count(r, "[mcp_servers.terragraph]"); n != 1 {
		t.Errorf("duplicate sections after re-run: %d", n)
	}
	if !strings.Contains(r, `command = "/opt/terragraph-mcp"`) {
		t.Error("the command was not updated")
	}
	if !strings.Contains(r, "[mcp_servers.playwright]") {
		t.Error("replacing our section removed someone else's")
	}

	if _, changed, _ := mergeTOML(replaced, "terragraph", "/opt/terragraph-mcp", []string{"--repo", "."}); changed {
		t.Error("TOML merge is not idempotent")
	}
}

// TestTOMLQuotingHandlesWindowsPaths matters because a backslash is an escape inside a TOML
// basic string, so an unescaped Windows path silently corrupts the command.
func TestTOMLQuotingHandlesWindowsPaths(t *testing.T) {
	out, _, err := mergeTOML(nil, "terragraph", `C:\Tools\terragraph-mcp.exe`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `command = "C:\\Tools\\terragraph-mcp.exe"`) {
		t.Errorf("backslashes were not escaped:\n%s", out)
	}
}

func TestPlanAndApplyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	opts := Options{Command: "terragraph-mcp", Args: []string{"--repo", "."}}

	changes, err := Plan(dir, Targets(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != len(Targets()) {
		t.Fatalf("planned %d changes for %d targets", len(changes), len(Targets()))
	}
	for _, c := range changes {
		if c.Action != ActionCreate {
			t.Errorf("%s: action = %q in an empty directory, want create", c.Target.ID, c.Action)
		}
	}

	// Planning must not touch the disk.
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Plan wrote %d entries before Apply", len(entries))
	}

	if err := Apply(changes); err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if _, err := os.Stat(c.Abs); err != nil {
			t.Errorf("%s: %v", c.Target.ID, err)
		}
	}

	// Everything written must be valid in its own format.
	for _, c := range changes {
		raw, err := os.ReadFile(c.Abs)
		if err != nil {
			t.Fatal(err)
		}
		if c.Target.Format == FormatJSON {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Errorf("%s wrote invalid JSON: %v", c.Target.ID, err)
			}
		}
	}

	// A second plan over the same directory is a no-op.
	again, err := Plan(dir, Targets(), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range again {
		if c.Action != ActionSkip {
			t.Errorf("%s: re-planning gave %q, want %q", c.Target.ID, c.Action, ActionSkip)
		}
	}
}

func TestChoosePathPrefersExistingFile(t *testing.T) {
	dir := t.TempDir()
	// Kilo accepts several locations; adding a second when one is in use would split the
	// configuration across two files that disagree.
	if err := os.WriteFile(filepath.Join(dir, "kilo.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	kilo, _ := Lookup("kilo")
	if got := choosePath(dir, kilo.Paths); got != "kilo.json" {
		t.Errorf("choosePath = %q, want the existing kilo.json rather than the default", got)
	}
}

func TestDetectFindsConfiguredTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	found := Detected(dir)
	if len(found) != 1 || found[0].ID != "cursor" {
		var ids []string
		for _, f := range found {
			ids = append(ids, f.ID)
		}
		t.Errorf("Detected = %v, want [cursor]", ids)
	}
	if len(Detected(t.TempDir())) != 0 {
		t.Error("an empty directory should detect nothing")
	}
}
