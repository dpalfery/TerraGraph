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
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func main() {
	repo := flag.String("repo", ".", "repository to index")
	flag.Parse()

	if err := index.StatFS(*repo); err != nil {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: cannot read %s: %v\n", *repo, err)
		os.Exit(1)
	}

	host := index.NewHost(*repo)

	// Build once at startup so a broken repository fails loudly here rather than inside
	// the first tool call, where the agent would read it as "no results".
	if _, err := host.Current(); err != nil {
		fmt.Fprintf(os.Stderr, "terragraph-mcp: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:        "terragraph",
		Title:       "TerraGraph",
		Description: "An in-memory graph of this repository's Terraform configuration.",
		Version:     version,
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
	Address string `json:"address" jsonschema:"A Terraform address (aws_s3_bucket.logs, var.environment, module.vpc), a bare local name, or a resource type."`
}

type impactIn struct {
	Address   string `json:"address" jsonschema:"The address to trace from."`
	Direction string `json:"direction,omitempty" jsonschema:"'dependents' (default) for what a change would affect, or 'dependencies' for what this needs."`
	Depth     int    `json:"depth,omitempty" jsonschema:"How many hops to walk. Defaults to 3."`
}

type modulesIn struct {
	Filter string `json:"filter,omitempty" jsonschema:"Substring of a module source or call name. Omit to inventory every module."`
}

type emptyIn struct{}

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
		return text(render.ForAddress(ix, in.Address))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_impact",
		Description: `Transitive blast radius: everything reachable from an address.

'dependents' answers "what breaks if I change this". 'dependencies' answers "what does
this need to exist".

Important limit, stated in every result: this is what is CONNECTED, not what a plan would
replace. Static configuration knows which expressions read which addresses; only
'terraform plan' knows which changes force replacement. Blocks using count or for_each are
named, because their real instance count is unknown without a plan, which makes the set a
lower bound.`,
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
		return text(render.Impact(ix, in.Address, dir, depth))
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
		return text(render.Modules(ix, in.Filter))
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.Orphans(ix))
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "terra_status",
		Description: `The shape of the index and how far to trust the other five tools.

Reports node and edge counts, the root modules discovered, how many references could not be
resolved (they point into remote modules this index cannot see), any parse errors, and
whether a plan overlay is loaded. Check this when an answer looks thinner than the
repository should support — the cause is usually unresolved references or a parse error,
both of which are reported here rather than silently narrowing every other result.`,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
		ix, err := current()
		if err != nil {
			return nil, nil, err
		}
		return text(render.Status(ix, host.RepoRoot(), host.Builds()))
	})
}
