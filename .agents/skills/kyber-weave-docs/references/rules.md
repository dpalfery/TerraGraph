# Documentation rule reference

Every rule `kyber-weave docs validate` and `docs drift` can emit, and what clears it.

## Schema — `docs validate`

Needs no code index.

### `KW-DOC-SPEC-001` — no frontmatter, or unparseable

The file has no `---` block, or the YAML inside it failed to parse.

Clear it by adding the six base keys. If the block exists but fails, the usual causes are
tab indentation, an unquoted value containing `:`, or a list written inline where `- `
items are expected. Quote any title containing a colon:

```yaml
title: "Skills in your ALM: a governance playbook"
```

### `KW-DOC-SPEC-002` — outside a closed vocabulary

`doc-type` or `status` is not in the closed set, or `last-reviewed` is not ISO
`yyyy-MM-dd`. `2026-8-1` fails; `2026-08-01` passes. `Current` fails on the value, not the
case — comparison is case-insensitive, so the real cause is a value like `active` or
`published` that simply is not in the set.

### `KW-DOC-SPEC-003` — required key missing

A base key, or a key this `doc-type` requires. Before supplying it, check the `doc-type`
is right: this rule fires most often on a document mislabelled `architecture` that cannot
name a `component`.

For `architecture` and `runbook` it also enforces the pairing invariant — `source-root`
present without `code-refs`, or the reverse.

### `KW-DOC-SPEC-004` — value not in the catalog

`component` or `owner` is not a row in `<docs-root>/catalog.md`. The diagnostic suggests
the nearest catalog value when the edit distance is plausible.

Two fixes, and picking the right one matters: if the value is a typo, correct the
document. If the unit genuinely exists and is absent from the catalog, add the row. Do not
retype the frontmatter to match an unrelated component just to pass.

### `KW-DOC-SPEC-005` — source-root does not exist

The path is not a directory or file in the repository. Paths are repository-relative and
forward-slashed — not relative to the document, and not absolute.

### `KW-DOC-SPEC-006` — bad reference

Either two documents declare the same `id`, or `decided-by` / `supersedes` names an `id`
no document declares.

Ids are permanent and unique. When two collide, the newer document changes — renaming an
established id breaks every reference to it.

## Drift — `docs drift`

Requires a CodeGraph index at `.codegraph/codegraph.db` and the `sqlite3` CLI.

### `KW-DOC-DRIFT-001` — code-refs symbol unresolved

The symbol is not in the index. Either it was renamed or deleted, or it never existed.

When the index itself is missing, this is reported **once as critical** rather than per
document. That is an environment problem, not a documentation problem: build or restore
the index.

Otherwise the diagnostic names the nearest surviving symbol when one is close. Update the
reference, or drop it if the document no longer covers that code. Dropping is legitimate
and often correct — a stale claim is worse than no claim.

### `KW-DOC-DRIFT-002` — api-endpoints route unmatched

Route strings are exact, including method and full path template:

```yaml
api-endpoints:
  - GET /api/me/usage
```

`/api/me/usage` alone fails. So does `GET /api/me/usage/` and a concrete
`GET /api/me/123` where the indexed route is templated.

### `KW-DOC-DRIFT-003` — source-root indexed nothing

Warning, not error. The path exists but the index holds no file beneath it. Either the
path is wrong, or the index is stale — the tool cannot tell which, which is why it does
not fail the build.

## Exit codes

`docs validate` and `docs drift` exit non-zero on any **error**. Warnings and info do not
gate. `--no-info` hides informational findings; `--format sarif` emits SARIF for code
scanning.
