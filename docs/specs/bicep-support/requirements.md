---
id: specs/bicep-support/requirements
title: Bicep support requirements
doc-type: requirements
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-30
---

# Requirements Document

**Phase status:** Approved (human approval 2026-09-30)

The product decisions below were agreed on 2026-09-29. Approval was recorded against this artifact on 2026-09-30. Resolutions of contradictions raised in external review were recorded on 2026-09-30 and are marked in Decisions.

## Introduction

TerraGraph indexes a repository into one in-memory graph and serves it through the `terragraph` CLI and the `terragraph-mcp` server. Today that graph is Terraform only: `index.Host.Current` calls `tfhcl.Load`, the config fingerprint counts `.tf` files, and the MCP server registers `terra_explore`, `terra_for_address`, `terra_impact`, `terra_modules`, `terra_orphans`, and `terra_status`.

This specification adds Bicep as the other language a repository can be. A repository is one language or the other. The Bicep path is a Go parser of `.bicep` source. It emits the same graph model, with the kinds and edges a Bicep file actually has, and it keeps the product rule that a symbolic reference is an edge and a mention in a comment or a string is not.

**Operative** means a `.tf` or `.bicep` file that is not under a skip directory and not under a demoted segment. Skip directories are never walked. Demoted segments of the *selected* language are indexed and ranked lower, not excluded. Files of the *other* language that sit only under demoted segments are skipped and reported. Classification counts only operative files when deciding whether a mix is a hard refusal.

## Decisions

1. Index `.bicep` source only. `.bicepparam` files and compiled ARM JSON are out of scope. An extensibility *resource* is an ordinary resource node whose type string is kept. **Resolution 2026-09-30:** an `extension` or `provider` *declaration* is recognized, produces no node, and does not discard the file. Decision 1 is otherwise unchanged. Whole-file rejection of `extension` would make Decision 1 unreachable, because files that use extensibility providers declare `extension` first.

2. Auto-detect the language. Operative `.tf` and operative `.bicep` together are a hard refusal. Files of the other language that sit only under demoted directories are skipped and reported. Demoted files of the selected language are indexed and demoted in ranking, not excluded.

3. Parse Bicep in Go, in process. Do not shell out to the `bicep` CLI, do not embed the .NET compiler, and do not read ARM JSON as the source of the graph. The CLI's `build` and JSON-RPC compile methods return an ARM template, not the symbol graph (Microsoft Learn, Bicep CLI and Bicep CLI JSON-RPC).

4. The graph key is the file plus the symbolic name. A fully literal Azure resource ID is an alias on that node. A computed ID is omitted. A resource with no `scope` property inherits the file `targetScope` for `ScopeExpr` and still omits the alias unless an explicit `scope` supplies every ID segment as a string literal.

5. Reuse `resource`, `module`, and `output`. Add `param`, `var`, and `existing`. The Bicep language treats `existing` as a modifier on `resource`; the graph uses a distinct kind so a read can be told from a deploy. References, `dependsOn`, module calls, `parent`, and `scope` have the edge kinds named in Requirement 7. `@secure()` sets `Sensitive`. `@description()` sets `Doc`.

6. A `.bicep` file that no local module path points at is a root. A file used only as a local module is a child. **Resolution 2026-09-30:** `Shared` matches Terraform `OwningStack`: a child reached from more than one distinct caller is shared and has no single owning stack. Two call sites in the same calling file do not by themselves mark shared. "More than one root" is not the rule.

7. External modules are call-only. Record the call and the source string. Do not restore, read a cache, or use credentials. Do not read `bicepconfig.json`; module aliases defined only there are treated as non-local.

8. A Bicep repository exposes six MCP tools — `bicep_explore`, `bicep_for_address`, `bicep_impact`, `bicep_modules`, `bicep_orphans`, `bicep_status` — each doing the job of the matching `terra_*` tool, with Bicep wording. A Terraform repository keeps `terra_*` and does not register the Bicep names. CLI subcommands stay `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`.

9. No deployment overlay for Bicep. No what-if file, no live Azure, no new overlay seam. The Terraform plan/state overlay is unchanged on a Terraform repository. Bicep has no analogue of Terraform `moved`/`Deprecated` authority demotion; that absence is accepted.

10. Fixtures target the Bicep language surface of v0.47.16 (Azure/bicep release v0.47.16, 2026-09-02). One unparsable file does not blank the repository. CI stays `go test` and does not install the `bicep` binary. Correctness fixtures gate the implementation. A token-saving claim in the README or the MCP description waits on a later benchmark against one public Bicep repository, which this specification does not perform. The indexed and recognized-and-skipped syntax subset is normative in Requirement 12; parser strategy stays in design.

## Requirements

### Requirement 1: Terraform repositories stay Terraform

**User Story:** As an agent using TerraGraph on a Terraform repository, I want the current answers preserved, so that Bicep support does not change a repository I already index.

#### Acceptance Criteria

1.1. WHEN the repository contains operative `.tf` files and no operative `.bicep` files THEN the system SHALL build the graph with `tfhcl.Load` and SHALL keep the existing node kinds, edge kinds, addresses, budgets, and plan/state overlay behavior.

1.2. WHEN the MCP server is started on a Terraform repository THEN the system SHALL register `terra_explore`, `terra_for_address`, `terra_impact`, `terra_modules`, `terra_orphans`, and `terra_status`, and SHALL NOT register a `bicep_*` tool.

1.3. IF the existing `testdata` Terraform fixtures are indexed THEN their graph queries SHALL match the behavior those tests already assert.

### Requirement 2: One language per repository

**User Story:** As an agent opening a repository, I want TerraGraph to index either Terraform or Bicep, so that I am not given a graph that silently drops one of them.

#### Acceptance Criteria

2.1. WHEN operative `.tf` files and operative `.bicep` files are both present THEN the system SHALL refuse to build a graph, SHALL return an error that states both file counts, and SHALL NOT serve a previously built snapshot.

2.2. WHEN operative `.bicep` files are present and no operative `.tf` files are present THEN the system SHALL index Bicep.

2.3. WHEN operative `.tf` files are present and every `.bicep` file is under a demoted segment THEN the system SHALL index Terraform and SHALL report those `.bicep` paths as skipped.

2.4. WHEN operative `.bicep` files are present and every `.tf` file is under a demoted segment THEN the system SHALL index Bicep and SHALL report those `.tf` paths as skipped.

2.5. WHEN the repository contains no `.tf` files and at least one `.bicep` file, including a repository whose only `.bicep` files are demoted, THEN the system SHALL index Bicep. Those demoted files SHALL still be ranking-demoted per 2.9 rather than treated as operative.

2.6. WHEN the repository contains neither `.tf` nor `.bicep` files THEN the system SHALL keep today's empty Terraform snapshot and `terra_*` tools.

2.7. Skip directories and demoted segments SHALL be the same closed sets Terraform already uses, owned by a shared helper so classification and `tfhcl.Discover` cannot drift. Skip directories are `.git`, `.terraform`, `node_modules`, `.idea`, `.vscode`, and they SHALL never be walked. Demoted segments are `examples`, `example`, `test`, `tests`, `fixtures`, `testdata`, `templates`. `tfhcl.Discover` indexes demoted `.tf` files and ranking demotes them; it does not skip them. Classification SHALL use the skip set to avoid walking, and the demote set to decide operative vs skipped-other-language vs same-language-demoted.

2.8. WHEN a file's name ends in `.tf.json` THEN classification SHALL NOT count it as a Terraform source, matching `tfhcl.Discover`, which skips `.tf.json`.

2.9. WHEN the selected language is Bicep and a `.bicep` file sits under a demoted segment THEN the system SHALL index that file and ranking SHALL apply the same authority discount Terraform applies to demoted paths (0.5), and SHALL NOT exclude that file from the graph.

### Requirement 3: Bicep source only

**User Story:** As an agent editing Bicep, I want the index to follow the `.bicep` files, so that generated templates and parameter files are not treated as a second source of truth.

#### Acceptance Criteria

3.1. WHEN the selected language is Bicep THEN the system SHALL read `.bicep` files and SHALL NOT create nodes from `.bicepparam` files or from JSON files.

3.2. WHEN a `.bicep` resource uses an extensibility provider type THEN the system SHALL store that type string on an ordinary resource or existing node and SHALL NOT build a separate provider catalog.

3.3. WHILE indexing, the system SHALL NOT invoke the `bicep` CLI, SHALL NOT load a .NET runtime, SHALL NOT download modules or otherwise use the network, and SHALL NOT write a file under the repository being indexed. Temporary artifacts outside that tree are not forbidden by this requirement.

3.4. WHEN a file contains an `extension` or `provider` declaration THEN the system SHALL recognize the statement, SHALL emit no node for it, and SHALL keep indexing the rest of that file.

### Requirement 4: References are not mentions

**User Story:** As an agent asking what uses a Bicep symbol, I want mentions in comments and strings excluded, so that the answer is the dependency and not a text search.

#### Acceptance Criteria

4.1. WHEN a declaration's expression names another declaration in the same file THEN the system SHALL emit a `REFERENCES` edge to that declaration.

4.2. WHEN the same name appears only inside a `//` comment, a `/* */` comment, a single-quoted string literal (`'...'`), or a triple-quoted multiline string (`'''...'''`), including a verbatim multiline form that does not interpolate, THEN the system SHALL NOT emit an edge for that occurrence.

4.3. WHEN the name appears inside a string interpolation (`${...}`), including interpolations inside single-quoted and triple-quoted strings, THEN the system SHALL treat the interpolated expression as a reference.

4.4. WHEN the referenced name is not declared in the file, is not a bound identifier under 4.5, and is not a Bicep built-in function or scope function THEN the system SHALL record the edge as unresolved rather than inventing a node.

4.5. WHEN an identifier is bound by a `for` loop variable (`for item in ...` or `for (item, index) in ...`) or by a lambda parameter (`x =>` in `map`, `filter`, `reduce`, or `sort`) THEN the system SHALL treat that name as a local binding in that scope, SHALL NOT emit an edge for it, and SHALL let it shadow a declaration of the same name.

### Requirement 5: Identity

**User Story:** As an agent looking up a Bicep declaration, I want one stable identity per declaration, so that two files that both declare `stg` do not collapse into one node.

#### Acceptance Criteria

5.1. WHEN a declaration is indexed THEN its graph key SHALL be the repo-relative `.bicep` path plus the symbolic name, and that pair SHALL be unique in the snapshot.

5.2. WHEN two files declare the same symbolic name THEN lookup by that name SHALL return both nodes.

5.3. WHEN a resource or existing declaration has a type, a name, and a `scope` whose every ID segment is a string literal THEN the system SHALL store the Azure resource ID as an alias of that same node.

5.4. IF any segment of the resource ID is computed, interpolated, or supplied by a zero-argument scope function (`resourceGroup()`, `subscription()`, `managementGroup()`) THEN the system SHALL omit the alias.

5.5. Params, vars, outputs, and module calls SHALL NOT receive a resource ID alias. `Node.Version` SHALL remain unpopulated on Bicep module calls; a registry or template-spec version embedded in the source string (`br:`, `br/public:`, `ts:`) SHALL stay in `Source`. Version-skew queries that group on `Node.Version` SHALL therefore not distinguish Bicep modules.

5.6. WHEN a resource or existing declaration has no `scope` property THEN the system SHALL treat the file's `targetScope` as the inherited scope for `ScopeExpr` and SHALL omit a resource ID alias. Implicit current-scope is not a literal ID segment.

5.7. WHEN `scope` is a scope function THEN alias formation SHALL follow these overloads: `resourceGroup('<sub>', '<rg>')` with both arguments string literals and a literal name and type SHALL form `/subscriptions/<sub>/resourceGroups/<rg>/providers/<type-path>/<name>`; `subscription('<sub>')` with a literal name and type SHALL form `/subscriptions/<sub>/providers/<type-path>/<name>`; `managementGroup('<mg>')` with a literal name and type SHALL form `/providers/Microsoft.Management/managementGroups/<mg>/providers/<type-path>/<name>`; `tenant()` with a literal name and type SHALL form `/providers/<type-path>/<name>`; `resourceGroup('<rg>')` (one argument) SHALL store `ScopeExpr` and SHALL omit the alias; zero-argument `resourceGroup()`, `subscription()`, and `managementGroup()` SHALL store `ScopeExpr` and SHALL omit the alias. `<type-path>` SHALL be the declared type with the `@api-version` suffix stripped.

5.8. WHEN a resource has `parent` naming a node that already has a resource ID alias, and the child's name and type are string literals, THEN the system SHALL store the child alias as the parent alias plus the child type-path plus the child name. IF the parent has no alias or any child segment is computed THEN the system SHALL omit the child alias.

5.9. WHEN `scope` is a function whose argument names another declaration (including `resourceGroup(rg.name)` and `scope: resourceGroup(rg.name)`) THEN the system SHALL emit `REFERENCES` to that declaration, SHALL NOT emit `SCOPED_TO`, and SHALL omit the resource ID alias.

5.10. WHEN `targetScope` is a string literal THEN the system SHALL store that literal as `ScopeExpr` on the file's stack node. WHEN `targetScope` is any other expression THEN the system SHALL store the expression text and SHALL NOT use it to form a resource ID alias. WHEN a file omits `targetScope` THEN the system SHALL treat the inherited scope as `resourceGroup` for `ScopeExpr` and SHALL still omit a resource ID alias for unscoped resources.

### Requirement 6: Declarations that are indexed

**User Story:** As an agent reading a Bicep file, I want each declaration represented as the thing it is, so that the tool output does not call a param a Terraform variable.

#### Acceptance Criteria

6.1. The system SHALL index `param` as kind `param`, `var` as kind `var`, `output` as kind `output`, `resource` as kind `resource`, and `module` as kind `module`.

6.2. WHEN a `param` carries `@secure()` THEN `Sensitive` SHALL be true. WHEN an output is decorated with `@secure()` or its declared type is `secureString` or `secureObject` THEN `Sensitive` SHALL be true.

6.3. WHEN a declaration carries `@description()` with a string literal THEN that literal SHALL be stored as `Doc`.

6.4. WHEN a resource or module declaration contains a `for` loop, including `for item in <expr>` and `for (item, index) in <expr>`, THEN `HasForEach` SHALL be true and the system SHALL index one node, not one node per iteration. Loop variables SHALL be bound per 4.5. The collection expression SHALL be walked for references.

6.5. The system SHALL preserve the declared type string, including a resource type's API version, on `Node.Type`.

6.6. WHEN a resource or module declaration contains `if <expr>` THEN the system SHALL keep one node, SHALL walk the condition for references, and SHALL NOT set `HasForEach` or `HasCount` for the `if`. Conditional deployment SHALL be invisible to `ExpandingBlocks` and SHALL be described as a static-configuration caveat, not as instance expansion.

6.7. WHEN a `var`, `output`, or nested array or object contains a `for` expression THEN the system SHALL walk that expression for references and SHALL bind its loop variables per 4.5. A `for` expression on a `var` or `output` SHALL NOT set `HasForEach` on that node.

6.8. WHEN a declaration carries `@batchSize()` THEN the system SHALL keep the file, SHALL leave the decorator visible in the node's Body, and SHALL NOT fail the parse for that decorator. No new graph field SHALL be added for `@batchSize()`. **Decided 2026-09-30 (delegated):** Body-only.

6.9. WHEN the indexed language is Bicep THEN `HasCount` SHALL be false on every node. Bicep has no `count` meta-argument. `ExpandingBlocks` for Bicep SHALL be driven only by `HasForEach`.

6.10. WHEN a resource declaration carries the `existing` modifier THEN the system SHALL index it as kind `existing` rather than kind `resource`. The language construct is a modifier; the graph kind is distinct.

### Requirement 7: Relationships

**User Story:** As an agent tracing a Bicep change, I want parent, scope, dependency, and module calls distinguished, so that blast radius is not one undifferentiated list.

#### Acceptance Criteria

7.1. A symbolic reference SHALL be `REFERENCES`, directed from the using declaration to the used declaration.

7.2. `dependsOn` SHALL be `DEPENDS_ON`, directed from the dependent to the dependency.

7.3. A module declaration SHALL be `CALLS`, directed from the module node to a `module_source` node for its source string.

7.4. `parent` SHALL be `CONTAINS`, directed from the parent resource to the child.

7.5. WHEN `scope` names another node in the snapshot directly (an identifier or member access that resolves to a resource or existing node, not a scope function) THEN the system SHALL emit `SCOPED_TO` from the scoped declaration to that node.

7.6. WHEN `scope` is a scope function that does not name a node, including `resourceGroup()` with no arguments, THEN the system SHALL store the expression text on the node and SHALL NOT invent a node to point at.

7.7. WHEN a local module passes a parameter THEN the system SHALL emit `INPUTS` from the call to that child `param`.

7.8. WHEN an expression selects an output of a local module (`mod.outputs.accountId` or `mod.accountId`) and that output exists on the child file THEN the system SHALL emit `REFERENCES` from the using declaration to that child output node.

7.9. WHEN an expression selects an output of a local module and the child file is known but that output name does not exist THEN the system SHALL resolve as far as the module node and SHALL record the edge as unresolved.

7.10. WHEN `scope` is a function whose argument names another declaration THEN Requirement 5.9 SHALL apply: `REFERENCES` wins, `SCOPED_TO` is omitted, and the resource ID alias is omitted.

### Requirement 8: Roots and local modules

**User Story:** As an agent asking what this repository deploys, I want entry files distinguished from files that only exist to be called, so that shared modules are not reported as extra deployments.

#### Acceptance Criteria

8.1. WHEN a `.bicep` file is not the target of any local module path THEN the system SHALL treat that file as a root and SHALL emit one `stack` node for it.

8.2. WHEN a `.bicep` file is the target of a local module path THEN the system SHALL NOT treat that file as a root.

8.3. A module source SHALL be local only when it is a string literal beginning with `./` or `../`. Every other source SHALL be call-only. The system SHALL NOT read `bicepconfig.json`; a module source that uses a registry or template-spec alias defined only there SHALL be treated as non-local.

8.4. WHEN a child file is the target of local module calls from more than one distinct calling `.bicep` file THEN its nodes SHALL be marked shared and SHALL have no single owning stack, matching Terraform `OwningStack` (Bicep analogue, mapped directory→file). Two call sites in the same calling file SHALL NOT by themselves mark the child shared. The callers need not themselves be roots. **Decided 2026-09-30 (delegated):** distinct calling files.

### Requirement 9: External modules stay unresolved

**User Story:** As an operator running the index in CI, I want external Bicep modules left unresolved, so that indexing never needs network access or credentials.

#### Acceptance Criteria

9.1. WHEN a module source is not a local path THEN the system SHALL keep the module node, the source string, and a `CALLS` edge to a `module_source` node, and SHALL NOT read the module body.

9.2. WHEN an expression selects an output of an external module THEN the system SHALL leave that reference unresolved and SHALL report it as a coverage gap.

9.3. The system SHALL NOT read `~/.bicep`, a registry, a template spec, `bicepconfig.json`, or any other cache.

### Requirement 10: Same six jobs, Bicep names on a Bicep repository

**User Story:** As an agent connected to a Bicep repository, I want six tools whose jobs I already know, under Bicep names, so that I do not call a Terraform tool against a Bicep graph.

#### Acceptance Criteria

10.1. WHEN the indexed language is Bicep THEN the MCP server SHALL register only `bicep_explore`, `bicep_for_address`, `bicep_impact`, `bicep_modules`, `bicep_orphans`, and `bicep_status`.

10.2. `bicep_explore` SHALL rank declarations the way `terra_explore` ranks Terraform blocks, within the same default character budget of 12000, and an exact symbolic name or an exact literal resource ID SHALL outrank prose. Ranking SHALL apply the demoted-path authority discount (2.9) and SHALL NOT invent a Bicep analogue of Terraform `moved`/`Deprecated` demotion.

10.3. `bicep_for_address` SHALL accept a symbolic name, a file-qualified symbolic name, or a literal resource ID alias, and SHALL return references rather than textual mentions, within the default character budget of 4000.

10.4. `bicep_impact` SHALL walk the Bicep edge kinds `REFERENCES`, `DEPENDS_ON`, `CONTAINS`, `SCOPED_TO`, `CALLS`, and `INPUTS`, within the default depth of 3 and the default character budget of 4000, and SHALL describe the graph as static configuration. The walk set is this closed Bicep list; it is not the Terraform edge-kind set. Terraform `PROVIDES`, `MOVED_FROM`, and `REPLACE_TRIGGERED_BY` SHALL NOT be required on a Bicep graph.

10.5. `bicep_modules` SHALL list module calls and their source strings, and SHALL say that an external source was not loaded.

10.6. `bicep_orphans` SHALL report unreferenced `param` and `var` declarations, and outputs of child modules that no caller consumes. Outputs of root files SHALL be excluded. A `param` that callers wire through `INPUTS` and nothing reads SHALL be reported as wired but unread.

10.7. `bicep_status` SHALL report node and edge counts, roots, parse errors, unresolved references, skipped files of the other language, and that no deployment overlay is loaded.

10.8. The CLI subcommands `status`, `explore`, `refs`, `impact`, `modules`, and `orphans` SHALL keep those names on both languages, and their prose SHALL name the language that was indexed.

10.9. WHEN classification fails with a mixed-language error THEN both the CLI and the MCP server SHALL exit non-zero and SHALL print that error.

10.10. WHEN ranking or matching a Bicep resource type THEN the system SHALL compare the type path with the `@api-version` suffix stripped, so `Microsoft.Storage/storageAccounts@2023-01-01` matches a query for `Microsoft.Storage/storageAccounts`.

### Requirement 11: No Bicep deployment overlay

**User Story:** As an agent asking what a Bicep change will replace, I want the tool to say it cannot know, so that I do not read silence as "no replacement".

#### Acceptance Criteria

11.1. WHEN the indexed language is Bicep THEN the system SHALL NOT load plan JSON, state JSON, or an Azure what-if document, and SHALL NOT contact Azure.

11.2. WHEN `--plan` is set on a Bicep repository THEN the system SHALL still serve the static graph and SHALL report that the flag does not apply to Bicep.

11.3. WHEN the indexed language is Terraform THEN Requirement 1's overlay behavior SHALL be unchanged.

11.4. WHEN the indexed language is Bicep THEN status and impact SHALL say that no overlay is loaded rather than implying "no replacements", and SHALL NOT reuse Terraform overlay kinds, replacement lists, or `HasCount`-driven instance expansion.

### Requirement 12: A bad file does not blank the repository

**User Story:** As an agent indexing a repository that contains one broken Bicep file, I want the rest of the graph, so that a syntax error does not look like an empty repository.

#### Acceptance Criteria

12.1. WHEN one `.bicep` file has a token error, or a statement that is neither in the indexed subset (12.3) nor in the recognized-and-skipped set (12.4, 12.5), THEN the system SHALL omit that file, SHALL record a parse error that names the file, and SHALL index the other `.bicep` files. Terraform may keep partial blocks from a damaged `.tf` file; Bicep whole-file omission applies only to this unrecognizable/token case, not to recognized-and-skipped statements.

12.2. WHEN `bicep_status` or CLI `status` runs after such a failure THEN the output SHALL include that parse error.

12.3. The indexed subset is normative here; parser strategy stays in design. WHEN a `.bicep` file is parsed THEN the system SHALL index these constructs against Bicep v0.47.16 syntax: `//` and `/* */` comments; `targetScope`; decorators on indexed declarations; `param`, `var`, `output`, `resource`, `resource ... existing`, `module`; `if <expr>` on a resource or module; `for <name> in <expr>` and `for (<name>, <index>) in <expr>` on a resource or module; object and array bodies; `parent`, `scope`, `dependsOn`, module `params`, and `name`; expressions consisting of identifiers, member access, index, parentheses, unary and binary operators, conditionals, function calls, `for` expressions including tuple form, and lambdas in `map` / `filter` / `reduce` / `sort`; string literals in single-quoted and triple-quoted multiline forms, with interpolations walked per 4.3. Function names SHALL NOT be references; arguments SHALL be walked. `resourceGroup`, `subscription`, `managementGroup`, `tenant`, `deployment`, and `environment` SHALL be treated as scope or runtime functions, not declarations. The implementation SHALL NOT require the `bicep` binary in CI.

12.4. WHEN a file contains `metadata`, `extension`, `provider`, `func`, `type`, `export`, or `assert` THEN the system SHALL recognize the statement, SHALL emit no node for it (except that `assert` expressions SHALL still be walked for references), and SHALL keep indexing the rest of that file. Recognized-and-skipped SHALL never omit a file; the file is kept either way. **Decided 2026-09-30 (delegated):** the wider set is kept.

12.5. WHEN a file contains `import` THEN the system SHALL keep the rest of the file, SHALL emit no nodes for imported symbols, and SHALL record a coverage-gap marker on `ParseErrors` with a `coverage gap:` prefix (no sibling slice) so imported names resolve as unresolved rather than as invented declarations. **Decided 2026-09-30 (delegated).**

12.6. Decorators other than `@secure()`, `@description()`, and `@batchSize()` SHALL be ignored and SHALL NOT fail the file.

### Requirement 13: The snapshot notices Bicep edits

**User Story:** As an agent in a long-lived MCP session, I want an edit to a `.bicep` file to refresh the graph, so that I am not answered from the file I just changed.

#### Acceptance Criteria

13.1. WHEN the selected language is Bicep and a `.bicep` file is created, modified, or deleted THEN the next `Current` call SHALL rebuild the configuration snapshot.

13.2. WHEN the selected language is Bicep THEN `.bicepparam` and `.json` files SHALL NOT be part of the configuration fingerprint.

13.3. WHEN the selected language is Terraform THEN the configuration fingerprint SHALL remain `.tf` files, and a change to a skipped `.bicep` file SHALL NOT rebuild the snapshot.

### Requirement 14: Advertising waits on a benchmark

**User Story:** As a reader of the README, I want Bicep described at the strength of the evidence, so that the Terraform token result is not repeated as a Bicep result.

#### Acceptance Criteria

14.1. The README and the MCP server description SHALL NOT state a Bicep token saving.

14.2. The MCP server description on a Bicep repository SHALL describe an in-memory graph of that repository's Bicep configuration.

14.3. Durable documentation updated at closeout SHALL state that `.bicep` files are indexed and SHALL state the limits in this specification: no `.bicepparam`, no ARM JSON, no external module restore, no deployment overlay, and no token-saving claim.

14.4. A benchmark against a public Bicep repository is out of scope for this specification.
