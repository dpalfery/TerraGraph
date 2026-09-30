---
id: specs/bicep-support/tasks
title: Bicep support implementation tasks
doc-type: plan
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-29
---

# Implementation Tasks

**Phase status:** Draft
**Development mode:** test-first

Each implementation task is RED, then GREEN, then REFACTOR. Do not change production code for a task until its test fails for the reason named here. The runner for every contract is `go test -race ./...` from the repository root, which is the CI `test` job. A narrower package command is listed where it is the fastest way to see the RED failure; the task is not green until the full command passes.

Requirements covered: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14.

## 1. Classify a repository as Terraform or Bicep

**Objective:** Add `internal/source.Classify` and route `tfhcl.Discover` through the same skip and demote sets.

**Files:** `internal/source/classify.go`, `internal/source/classify_test.go`, `internal/tfhcl/discover.go`.

**Acceptance:** The six outcomes in design section Classification, including `MixedLanguageError` text that contains both counts. Existing `tfhcl` fixture tests stay green.

**Depends on:** none.

**Requirements:** 2, 3.1.

**Skills:** test-dev for the RED test. Implementation is Go in the files above.

**Test contract:**

- File: `internal/source/classify_test.go`
- Command: `go test -race ./internal/source/`
- Observable behavior: temp-dir cases for operative mix, operative Bicep with demoted Terraform, operative Terraform with demoted Bicep, Bicep only under `examples/`, neither language, and Terraform only.
- RED: the test file does not compile, or `Classify` is missing.
- GREEN: each case returns the language, skipped paths, and error specified in the design, and `go test -race ./internal/tfhcl/ ./internal/source/` passes.

## 2. Extend the graph model

**Objective:** Add the Bicep kinds, `SCOPED_TO`, `ResourceID`, and `ScopeExpr` without changing Terraform keys.

**Files:** `internal/graph/model.go`, `internal/graph/model_test.go`.

**Acceptance:** `KindParam`, `KindVar`, `KindExisting`, and `EdgeScopedTo` exist. `ResourceID` and `ScopeExpr` are fields on `Node`. `Key()` is still `ModuleDir|Address`. A node whose `ModuleDir` is a `.bicep` path and whose `Address` is a symbolic name does not collide with a second file's node of the same name. `Build` indexes a non-empty `ResourceID` for lookup via a new `ByResourceID`.

**Depends on:** none. Can run beside task 1.

**Requirements:** 5, 6.1, 7.5.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/graph`.

**Test contract:**

- File: `internal/graph/model_test.go`
- Command: `go test -race ./internal/graph/`
- Observable behavior: two nodes with address `stg` and different `.bicep` module dirs have different keys; `ByResourceID` returns the node for a stored ID and nil for an empty ID.
- RED: the test does not compile because the kinds, the edge kind, the fields, or `ByResourceID` are absent.
- GREEN: `go test -race ./internal/graph/ ./internal/tfhcl/ ./internal/index/` passes with no change to existing Terraform assertions.

## 3. Select the loader and fingerprint the files that loader reads

**Objective:** `Host.Current` classifies first, calls `tfhcl.Load` for Terraform, refuses a mix without serving a stale snapshot, and stamps only the selected files.

**Files:** `internal/index/host.go`, `internal/index/host_language_test.go`. The Bicep branch may return a typed "not implemented" error until task 4, but the Terraform and mixed paths must be finished here.

**Acceptance:** A Terraform temp repo still loads through `tfhcl.Load` and still accepts `--plan`. A skipped `.bicep` edit does not change the Terraform config stamp. Creating the first operative `.bicep` beside operative `.tf` returns `MixedLanguageError` and a nil index even when a snapshot was previously built. An empty directory still yields the empty Terraform snapshot.

**Depends on:** 1.

**Requirements:** 1, 2.1, 2.6, 11.3, 13.3.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/index`.

**Test contract:**

- File: `internal/index/host_language_test.go`
- Command: `go test -race ./internal/index/`
- Observable behavior: the three host cases in the acceptance text, using temp directories. Overlay discovery still runs for the Terraform case when a plan JSON file is present, asserted through the existing overlay status rather than a new Bicep path.
- RED: mixed classification currently falls through to `tfhcl.Load` and returns a graph, so the new test fails on a non-nil index or a nil error.
- GREEN: the new tests pass and the existing `internal/index` tests pass unmodified in their assertions.

## 4. Parse the supported Bicep subset into nodes

**Objective:** `bicep.Load` turns the supported subset into nodes, roots, and parse errors. Wire it into `Host.Current` in place of the task 3 stub.

**Files:** `internal/bicep/load.go`, `internal/bicep/load_test.go`, `testdata/bicep/`, `internal/index/host.go`.

**Acceptance:**

- `param`, `var`, `output`, `resource`, `existing`, and `module` become the kinds in Requirement 6.
- `@secure()` sets `Sensitive`. `@description('...')` sets `Doc`.
- A `for` loop sets `HasForEach` and still produces one node.
- Type strings, including `Microsoft.Storage/storageAccounts@2023-01-01`, are stored on `Node.Type`.
- A file no local module points at is a root and has a `stack.` node. A file that is a local `./` or `../` target is not a root.
- A literal `resourceGroup('<sub>', '<rg>')` plus a literal name stores `ResourceID`. `resourceGroup()` stores `ScopeExpr` and leaves `ResourceID` empty.
- `import` in one file becomes one `ParseErrors` entry and the sibling valid file is still indexed.
- `.bicepparam` and `.json` beside the files produce no nodes.
- No file is written. The test can assert this by snapshotting the temp dir's file list before and after `Load`.

**Depends on:** 2, 3.

**Requirements:** 3, 5, 6, 8.1, 8.2, 12, 13.1, 13.2.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/bicep` and the host branch.

**Test contract:**

- File: `internal/bicep/load_test.go`
- Command: `go test -race ./internal/bicep/`
- Observable behavior: fixtures under `testdata/bicep/` covering the acceptance list. A host test in `internal/index/host_language_test.go` shows a `.bicep` edit increments `Builds`.
- RED: `bicep.Load` does not exist, so the test does not compile, or it returns nil nodes for a fixture the subset must accept.
- GREEN: `go test -race ./internal/bicep/ ./internal/index/` passes. CI gains no `bicep` install step.

## 5. Resolve references, module calls, and the edges

**Objective:** After nodes exist, emit the edges in the design's direction table, and leave external modules unloaded.

**Files:** `internal/bicep/refs.go`, `internal/bicep/refs_test.go`, fixtures added under `testdata/bicep/`.

**Acceptance:**

- `stg.name` emits `REFERENCES`. The same characters in a comment or a quoted string do not. `${stg}` does.
- `dependsOn` emits `DEPENDS_ON`. `parent` emits `CONTAINS`. `scope` naming another resource emits `SCOPED_TO`.
- A local module emits `CALLS` to `source.<path>` and `INPUTS` to each child param it sets.
- A `br:` or other non-relative source emits `CALLS` to a `module_source` node, adds no body nodes, and a `mod.outputs.x` reference is unresolved.
- A child used by two roots is `Shared`.
- Built-in function names produce no edges. An unknown identifier produces an unresolved edge and no invented node.
- `assert` adds no node and still emits a reference used in its expression.

**Depends on:** 4.

**Requirements:** 4, 7, 8.3, 8.4, 9.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/bicep`.

**Test contract:**

- File: `internal/bicep/refs_test.go`
- Command: `go test -race ./internal/bicep/`
- Observable behavior: edge kind, direction, and unresolved flag for each row above, asserted on the fixture graph rather than on rendered text.
- RED: the declaration tests from task 4 pass, and the new edge assertions fail because those edges are absent.
- GREEN: the edge assertions pass and task 4's node assertions still pass.

## 6. Look up symbolic names and literal resource IDs

**Objective:** Explore, for-address, impact, modules, and orphans answer Bicep graphs with the existing budgets and weights.

**Files:** `internal/index/queries.go`, `internal/index/index.go`, `internal/index/bicep_query_test.go`, `internal/render/render.go`, `internal/render/render_test.go`.

**Acceptance:**

- Explore ranks an exact symbolic name and an exact `ResourceID` at the address weight, above a comment that only mentions the name.
- For-address on the symbolic name, and on the resource ID, returns the reference edge and does not return the comment or the string literal.
- Impact walks `REFERENCES`, `DEPENDS_ON`, `CONTAINS`, `SCOPED_TO`, and `CALLS` to depth 3.
- Modules lists the source string and states that a non-local source was not loaded.
- Orphans reports an unread `param`, a `param` that is wired through `INPUTS` and never read, an unread `var`, and a child output with no caller. A root output is absent.
- Default budgets stay 12000 for explore and 4000 for the other commands.

**Depends on:** 5.

**Requirements:** 10.2, 10.3, 10.4, 10.5, 10.6.

**Skills:** test-dev for the RED test. Implementation is Go in `internal/index` and `internal/render`.

**Test contract:**

- File: `internal/index/bicep_query_test.go` and `internal/render/render_test.go`
- Command: `go test -race ./internal/index/ ./internal/render/`
- Observable behavior: query results on a `bicep.Load` graph built from `testdata/bicep/`.
- RED: Bicep `param` and `var` nodes are ignored by `Orphans`, and `ResourceID` lookup misses, so the new assertions fail.
- GREEN: those assertions pass and existing Terraform render tests pass.

## 7. Register language-specific MCP tools and CLI prose

**Objective:** A Bicep repository exposes only the six `bicep_*` tools. A Terraform repository exposes only the six `terra_*` tools. CLI verbs stay. Status names the language, skipped files, parse errors, and the overlay state.

**Files:** `cmd/terragraph-mcp/main.go`, `cmd/terragraph-mcp/register.go` (extract registration so it can be tested), `cmd/terragraph-mcp/register_test.go`, `cmd/terragraph/main.go`, `internal/render/render.go`.

**Acceptance:**

- Registration given a Bicep index adds the six `bicep_*` names and no `terra_*` name. The server description does not contain a token percentage.
- Registration given a Terraform index is the reverse.
- CLI `status` on a Bicep fixture includes roots, parse errors, unresolved count, skipped paths, and a line that no deployment overlay applies. `--plan` does not change that line into a Terraform plan load.
- CLI usage still documents `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`.
- A mixed temp repo makes the CLI path return exit code 1. Test the error through the same helper the CLI uses, not by spawning a process, unless the helper is `Classify` plus the exit wrapper already called from `main`.

**Depends on:** 3, 6.

**Requirements:** 1.2, 10.1, 10.7, 10.8, 10.9, 11.1, 11.2, 14.1, 14.2.

**Skills:** test-dev for the RED test. Implementation is Go in `cmd/terragraph`, `cmd/terragraph-mcp`, and `internal/render`.

**Test contract:**

- File: `cmd/terragraph-mcp/register_test.go` and the status cases in `internal/render/render_test.go`
- Command: `go test -race ./cmd/terragraph-mcp/ ./internal/render/ ./cmd/terragraph/`
- Observable behavior: tool-name sets and the status sentences in the acceptance text.
- RED: `registerTools` always registers `terra_*`, so the Bicep name assertion fails.
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

| Requirement | Tasks |
|---|---|
| 1 | 3, 7 |
| 2 | 1, 3 |
| 3 | 1, 4 |
| 4 | 5 |
| 5 | 2, 4, 6 |
| 6 | 2, 4 |
| 7 | 2, 5 |
| 8 | 4, 5 |
| 9 | 5 |
| 10 | 6, 7 |
| 11 | 3, 7 |
| 12 | 4 |
| 13 | 3, 4 |
| 14 | 7, 8 |
