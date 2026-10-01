---
id: specs/bicep-support/tasks
title: Bicep support implementation tasks
doc-type: plan
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-30
---

# Implementation Tasks

**Phase status:** Approved (human approval 2026-09-30)
**Development mode:** test-first

Grounded in `docs/specs/bicep-support/requirements.md` and `docs/specs/bicep-support/design.md` (both revised 2026-09-30, both approved 2026-09-30). Each implementation task is RED, then GREEN, then REFACTOR. Do not change production code for a task until its test fails for the reason named here. The runner for every contract is `go test -race ./...` from the repository root, which is the CI `test` job. A narrower package command is listed where it is the fastest way to see the RED failure; the task is not green until the full command passes.

Requirements covered: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14.

## 1. Classify a repository as Terraform or Bicep

**Objective:** Add `internal/source.Classify` and route `tfhcl.Discover` through the same skip and demote sets.

**Files:** `internal/source/classify.go`, `internal/source/classify_test.go`, `internal/tfhcl/discover.go`.

**Acceptance:**

- The classification outcomes in design section Classification, including `MixedLanguageError` text that contains both operative counts.
- Skip directories `.git`, `.terraform`, `node_modules`, `.idea`, and `.vscode` are never walked. Demoted segments are `examples`, `example`, `test`, `tests`, `fixtures`, `testdata`, `templates`. `tfhcl.Discover` calls the same helpers so the sets cannot drift.
- A name ending in `.tf.json` is not counted as a Terraform source (Discover parity). A repository whose only language-like file is `.tf.json` classifies as Terraform with an empty parse list. A repository of operative `.bicep` plus `.tf.json` classifies as Bicep, not as a mix.
- A repository that contains only `.bicepparam` files, or only `.json` files, and no `.tf` or `.bicep` files, classifies as Terraform with an empty parse list (Requirement 2.6 empty snapshot).
- Demoted files of the selected language are on the parse list and are not listed in `Skipped`. `Skipped` holds only the other language.
- Existing `tfhcl` fixture tests stay green.

**Depends on:** none.

**Requirements:** 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, 2.9, 3.1.

**Skills:** test-dev for the RED test. Implementation is Go in the files above.

**Test contract:**

- File: `internal/source/classify_test.go`
- Command: `go test -race ./internal/source/`
- Observable behavior: temp-dir cases for operative mix, operative Bicep with demoted Terraform, operative Terraform with demoted Bicep, Bicep only under `examples/`, neither language, Terraform only, `.tf.json` only, operative `.bicep` plus `.tf.json`, `.bicepparam` only, `.json` only, and a `.bicep` file under `.git` that is not counted.
- RED: the test file does not compile, or `Classify` is missing, so the `.tf.json` and empty-parse-list cases cannot run.
- GREEN: each case returns the language, skipped paths, parse list, and error specified in the design, `tfhcl` fixtures still pass, and `go test -race ./...` passes.

## 2. Extend the graph model

**Objective:** Add the Bicep kinds, `SCOPED_TO`, `ResourceID`, and `ScopeExpr` without changing Terraform keys.

**Files:** `internal/graph/model.go`, `internal/graph/model_test.go`.

**Acceptance:** `KindParam`, `KindVar`, `KindExisting`, and `EdgeScopedTo` exist. `KindExisting` is a distinct kind from `KindResource`. `ResourceID` and `ScopeExpr` are fields on `Node`. `Key()` is still `ModuleDir|Address`. A node whose `ModuleDir` is a `.bicep` path and whose `Address` is a symbolic name does not collide with a second file's node of the same name. `Build` indexes a non-empty `ResourceID` for lookup via a new `ByResourceID`. An empty `ResourceID` is not indexed.

**Depends on:** none. Can run beside task 1.

**Requirements:** 5.1, 5.2, 6.1, 6.10, 7.5.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/graph`.

**Test contract:**

- File: `internal/graph/model_test.go`
- Command: `go test -race ./internal/graph/`
- Observable behavior: two nodes with address `stg` and different `.bicep` module dirs have different keys; `ByResourceID` returns the node for a stored ID and nil for an empty ID; `KindExisting` is not equal to `KindResource`.
- RED: the test does not compile because the kinds, the edge kind, the fields, or `ByResourceID` are absent.
- GREEN: `go test -race ./...` passes with no change to existing Terraform assertions.

## 3. Select the loader and fingerprint the files that loader reads

**Objective:** `Host.Current` classifies first, calls `tfhcl.Load` for Terraform, refuses a mix without serving a stale snapshot, and stamps only the selected files.

**Files:** `internal/index/host.go`, `internal/index/host_language_test.go`. The Bicep branch may return a typed "not implemented" error until task 4, but that error SHALL be distinguishable from `MixedLanguageError` by type (`errors.As`). The Terraform and mixed paths must be finished here.

**Acceptance:**

- A Terraform temp repo still loads through `tfhcl.Load` and still accepts `--plan`.
- An empty directory, a `.bicepparam`-only directory, and a `.json`-only directory still yield the empty Terraform snapshot.
- A skipped `.bicep` edit in a Terraform repository does not change the Terraform config stamp and does not increment `Builds`.
- Creating the first operative `.bicep` beside operative `.tf` returns `MixedLanguageError` and a nil index even when a snapshot was previously built. The previous snapshot is dropped, not served with an error annotation.
- Demoted files of the selected language remain on the parse list Host passes to the loader. A Terraform repo with `main.tf` and `examples/demo.tf` indexes both; `examples/demo.tf` is not in `Skipped`.
- A Bicep classification before task 4 returns the stub error, not `MixedLanguageError`, and not a Terraform graph.

**Depends on:** 1.

**Requirements:** 1.1, 1.3, 2.1, 2.6, 2.9, 11.3, 13.3.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/index`.

**Test contract:**

- File: `internal/index/host_language_test.go`
- Command: `go test -race ./internal/index/`
- Observable behavior: the host cases in the acceptance text, using temp directories. Overlay discovery still runs for the Terraform case when a plan JSON file is present, asserted through the existing overlay status rather than a new Bicep path. `errors.As` on the Bicep stub does not match `MixedLanguageError`. After mixed classification, a subsequent `Current` on the same host does not return the prior index.
- RED: mixed classification currently falls through to `tfhcl.Load` and returns a graph, so the new test fails on a non-nil index or a nil error (snapshot not dropped).
- GREEN: the new tests pass, the existing `internal/index` tests pass unmodified in their assertions, and `go test -race ./...` passes.

## 4. Parse the supported Bicep subset into nodes

**Objective:** `bicep.Load` turns the supported subset into nodes, roots, and parse errors. Wire it into `Host.Current` in place of the task 3 stub.

**Files:** `internal/bicep/load.go`, `internal/bicep/load_test.go`, `testdata/bicep/`, `internal/index/host.go`.

**Acceptance:**

- `param`, `var`, `output`, `resource`, `existing`, and `module` become the kinds in Requirement 6. `resource ... existing` is kind `existing`, not `resource`.
- `@secure()` sets `Sensitive` on a param. An output decorated with `@secure()` or declared as `secureString` or `secureObject` also sets `Sensitive`. `@description('...')` with a string literal, including a triple-quoted multiline string, sets `Doc`. `@batchSize()` remains visible in `Body` and does not fail the parse. Other decorators are ignored and do not fail the file.
- A `for` loop on a resource or module, including `for item in <expr>` and tuple `for (item, index) in <expr>`, sets `HasForEach` and still produces one node. A `for` expression on a `var` or `output` does not set `HasForEach`. `if <expr>` on a resource or module keeps one node and sets neither `HasForEach` nor `HasCount`.
- Type strings, including `Microsoft.Storage/storageAccounts@2023-01-01`, are stored on `Node.Type`. An extensibility provider type string is stored on an ordinary resource or existing node; there is no provider catalog.
- `Node.Version` is empty on Bicep module calls. A `br:` / `ts:` version substring stays in `Source`. Params, vars, outputs, and module calls have no `ResourceID`.
- A file no local module points at is a root and has a `stack.` node. `Graph.Roots` lists repo-relative `.bicep` file paths, not directories. Two root files in one directory are two roots. A file that is a local `./` or `../` target is not a root. `pathsByDir` is empty after `Load`.
- Resource ID alias table (Requirement 5.7): `resourceGroup('<sub>', '<rg>')` plus literal name and type stores `/subscriptions/<sub>/resourceGroups/<rg>/providers/<type-path>/<name>`. `subscription('<sub>')` stores `/subscriptions/<sub>/providers/<type-path>/<name>`. `managementGroup('<mg>')` stores `/providers/Microsoft.Management/managementGroups/<mg>/providers/<type-path>/<name>`. `tenant()` stores `/providers/<type-path>/<name>`. One-argument `resourceGroup('<rg>')` stores `ScopeExpr` and omits the alias. Zero-argument `resourceGroup()`, `subscription()`, and `managementGroup()` store `ScopeExpr` and omit the alias. `<type-path>` is the declared type with `@api-version` stripped.
- A resource with no `scope` property inherits the file `targetScope` for `ScopeExpr` and omits the alias (Requirement 5.6).
- `targetScope` that is a string literal is stored as `ScopeExpr` on that file's stack node. Any other `targetScope` expression stores the expression text and is never used to form an alias. An omitted `targetScope` is treated as `resourceGroup` for `ScopeExpr` (Requirement 5.10).
- Parent-derived IDs (Requirement 5.8): when `parent` names a node that already has a `ResourceID` and the child's name and type are string literals, the child alias is that parent alias plus the child type-path plus the child name. If the parent has no alias or any child segment is computed, the child alias is omitted.
- Recognized-and-skipped vs omit-file (Requirement 12): `metadata`, `extension`, `provider`, `func`, `type`, `export`, and `assert` keep the file and emit no node for the statement (`assert` expressions are still present to walk in task 5). `import` keeps the file, emits no nodes for imported symbols, and records a coverage-gap marker on `ParseErrors` with a `coverage gap:` prefix (no sibling slice) so those names resolve as unresolved. A token error, or a statement in neither the indexed subset nor the recognized-and-skipped set, omits that file, records one parse error that names it, and still indexes sibling files.
- Triple-quoted multiline strings `'''...'''` parse as string literals and do not fail the file.
- `Load` does not read `bicepconfig.json`. A `bicepconfig.json` beside the fixture does not change roots, module locality, or aliases.
- `.bicepparam` and `.json` beside the files produce no nodes.
- No file is written under the repository being indexed. The test snapshots the temp dir's file list before and after `Load`. The test does not invoke the `bicep` CLI and does not require a network.
- A demoted `.bicep` path (under `examples/`) is present in the graph, not omitted.
- A host test shows a `.bicep` create, edit, or delete increments `Builds`. `.bicepparam` and `.json` edits do not.

**Depends on:** 2, 3.

**Requirements:** 3.1, 3.2, 3.3, 3.4, 5.3, 5.4, 5.5, 5.6, 5.7, 5.8, 5.10, 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8, 6.10, 8.1, 8.2, 8.3, 9.3, 12.1, 12.3, 12.4, 12.5, 12.6, 13.1, 13.2.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/bicep` and the host branch.

**Test contract:**

- File: `internal/bicep/load_test.go`
- Command: `go test -race ./internal/bicep/`
- Observable behavior: fixtures under `testdata/bicep/` covering the acceptance list, including one fixture that mixes a recognized-and-skipped statement with an indexed resource in the same file, one token-error sibling, one 1-arg `resourceGroup('<rg>')`, one parent-derived ID, one non-literal `targetScope`, one tuple `for`, one `'''` string, and a `bicepconfig.json` that must be ignored. A host test in `internal/index/host_language_test.go` shows a `.bicep` edit increments `Builds`.
- RED: `bicep.Load` does not exist, so the test does not compile, or a recognized-and-skipped fixture is omitted as a parse error, or a 1-arg `resourceGroup` incorrectly stores a `ResourceID`.
- GREEN: `go test -race ./...` passes. CI gains no `bicep` install step.

## 5. Resolve references, module calls, and the edges

**Objective:** After nodes exist, emit the edges in the design's direction table, and leave external modules unloaded.

**Files:** `internal/bicep/refs.go`, `internal/bicep/refs_test.go`, fixtures added under `testdata/bicep/`.

**Acceptance:**

- `stg.name` emits `REFERENCES`. The same characters in a `//` comment, a `/* */` comment, a single-quoted string, or a triple-quoted multiline string `'''...'''` (including a verbatim form with no interpolation) do not. `${stg}` inside a single-quoted or triple-quoted string does emit `REFERENCES`.
- A `for` loop variable (`for item in ...` or `for (item, index) in ...`) and a lambda parameter (`x =>` in `map`, `filter`, `reduce`, or `sort`) are local bindings: no edge for that name, and the binding shadows a declaration of the same name. The collection expression, loop or lambda body, and `if` conditions are walked.
- A `for` expression on a `var`, `output`, or nested array or object is walked and binds loop variables; it does not invent extra nodes.
- `dependsOn` emits `DEPENDS_ON`. `parent` emits `CONTAINS`. `scope` naming another resource or existing node directly (identifier or member access) emits `SCOPED_TO`.
- When `scope` is a function whose argument names another declaration (`scope: resourceGroup(rg.name)`), emit `REFERENCES` to that declaration, emit no `SCOPED_TO`, and omit the resource ID alias (Requirement 5.9).
- A local module emits `CALLS` to `source.<path>` and `INPUTS` from the call to each child `param` it sets.
- `mod.outputs.accountId` or `mod.accountId` on a known local module whose child file declares that output emits `REFERENCES` to that child output node.
- The same selection when the child file is known but that output name does not exist resolves as far as the module node and records the edge as unresolved.
- A `br:` or other non-relative source, including a registry or template-spec alias that would only resolve via `bicepconfig.json`, emits `CALLS` to a `module_source` node, adds no body nodes, and a `mod.outputs.x` reference stays unresolved as a coverage gap.
- `Shared` matches Terraform `OwningStack`: a child reached from more than one distinct calling `.bicep` file is `Shared == true` with empty `Stack`. Two call sites in the same calling file leave `Shared == false` and `Stack` equal to that caller file. A child with exactly one distinct caller is `Shared == false`. Callers need not themselves be roots.
- Built-in and scope function names (`resourceGroup`, `subscription`, `managementGroup`, `tenant`, `deployment`, `environment`, and other function names) produce no edges. Arguments are walked. An unknown identifier produces an unresolved edge and no invented node.
- `assert` adds no node and still emits a reference used in its expression. Imported names resolve as unresolved rather than as invented declarations.

**Depends on:** 4.

**Requirements:** 4.1, 4.2, 4.3, 4.4, 4.5, 5.9, 6.4, 6.7, 7.1, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7, 7.8, 7.9, 7.10, 8.3, 8.4, 9.1, 9.2, 9.3, 12.4, 12.5.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/bicep`.

**Test contract:**

- File: `internal/bicep/refs_test.go`
- Command: `go test -race ./internal/bicep/`
- Observable behavior: edge kind, direction, and unresolved flag for each row above, asserted on the fixture graph rather than on rendered text. Dedicated fixtures for lambda parameter shadowing, loop-variable shadowing, `mod.outputs.x` to a child output, unknown output on a known local module, external output unresolved, single-caller `Shared == false` (including two call sites in that file), two calling files `Shared == true`, and `INPUTS` edges.
- RED: the declaration tests from task 4 pass, and the new edge assertions fail because those edges are absent, or `Shared` is true for a single distinct caller, or a lambda/loop binding emits a `REFERENCES` edge to the shadowed declaration.
- GREEN: the edge assertions pass, task 4's node assertions still pass, and `go test -race ./...` passes.

## 6. Look up symbolic names and literal resource IDs

**Objective:** Explore, for-address, impact, modules, and orphans answer Bicep graphs with the existing budgets and weights.

**Files:** `internal/index/queries.go`, `internal/index/index.go`, `internal/index/bicep_query_test.go`, `internal/render/render.go`, `internal/render/render_test.go`.

**Acceptance:**

- Explore ranks an exact symbolic name and an exact `ResourceID` at the address weight, above a comment that only mentions the name. Ranking applies the demoted-path authority discount (0.5) to a `.bicep` file under a demoted segment; that node stays in the graph and is not excluded. Bicep ranking does not apply Terraform `moved` / `Deprecated` demotion.
- Ranking and `ByType` match a Bicep resource type with the `@api-version` suffix stripped: a query for `Microsoft.Storage/storageAccounts` hits a node whose `Type` is `Microsoft.Storage/storageAccounts@2023-01-01`. Stored `Node.Type` still keeps the API version.
- For-address on the symbolic name, a file-qualified symbolic name, and the resource ID, returns the reference edge and does not return the comment or the string literal.
- Impact walks the closed Bicep set `REFERENCES`, `DEPENDS_ON`, `CONTAINS`, `SCOPED_TO`, `CALLS`, and `INPUTS` to depth 3. An `INPUTS` edge is included in that walk. Terraform `PROVIDES`, `MOVED_FROM`, and `REPLACE_TRIGGERED_BY` are not required and are not walked. Impact describes the graph as static configuration.
- A resource or module `if <expr>` is one node: the condition was walked for references (task 5); impact does not expand it into instances. `HasCount` is false on every Bicep node. `ExpandingBlocks` for Bicep is driven only by `HasForEach`.
- Modules lists the source string and states that a non-local source was not loaded.
- Orphans reports an unread `param`, a `param` that is wired through `INPUTS` and never read, an unread `var`, and a child output with no caller. A root-file output is absent. `isRootModule` compares file paths (`ModuleDir` ∈ `Roots`), not directories, so two files in one directory do not leak each other's root outputs.
- Default budgets stay 12000 for explore and 4000 for the other commands.

**Depends on:** 5.

**Requirements:** 2.9, 6.6, 6.9, 10.2, 10.3, 10.4, 10.5, 10.6, 10.10, 11.4.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/index` and `internal/render`.

**Test contract:**

- File: `internal/index/bicep_query_test.go` and `internal/render/render_test.go`
- Command: `go test -race ./internal/index/ ./internal/render/`
- Observable behavior: query results on a `bicep.Load` graph built from `testdata/bicep/`. Cases for version-stripped type match, demoted-path score discount with the node still present, impact including an `INPUTS` edge and excluding Terraform-only kinds, `HasCount == false` on every Bicep node, `if` not treated as an expanding block, and root-output exclusion using file-path `Roots`.
- RED: Bicep `param` and `var` nodes are ignored by `Orphans`, `ResourceID` lookup misses, a version-stripped type query misses, or impact does not follow `INPUTS`.
- GREEN: those assertions pass, existing Terraform render tests pass, and `go test -race ./...` passes.

## 7. Register language-specific MCP tools and CLI prose

**Objective:** A Bicep repository exposes only the six `bicep_*` tools. A Terraform repository exposes only the six `terra_*` tools. CLI verbs stay. Status names the language, skipped files, parse errors, and the overlay state.

**Files:** `cmd/terragraph-mcp/main.go`, `cmd/terragraph-mcp/register.go` (extract registration so it can be tested), `cmd/terragraph-mcp/register_test.go`, `cmd/terragraph/main.go`, `internal/render/render.go`.

**Acceptance:**

- Registration given a Bicep index adds the six `bicep_*` names and no `terra_*` name. The server description describes an in-memory graph of that repository's Bicep configuration and does not contain a token percentage.
- Registration given a Terraform index is the reverse (Requirement 1.2).
- Tools are registered from the language classified at process start, once, before `registerTools`. A later `Classify` / `Current` result of the other language does not change the registered set; a language flip requires a restart. The test asserts registration is a function of the startup language and that there is no per-call re-registration hook.
- Directory-versus-file audit on a Bicep graph: `Graph.Roots` are repo-relative `.bicep` paths; `pathsByDir` is empty; `isRootModule` / Modules grouping treat `ModuleDir` and `Stack` as file paths, not directories; shared prose is "shared by several callers" (callers need not be roots). Host does not pass Bicep roots to `overlay.Discover`.
- CLI `status` on a Bicep fixture includes node and edge counts, roots as file paths, parse errors (including a coverage-gap marker from `import`), unresolved count, skipped paths of the other language, and a sentence that no deployment overlay is loaded. That sentence does not imply "no replacements" and does not mention `count` instance expansion. `--plan` does not change that line into a Terraform plan load.
- CLI usage still documents `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`. Command output names the language that was indexed.
- A mixed temp repo makes the CLI path return exit code 1. Test the error through the same helper the CLI uses, not by spawning a process, unless the helper is `Classify` plus the exit wrapper already called from `main`.

**Depends on:** 3, 6.

**Requirements:** 1.2, 8.1, 10.1, 10.7, 10.8, 10.9, 11.1, 11.2, 11.4, 12.2, 13.1, 14.1, 14.2.

**Skills:** test-dev for the RED test. Implementation is Go in `cmd/terragraph`, `cmd/terragraph-mcp`, and `internal/render`.

**Test contract:**

- File: `cmd/terragraph-mcp/register_test.go` and the status cases in `internal/render/render_test.go`
- Command: `go test -race ./cmd/terragraph-mcp/ ./internal/render/ ./cmd/terragraph/`
- Observable behavior: tool-name sets, startup-only registration, the directory-versus-file audit, and the status sentences in the acceptance text.
- RED: `registerTools` always registers `terra_*`, so the Bicep name assertion fails, or Bicep status prints module directories instead of `.bicep` file paths.
- GREEN: both name sets pass, Terraform status text for the existing fixture still reports `.tf` snapshots, and `go test -race ./...` passes.

## 8. Close out the specification into durable documentation

**Objective:** After tasks 1–7 are implemented and reviewed, migrate the facts that are true of the built code into the README, then archive this specification.

**Files:** `README.md`, `docs/specs/README.md`, and the move of `docs/specs/bicep-support/` to `docs/specs/archive/bicep-support/`.

**Acceptance:**

- The README says a repository of `.bicep` files is indexed, and it states the limits: no `.bicepparam`, no ARM JSON, no external module restore, no deployment overlay.
- The README does not state a Bicep token saving and does not reuse the Terraform benchmark percentage as a Bicep result.
- The specification index lists `bicep-support` as archived and links the archive paths.
- The archive is not linked from the README as current guidance.

**Depends on:** 1, 2, 3, 4, 5, 6, 7, and the review that accepted them.

**Requirements:** 14.3, 14.4.

**Skills:** docs-dev, assigned through the conductor.

**Verification:** This task is documentation closeout, not a new behavior test. Replacement validation is `kyber-weave docs validate .` and `kyber-weave docs drift .` on the updated README and index, plus a read-through that Requirement 14's sentences are present and that no Bicep token figure was added. Do not add a benchmark.

## Coverage

| Criterion | Tasks |
|---|---|
| 1.1 | 3 |
| 1.2 | 7 |
| 1.3 | 3 |
| 2.1 | 1, 3 |
| 2.2 | 1 |
| 2.3 | 1 |
| 2.4 | 1 |
| 2.5 | 1 |
| 2.6 | 1, 3 |
| 2.7 | 1 |
| 2.8 | 1 |
| 2.9 | 1, 3, 4, 6 |
| 3.1 | 1, 4 |
| 3.2 | 4 |
| 3.3 | 4 |
| 3.4 | 4 |
| 4.1 | 5 |
| 4.2 | 5 |
| 4.3 | 5 |
| 4.4 | 5 |
| 4.5 | 5 |
| 5.1 | 2 |
| 5.2 | 2, 6 |
| 5.3 | 4 |
| 5.4 | 4 |
| 5.5 | 4 |
| 5.6 | 4 |
| 5.7 | 4 |
| 5.8 | 4 |
| 5.9 | 5 |
| 5.10 | 4 |
| 6.1 | 2, 4 |
| 6.2 | 4 |
| 6.3 | 4 |
| 6.4 | 4, 5 |
| 6.5 | 4 |
| 6.6 | 4, 6 |
| 6.7 | 4, 5 |
| 6.8 | 4 |
| 6.9 | 6 |
| 6.10 | 2, 4 |
| 7.1 | 5 |
| 7.2 | 5 |
| 7.3 | 5 |
| 7.4 | 5 |
| 7.5 | 2, 5 |
| 7.6 | 4, 5 |
| 7.7 | 5, 6 |
| 7.8 | 5 |
| 7.9 | 5 |
| 7.10 | 5 |
| 8.1 | 4, 7 |
| 8.2 | 4 |
| 8.3 | 4, 5 |
| 8.4 | 5 |
| 9.1 | 5 |
| 9.2 | 5 |
| 9.3 | 4, 5 |
| 10.1 | 7 |
| 10.2 | 6 |
| 10.3 | 6 |
| 10.4 | 6 |
| 10.5 | 6 |
| 10.6 | 6 |
| 10.7 | 7 |
| 10.8 | 7 |
| 10.9 | 7 |
| 10.10 | 6 |
| 11.1 | 7 |
| 11.2 | 7 |
| 11.3 | 3 |
| 11.4 | 6, 7 |
| 12.1 | 4 |
| 12.2 | 7 |
| 12.3 | 4 |
| 12.4 | 4, 5 |
| 12.5 | 4, 5 |
| 12.6 | 4 |
| 13.1 | 4, 7 |
| 13.2 | 4 |
| 13.3 | 3 |
| 14.1 | 7 |
| 14.2 | 7 |
| 14.3 | 8 |
| 14.4 | 8 |
