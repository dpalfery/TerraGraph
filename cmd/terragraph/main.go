// Command terragraph is the human-facing CLI.
//
// It is deliberately a separate binary from terragraph-mcp. JSON-RPC owns stdout, and a
// CLI that also writes there — tables, colour, progress — corrupts the stream. Separate
// entry points make that structurally impossible instead of a rule someone has to keep.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dpalfery/terragraph/internal/index"
	"github.com/dpalfery/terragraph/internal/render"
)

const usage = `terragraph — an in-memory Terraform graph

usage:
  terragraph status                      shape of the graph and how far to trust it
  terragraph explore <query>             ranked retrieval over the configuration
  terragraph refs <address>              what actually references this (not what mentions it)
  terragraph impact <address>            transitive blast radius
  terragraph modules [filter]            module inventory and version skew
  terragraph orphans                     unreferenced variables, locals, child outputs

common flags:
  --repo DIR        repository to index (default: current directory)

explore flags:
  --max N           maximum nodes to return (default 5)
  --budget N        total characters of configuration across all results (default 12000)

impact flags:
  --direction D     "dependents" (default) or "dependencies"
  --depth N         how many hops to walk (default 3)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]
	var code int

	switch cmd {
	case "status", "stats":
		code = runStatus(args)
	case "explore":
		code = runExplore(args)
	case "refs", "for-address":
		code = runRefs(args)
	case "impact":
		code = runImpact(args)
	case "modules":
		code = runModules(args)
	case "orphans":
		code = runOrphans(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

// parseInterspersed parses flags that appear after positional arguments.
//
// Go's flag package stops at the first non-flag argument, so `terragraph refs X --repo Y`
// silently ignores --repo and indexes the wrong tree. That failure is invisible: it
// returns a confident answer about a different repository. Since both people and agents
// naturally write the subject before the options, the parser accommodates them.
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return positional
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// open parses the shared flags and builds the index. Every command needs exactly this.
func open(fs *flag.FlagSet, args []string) (*index.Index, string, []string, int) {
	repo := fs.String("repo", ".", "repository to index")
	positional := parseInterspersed(fs, args)

	host := index.NewHost(*repo)
	ix, err := host.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "terragraph: %v\n", err)
		return nil, "", nil, 1
	}
	return ix, host.RepoRoot(), positional, 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	ix, root, _, code := open(fs, args)
	if code != 0 {
		return code
	}
	fmt.Print(render.Status(ix, root, 1))
	return 0
}

func runExplore(args []string) int {
	fs := flag.NewFlagSet("explore", flag.ExitOnError)
	max := fs.Int("max", 5, "maximum nodes to return")
	budget := fs.Int("budget", index.DefaultCharBudget, "total characters across all results")

	ix, _, rest, code := open(fs, args)
	if code != 0 {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "terragraph explore: need a query")
		return 2
	}
	fmt.Print(render.Explore(ix, joinArgs(rest), *max, *budget))
	return 0
}

func runRefs(args []string) int {
	fs := flag.NewFlagSet("refs", flag.ExitOnError)
	ix, _, rest, code := open(fs, args)
	if code != 0 {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "terragraph refs: need an address")
		return 2
	}
	fmt.Print(render.ForAddress(ix, rest[0]))
	return 0
}

func runImpact(args []string) int {
	fs := flag.NewFlagSet("impact", flag.ExitOnError)
	dir := fs.String("direction", "dependents", `"dependents" or "dependencies"`)
	depth := fs.Int("depth", 3, "how many hops to walk")

	ix, _, rest, code := open(fs, args)
	if code != 0 {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "terragraph impact: need an address")
		return 2
	}

	d := index.Dependents
	if *dir == string(index.Dependencies) {
		d = index.Dependencies
	}
	fmt.Print(render.Impact(ix, rest[0], d, *depth))
	return 0
}

func runModules(args []string) int {
	fs := flag.NewFlagSet("modules", flag.ExitOnError)
	ix, _, rest, code := open(fs, args)
	if code != 0 {
		return code
	}
	var filter string
	if len(rest) > 0 {
		filter = rest[0]
	}
	fmt.Print(render.Modules(ix, filter))
	return 0
}

func runOrphans(args []string) int {
	fs := flag.NewFlagSet("orphans", flag.ExitOnError)
	ix, _, _, code := open(fs, args)
	if code != 0 {
		return code
	}
	fmt.Print(render.Orphans(ix))
	return 0
}

func joinArgs(args []string) string {
	out := args[0]
	for _, a := range args[1:] {
		out += " " + a
	}
	return out
}
