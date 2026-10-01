---
id: specs/bicep-support/design
title: Bicep support design
doc-type: spec
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-30
---

# Design

**Phase status:** Approved (human approval 2026-09-30)

Grounded in `docs/specs/bicep-support/requirements.md` (revised 2026-09-30, approved 2026-09-30). This design is written to those agreed decisions so the specification can be reviewed as one set. The indexed and recognized-and-skipped syntax subset is owned by Requirement 12; this artifact records parser strategy and graph mapping, not a second copy of that list. Development mode for the later tasks phase is test-first; test contracts live in `tasks.md`.

## Overview

`index.Host.Current` classifies the repository, then builds one graph with either `tfhcl.Load` or a new `bicep.Load`. Both return `*graph.Graph`. Ranking, budgets, the in-memory snapshot, and the two binaries stay. The MCP server registers `terra_*` or `bicep_*` from the language classified at process start.

Bicep is parsed in Go from `.bicep` source. The official compiler is not a runtime dependency. `bicep build` and the JSON-RPC `bicep/compile` method emit an ARM template (Microsoft Learn: [Bicep CLI](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/bicep-cli), [Bicep CLI JSON-RPC](https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/bicep-cli-jsonrpc)). That template has already dropped comments and folded references into ARM expressions, so it cannot enforce Requirement 4. The .NET semantic model in `Azure.Bicep.Core` can, and it would put a second runtime in the product.

The Go parser is statement-oriented against the v0.47.16 surface. Requirement 12 names what is indexed, what is recognized and skipped, and what omits a file. Only a token error or a statement that is in neither 12.3 nor 12.4/12.5 omits that file. Recognized-and-skipped constructs, including `extension` and `import`, keep the file.

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

New package `internal/source`. It owns the skip-directory set and the demoted-segment set that `tfhcl.Discover` uses today, and `tfhcl` calls those helpers so the two cannot drift (Requirement 2.7). `index.Authority` uses the same demote helper so a Bicep path under `examples/` gets the 0.5 discount rather than a Terraform-only check.

`Classify(repoRoot string) (Result, error)` walks the tree once.

`Result` carries:

- `Language`, one of `terraform` or `bicep`
- `Skipped`, repo-relative paths of the *other* language that were ignored because they were demoted
- the file list the selected loader will parse, including demoted files of the *selected* language

A demoted file of the selected language is indexed and ranking-demoted. It is not excluded and it is not listed in `Skipped` (Requirement 2.9). `Skipped` is only the other language.

Rules, in order:

1. Ignore skip directories. Do not walk them.
2. A name ending in `.tf.json` is not a Terraform source. Classification does not count it, matching `tfhcl.Discover`, which already skips `.tf.json` (Requirement 2.8).
3. Count `.tf` and `.bicep` outside demoted segments. If both counts are greater than zero, return `MixedLanguageError` with both counts. `Error()` includes the counts.
4. Otherwise if any operative `.bicep` exists, language is `bicep`. Demoted `.tf` paths go to `Skipped`. Demoted `.bicep` files stay on the parse list.
5. Otherwise if any `.tf` exists, language is `terraform`. Demoted `.bicep` paths go to `Skipped`. Demoted `.tf` files stay on the parse list.
6. Otherwise if any `.bicep` exists, language is `bicep` (including a repository whose only `.bicep` files are demoted).
7. Otherwise language is `terraform` and the file list is empty.

`MixedLanguageError` is not a parse failure. Requirement 2.1 forbids serving a previously built snapshot: `Host.Current` returns the error with a nil index and drops any snapshot already held. A later call that classifies cleanly builds a new snapshot. A failed `tfhcl.Load` or `bicep.Load` still keeps the previous snapshot, which is the behavior `Host.Current` has today for a broken rebuild. Keeping a stale graph with an error annotation on a mixed repository was considered and rejected: it would violate 2.1.

### Loader selection and fingerprint

`Host.Current` calls `Classify` before either loader. Terraform calls `tfhcl.Load`. Bicep calls `bicep.Load`. The index stores the language and `Skipped` beside the graph so status can print them without overloading parse errors.

`computeConfigStamp` fingerprints the file list `Classify` selected. A Terraform snapshot still changes when a `.tf` file changes, including a demoted `.tf` file, because those files are indexed today. It does not change when a skipped `.bicep` file changes. A Bicep snapshot changes when a selected `.bicep` file is created, edited, or deleted. `.bicepparam` and `.json` are not stamped.

`--plan` is applied only after a Terraform load, through the existing `overlay.Discover` / `overlay.ParseExplicit` path. On a Bicep load the host does not call the overlay, leaves the resolver empty, and records that `--plan` does not apply. The static index still returns. Bicep `Graph.Roots` are file paths (see File-scoped ModuleDir); passing them to `overlay.Discover` would treat those paths as directories and is forbidden.

### Bicep loader

New package `internal/bicep`. `Load(repoRoot string) (*graph.Graph, error)` mirrors `tfhcl.Load`: discover files, build nodes, classify roots, then resolve edges once every node exists. It returns `graph.Build(...)`. It performs no writes and no network I/O. `Load` itself does not re-walk for language detection; it receives the `.bicep` paths `Classify` already accepted, including demoted ones. A walk error from `Classify` is the load error. The loader does not read `bicepconfig.json`; a module source that is only a config alias is non-local (Requirement 8.3).

Parse errors are strings on `Graph.ParseErrors`, the same field Terraform uses. A token error or an unrecognized statement omits that file: one error, no nodes from it, other files indexed (Requirement 12.1). A recognized-and-skipped statement does not omit the file and does not produce a node, except that `assert` expressions are still walked and `import` records a coverage-gap marker so imported names resolve as unresolved.

Per-file omission is coarser than Terraform's keep-partial behavior on a damaged `.tf` file. That is accepted: the HCL parser can resynchronize on block boundaries; the Bicep scanner does not, and widening to per-statement recovery after a token error is a requirements change. Recognized-and-skipped already keeps the file for known extra statements, which is the case whole-file rejection previously got wrong for `extension`.

### Tools

`cmd/terragraph-mcp` classifies once at startup, before `registerTools`. Classification failure exits 1. Terraform registers the six existing tools. Bicep registers the six `bicep_*` tools and does not register `terra_*`. The server description on a Bicep repository is an in-memory graph of that repository's Bicep configuration, with no token figure.

Tool names are not re-checked on each call. An MCP process that started on Terraform keeps `terra_*` for the life of that process; one that started on Bicep keeps `bicep_*`. A language flip (the repository becomes the other language) requires a restart. Same-language edits still rebuild through `Current` (Requirement 13). A mixed classification mid-session returns `MixedLanguageError` and drops the snapshot; it does not re-register tools.

The tool handlers call the same `index` and `render` functions as the Terraform tools. Render branches on language for headings, identity wording, and the empty-orphan sentence. Budgets stay `index.DefaultCharBudget` (12000) for explore and 4000 for the other four relationship tools. Impact depth stays 3.

`bicep_impact` walks the closed Bicep set `REFERENCES`, `DEPENDS_ON`, `CONTAINS`, `SCOPED_TO`, `CALLS`, and `INPUTS`. It does not walk every edge kind the way `terra_impact` currently does. Terraform `PROVIDES`, `MOVED_FROM`, and `REPLACE_TRIGGERED_BY` are not required on a Bicep graph and are not part of the walk. Terraform `Impact` is unchanged.

CLI verbs in `cmd/terragraph` stay `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`. The static usage banner keeps those verbs. Command output names the language that was indexed. A mixed repository exits 1 with `MixedLanguageError`.

## Components and interfaces

| Piece | Responsibility |
|---|---|
| `internal/source` | Skip and demote rules, `Classify`, `MixedLanguageError` |
| `internal/tfhcl` | Unchanged Terraform load, using the shared skip and demote rules |
| `internal/bicep` | `.bicep` parser, nodes, edges, roots |
| `internal/graph` | New kinds, `SCOPED_TO`, `ResourceID`, `ScopeExpr` |
| `internal/index` | Loader selection, fingerprint, alias lookup, type-path matching, Bicep impact walk, orphan kinds |
| `internal/render` | Language-specific prose |
| `cmd/terragraph`, `cmd/terragraph-mcp` | Same verbs; MCP names follow the language classified at start |

`bicep.Load` is the only new entry into `graph.Build` besides `tfhcl.Load`.

## Data model

Terraform keys stay `ModuleDir|Address`, and `ModuleDir` stays the repo-relative directory. For a Bicep node, `ModuleDir` holds the repo-relative `.bicep` path, because that file is the resolution scope. `Address` is the symbolic name (`stg`, not `Microsoft.Storage/storageAccounts/stg`). `File` repeats that path so locations stay `file:line`. `Key()` does not change. Two files that declare `stg` produce two keys.

### File-scoped ModuleDir

The general rule: a Bicep file is a module. Roots, stack identity, and `isRootModule` compare file paths, not directories.

| Surface | Bicep value | Implication |
|---|---|---|
| `Graph.Roots` | repo-relative `.bicep` paths | Two files in one directory can both be roots |
| `Node.ModuleDir` | that file path | `Key()` distinguishes `infra/main.bicep|stg` from `infra/net.bicep|stg` |
| `Node.Stack` on a root | the same file path | `writeIdentity` treats `Stack == ModuleDir` as "this is the root" |
| `Node.Stack` on a single-parent child | the unique calling file path | Matches Terraform `OwningStack`, which returns the unique caller, not a walk to a root |
| `pathsByDir` | empty map | Overlay instance lookup is unused; `InstancesOf` short-circuits when no overlay is loaded |
| `isRootModule` | `ModuleDir` ∈ `Roots` | Task 6 orphan exclusion of root-file outputs works if both sides are file paths |
| Modules display | `Stack` or `ModuleDir` printed as a file path | Task 7 render must not assume a directory. Shared prose is "shared by several callers", because callers need not be roots |
| Overlay path | not consulted | `overlay.Discover` joins `repoRoot` to each root as a directory. Host must not pass Bicep roots there |

Empty `pathsByDir` is a choice, not an omission. Populating it with file-path keys would still be useless without an overlay, and would invite a later caller to treat those keys as directories.

### Resource ID alias

Requirement 5 owns when an alias exists. The mapping here is how `ResourceID` and `ScopeExpr` are filled.

`Node.ResourceID` is the alias. It is empty unless type, name, and every ID segment are string literals. A resource with no `scope` property inherits the file `targetScope` for `ScopeExpr` and omits the alias: implicit current-scope is not a literal ID segment (Requirement 5.6). `targetScope` that is a string literal is stored as `ScopeExpr` on that file's stack node; any other `targetScope` expression stores the expression text and is never used to form an alias; an omitted `targetScope` is treated as `resourceGroup` for `ScopeExpr` (Requirement 5.10).

Alias forms when `scope` is a scope function (Requirement 5.7). `<type-path>` is the declared type with the `@api-version` suffix stripped.

| Scope expression | Alias |
|---|---|
| `resourceGroup('<sub>', '<rg>')` plus literal name and type | `/subscriptions/<sub>/resourceGroups/<rg>/providers/<type-path>/<name>` |
| `subscription('<sub>')` plus literal name and type | `/subscriptions/<sub>/providers/<type-path>/<name>` |
| `managementGroup('<mg>')` plus literal name and type | `/providers/Microsoft.Management/managementGroups/<mg>/providers/<type-path>/<name>` |
| `tenant()` plus literal name and type | `/providers/<type-path>/<name>` |
| `resourceGroup('<rg>')` (one argument) | omit alias; store `ScopeExpr` |
| `resourceGroup()`, `subscription()`, `managementGroup()` (no arguments) | omit alias; store `ScopeExpr` |

Parent-derived IDs (Requirement 5.8) are adopted, not deferred: when `parent` names a node that already has a `ResourceID`, and the child's name and type are string literals, the child alias is that parent alias plus the child type-path plus the child name. If the parent has no alias or any child segment is computed, omit the child alias.

When `scope` is a function whose argument names another declaration, including `scope: resourceGroup(rg.name)`, emit `REFERENCES` to that declaration, emit no `SCOPED_TO`, and omit the alias (Requirement 5.9). `SCOPED_TO` is only for a `scope` that names a node directly.

`Graph` indexes non-empty `ResourceID` values for lookup. The alias is not a second node and is not written into `Address`. Params, vars, outputs, and module calls never receive a resource ID alias. `Node.Version` stays empty on Bicep module calls; a registry or template-spec version in the source string stays in `Source`.

### Kinds, edges, flags

`existing` is a modifier on `resource` in the language and a distinct graph kind so a read can be told from a deploy (Requirement 6.10). It maps to `KindExisting`, not to `KindResource`.

New kinds, added next to the existing closed set:

| Kind constant | Value | Bicep construct |
|---|---|---|
| `KindParam` | `param` | `param` |
| `KindVar` | `var` | `var` |
| `KindExisting` | `existing` | `resource ... existing` (modifier → kind) |

`KindResource`, `KindModuleCall`, `KindOutput`, `KindStack`, and `KindModuleSource` are reused. `KindVariable` and `KindLocal` are not used for Bicep.

New edge kind: `EdgeScopedTo` with value `SCOPED_TO`.

Directions:

| Edge | From | To |
|---|---|---|
| `REFERENCES` | using declaration | used declaration |
| `DEPENDS_ON` | declaration with `dependsOn` | each dependency |
| `CALLS` | module node | `module_source` node (`source.<literal>`) |
| `CONTAINS` | parent resource | child |
| `SCOPED_TO` | scoped declaration | the node the scope expression names directly |
| `INPUTS` | local module call | child `param` |

A `module_source` node is created for every module source string, local or not, the way `tfhcl` already does. External sources get no further nodes.

`HasForEach` is set when the resource or module declaration itself uses `for`. A `for` expression on a `var` or `output` is walked and does not set `HasForEach`. `if <expr>` on a resource or module keeps one node, walks the condition, and sets neither `HasForEach` nor `HasCount`. Loops and conditionals are not expanded. `HasCount` is false on every Bicep node (Requirement 6.9). `ExpandingBlocks` for Bicep is therefore `HasForEach` only; impact and status must not imply `count` instance expansion.

`@secure()` sets `Sensitive` on that `param`. An output decorated with `@secure()` or declared as `secureString` or `secureObject` also sets `Sensitive`. `@description('...')` with a string literal sets `Doc`. `@batchSize()` is kept: the decorator remains visible in `Body` and does not fail the parse. No new graph field is added. Other decorators are ignored and do not fail the file.

### Roots and Shared

One `KindStack` node per root file. Its address is `stack.` plus the repo-relative path. `Graph.Roots` lists those file paths. A file reached by a local `./` or `../` module literal is removed from the root set. Path normalization is relative to the calling file and cleaned with `path/filepath`. A cycle stops at the same depth cap `tfhcl` uses (`maxModulePathDepth`, 32). A module source is local only when it is a string literal beginning with `./` or `../`.

`Shared` matches Terraform `OwningStack` (Requirement 8.4; Bicep analogue, mapped directory→file), not "more than one root":

- A root file owns itself: `Stack` is that file path, `Shared` is false.
- A child reached from exactly one distinct calling `.bicep` file: `Shared` is false, `Stack` is that caller file. Two call sites in the same calling file still count as one caller.
- A child reached from more than one distinct calling file: `Shared` is true, `Stack` is empty. The callers need not themselves be roots.

The single-parent rule is stated and tested: one distinct caller ⇒ `Shared == false`.

### Parser strategy

The parser is a Go statement scan of `.bicep` source. It does not invoke the `bicep` binary. The normative indexed subset, recognized-and-skipped set, and omit-file rule are Requirement 12. This section is how the parser applies that rule.

Three outcomes per statement:

1. Indexed (Requirement 12.3): emit the node and walk its expressions.
2. Recognized-and-skipped (Requirement 12.4): emit no node (`assert` expressions are still walked for references) and keep the file. `extension` and `provider` declarations are this case, not a whole-file reject. So are `metadata`, `func`, `type`, `export`, and `assert`.
3. `import` (Requirement 12.5): keep the file, emit no nodes for imported symbols, record a coverage-gap marker on `ParseErrors` with a `coverage gap:` prefix; no sibling slice; the file is kept; status reuses existing rendering.
4. Token error, or a statement in neither 12.3 nor 12.4/12.5 (Requirement 12.1): omit the file, append one parse error that names it, index the other files.

The parser maintains a scope stack for bound identifiers (Requirement 4.5). `for item in <expr>` and `for (item, index) in <expr>` bind those names in the loop body. Lambdas in `map`, `filter`, `reduce`, and `sort` (`x =>`) bind their parameters in the lambda body. A bound name is not a reference and shadows a declaration of the same name. The collection expression, the loop or lambda body, and `if` conditions are walked. Tuple `for` is supported. `for` expressions (including on `var`/`output` and nested arrays or objects) are walked, not expanded.

String literals are single-quoted `'...'` and triple-quoted multiline `'''...'''`, including verbatim multiline forms that do not interpolate. A name inside those literals is not a reference. Interpolations (`${...}`) in either form are walked.

Function names are not references. Arguments are walked. `resourceGroup`, `subscription`, `managementGroup`, `tenant`, `deployment`, and `environment` are scope or runtime functions, not declarations.

A later widening of Requirement 12 is a requirements change, not a parser bug.

### Lookup and ranking

`Graph.ByAddress` already returns every node with that symbolic name. `ForAddress` also matches `ResourceID`. A query equal to a `ResourceID` resolves to that node even though `Address` is the symbolic name. Explore gives that match the existing exact-address weight (6.0). Weights are not retuned.

When ranking or matching a Bicep resource type, compare the type path with the `@api-version` suffix stripped (Requirement 10.10). `scoreExact` awards `typeWeight` when the query equals either the stored `Type` or that stripped path. `ByType` for Bicep also hits on the stripped path, so `Microsoft.Storage/storageAccounts` matches `Microsoft.Storage/storageAccounts@2023-01-01`. `Graph.Build` may register both keys; the stored `Node.Type` still keeps the API version (Requirement 6.5).

Authority applies the demoted-path 0.5 discount to Bicep files under demoted segments. Those nodes stay in the graph. Bicep has no analogue of Terraform `moved`/`Deprecated` authority demotion; that absence is accepted (Requirement 10.2).

Local module outputs (Requirements 7.8, 7.9): `mod.outputs.x` or `mod.x` on a known local module resolves to that child's output node when the output exists. When the child file is known but that output name does not exist, resolve as far as the module node and record the edge as `Unresolved`. An output of an external module is unresolved as far as the module node (Requirement 9.2).

`Orphans` treats `KindParam` as it treats `KindVariable`, and `KindVar` as it treats `KindLocal`. Root-file outputs are excluded by `isRootModule` using the Bicep file path as the module scope. Child outputs with no incoming `REFERENCES` from a caller are orphans. `INPUTS` into a param counts as wired, not as read, which is the Terraform variable rule.

## Error handling

| Condition | Result |
|---|---|
| Operative `.tf` and operative `.bicep` | `MixedLanguageError`, snapshot dropped, CLI and MCP exit 1 |
| Unreadable repository root | existing `StatFS` / walk error, exit 1 |
| One `.bicep` file with a token error or unrecognized statement | `ParseErrors` entry, that file omitted, other files indexed |
| Recognized-and-skipped statement (`extension`, `import`, …) | file kept; no node for the statement; `import` adds a coverage-gap marker |
| External module, or output access on one | node retained, `Unresolved` set, status counts it |
| Unknown output on a known local module | edge as far as the module node, `Unresolved` |
| `--plan` on Bicep | static graph served, status says the flag does not apply |
| Failed rebuild of a repository that previously loaded | previous snapshot kept, except a new mixed-language classification, which discards it |
| Language flip during an MCP session | tools stay as registered at start; restart required |
| Empty repository | empty Terraform graph, `terra_*` tools |

Parse errors name the repo-relative file. They do not include source snippets large enough to dump a file into status.

## Testing strategy

Tests are Go tests under `go test -race ./...`, which is the CI `test` job. No `bicep` binary, no Azure credentials, no network. Development mode is test-first; the RED/GREEN contracts belong in `tasks.md`.

- `internal/source`: table tests for the classification outcomes, including `.tf.json` ignored, demoted Bicep indexed not skipped, and `MixedLanguageError` text that contains both counts, using temp directories.
- `internal/tfhcl`: existing fixture tests stay green after the skip-list move.
- `internal/bicep`: synthetic files under `testdata/bicep/`. One fixture covers references against the same names buried in a `//` comment, a `/* */` comment, a single-quoted string, a triple-quoted multiline string, and an interpolation. One covers two files with the same symbolic name. One covers a literal resource ID, a one-argument `resourceGroup('<rg>')` with no alias, and a no-argument `resourceGroup()`. One covers `scope: resourceGroup(rg.name)` as `REFERENCES` without `SCOPED_TO`. One covers parent-derived IDs. One covers a local module, `INPUTS`, `mod.outputs.x` to a child output, an unknown output on that local module, and a `br:` module. One covers `Shared == false` for a single distinct caller (including two call sites in that file) and `Shared == true` for two calling files. One covers loop-variable and lambda-parameter shadowing. One covers `extension` / `import` keeping the file beside a sibling, and a token error omitting only the broken file. One covers a demoted `.bicep` path present in the graph.
- `internal/index`: fingerprint changes on `.bicep` edit for a Bicep repo and does not for a skipped `.bicep` in a Terraform repo; mixed classification returns the error and drops a prior snapshot; impact on a Bicep graph walks the closed set including `INPUTS`; type ranking matches a version-stripped query.
- `internal/render` and a `cmd/terragraph-mcp` registration test: Bicep tool names present and `terra_*` absent, and the reverse for Terraform.

Fixtures contain no secrets. `@secure()` is tested with a placeholder parameter name, not a credential.

Fixture realism: fixtures are written against the Bicep v0.47.16 language surface. CI does not install or invoke the `bicep` binary. A maintainer who has `bicep` 0.47.16 locally runs `bicep build` on each fixture under `testdata/bicep/` before merging a fixture change, as a manual check that the fixture is still accepted by the compiler. That step is not a gate and is not added to CI.

## Alternatives rejected

**ARM JSON as the graph source.** It cannot separate a reference from a mention, which is Requirement 4.

**Shelling out to `bicep build`.** The output is that ARM JSON. The command also writes a file beside the source and restores external modules, which breaks Requirements 3 and 9.

**Embedding `Azure.Bicep.Core`.** It is the real semantic model and the NuGet package for v0.47.16 targets .NET. TerraGraph is a pair of Go binaries. The subset parser is the cost of keeping that.

**One graph for a mixed repository.** Addresses and tool names would need a language dimension, and a partial index looks complete. Requirement 2 refuses the mix instead.

**Resource ID as the only key.** Params, vars, outputs, and module calls have none, and most resource names are interpolated. The symbolic name is always present. The ID is an alias when it is fully written down.

**Reusing `variable` and `local` for `param` and `var`.** The words would be wrong in tool output. The new kinds are the model change.

**A second set of questions under the Bicep tool names.** Explore, reverse lookup, impact, modules, orphans, and status are the jobs. Only the names and the wording change. Bicep impact still uses a closed walk set, not "whatever `terra_impact` walks".

**A what-if overlay in this specification.** Azure what-if is a deployment prediction. It needs a live call or a captured document, and its actions are not Terraform plan actions. Requirement 11 leaves it out. The Terraform overlay code stays where it is.

**Whole-file rejection of `extension`, `metadata`, `func`, `type`, or `export`.** An extensibility resource is an ordinary node (Decision 1). Files that use those providers declare `extension` first, so rejecting the file would make Decision 1 unreachable. Requirement 12.4 keeps the file.

**Keeping a stale snapshot with an error annotation on mixed classification.** Requirement 2.1 forbids serving the previous snapshot. Failed loads still keep it; mixed classification does not.

**Re-registering MCP tools when `Classify` later returns the other language.** The server registers tools once at process start. A language flip requires a restart.

**Reading `bicepconfig.json` for module aliases.** Requirement 8.3 treats those sources as non-local.

**Per-statement recovery after a token error.** Coarser than Terraform keep-partial, and accepted until Requirement 12 says otherwise. Recognized-and-skipped already keeps the file for known extras.

**Deferring parent-derived resource IDs.** Requirement 5.8 states the rule; the loader implements it.

## Trace

| Requirement | Design section |
|---|---|
| 1 | Loader selection, Testing strategy |
| 2 | Classification (operative mix, demoted-not-excluded, `.tf.json` parity) |
| 3 | Bicep loader, Parser strategy (`extension`/`provider` kept) |
| 4 | Parser strategy (comments, strings, interpolations, bound identifiers) |
| 5 | Data model (key, alias table, inherited `targetScope`, parent-derived IDs, 5.9 `REFERENCES`) |
| 6 | Data model (kinds, `existing` modifier, `@secure` outputs, `@batchSize`, `HasCount`, `if`/`for`) |
| 7 | Data model (edge table, `INPUTS`, local outputs, `SCOPED_TO` vs `REFERENCES`) |
| 8 | Data model (file-scoped ModuleDir, Roots as file paths, Shared / `OwningStack`) |
| 9 | Bicep loader, Data model, Error handling (`bicepconfig.json` ignored) |
| 10 | Tools (closed impact walk, MCP names at start), Lookup and ranking (version-stripped type, demoted authority) |
| 11 | Loader selection and fingerprint (no overlay, `HasCount` caveat) |
| 12 | Parser strategy, Error handling, Testing strategy (manual `bicep build`) |
| 13 | Loader selection and fingerprint, Tools (restart on language flip) |
| 14 | Tools |
