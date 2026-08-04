---
id: documentation-ontology
title: The documentation ontology
doc-type: reference
status: draft
owner: 'dpalfery'
last-reviewed: 2026-08-04
---

# The documentation ontology

Every governed document in `docs/` conforms to this schema. `kyber-weave docs
validate` enforces it; `kyber-weave docs drift` checks that what documents claim
about code is still true.

## Frontmatter

Keys are hyphenated. Unknown keys are ignored, so your own metadata never breaks
parsing.

```yaml
---
id: payments/architecture
title: Payments service
doc-type: architecture
status: current
component: Payments
source-root: src/Payments
owner: payments-team
last-reviewed: 2026-08-04
code-refs:
  - PaymentProcessor
---
```

| Key | Meaning |
|---|---|
| `id` | Permanent, unique slug. Other documents reference this, never the file path. |
| `title` | Human title. |
| `doc-type` | One of the closed set below. Decides which other keys are required. |
| `status` | Currency of the document, from the closed set below. |
| `component` | The unit of the system this covers. Must exist in `catalog.md`. |
| `source-root` | Repository-relative path to that component's source. Must exist. |
| `owner` | Who answers for it. Must exist in `catalog.md`. |
| `last-reviewed` | ISO `yyyy-MM-dd`. Any other format is an error. |
| `code-refs` | Symbols this document formally claims. Resolved against the code graph. |
| `api-endpoints` | Exact route strings, e.g. `GET /api/me/usage`. |
| `decided-by` | Ids of the ADRs that decided this document's content. |
| `supersedes` | Ids of documents this one replaces. |

## Closed vocabularies

**doc-type** — `architecture`, `onboarding`, `requirements`, `adr`, `plan`, `spec`,
`runbook`, `reference`, `rule`, `governance`, `index`

**status** — `current`, `draft`, `needs-review`, `superseded`

A value outside these sets is an error. An open vocabulary is not a vocabulary — it
is a text field that drifts until two documents of the same kind carry different
labels and neither is findable by the other's name. If nothing fits, use
`reference`; widen the set in `.kyber-weave/kyber-weave.yml` deliberately or not at
all.

## Required keys

**Every document**: `id`, `title`, `owner`, `last-reviewed`, `doc-type`, `status`

| Doc type | Additionally required |
|---|---|
| `architecture`, `requirements`, `runbook`, `plan`, `spec` | `component` |
| `onboarding` | `component`, `source-root` |
| `adr`, `reference`, `rule`, `governance`, `index` | — |

## The pairing invariant

For `architecture` and `runbook`, `source-root` and `code-refs` travel together. A
source root without symbols claims coverage the document does not have; symbols
without a root leave nothing to check them against. Both halves are what make drift
detection possible.

## code-refs are claims, not mentions

Listing a symbol asserts the document is answerable for it. A document that merely
discusses `PaymentProcessor` in prose has not claimed it; one listing it in
`code-refs` has, and will fail `KW-DOC-DRIFT-001` when the symbol is renamed.

That distinction is the point of the ontology. After a rename, prose still reads
correctly — no linter, reviewer, or test notices. Only resolution does.

## Ranking consequences

Retrieval weights declared identity above prose, and scales by how far a document
counts as current guidance: `plan` and `spec` are demoted to 0.55, `superseded` to
0.4, `draft` and `needs-review` to 0.85, `adr` to 0.9.

So `doc-type` and `status` are not cosmetic. Labelling a standard as a `plan` buries
it; labelling a closed plan as `reference` promotes a work artifact into guidance an
agent will act on.

## Adopting an existing tree

Fill the mechanical keys in bulk with `status: draft`, get `docs validate` clean,
then add `code-refs` selectively and promote to `current` as each component is
reviewed. The `kyber-weave-docs` skill covers the whole procedure.
