---
name: kyber-weave-docs
description: "Generate conformant Kyber-Weave frontmatter for repository
  documentation so that docs validate and docs drift pass. Use when a document fails a
  KW-DOC-SPEC or KW-DOC-DRIFT rule, when retrofitting an existing documentation
  tree after kyber-weave docs init, or when authoring a new governed document
  and you must choose doc-type, status, component, source-root, and code-refs.
  Not for editing prose or style, authoring SKILL.md skills, or writing harness
  agent definitions."
license: MIT
metadata:
  author: dpalfery
  version: 0.1.1
---

# Authoring Kyber-Weave documentation

Your job is to make a Markdown document **conformant**: correct frontmatter, honest
claims about code, and a `doc-type` that matches what the document actually is.

Verify with the tool, never by eye:

```bash
kyber-weave docs validate .
kyber-weave docs drift .
```

## Required frontmatter

Every governed document needs all six base keys:

```yaml
---
id: docgraph/architecture      # permanent, unique slug; others reference this
title: DocGraph architecture
doc-type: architecture         # closed vocabulary — see below
status: current                # closed vocabulary — see below
owner: dpalfery                # MUST already exist in <docs-root>/catalog.md
last-reviewed: 2026-08-01      # ISO yyyy-MM-dd, no other format
---
```

**doc-type** is one of: `architecture`, `onboarding`, `requirements`, `adr`, `plan`,
`spec`, `runbook`, `reference`, `rule`, `governance`, `index`.

**status** is one of: `current`, `draft`, `needs-review`, `superseded`.

These are closed sets. If nothing fits, the answer is `reference` — never invent a value,
and never widen the vocabulary to fit one document.

## Additional keys by doc-type

| doc-type | Also required |
|---|---|
| `architecture`, `requirements`, `runbook`, `plan`, `spec` | `component` |
| `onboarding` | `component`, `source-root` |
| `adr`, `reference`, `rule`, `governance`, `index` | nothing |

`component` and `owner` must already be rows in `<docs-root>/catalog.md`. If the value you
need is not there, **add the catalog row first** — inventing a component in frontmatter
fails `KW-DOC-SPEC-004`.

## The pairing invariant

For `architecture` and `runbook` only: `source-root` and `code-refs` travel together.

- An architecture doc with `source-root` **must** list `code-refs`
- An architecture doc with `code-refs` **must** declare `source-root`
- A runbook with `source-root` **must** list `code-refs`

If you cannot name real symbols, drop `source-root` rather than inventing them.

## code-refs are claims, not mentions

```yaml
code-refs:
  - DocumentIndex
  - DocumentCorpus
api-endpoints:
  - GET /api/me/usage
```

Listing a symbol asserts **this document is answerable for it**. Every entry is resolved
against a live code index — a name that does not resolve fails `KW-DOC-DRIFT-001`.

So: list only symbols the document genuinely describes. Do not list every type it
mentions in passing. Three accurate entries beat twenty aspirational ones.

**Verify before writing.** Confirm each symbol exists, and prefer the bare name over a
fully qualified one unless it is ambiguous. If a code graph index is present you can check
directly:

```bash
sqlite3 .codegraph/codegraph.db \
  "SELECT name, kind, file_path FROM nodes WHERE name='DocumentIndex' AND kind<>'import';"
```

`source-root` must be a real repository-relative directory that actually contains the
indexed source for that component.

LOAD `references/retrofit.md` when converting an existing documentation tree.
LOAD `references/rules.md` for every rule id and what clears it.

## Choosing doc-type honestly

The type drives retrieval ranking, so a wrong one is not cosmetic:

- **`plan` and `spec` are demoted to 0.55** — they are records of intent, not current guidance
- **`superseded` is demoted to 0.4**
- **`adr` sits at 0.9**

Labelling a standard as a `plan` buries it. Labelling a closed plan as `reference`
promotes a work artifact into guidance an agent will act on. Pick what the document *is*,
not what would rank best.

Set `status: draft` when you have filled the mechanical keys but a human has not confirmed
the semantic ones. Draft is demoted to 0.85, which degrades gracefully — far better than a
confident `current` on metadata nobody checked.

## Example

A file at `docs/payments/architecture.md` opening with `# Payments service` and no
frontmatter. The catalog already has a `Payments` component owned by `payments-team`,
whose source root is `src/Payments`.

Verify the symbols first:

```bash
sqlite3 .codegraph/codegraph.db \
  "SELECT name, kind FROM nodes WHERE name IN ('PaymentProcessor','RefundHandler') AND kind<>'import';"
```

`PaymentProcessor` returns a class; `RefundHandler` returns nothing. So only the first is
listed — the second would fail `KW-DOC-DRIFT-001`:

```yaml
---
id: payments/architecture
title: Payments service
doc-type: architecture
status: draft
component: Payments
source-root: src/Payments
owner: payments-team
last-reviewed: 2026-08-01
code-refs:
  - PaymentProcessor
---
```

`status: draft` because a human has not yet confirmed the component and source-root are
right. `source-root` and `code-refs` appear together, satisfying the pairing invariant for
an architecture document.

## Workflow

1. Read the document. Decide what it actually *is* → `doc-type`.
2. Check `<docs-root>/catalog.md` for the `component` and `owner`. Add a row if missing.
3. Write the base keys. Use a real ISO date for `last-reviewed`.
4. Add type-specific keys. Honour the pairing invariant.
5. Verify every `code-refs` symbol resolves before listing it.
6. Run `kyber-weave docs validate .` and fix findings by rule id.
7. Run `kyber-weave docs drift .` and correct or drop unresolved symbols.

Fix what a rule reports. Do not widen the ontology in `.kyber-weave/kyber-weave.yml` to
make a failure disappear — that discards the guarantee the corpus exists to provide.

## Never

- Invent a `component` or `owner` that is not in the catalog
- Add a `code-refs` symbol you have not verified
- Change `doc-type` or `status` vocabularies to fit one document
- Set `status: current` on frontmatter you filled in without review
- Backdate or forward-date `last-reviewed` — use the date it was actually reviewed
