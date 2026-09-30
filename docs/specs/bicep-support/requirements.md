---
id: specs/bicep-support/requirements
title: Bicep support requirements
doc-type: requirements
status: draft
component: TerraGraph
owner: dpalfery
last-reviewed: 2026-09-29
---

# Requirements Document

**Phase status:** Draft

The product decisions below were agreed on 2026-09-29. This written phase stays Draft until that approval is recorded against this artifact.

## Introduction

TerraGraph indexes a repository into one in-memory graph and serves it through the `terragraph` CLI and the `terragraph-mcp` server. Today that graph is Terraform only: `index.Host.Current` calls `tfhcl.Load`, the config fingerprint counts `.tf` files, and the MCP server registers `terra_explore`, `terra_for_address`, `terra_impact`, `terra_modules`, `terra_orphans`, and `terra_status`.

This specification adds Bicep as the other language a repository can be. A repository is one language or the other. The Bicep path is a Go parser of `.bicep` source. It emits the same graph model, with the kinds and edges a Bicep file actually has, and it keeps the product rule that a symbolic reference is an edge and a mention in a comment or a string is not.

## Decisions

1. Index `.bicep` source only. `.bicepparam` files and compiled ARM JSON are out of scope. An extensibility resource is an ordinary resource node whose type string is kept.
2. Auto-detect the language. Operative `.tf` and operative `.bicep` together are a hard refusal. Files of the other language that sit only under demoted directories are skipped and reported.
3. Parse Bicep in Go, in process. Do not shell out to the `bicep` CLI, do not embed the .NET compiler, and do not read ARM JSON as the source of the graph. The CLI's `build` and JSON-RPC compile methods return an ARM template, not the symbol graph (Microsoft Learn, Bicep CLI and Bicep CLI JSON-RPC).
4. The graph key is the file plus the symbolic name. A fully literal Azure resource ID is an alias on that node. A computed ID is omitted.
5. Reuse `resource`, `module`, and `output`. Add `param`, `var`, and `existing`. References, `dependsOn`, module calls, `parent`, and `scope` have the edge kinds named in Requirement 7. `@secure()` sets `Sensitive`. `@description()` sets `Doc`.
6. A `.bicep` file that no local module path points at is a root. A file used only as a local module is a child.
7. External modules are call-only. Record the call and the source string. Do not restore, read a cache, or use credentials.
8. A Bicep repository exposes six MCP tools — `bicep_explore`, `bicep_for_address`, `bicep_impact`, `bicep_modules`, `bicep_orphans`, `bicep_status` — each doing the job of the matching `terra_*` tool, with Bicep wording. A Terraform repository keeps `terra_*` and does not register the Bicep names. CLI subcommands stay `status`, `explore`, `refs`, `impact`, `modules`, and `orphans`.
9. No deployment overlay for Bicep. No what-if file, no live Azure, no new overlay seam. The Terraform plan/state overlay is unchanged on a Terraform repository.
10. Fixtures target the Bicep language surface of v0.47.16 (Azure/bicep release v0.47.16, 2026-09-02). One unparsable file does not blank the repository. CI stays `go test` and does not install the `bicep` binary. Correctness fixtures gate the implementation. A token-saving claim in the README or the MCP description waits on a later benchmark against one public Bicep repository, which this specification does not perform.

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

2.1. WHEN operative `.tf` files and operative `.bicep` files are both present outside skip directories and outside demoted segments THEN the system SHALL refuse to build a graph, SHALL return an error that states both file counts, and SHALL NOT serve a previously built snapshot.

2.2. WHEN operative `.bicep` files are present and no operative `.tf` files are present THEN the system SHALL index Bicep.

2.3. WHEN operative `.tf` files are present and every `.bicep` file is under a demoted segment THEN the system SHALL index Terraform and SHALL report those `.bicep` paths as skipped.

2.4. WHEN operative `.bicep` files are present and every `.tf` file is under a demoted segment THEN the system SHALL index Bicep and SHALL report those `.tf` paths as skipped.

2.5. WHEN the repository contains no `.tf` files and at least one `.bicep` file, including a repository whose only `.bicep` files are demoted THEN the system SHALL index Bicep.

2.6. WHEN the repository contains neither `.tf` nor `.bicep` files THEN the system SHALL keep today's empty Terraform snapshot and `terra_*` tools.

2.7. The skip directories and demoted segments SHALL be the sets already used by `tfhcl.Discover` (`.git`, `.terraform`, `node_modules`, `.idea`, `.vscode`; and `examples`, `example`, `test`, `tests`, `fixtures`, `testdata`, `templates`).

### Requirement 3: Bicep source only

**User Story:** As an agent editing Bicep, I want the index to follow the `.bicep` files, so that generated templates and parameter files are not treated as a second source of truth.

#### Acceptance Criteria

3.1. WHEN the selected language is Bicep THEN the system SHALL read `.bicep` files and SHALL NOT create nodes from `.bicepparam` files or from JSON files.

3.2. WHEN a `.bicep` resource uses an extensibility provider type THEN the system SHALL store that type string on an ordinary resource or existing node and SHALL NOT build a separate provider catalog.

3.3. The system SHALL NOT invoke the `bicep` CLI, SHALL NOT load a .NET runtime, SHALL NOT download modules, and SHALL NOT write a file under the repository or elsewhere as part of indexing.

### Requirement 4: References are not mentions

**User Story:** As an agent asking what uses a Bicep symbol, I want mentions in comments and strings excluded, so that the answer is the dependency and not a text search.

#### Acceptance Criteria

4.1. WHEN a declaration's expression names another declaration in the same file THEN the system SHALL emit a `REFERENCES` edge to that declaration.

4.2. WHEN the same name appears only inside a `//` comment, a `/* */` comment, or a string literal THEN the system SHALL NOT emit an edge for that occurrence.

4.3. WHEN the name appears inside a string interpolation (`${...}`) THEN the system SHALL treat the interpolated expression as a reference.

4.4. WHEN the referenced name is not declared in the file and is not a Bicep built-in function or scope function THEN the system SHALL record the edge as unresolved rather than inventing a node.

### Requirement 5: Identity

**User Story:** As an agent looking up a Bicep declaration, I want one stable identity per declaration, so that two files that both declare `stg` do not collapse into one node.

#### Acceptance Criteria

5.1. WHEN a declaration is indexed THEN its graph key SHALL be the repo-relative `.bicep` path plus the symbolic name, and that pair SHALL be unique in the snapshot.

5.2. WHEN two files declare the same symbolic name THEN lookup by that name SHALL return both nodes.

5.3. WHEN a resource or existing declaration has a type, a name, and a scope whose every segment is a string literal THEN the system SHALL store the Azure resource ID as an alias of that same node.

5.4. IF any segment of the resource ID is computed, interpolated, or supplied by a zero-argument scope function such as `resourceGroup()` THEN the system SHALL omit the alias.

5.5. Params, vars, outputs, and module calls SHALL NOT receive a resource ID alias.

### Requirement 6: Declarations that are indexed

**User Story:** As an agent reading a Bicep file, I want each declaration represented as the thing it is, so that the tool output does not call a param a Terraform variable.

#### Acceptance Criteria

6.1. The system SHALL index `param` as kind `param`, `var` as kind `var`, `output` as kind `output`, `resource` as kind `resource`, `existing` as kind `existing`, and `module` as kind `module`.

6.2. WHEN a `param` carries `@secure()` THEN `Sensitive` SHALL be true.

6.3. WHEN a declaration carries `@description()` with a string literal THEN that literal SHALL be stored as `Doc`.

6.4. WHEN a resource or module declaration contains a `for` loop THEN `HasForEach` SHALL be true and the system SHALL index one node, not one node per iteration.

6.5. The system SHALL preserve the declared type string, including a resource type's API version, on `Node.Type`.

### Requirement 7: Relationships

**User Story:** As an agent tracing a Bicep change, I want parent, scope, dependency, and module calls distinguished, so that blast radius is not one undifferentiated list.

#### Acceptance Criteria

7.1. A symbolic reference SHALL be `REFERENCES`, directed from the using declaration to the used declaration.

7.2. `dependsOn` SHALL be `DEPENDS_ON`, directed from the dependent to the dependency.

7.3. A module declaration SHALL be `CALLS`, directed from the module node to a `module_source` node for its source string.

7.4. `parent` SHALL be `CONTAINS`, directed from the parent resource to the child.

7.5. WHEN `scope` names another node in the snapshot THEN the system SHALL emit `SCOPED_TO` from the scoped declaration to that node.

7.6. WHEN `scope` is a scope function that does not name a node, including `resourceGroup()` with no arguments THEN the system SHALL store the expression text on the node and SHALL NOT invent a node to point at.

7.7. WHEN a local module passes a parameter THEN the system SHALL emit `INPUTS` from the call to that child `param`.

### Requirement 8: Roots and local modules

**User Story:** As an agent asking what this repository deploys, I want entry files distinguished from files that only exist to be called, so that shared modules are not reported as extra deployments.

#### Acceptance Criteria

8.1. WHEN a `.bicep` file is not the target of any local module path THEN the system SHALL treat that file as a root and SHALL emit one `stack` node for it.

8.2. WHEN a `.bicep` file is the target of a local module path THEN the system SHALL NOT treat that file as a root.

8.3. A module source SHALL be local only when it is a string literal beginning with `./` or `../`. Every other source SHALL be call-only.

8.4. WHEN a child file is reached from more than one root THEN its nodes SHALL be marked shared, matching the Terraform rule that a module with no single owner is not attributed to one stack.

### Requirement 9: External modules stay unresolved

**User Story:** As an operator running the index in CI, I want external Bicep modules left unresolved, so that indexing never needs network access or credentials.

#### Acceptance Criteria

9.1. WHEN a module source is not a local path THEN the system SHALL keep the module node, the source string, and a `CALLS` edge to a `module_source` node, and SHALL NOT read the module body.

9.2. WHEN an expression selects an output of an external module THEN the system SHALL leave that reference unresolved and SHALL report it as a coverage gap.

9.3. The system SHALL NOT read `~/.bicep`, a registry, a template spec, or any other cache.

### Requirement 10: Same six jobs, Bicep names on a Bicep repository

**User Story:** As an agent connected to a Bicep repository, I want six tools whose jobs I already know, under Bicep names, so that I do not call a Terraform tool against a Bicep graph.

#### Acceptance Criteria

10.1. WHEN the indexed language is Bicep THEN the MCP server SHALL register only `bicep_explore`, `bicep_for_address`, `bicep_impact`, `bicep_modules`, `bicep_orphans`, and `bicep_status`.

10.2. `bicep_explore` SHALL rank declarations the way `terra_explore` ranks Terraform blocks, within the same default character budget of 12000, and an exact symbolic name or an exact literal resource ID SHALL outrank prose.

10.3. `bicep_for_address` SHALL accept a symbolic name, a file-qualified symbolic name, or a literal resource ID alias, and SHALL return references rather than textual mentions, within the default character budget of 4000.

10.4. `bicep_impact` SHALL walk the same edge kinds as `terra_impact`, within the default depth of 3 and the default character budget of 4000, and SHALL describe the graph as static configuration.

10.5. `bicep_modules` SHALL list module calls and their source strings, and SHALL say that an external source was not loaded.

10.6. `bicep_orphans` SHALL report unreferenced `param` and `var` declarations, and outputs of child modules that no caller consumes. Outputs of root files SHALL be excluded. A `param` that callers wire and nothing reads SHALL be reported as wired but unread.

10.7. `bicep_status` SHALL report node and edge counts, roots, parse errors, unresolved references, skipped files of the other language, and that no deployment overlay is loaded.

10.8. The CLI subcommands `status`, `explore`, `refs`, `impact`, `modules`, and `orphans` SHALL keep those names on both languages, and their prose SHALL name the language that was indexed.

10.9. WHEN classification fails with a mixed-language error THEN both the CLI and the MCP server SHALL exit non-zero and SHALL print that error.

### Requirement 11: No Bicep deployment overlay

**User Story:** As an agent asking what a Bicep change will replace, I want the tool to say it cannot know, so that I do not read silence as "no replacement".

#### Acceptance Criteria

11.1. WHEN the indexed language is Bicep THEN the system SHALL NOT load plan JSON, state JSON, or an Azure what-if document, and SHALL NOT contact Azure.

11.2. WHEN `--plan` is set on a Bicep repository THEN the system SHALL still serve the static graph and SHALL report that the flag does not apply to Bicep.

11.3. WHEN the indexed language is Terraform THEN Requirement 1's overlay behavior SHALL be unchanged.

### Requirement 12: A bad file does not blank the repository

**User Story:** As an agent indexing a repository that contains one broken Bicep file, I want the rest of the graph, so that a syntax error does not look like an empty repository.

#### Acceptance Criteria

12.1. WHEN one `.bicep` file fails to parse or uses a construct outside the supported subset THEN the system SHALL omit that file, SHALL record a parse error that names the file, and SHALL index the other `.bicep` files.

12.2. WHEN `bicep_status` or CLI `status` runs after such a failure THEN the output SHALL include that parse error.

12.3. The supported subset SHALL be the one specified in the design, and the fixtures SHALL be written against Bicep v0.47.16 syntax. The implementation SHALL NOT require the `bicep` binary in CI.

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

14.3. Durable documentation updated at closeout MAY state that `.bicep` files are indexed and MAY state the limits in this specification: no `.bicepparam`, no ARM JSON, no external module restore, no deployment overlay, and no token-saving claim.

14.4. A benchmark against a public Bicep repository is out of scope for this specification.
