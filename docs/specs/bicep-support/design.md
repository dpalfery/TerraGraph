---
id: specs/bicep-support/design
title: Bicep support design
doc-type: spec
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-29
---

# Design

**Phase status:** Draft

Grounded in `docs/specs/bicep-support/requirements.md`. Requirements stay Draft until that phase is approved; this design is written to those agreed decisions so the specification can be reviewed as one set.

## Overview

`index.Host.Current` classifies the repository, then builds one graph with either `tfhcl.Load` or a new `bicep.Load`. Both return `*graph.Graph`. Ranking, budgets, the in-memory snapshot, and the two binaries stay. The MCP server registers `terra_*` or `bicep_*` from the language it actually indexed.

Bicep is parsed in Go from `.bicep` source. The official compiler is not a runtime dependency. `bicep build` and the JSON-RPC `bicep/compile` method emit an ARM template (Microsoft Learn: [Bicep CLI](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/bicep-cli), [Bicep CLI JSON-RPC](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/bicep-cli-jsonrpc)). That template has already dropped comments and folded references into ARM expressions, so it cannot enforce Requirement 4. The .NET semantic model in `Azure.Bicep.Core` can, and it would put a second runtime in the product. The Go parser accepts a stated subset of the v0.47.16 language and reports every other construct as a parse error on that file.

```mermaid
flowchart TD
  current[index.Host.Current]
  classify[source.Classify]
  refuse[mixed-language error and no snapshot]
  tf[tfhcl.Load]
  bic[bicep.Load]
  graph[graph.Graph]
  idx[index.Index]
  current --> classify
  classify -->|operative mix| refuse
  classify -->|terraform| tf
  classify -->|bicep| bic
  tf --> graph
  bic --> graph
  graph --> idx
```

## Architecture

### Classification

New package `internal/source`. It owns the skip-directory set and the demoted-segment set that `tfhcl.Discover` uses today, and `tfhcl` calls those helpers so the two cannot drift (Requirement 2.7).

`Classify(repoRoot string) (Result, error)` walks the tree once.

`Result` carries:

- `Language`, one of `terraform` or `bicep`
- `Skipped`, repo-relative paths of the other language that were ignored because they were demoted, or because the selected language does not read them
- the file list the selected loader will parse

Rules, in order:

1. Ignore skip directories.
2. Count `.tf` and `.bicep` outside demoted segments. If both counts are greater than zero, return `MixedLanguageError` with both counts. `Error()` includes the counts.
3. Otherwise if any operative `.bicep` exists, language is `bicep`. Demoted `.tf` paths go to `Skipped`.
4. Otherwise if any `.tf` exists, language is `terraform`. Demoted `.bicep` paths go to `Skipped`.
5. Otherwise if any `.bicep` exists, language is `bicep`.
6. Otherwise language is `terraform` and the file list is empty.

`MixedLanguageError` is not a parse failure. `Host.Current` returns it with a nil index and drops any snapshot already held. A later call that classifies cleanly builds a new snapshot. A failed `tfhcl.Load` or `bicep.Load` still keeps the previous snapshot, which is the behavior `Host.Current` has today for a broken rebuild.

### Loader selection and fingerprint

`Host.Current` calls `Classify` before either loader. Terraform calls `tfhcl.Load`. Bicep calls `bicep.Load`. The index stores the language and `Skipped` beside the graph so status can print them without overloading parse errors.

`computeConfigStamp` fingerprints the file list `Classify` selected. A Terraform snapshot still changes when a `.tf` file changes, including a demoted `.tf` file, because those files are indexed today. It does not change when a skipped `.bicep` file changes. A Bicep snapshot changes when a selected `.bicep` file is created, edited, or deleted. `.bicepparam` and `.json` are not stamped.

`--plan` is applied only after a Terraform load, through the existing `overlay.Discover` / `overlay.ParseExplicit` path. On a Bicep load the host does not call the overlay, leaves the resolver empty, and records that `--plan` does not apply. The static index still returns.

### Bicep loader

New package `internal/bicep`. `Load(repoRoot string) (*graph.Graph, error)` mirrors `tfhcl.Load`: discover files, build nodes, classify roots, then resolve edges once every node exists. It returns `graph.Build(...)`. It performs no writes and no network I/O. `Load` itself does not re-walk for language detection; it receives the `.bicep` paths `Classify` already accepted, including demoted ones. A walk error from `Classify` is the load error.

Parse errors are strings on `Graph.ParseErrors`, the same field Terraform uses. One bad file appends one error and contributes no nodes.

### Tools

`cmd/terragraph-mcp` reads the language from the index built at startup. Classification failure exits 1 before `registerTools`. Terraform registers the six existing tools. Bicep registers the six `bicep_*` tools and does not register `terra_*`. The server description on a Bicep repository is an in-memory graph of that repository's Bicep configuration, with no token figure.

The tool handlers call the same `index` and `render` functions as the Terraform tools. Render branches on language for headings and for the empty-orphan sentence. Budgets stay `index.DefaultCharBudget` (12000) for explore and 4000 for the other four relationship tools. Impact depth stays 3.

CLI verbs in `cmd/terragraph` stay `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`. The static usage banner keeps those verbs. Command output names the language that was indexed. A mixed repository exits 1 with `MixedLanguageError`.

## Components and interfaces

| Piece | Responsibility |
|---|---|
| `internal/source` | Skip and demote rules, `Classify`, `MixedLanguageError` |
| `internal/tfhcl` | Unchanged Terraform load, using the shared skip and demote rules |
| `internal/bicep` | `.bicep` subset parser, nodes, edges, roots |
| `internal/graph` | New kinds, `SCOPED_TO`, `ResourceID`, `ScopeExpr` |
| `internal/index` | Loader selection, fingerprint, alias lookup, orphan kinds |
| `internal/render` | Language-specific prose |
| `cmd/terragraph`, `cmd/terragraph-mcp` | Same verbs; MCP names follow the language |

`bicep.Load` is the only new entry into `graph.Build` besides `tfhcl.Load`.

## Data model

Terraform keys stay `ModuleDir|Address`, and `ModuleDir` stays the repo-relative directory. For a Bicep node, `ModuleDir` holds the repo-relative `.bicep` path, because that file is the resolution scope. `Address` is the symbolic name (`stg`, not `Microsoft.Storage/storageAccounts/stg`). `File` repeats that path so locations stay `file:line`. `Key()` does not change. Two files that declare `stg` produce two keys.

`Node.ResourceID` is the alias. It is empty unless type, name, and every scope argument are string literals. Supported scope forms for an alias:

| Scope expression | ID shape |
|---|---|
| `resourceGroup('<sub>', '<rg>')` | `/subscriptions/<sub>/resourceGroups/<rg>/providers/<type-path>/<name>` |
| `subscription('<sub>')` | `/subscriptions/<sub>/providers/<type-path>/<name>` |
| `managementGroup('<mg>')` | `/providers/Microsoft.Management/managementGroups/<mg>/providers/<type-path>/<name>` |
| `tenant()` | `/providers/<type-path>/<name>` |

`<type-path>` splits the literal type on `/` and drops the `@api-version`. `resourceGroup()` with no arguments, `subscription()` with no arguments, a parameter, a variable, or an interpolation leaves `ResourceID` empty. The scope text is still stored on `ScopeExpr` when no `SCOPED_TO` edge exists. `Graph` indexes non-empty `ResourceID` values for lookup. The alias is not a second node and is not written into `Address`.

New kinds, added next to the existing closed set:

| Kind constant | Value | Bicep construct |
|---|---|---|
| `KindParam` | `param` | `param` |
| `KindVar` | `var` | `var` |
| `KindExisting` | `existing` | `resource ... existing` |

`KindResource`, `KindModuleCall`, `KindOutput`, `KindStack`, and `KindModuleSource` are reused. `KindVariable` and `KindLocal` are not used for Bicep.

New edge kind: `EdgeScopedTo` with value `SCOPED_TO`.

Directions:

| Edge | From | To |
|---|---|---|
| `REFERENCES` | using declaration | used declaration |
| `DEPENDS_ON` | declaration with `dependsOn` | each dependency |
| `CALLS` | module node | `module_source` node (`source.<literal>`) |
| `CONTAINS` | parent resource | child |
| `SCOPED_TO` | scoped declaration | the node the scope expression names |
| `INPUTS` | local module call | child `param` |

A `module_source` node is created for every module source string, local or not, the way `tfhcl` already does. External sources get no further nodes. A reference such as `mod.outputs.accountId` on an external module resolves as far as the module node and then records `Unresolved`.

`HasForEach` is set when the declaration itself uses `for`. The body is still walked for references. The parser does not expand the loop.

`@secure()` sets `Sensitive` on that `param`. `@description('...')` with a string literal sets `Doc`. Other decorators are ignored and do not fail the file.

One `KindStack` node per root file. Its address is `stack.` plus the repo-relative path. `Graph.Roots` lists those paths. A file reached by a local `./` or `../` module literal is removed from the root set. Path normalization is relative to the calling file and cleaned with `path/filepath`. A cycle stops at the same depth cap `tfhcl` uses (`maxModulePathDepth`, 32). A child reached from two roots gets `Shared` true and no single `Stack`.

`targetScope = '<literal>'` is stored as `ScopeExpr` on that file's stack node. It does not by itself produce a resource ID.

### Supported syntax

A file is indexed when every statement is in this subset. Fixtures are written so that Bicep v0.47.16 accepts them; the tests do not run the compiler.

Indexed:

- `//` and `/* */` comments
- `targetScope = '<literal>'`
- decorators, of which only `@secure()` and `@description()` change the node
- `param`, `var`, `output`, `resource`, `resource ... existing`, `module`
- `if <expr>` on a resource or module, without a new flag
- `for <name> in <expr>` on a resource or module, setting `HasForEach`
- object and array bodies
- `parent`, `scope`, `dependsOn`, module `params`, and `name`
- expressions: identifiers, member access, index, parentheses, unary and binary operators, conditional expressions, string literals, interpolation, function calls, and `for` expressions

Function names are not references. Arguments are walked. `resourceGroup`, `subscription`, `managementGroup`, `tenant`, `deployment`, and `environment` are scope or runtime functions, not declarations. An argument that is itself a symbolic name still produces `REFERENCES`.

`assert <expr>` is recognized, produces no node, and is walked so a name inside the assertion is still a reference.

Rejected, one parse error for the whole file, rest of the repository kept:

- `import`, `export`, `type`, `extension`
- any statement the parser does not recognize
- a token error

The file is omitted rather than half-indexed. A later widening of the subset is a requirements change.

### Lookup and ranking

`Graph.ByAddress` already returns every node with that symbolic name. `ForAddress` also matches `ResourceID`. A query equal to a `ResourceID` resolves to that node even though `Address` is the symbolic name. Explore gives that match the existing exact-address weight (6.0). Weights are not retuned.

`Orphans` treats `KindParam` as it treats `KindVariable`, and `KindVar` as it treats `KindLocal`. Root-file outputs are excluded by the same root check, using the Bicep file path as the module scope. Child outputs with no incoming `REFERENCES` from a caller are orphans. `INPUTS` into a param counts as wired, not as read, which is the Terraform variable rule.

## Error handling

| Condition | Result |
|---|---|
| Operative `.tf` and operative `.bicep` | `MixedLanguageError`, no snapshot, CLI and MCP exit 1 |
| Unreadable repository root | existing `StatFS` / walk error, exit 1 |
| One `.bicep` file outside the subset or syntactically invalid | `ParseErrors` entry, other files indexed |
| External module, or output access on one | node retained, `Unresolved` set, status counts it |
| `--plan` on Bicep | static graph served, status says the flag does not apply |
| Failed rebuild of a repository that previously loaded | previous snapshot kept, except a new mixed-language classification, which discards it |
| Empty repository | empty Terraform graph, `terra_*` tools |

Parse errors name the repo-relative file. They do not include source snippets large enough to dump a file into status.

## Testing strategy

Tests are Go tests under `go test -race ./...`, which is the CI `test` job. No `bicep` binary, no Azure credentials, no network.

- `internal/source`: table tests for the six classification outcomes, using temp directories.
- `internal/tfhcl`: existing fixture tests stay green after the skip-list move.
- `internal/bicep`: synthetic files under `testdata/bicep/`. One fixture covers references against the same names buried in a comment, a string, and an interpolation. One covers two files with the same symbolic name. One covers a literal resource ID and a `resourceGroup()` with no alias. One covers a local module and a `br:` module. One covers a rejected `import` beside a valid second file.
- `internal/index`: fingerprint changes on `.bicep` edit for a Bicep repo and does not for a skipped `.bicep` in a Terraform repo; mixed classification returns the error and drops a prior snapshot.
- `internal/render` and a `cmd/terragraph-mcp` registration test: Bicep tool names present and `terra_*` absent, and the reverse for Terraform.

Fixtures contain no secrets. `@secure()` is tested with a placeholder parameter name, not a credential.

## Alternatives rejected

**ARM JSON as the graph source.** It cannot separate a reference from a mention, which is Requirement 4.

**Shelling out to `bicep build`.** The output is that ARM JSON. The command also writes a file beside the source and restores external modules, which breaks Requirements 3 and 9.

**Embedding `Azure.Bicep.Core`.** It is the real semantic model and the NuGet package for v0.47.16 targets .NET. TerraGraph is a pair of Go binaries. The subset parser is the cost of keeping that.

**One graph for a mixed repository.** Addresses and tool names would need a language dimension, and a partial index looks complete. Requirement 2 refuses the mix instead.

**Resource ID as the only key.** Params, vars, outputs, and module calls have none, and most resource names are interpolated. The symbolic name is always present. The ID is an alias when it is fully written down.

**Reusing `variable` and `local` for `param` and `var`.** The words would be wrong in tool output. The new kinds are the model change.

**A second set of questions under the Bicep tool names.** Explore, reverse lookup, impact, modules, orphans, and status are the jobs. Only the names and the wording change.

**A what-if overlay in this specification.** Azure what-if is a deployment prediction. It needs a live call or a captured document, and its actions are not Terraform plan actions. Requirement 11 leaves it out. The Terraform overlay code stays where it is.

## Trace

| Requirement | Design section |
|---|---|
| 1 | Loader selection, Testing strategy |
| 2 | Classification |
| 3 | Bicep loader, Supported syntax |
| 4 | Supported syntax |
| 5 | Data model |
| 6 | Data model |
| 7 | Data model |
| 8 | Data model |
| 9 | Data model, Error handling |
| 10 | Tools, Lookup and ranking |
| 11 | Loader selection and fingerprint |
| 12 | Supported syntax, Error handling |
| 13 | Loader selection and fingerprint |
| 14 | Tools |
