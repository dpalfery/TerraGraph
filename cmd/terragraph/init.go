package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dpalfery/terragraph/internal/mcpinit"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)

	var tools stringList
	fs.Var(&tools, "tool", "agent to configure; repeatable or comma-separated")
	repo := fs.String("repo", ".", "project directory to configure")
	all := fs.Bool("all", false, "configure every supported tool")
	list := fs.Bool("list", false, "list supported tools and their config files")
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	force := fs.Bool("force", false, "rewrite an entry that is already correct")
	binary := fs.String("binary", "", "path or name of the terragraph-mcp executable")
	serverRepo := fs.String("server-repo", ".", "--repo value passed to the MCP server")

	_ = parseInterspersed(fs, args)

	if *list {
		printTargets()
		return 0
	}

	root, err := filepath.Abs(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "terragraph init: %v\n", err)
		return 1
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "terragraph init: %s is not a directory\n", *repo)
		return 1
	}

	selected, code := selectTargets(tools, *all, root)
	if code != 0 {
		return code
	}

	opts := mcpinit.DefaultOptions()
	opts.Force = *force
	if *binary != "" {
		opts.Command = *binary
	}
	opts.Args = []string{"--repo", *serverRepo}

	changes, err := mcpinit.Plan(root, selected, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "terragraph init: %v\n", err)
		return 1
	}

	report(changes, opts, root, *dryRun)

	if *dryRun {
		fmt.Println("\nDry run — nothing was written.")
		return 0
	}
	if err := mcpinit.Apply(changes); err != nil {
		fmt.Fprintf(os.Stderr, "terragraph init: %v\n", err)
		return 1
	}

	printNotes(changes, root)
	return 0
}

// selectTargets resolves --tool / --all / autodetection into a target list.
func selectTargets(tools stringList, all bool, root string) ([]mcpinit.Target, int) {
	switch {
	case all:
		return mcpinit.Targets(), 0

	case len(tools) > 0:
		var out []mcpinit.Target
		for _, name := range tools {
			t, ok := mcpinit.Lookup(name)
			if !ok {
				fmt.Fprintf(os.Stderr, "terragraph init: unknown tool %q\nsupported: %s\n",
					name, strings.Join(mcpinit.IDs(), ", "))
				return nil, 2
			}
			out = append(out, t)
		}
		return out, 0
	}

	// No flags: configure what this project already shows signs of using. Guessing is
	// only acceptable because the alternative is writing seven config files into a
	// repository that wanted one.
	found := mcpinit.Detected(root)
	if len(found) == 0 {
		fmt.Fprintf(os.Stderr, `terragraph init: no agent configuration detected in %s

Name one explicitly:
  terragraph init --tool claude
  terragraph init --tool copilot --tool cursor
  terragraph init --all

Supported: %s
`, root, strings.Join(mcpinit.IDs(), ", "))
		return nil, 2
	}

	names := make([]string, 0, len(found))
	for _, t := range found {
		names = append(names, t.Display)
	}
	fmt.Printf("Detected: %s\n", strings.Join(names, ", "))
	fmt.Println("(pass --tool to choose explicitly, or --all for every supported tool)")
	fmt.Println()
	return found, 0
}

func report(changes []mcpinit.Change, opts mcpinit.Options, root string, dryRun bool) {
	fmt.Printf("Registering the %q MCP server in %s\n", mcpinit.ServerName, root)
	fmt.Printf("  command: %s %s\n\n", opts.Command, strings.Join(opts.Args, " "))

	for _, c := range changes {
		fmt.Printf("  %-26s %-22s %s\n", c.Target.Display, c.Path, c.Action)

		if c.Action == mcpinit.ActionManual {
			fmt.Printf("      %s — add this by hand:\n", c.Reason)
			for _, line := range strings.Split(c.Snippet, "\n") {
				fmt.Printf("      %s\n", line)
			}
		}
		if dryRun && c.Action != mcpinit.ActionSkip && c.Action != mcpinit.ActionManual {
			fmt.Printf("      %s\n", strings.ReplaceAll(
				strings.TrimRight(string(c.After), "\n"), "\n", "\n      "))
		}
	}
}

// printNotes reports the caveats that decide whether a written config actually works.
func printNotes(changes []mcpinit.Change, root string) {
	var wrote bool
	for _, c := range changes {
		if c.Action == mcpinit.ActionCreate || c.Action == mcpinit.ActionUpdate {
			wrote = true
		}
	}

	for _, c := range changes {
		if c.Target.Note == "" || c.Action == mcpinit.ActionManual {
			continue
		}
		note := c.Target.Note
		if strings.Contains(note, "%s") {
			note = fmt.Sprintf(note, root)
		}
		fmt.Printf("\n  %s:\n    %s\n", c.Target.Display, note)
	}

	if wrote {
		fmt.Println("\nRestart the agent to pick up the new server, then ask it for terra_status.")
		fmt.Println("These files are usually worth committing so the whole team gets the tools.")
	}
}

func printTargets() {
	fmt.Print("Supported tools:\n\n")
	fmt.Printf("  %-14s %-26s %-22s %s\n", "--tool", "TOOL", "PROJECT FILE", "ROOT KEY")
	for _, t := range mcpinit.Targets() {
		key := t.Container
		if t.Format == mcpinit.FormatTOML {
			key = "[mcp_servers.*] (TOML)"
		}
		fmt.Printf("  %-14s %-26s %-22s %s\n", t.ID, t.Display, t.Paths[0], key)
	}
	fmt.Println("\nAliases:")
	for _, t := range mcpinit.Targets() {
		if len(t.Aliases) > 0 {
			fmt.Printf("  %-14s %s\n", t.ID, strings.Join(t.Aliases, ", "))
		}
	}
	fmt.Println("\nGitHub Copilot has two separate surfaces that do not read each other's files:")
	fmt.Println("  copilot      the VS Code extension  (.vscode/mcp.json, root key \"servers\")")
	fmt.Println("  copilot-cli  the CLI                (.github/mcp.json, root key \"mcpServers\")")
}
