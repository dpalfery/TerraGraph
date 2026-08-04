# TerraGraph

An in-memory graph of a repository's Terraform configuration, served to coding agents over
MCP. It exists to replace the `grep` → `glob` → `read` loop an agent otherwise runs to
understand infrastructure code: fewer tokens to reach an answer, and a better answer,
because the index can make distinctions text search cannot.

## The distinction that justifies it

Terraform already supplies what a code index normally has to invent. Every block has a
formal, unambiguous address, and every reference to one is a parseable expression traversal.

So this is not a faster grep — it answers a different question:

```hcl
# The audit trail lands in aws_s3_bucket.logs — see the CloudTrail runbook.
resource "aws_s3_bucket" "logs" { ... }

resource "aws_s3_bucket_policy" "logs" {
  bucket = aws_s3_bucket.logs.id                    # a reference
  policy = jsonencode({
    Resource = "arn:aws:s3:::aws_s3_bucket.logs/*"  # not a reference
  })
}
```

`grep aws_s3_bucket.logs` returns four hits and cannot rank or distinguish them.
`terra_for_address aws_s3_bucket.logs` returns the one real dependency, because a reference
here is a resolved HCL traversal — a comment and a quoted ARN produce nothing.

## Tools

| Tool | Answers |
|---|---|
| `terra_explore` | ranked retrieval over the configuration, within a character budget |
| `terra_for_address` | what *actually* references this — not what mentions it |
| `terra_impact` | transitive blast radius, in either direction |
| `terra_modules` | which stacks call which module, at which version |
| `terra_orphans` | variables, locals and child-module outputs nothing consumes |
| `terra_status` | index shape, coverage gaps, and how far to trust the rest |

## Install

```bash
go build -o terragraph ./cmd/terragraph && go build -o terragraph-mcp ./cmd/terragraph-mcp
```

Register the MCP server against a repository:

```bash
claude mcp add terragraph -- /absolute/path/to/terragraph-mcp --repo /absolute/path/to/your/infra
```

The CLI takes the same subcommands, for use without an agent:

```bash
terragraph status --repo .
terragraph explore "where does the audit log bucket live"
terragraph refs aws_s3_bucket.logs
terragraph impact var.environment --depth 3
terragraph modules vpc
terragraph orphans
```

## How it works

```
tfhcl.Load()      walk repo, discover roots, parse HCL,        → *graph.Graph
                  extract reference edges from traversals
      │
index.BuildCorpus()  BM25 term statistics over block bodies    → *index.Corpus
      │
index.NewIndex()     immutable snapshot; ranking + queries     → *index.Index
      │
index.Host.Current() fingerprint, cache, rebuild whole
```

Each stage is a pure transform of the one before it, and **nothing writes to disk**.

### There is no database

The graph lives in process memory and is rebuilt from the `.tf` files on demand. Parsing and
vectorising a repository costs milliseconds, so a persistence tier would buy latency at the
price of a cache-invalidation problem. The fingerprint is the newest mtime plus the file
count, so an edit and a deletion are both noticed.

The consequence worth knowing: editing one file rebuilds every node. That is comfortable
into the low thousands of blocks and is the first thing to revisit above that.

### Ranking

Declared identity outranks prose, because a block that *is* `aws_s3_bucket.logs` is a better
answer than one that merely mentions it:

| Contribution | Weight |
|---|---|
| Exact address | 6.0 |
| Exact local name | 3.5 |
| Exact resource type | 3.0 |
| Partial address coverage | up to 4.5 |
| Body relevance (BM25 over the block and its comments) | up to 1.0 |

Scores are then scaled by **authority**: an address a `moved` block has vacated drops to
0.4, and `examples/`, `test/` and `fixtures/` drop to 0.5 — demoted rather than excluded,
because sometimes the example is the clearest answer, and an exact address match scores far
above the discount either way.

Terms are stemmed for plurals, so a question about "the audit log bucket" reaches a resource
named `logs`. Compound identifiers are split *and* kept, so `aws_s3_bucket` is reachable from
"s3 bucket" — which is how people actually type it.

### The relevance floor and the explicit miss

A node scoring below 0.25 is not returned, and a query where nothing clears the floor returns
a stated miss rather than a best-effort list.

This matters more than it looks. Callers are told to try retrieval before grepping; if a miss
comes back as three weak results, the caller has no signal to fall back and will answer from
whatever was nearest.

### The character budget

`charBudget` (default 12000) is the token-reduction mechanism. It is a total across the
returned nodes, so narrowing `maxNodes` deepens each result rather than merely shortening the
list. The budget bounds the node count as well as each node's share — a tight budget returns
fewer *complete* blocks rather than many truncated ones, which is the right trade when half a
resource is nearly useless.

Nothing is dropped silently. The response says whether a node was cut for budget or for
irrelevance, since only the former makes asking again worthwhile, and a truncated block names
its exact line range so the caller can open precisely that instead of the whole file.

## Limits, stated up front

- **Static HCL only.** `count` and `for_each` are detected but not expanded, so an impact set
  containing them is a lower bound. `terra_impact` reports what is *connected*, which is not
  what `terraform plan` would replace — every impact answer says so.
- **Remote modules are not indexed.** A reference into a registry or git module resolves to
  the module call and stops there. `terra_status` reports how many references this affects.
- **`.terraform/` is excluded.** It holds verbatim copies of initialised modules, so walking
  it would double-count exactly the call sites `terra_modules` is meant to total.

A plan/state overlay addressing the first two is designed for but not built: the seam is
`index.Host`, which already tracks its inputs separately.

## Development

```bash
go test ./...
```

`testdata/repo/` is a small multi-stack fixture carrying one of each interesting case: two
roots sharing a local module, a registry module pinned at two versions, an orphan variable
and an unconsumed output, a `moved` block, an aliased provider, a `dynamic` block, and a
resource address written in both a comment and a string literal — the last being the case
that must produce **no** edge.
