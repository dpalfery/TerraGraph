---
id: plans/terragraph-v1
title: TerraGraph v1 — an in-memory Terraform graph for agents
doc-type: plan
status: current
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-08-04
---

# TerraGraph v1 — an in-memory Terraform graph for agents

> **This is a record of intent, not current guidance.** The body below is the plan as
> authored on 2026-08-03, before any code existed, and it is deliberately left as written.
> Retrieval demotes `plan` documents to 0.55 for exactly this reason — see
> [the ontology](../documentation-ontology.md). For what the tool actually does, read the
> [README](../README.md); for how it does it, read the source.
>
> It is still `status: current` rather than `superseded` because phases 5 and 6 have not
> been done and this is the only place they are specified.

## Delivery status

| Phase | State |
|---|---|
| 1. Loader + model | **Done.** `internal/tfhcl`, `internal/graph`. |
| 2. Index + host | **Done.** `internal/index/corpus.go`, `host.go`. |
| 3. Ranking + budget | **Done.** `internal/index/index.go`, `internal/render`. |
| 4. MCP server | **Done.** `cmd/terragraph-mcp`, all six tools, verified over JSON-RPC stdio. |
| 5. Plan overlay | **Not started.** Gated by this plan's own instruction to evaluate first. |
| 6. Token benchmark | **Not started.** Needs a real Terraform repository; the fixture is too small to measure. |

Two things diverged from the plan as written and the plan has not been edited to match:

- **Package layout.** The architecture sketch below puts `corpus.go`, `index.go` and
  `host.go` under `internal/graph/`. They shipped in `internal/index/`, with
  `internal/graph/` holding only the node and edge model. A reader following the sketch
  will look in the wrong directory.
- **`.terraform/` is excluded, not demoted.** The plan says demote. It holds verbatim
  copies of every initialised module, so indexing it double-counts exactly the call sites
  `terra_modules` totals — a demotion does not fix a duplicate. `examples/`, `test/` and
  `fixtures/` are demoted as planned.

Four defects were found by running the tool rather than by review, and are recorded here
because each was a design assumption that did not survive contact: `moved` blocks emitted a
duplicate `REFERENCES` edge alongside `MOVED_FROM`; `provider = aws.replica` never resolved
because providers use a separate address space; Go's `flag` package silently ignored
`--repo` after a positional argument, indexing the wrong tree; and `charBudget` did not
bound output, because flooring each node's share without capping the node count let a
1000-character budget return three times that.

---

## Context

`~/git/Personal/TerraGraph` is empty — greenfield, not yet a git repo.

The goal is a **CodeGraph alternative for Terraform**: an agent-facing index that answers
questions about an HCL codebase without the agent burning tokens on `grep` / `glob` /
`read` cycles. Success is measured two ways — fewer tokens to reach an answer, and a
*better* answer, because the index distinguishes things text search cannot.

Two reference points shaped the design:

- **kyber-weave's DocGraph** ([`DocumentIndex.cs`](https://github.com/dpalfery/kyber-weave/blob/main/src/KyberWeave.Core/Docs/Search/DocumentIndex.cs))
  — an immutable in-memory index, rebuilt wholesale when a cheap fingerprint changes. No
  database. Its thesis: *ranking on declared identity beats ranking on word frequency*, and
  the operation justifying an index over grep is the reverse lookup (`docs_for_symbol`) —
  formal ownership, not textual occurrence.
- **CodeGraph** (colbymchenry) — the opposite shape: a persistent SQLite artifact plus a
  daemon. TerraGraph is an alternative to it, not a plugin into it, so it takes DocGraph's
  in-memory shape and CodeGraph's role.

**The asymmetry that makes this work.** DocGraph had to *invent* an identity ontology
(eleven doc types, `code-refs` frontmatter) because Markdown carries none. Terraform already
has one. `aws_s3_bucket.logs` is a formal, unambiguous, globally-unique address, and a
reference to it inside an expression is a parseable traversal — not a string that happens to
match. TerraGraph gets DocGraph's central advantage for free, and can go further than
DocGraph ever could: it can tell **declares** from **references** from **mentions in a
comment or a string**, which is exactly the distinction `grep aws_s3_bucket.logs` collapses.

## Decisions already made

| Question | Decision |
|---|---|
| Data source | Static HCL is the graph. Plan/state JSON is an **optional overlay** that degrades gracefully — mirroring how kyber-weave treats CodeGraph. |
| Language | **Go**. `hashicorp/hcl/v2` + `hclsyntax` is the only stack where expression-level reference extraction is a first-class primitive. |
| Persistence | **None.** Pure in-memory, rebuilt from a fingerprint. No SQLite, no export artifact, no cache-invalidation problem. |
| kyber-weave seam | **MCP-only, zero coupling.** kyber's agents call TerraGraph's tools alongside kyber's own. No changes in the kyber repo, no wire format to version. |
| Index scope | **Whole repo, auto-discovered** — every root module and every local child module in one graph, with a stack dimension per node. |

### One scope note

You selected all four query families. That is a large v1, so the phases below are ordered so
each one ships something usable on its own. One honest limitation to state up front rather
than discover later: **`terra_impact` is only fully truthful about replace-vs-update once the
plan overlay exists.** Static HCL tells you what is *connected*; only a plan knows what
*forces replacement*. Phase 1–4 impact answers are a reachability set, and the tool will say
so in its own output rather than implying more precision than it has.

## Prerequisites

Neither is installed:

```bash
brew install go && git init /Users/david_palfery/git/Personal/TerraGraph
```

`terraform` is already present at `/opt/homebrew/bin/terraform` — needed only for the Phase 5
overlay fixtures.

## Architecture

```
cmd/terragraph/          CLI  — human output, owns stdout for text
cmd/terragraph-mcp/      MCP  — JSON-RPC, separate binary
internal/tfhcl/          load: walk repo, discover roots, parse HCL     → *Config
internal/graph/          model: nodes, edges, addresses
  ├── corpus.go          BM25 term statistics over block bodies         → *Corpus
  ├── index.go           immutable snapshot; ranking + retrieval        → *Index
  └── host.go            fingerprint, cache, rebuild                    → *Host
internal/overlay/        Resolver interface + plan/state JSON adapter (Phase 5)
internal/render/         budgeted output shared by CLI and MCP
```

Each stage is a pure transform of the one before it, and **nothing writes to disk** — the
DocGraph pipeline shape, carried over directly.

**Two binaries, not one.** This is kyber-weave's hardest-won lesson and it applies verbatim:
JSON-RPC owns stdout, and any CLI that also writes there (colors, progress, tables) corrupts
the stream. Separate entry points make that structurally impossible rather than a discipline
you have to maintain. See [`src/KyberWeave.Mcp/Program.cs`](https://github.com/dpalfery/kyber-weave/blob/main/src/KyberWeave.Mcp/Program.cs).

### Node and edge model

Nodes are keyed by **Terraform address**, which is already canonical — no synthetic IDs.

| Kind | Address form |
|---|---|
| `resource` | `aws_s3_bucket.logs` |
| `data` | `data.aws_ami.ubuntu` |
| `module` (call) | `module.vpc` |
| `variable` / `output` / `local` | `var.env`, `output.arn`, `local.tags` |
| `provider` | `provider.aws`, `provider.aws.us_east_1` |
| `stack` | synthetic — one per discovered root module |
| `module_source` | synthetic — one per `source`+`version` pair, so version-skew groups by it |
| `moved` / `removed` / `import` | lifecycle blocks |

Edges: `REFERENCES` (expression traversals), `DEPENDS_ON` (explicit), `CALLS`, `PROVIDES`,
`INPUTS` (module arg → target variable), `MOVED_FROM`, `REPLACE_TRIGGERED_BY`.

The `REFERENCES` edge is the whole product. Extraction is **not** via
`hashicorp/terraform-config-inspect` — that library is deliberately shallow and returns
top-level metadata only, no inter-block references. Use `hclsyntax` and walk expression
bodies with `hcl.ExpressionVariables()` / traversal inspection.

### Ranking

Mirrors [`retrieval.md`](https://github.com/dpalfery/kyber-weave/blob/main/docs/docgraph/retrieval.md)'s
structure — identity outranks prose — but with Terraform's native identity in place of
frontmatter:

```
score = exact-identity + partial-identity + body-relevance
score = score × authority
```

| Contribution | Fires when |
|---|---|
| Exact address (`aws_s3_bucket.logs`) | highest — the query names the node outright |
| Exact local name (`logs`) | strong |
| Type match (`aws_s3_bucket`) | returns the family |
| Partial address coverage | query names part of a dotted address |
| Body relevance | BM25 over the block body + attached comments, squashed to 0..1 |

**Authority** is the demotion multiplier. DocGraph demotes drafts and superseded docs;
TerraGraph's equivalents are `examples/`, `test/`, `fixtures/`, `.terraform/` (demoted, not
excluded — sometimes the example *is* the answer) and addresses a `moved` block has vacated.

**Keep the relevance floor and the explicit miss.** This is the single most important
behavior to port. A query where nothing clears the threshold must return *"nothing scored
above the floor, N nodes considered"* — not three weak results. An agent told to try
TerraGraph before grepping needs a clear signal that it may now grep; a soft miss makes it
answer from whatever was nearest.

### The token budget — the mechanism, not a nicety

`charBudget` (default 12000, shared across returned nodes, floor per node) is *how* token
reduction happens. Port the two DocGraph refinements that make it work:

1. Narrowing `maxNodes` **deepens** each result rather than shortening the list — one knob
   gives depth or breadth.
2. Whatever did not fit is **named** in the response, and the caller is told whether it was
   dropped for lack of budget or lack of relevance — only the former makes asking again
   worthwhile. Silent truncation sends the agent straight back to `Read`.

## MCP tools

| Tool | Query family it serves |
|---|---|
| `terra_explore(query, maxNodes, charBudget)` | general retrieval — the `docs_explore` analogue |
| `terra_for_address(address)` | reverse lookup: everything referencing this. The `docs_for_symbol` analogue — declarations and references separated, comments excluded |
| `terra_impact(address, direction, depth)` | blast radius (states its static-only limits) |
| `terra_modules(name?)` | module reuse + version skew across stacks |
| `terra_orphans()` | unreferenced variables, unconsumed outputs, dead locals |
| `terra_status()` | node/edge counts, roots discovered, overlay loaded?, staleness — needed for the agent to trust the other five |

## Phases

1. **Loader + model** — repo walk, root discovery (dir with `backend`/`provider`/`*.tfvars`),
   HCL parse, node extraction, `REFERENCES` edge extraction. Ships: `terragraph stats`.
2. **Index + host** — `Corpus` BM25 stats, immutable `Index`, `Host` with an mtime+count
   fingerprint over `**/*.tf`. Single-clock for now; the two-clock split only earns its
   keep once the overlay lands.
3. **Ranking + budget** — scoring, authority, relevance floor, budgeted excerpting.
   Ships: `terragraph explore`.
4. **MCP server** — `github.com/modelcontextprotocol/go-sdk` over `StdioTransport`, all six
   tools. **This is the first genuinely useful milestone** — stop here and evaluate before
   building Phase 5.
5. **Plan overlay** — `Resolver` interface with a `terraform show -json` adapter, `IsAvailable`
   / `UnavailableReason` exactly like [`ICodeGraphResolver`](https://github.com/dpalfery/kyber-weave/blob/main/src/KyberWeave.Core/CodeGraph/ICodeGraphResolver.cs).
   Adds `for_each`/`count` expansion and honest replace-vs-update. Everything must still work
   fully without it.
6. **Token benchmark** — see below.

## Verification

**Fixtures.** A `testdata/` repo with at least: two root modules sharing one local module at
different call sites, a module called twice with different inputs, a deliberate orphan
variable and unconsumed output, a `moved` block, an aliased provider, and a resource
referenced from a comment *and* a string literal (the case that must **not** produce an edge —
this is the test that proves TerraGraph beats grep).

**Unit.** Table-driven Go tests per package. The edge extractor deserves the densest
coverage: `var.x`, `local.y.z`, `module.a.output`, `data.t.n.attr`, `each.value`, `count.index`,
and traversals inside `for` expressions and `dynamic` blocks.

**End-to-end, MCP.** Register the built binary and drive the tools for real:

```bash
claude mcp add terragraph -- /path/to/terragraph-mcp --repo /path/to/testdata
```

Then confirm by hand: `terra_for_address aws_s3_bucket.logs` returns the declaration plus
real referencing blocks and **omits** the comment and string-literal mentions; a nonsense
query returns the explicit miss, not three weak hits; and lowering `maxNodes` deepens rather
than shortens.

**Overlay (Phase 5).** `terraform init && terraform plan -out=tf.plan && terraform show -json tf.plan`
against a fixture, then confirm `terra_status` flips to overlay-loaded and `terra_impact`
starts reporting replacement. Then delete the plan file and confirm every tool still works
with only the joins degraded.

**Token benchmark — the actual success criterion.** Token reduction is the stated goal, so
measure it rather than assert it. Pick ~10 real questions against a real Terraform repo
("where is the VPC defined", "what uses var.environment", "which stacks call the eks module").
Answer each twice — once with grep/glob/read, once with TerraGraph — and record tokens
consumed and whether the answer was correct. Both numbers matter: a tool that halves tokens
while missing the answer is a regression, and this benchmark is what tells you which
`charBudget` default is right for *your* repos rather than DocGraph's.
