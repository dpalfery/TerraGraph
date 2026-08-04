// Command terragraph-mcp serves the Terraform graph to an agent over MCP.
//
// It is a separate binary from the terragraph CLI on purpose. JSON-RPC owns stdout here,
// and anything else written there corrupts the stream. Keeping the two entry points apart
// makes that impossible by construction rather than by discipline: this binary imports no
// rendering that writes anywhere but a buffer, and every diagnostic goes to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/dpalfery/terragraph/internal/index"
	"github.com/dpalfery/terragraph/internal/render"
	"github.com/dpalfery/terragraph/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	repo := flag.String("repo", ".", "repository to index")
	plan := flag.String("plan", "", "plan/state overlay: <root>=<path> pairs, or a bare path")
	showVersion := flag.Bool("version", false, "print the build identity and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("terragraph-mcp", version.String())
		return
	}

	if err := index.StatFS(*repo); err != nil {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: cannot read %s: %v\n", *repo, err)
		os.Exit(1)
	}

	host := index.NewHost(*repo).WithPlan(*plan)

	// Build once at startup so a broken repository fails loudly here rather than inside
	// the first tool call, where the agent would read it as "no results".
	if _, err := host.Current(); err != nil {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: %v\n", err)
		os.Exit(1)
	}

	// A malformed --plan silently degrades to no overlay, and an agent would then read
	// "static HCL only" as the truth about the repository rather than about a typo.
	if perr := host.ExplicitPlanError(); perr != nil {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: --plan ignored: %v\n", perr)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:        "terragraph",
		Title:       "TerraGraph",
		Description: "An in-memory graph of this repository's Terraform configuration.",
		Version:     version.Version,
	}, nil)

	registerTools(server, host)

	// A client closing stdin is how an MCP session ends, not a failure. Reporting it as
	// one makes every clean shutdown look like a crash in the host's logs.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil &&
		!errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: %v\n", err)
		os.Exit(1)
	}
}

// text is the shape every tool returns: prose an agent reads directly.
func text(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

type exploreIn struct {
	Query      string `json:"query" jsonschema:"Free text, a resource address (aws_s3_bucket.logs), a type (aws_s3_bucket), a variable (var.environment), a module call (module.vpc) or a bare local name."`
	MaxNodes   int    `json:"maxNodes,omitempty" jsonschema:"Maximum nodes to return (1-25). Defaults to 5."`
	CharBudget int    `json:"charBudget,omitempty" jsonschema:"Total characters of configuration across all returned nodes (500-120000). Defaults to 12000."`
}

type addressIn struct {
	Address    string `json:"address" jsonschema:"A Terraform address (aws_s3_bucket.logs, var.environment, module.vpc), a bare local name, or a resource type."`
	CharBudget int    `json:"charBudget,omitempty" jsonschema:"Total characters of output (500-120000). Defaults to 4000."`
}

type impactIn struct {
	Address    string `json:"address" jsonschema:"The address to trace from."`
	Direction  string `json:"direction,omitempty" jsonschema:"'dependents' (default) for what a change would affect, or 'dependencies' for what this needs."`
	Depth      int    `json:"depth,omitempty" jsonschema:"How many hops to walk. Defaults to 3."`
	CharBudget int    `json:"charBudget,omitempty" jsonschema:"Total characters of output (500-120000). Defaults to 4000."`
}

type modulesIn struct {
	Filter     string `json:"filter,omitempty" jsonschema:"Substring of a module source or call name. Omit to inventory every module."`
	CharBudget int    `json:"charBudget,omitempty" jsonschema:"Total characters of output (500-120000). Defaults to 4000."`
}

type budgetIn struct {
	CharBudget int `json:"charBudget,omitempty" jsonschema:"Total characters of output (500-120000). Defaults to 4000."`
}

type emptyIn struct{}

// orBudget applies a default so every tool is bounded even when the caller omits it.
//
// The relationship tools default lower than retrieval. Retrieval returns source and needs
// room; a list whose header already states its true total loses much less to truncation,
// and a benchmark on a 29-stack repository showed the inherited 12000 made those tools
// cost more than the grep they replace.
func orBudget(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func registerTools(s *mcp.Server, host *index.Host) {
	current := func() (*index.Index, error) { return host.Current() }

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_explore",
		Description: `Retrieve Terraform configuration for a question, address, type or module name.

Prefer this over grepping .tf files. Ranking uses Terraform's own declared identity — a
block that IS aws_s3_bucket.logs outranks one that merely mentions it — and results carry
the block's source, its location, and how many things reference it.

charBudget is shared across the returned nodes, so lowering maxNodes deepens each result
instead of merely shortening the list. Terraform blocks are small, so maxNodes=1 usually
returns the whole block and makes reading the file unnecessary. Anything truncated is
reported with its exact line range so you can open precisely that, never the whole file.

If nothing clears the relevance threshold this says so explicitly. That is a real miss and
a signal that grep is now reasonable — it is not a suggestion to retry with more words.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in exploreIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		maxNodes := in.MaxNodes
		if maxNodes == 0 {
			maxNodes = 5
		}
		budget := in.CharBudget
		if budget == 0 {
			budget = index.DefaultCharBudget
		}
		return text(render.Explore(ix, in.Query, maxNodes, budget))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_for_address",
		Description: `Reverse lookup: every expression that actually references an address.

This is the operation that justifies an index over grep. A reference here is a resolved
HCL traversal, so the result excludes the address written in a comment, inside a quoted
string such as an IAM policy ARN, or in documentation — which grep cannot tell apart from
a real dependency.

Use before renaming, moving or deleting anything. It also separates declarations from
uses, and shows the same address declared in more than one stack rather than silently
picking one.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addressIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.ForAddress(ix, in.Address, orBudget(in.CharBudget, index.DefaultListBudget)))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_impact",
		Description: `Transitive blast radius: everything reachable from an address.

'dependents' answers "what breaks if I change this". 'dependencies' answers "what does
this need to exist".

Every result states how much it knows, and there are three cases.

With a plan overlay loaded it names exactly what will be DESTROYED AND RECREATED, and
resolves count/for_each into real instance counts. With only a state overlay the instance
counts are real but replacement is unknowable. With neither, the answer is what is
CONNECTED — a lower bound, because expanding blocks could be any number of instances — and
it says so rather than implying more precision than it has.

Check terra_status to see which case you are in before relying on the answer.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in impactIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		dir := index.Dependents
		if in.Direction == string(index.Dependencies) {
			dir = index.Dependencies
		}
		depth := in.Depth
		if depth == 0 {
			depth = 3
		}
		return text(render.Impact(ix, in.Address, dir, depth, orBudget(in.CharBudget, index.DefaultListBudget)))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_modules",
		Description: `Module inventory across every root module in the repository, version skew first.

Answers "which stacks call this module, at which versions". Grep cannot do this: a module's
source and its version pin are separate attributes, in different files, in different
directories, and nothing textual connects one call site to another.

Sources pinned at more than one version are listed first and flagged, because that is the
finding rather than the background.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in modulesIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.Modules(ix, in.Filter, orBudget(in.CharBudget, index.DefaultListBudget)))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_orphans",
		Description: `Declarations nothing consumes: unreferenced variables and locals, and child-module
outputs no caller reads.

A caller passing a value does not count as using it. A variable every stack dutifully wires
up and nothing inside the module ever reads is dead configuration with live call sites,
and it is reported as such — that is the shape a linter and a grep both miss.

Root module outputs are excluded by design: they are the stack's public surface, so nothing
inside the repository is meant to consume them.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in budgetIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.Orphans(ix, orBudget(in.CharBudget, index.DefaultListBudget)))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_status",
		Description: `The shape of the index and how far to trust the other five tools.

Reports node and edge counts, the root modules discovered, how many references could not be
resolved (they point into remote modules this index cannot see), any parse errors, and
which stacks have a plan or state overlay.

Check this when an answer looks thinner than the repository should support — the cause is
usually unresolved references or a parse error, both reported here rather than silently
narrowing every other result. Check it before trusting terra_impact about replacement: a
repository with an overlay on some stacks and not others is the dangerous case, so the
stacks WITHOUT one are named explicitly.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.Status(ix, host.RepoRoot(), host.Builds()))
	})
}
