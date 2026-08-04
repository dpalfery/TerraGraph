// Package mcpinit registers the TerraGraph MCP server in a project's agent configuration.
//
// There is no shared standard here, only a family resemblance. Six tools use five different
// file locations, three different root keys, two different shapes for "a command with
// arguments", and one of them is TOML. Writing that by hand is exactly the kind of fiddly,
// easy-to-get-silently-wrong task worth automating: a config in the wrong file does not
// error, it just means the agent never sees the tools and nobody knows why.
package mcpinit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ServerName is the key TerraGraph registers itself under, in every tool.
const ServerName = "terragraph"

// Format is how a target's configuration file is encoded.
type Format int

const (
	FormatJSON Format = iota
	FormatTOML
)

// Target is one agent's project-scoped MCP configuration.
type Target struct {
	// ID is the value accepted by --tool.
	ID string

	// Aliases are other accepted spellings.
	Aliases []string

	// Display is the human name.
	Display string

	// Paths are candidate config files, repo-relative. The first that already exists is
	// used; otherwise Paths[0] is created. Several tools accept more than one location and
	// adding a second file when one is already in use would be worse than useless.
	Paths []string

	Format Format

	// Container is the root key holding server definitions. JSON targets only.
	Container string

	// Entry builds the per-server value. The shapes differ enough that a shared struct
	// would be mostly-empty fields and a lie about compatibility.
	Entry func(command string, args []string) any

	// Detect are paths whose presence suggests this tool is in use.
	Detect []string

	// Note is a caveat about whether the configuration actually works. A %s in it takes the
	// project root; render it with NoteFor rather than fmt.Sprintf.
	Note string

	// Docs is where to read more when the note is not enough.
	Docs string
}

// NoteFor renders the target's caveat for one project root.
//
// It is not a plain Sprintf, because a note that quotes a path is quoting it *into a config
// file* — the Codex one puts it in a TOML table header — and a Windows path is mostly
// backslashes, which TOML reads as escapes. Interpolated raw, C:\Users starts an invalid \U
// escape and the block the user was told to paste verbatim does not parse: a snippet that
// exists to fix a silent failure, failing silently. So the value is quoted for the format of
// the file it is going into, and Note templates leave the quotes off.
func (t Target) NoteFor(root string) string {
	if !strings.Contains(t.Note, "%s") {
		return t.Note
	}
	if t.Format == FormatTOML {
		return fmt.Sprintf(t.Note, tomlString(root))
	}
	return fmt.Sprintf(t.Note, jsonString(root))
}

// Targets is every supported tool, in a stable order.
//
// The per-tool shapes are taken from each vendor's own documentation rather than assumed
// from the others: Copilot's editor and CLI surfaces disagree with each other, OpenCode and
// Kilo take a command array where everyone else takes command-plus-args, and Codex is TOML.
func Targets() []Target {
	return []Target{
		{
			ID:      "claude",
			Aliases: []string{"claude-code"},
			Display: "Claude Code",
			Paths:   []string{".mcp.json"},
			Format:  FormatJSON, Container: "mcpServers",
			Detect: []string{".mcp.json", ".claude", "CLAUDE.md"},
			Docs:   "https://docs.claude.com/en/docs/claude-code/mcp",
			Entry: func(cmd string, args []string) any {
				return stdioEntry{Command: cmd, Args: args}
			},
		},
		{
			ID:      "copilot",
			Aliases: []string{"vscode", "copilot-vscode"},
			Display: "GitHub Copilot (VS Code)",
			Paths:   []string{".vscode/mcp.json"},
			Format:  FormatJSON, Container: "servers",
			Detect: []string{".vscode"},
			Docs:   "https://code.visualstudio.com/docs/agent-customization/mcp-servers",
			// The editor uses "servers", not "mcpServers", and wants an explicit
			// transport type. A file written in the common shape is silently ignored.
			Entry: func(cmd string, args []string) any {
				return typedStdioEntry{Type: "stdio", Command: cmd, Args: args}
			},
		},
		{
			ID:      "copilot-cli",
			Display: "GitHub Copilot CLI",
			Paths:   []string{".github/mcp.json"},
			Format:  FormatJSON, Container: "mcpServers",
			Detect: []string{".github/mcp.json", ".copilot"},
			Docs:   "https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-mcp-servers",
			Note: "Copilot CLI does not read .vscode/mcp.json — it is a separate surface with a\n" +
				"    different root key. Run --tool copilot as well if you use both.",
			Entry: func(cmd string, args []string) any {
				return copilotCLIEntry{Type: "local", Command: cmd, Args: args, Tools: []string{"*"}}
			},
		},
		{
			ID:      "cursor",
			Display: "Cursor",
			Paths:   []string{".cursor/mcp.json"},
			Format:  FormatJSON, Container: "mcpServers",
			Detect: []string{".cursor", ".cursorrules"},
			Docs:   "https://docs.cursor.com/context/model-context-protocol",
			Entry: func(cmd string, args []string) any {
				return stdioEntry{Command: cmd, Args: args}
			},
		},
		{
			ID:      "opencode",
			Display: "OpenCode",
			Paths:   []string{"opencode.json", "opencode.jsonc"},
			Format:  FormatJSON, Container: "mcp",
			Detect: []string{"opencode.json", "opencode.jsonc", ".opencode"},
			Docs:   "https://opencode.ai/docs/mcp-servers/",
			// One array, not command plus args, and the env key is "environment".
			Entry: func(cmd string, args []string) any {
				return arrayEntry{Type: "local", Command: append([]string{cmd}, args...), Enabled: true}
			},
		},
		{
			ID:      "kilo",
			Aliases: []string{"kilocode"},
			Display: "Kilo",
			Paths:   []string{"kilo.jsonc", "kilo.json", ".kilo/kilo.jsonc"},
			Format:  FormatJSON, Container: "mcp",
			Detect: []string{"kilo.jsonc", "kilo.json", ".kilo", ".kilocode"},
			Docs:   "https://kilo.ai/docs/automate/mcp/using-in-kilo-code",
			Entry: func(cmd string, args []string) any {
				return arrayEntry{Type: "local", Command: append([]string{cmd}, args...), Enabled: true}
			},
		},
		{
			ID:      "codex",
			Display: "OpenAI Codex CLI",
			Paths:   []string{".codex/config.toml"},
			Format:  FormatTOML,
			Detect:  []string{".codex"},
			Docs:    "https://developers.openai.com/codex/mcp",
			// This one is not a nicety. Codex ignores project-local config entirely for a
			// project it does not trust, so without this the file is written, looks
			// correct, and does nothing.
			// The %s is quoted by NoteFor, not by this template — the path is a TOML string
			// literal and has to be escaped as one.
			Note: "Codex ignores project-local config for untrusted projects. Mark this project\n" +
				"    trusted in ~/.codex/config.toml, or the server will never load:\n" +
				"      [projects.%s]\n" +
				"      trust_level = \"trusted\"",
		},
	}
}

// Entry shapes. Each exists because a tool genuinely requires it.

type stdioEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type typedStdioEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type copilotCLIEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

type arrayEntry struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

// Lookup resolves a --tool value, accepting aliases.
func Lookup(name string) (Target, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, t := range Targets() {
		if t.ID == want {
			return t, true
		}
		for _, a := range t.Aliases {
			if a == want {
				return t, true
			}
		}
	}
	return Target{}, false
}

// IDs lists every accepted --tool value, for error messages and --help.
func IDs() []string {
	var out []string
	for _, t := range Targets() {
		out = append(out, t.ID)
	}
	return out
}

// Detected returns the targets with evidence of use in this repository.
//
// Detection is a convenience, never a requirement: --tool always wins, because a tool that
// has not been configured yet leaves no trace and is exactly the case `init` is for.
func Detected(repoRoot string) []Target {
	var out []Target
	for _, t := range Targets() {
		for _, d := range t.Detect {
			if _, err := os.Stat(filepath.Join(repoRoot, d)); err == nil {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// Action is what a Change would do to a file.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionSkip   Action = "already registered"
	ActionManual Action = "needs a manual edit"
)

// Change is one planned edit, computed without touching the disk.
type Change struct {
	Target Target
	Path   string // repo-relative
	Abs    string
	Action Action
	Before []byte
	After  []byte

	// Reason explains an ActionManual, which happens when a file cannot be rewritten
	// safely — a JSONC file with comments, for example, where re-encoding would silently
	// delete them.
	Reason string

	// Snippet is what to paste by hand when Action is ActionManual.
	Snippet string
}

// Options controls what Plan produces.
type Options struct {
	// Command is the MCP server executable. A bare name is preferred over an absolute
	// path: the config is usually committed, and an absolute path is only correct on the
	// machine that generated it.
	Command string

	// Args are passed to the server.
	Args []string

	// Force rewrites an entry that is already present and identical.
	Force bool
}

// DefaultOptions resolves sensible values, preferring a PATH lookup over the absolute path
// of the running binary.
func DefaultOptions() Options {
	return Options{Command: resolveServerCommand(), Args: []string{"--repo", "."}}
}

// resolveServerCommand prefers `terragraph-mcp` from PATH so a committed config works for
// everyone on the team. It falls back to the binary sitting next to this one, which is
// correct locally and at least honest about being a path.
func resolveServerCommand() string {
	const bin = "terragraph-mcp"

	if _, err := exec_LookPath(bin); err == nil {
		return bin
	}
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), bin)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return bin
}

// Plan computes the edits for a set of targets without writing anything.
func Plan(repoRoot string, targets []Target, opts Options) ([]Change, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}

	changes := make([]Change, 0, len(targets))
	for _, t := range targets {
		c, err := planOne(abs, t, opts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Display, err)
		}
		changes = append(changes, c)
	}
	return changes, nil
}

func planOne(repoRoot string, t Target, opts Options) (Change, error) {
	rel := choosePath(repoRoot, t.Paths)
	c := Change{Target: t, Path: rel, Abs: filepath.Join(repoRoot, rel)}

	existing, err := os.ReadFile(c.Abs)
	switch {
	case err == nil:
		c.Before = existing
		c.Action = ActionUpdate
	case os.IsNotExist(err):
		c.Action = ActionCreate
	default:
		return c, err
	}

	if t.Format == FormatTOML {
		after, changed, err := mergeTOML(c.Before, ServerName, opts.Command, opts.Args)
		if err != nil {
			return c, err
		}
		c.After = after
		if !changed && !opts.Force {
			c.Action = ActionSkip
		}
		return c, nil
	}

	entry := t.Entry(opts.Command, opts.Args)
	after, changed, err := mergeJSON(c.Before, t.Container, ServerName, entry)
	if err != nil {
		// A file we cannot parse is not a file we may rewrite. Re-encoding a JSONC file
		// would delete every comment in it, which is a far worse outcome than asking for
		// twenty seconds of manual work.
		c.Action = ActionManual
		c.Reason = err.Error()
		c.Snippet, _ = snippetJSON(t.Container, ServerName, entry)
		return c, nil
	}
	c.After = after
	if !changed && !opts.Force {
		c.Action = ActionSkip
	}
	return c, nil
}

// choosePath picks the first candidate that exists, else the first candidate.
func choosePath(repoRoot string, candidates []string) string {
	for _, p := range candidates {
		if _, err := os.Stat(filepath.Join(repoRoot, p)); err == nil {
			return p
		}
	}
	return candidates[0]
}

// Apply writes the planned changes.
func Apply(changes []Change) error {
	for i := range changes {
		c := &changes[i]
		if c.Action == ActionSkip || c.Action == ActionManual {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(c.Abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(c.Abs, c.After, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// SortTargets keeps output deterministic.
func SortTargets(in []Target) {
	sort.SliceStable(in, func(i, j int) bool { return in[i].ID < in[j].ID })
}
