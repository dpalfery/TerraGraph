# Retrofitting an existing documentation tree

Converting a tree that has never been governed. The corpus starts at "every file fails
`KW-DOC-SPEC-001`" — no frontmatter at all.

Work in this order. Each step makes the next one's errors meaningful instead of drowned.

## 1. Establish the vocabulary first

Nothing else can pass until `<docs-root>/catalog.md` names the components and owners the
documents will claim. `kyber-weave docs init` seeds the table; fill it from what the
repository actually contains.

One row per genuine unit of the system. Resist one row per document — a catalog with
forty components is a list of files, and the closed vocabulary stops meaning anything.

Owners come from `CODEOWNERS`, `git log`, or the team that answers pages. A placeholder
owner is worse than an honest `unassigned` row, because it looks reviewed.

## 2. Mechanical keys, in bulk, as draft

For every file, fill what needs no judgment:

| Key | Derive from |
|---|---|
| `id` | Path below the docs root, without `.md`, e.g. `docgraph/architecture` |
| `title` | The H1, or the filename in title case |
| `last-reviewed` | Today, or `git log -1 --format=%ad --date=short <file>` |
| `owner` | `CODEOWNERS` entry for the path, else the dominant committer |
| `status` | **`draft`** — always, at this stage |
| `doc-type` | Best guess from path and filename; see the heuristics below |

`status: draft` is the important one. It is honest, and retrieval demotes draft to 0.85 so
a half-retrofitted corpus degrades gracefully instead of serving unreviewed metadata as
current guidance. Promote to `current` only when a human has confirmed that file.

### doc-type heuristics

| Signal in path or filename | Likely type |
|---|---|
| `adr/`, `decisions/`, `NNNN-*.md` | `adr` |
| `runbook`, `playbook`, `oncall`, `incident` | `runbook` |
| `architecture`, `design`, `hld`, `lld` | `architecture` |
| `onboarding`, `getting-started`, `setup` | `onboarding` |
| `requirements`, `prd`, `user-stories` | `requirements` |
| `plan`, `roadmap`, `proposal` | `plan` |
| `spec`, `rfc` | `spec` |
| `policy`, `standard`, `convention` | `rule` or `governance` |
| `README.md` at a directory root | `index` |
| anything else | `reference` |

`reference` is the correct fallback. It requires no extra keys and carries no ranking
penalty, so a wrong guess there is recoverable. Guessing `architecture` is not — it
demands `component` and drags in the pairing invariant.

## 3. Validate and iterate

```bash
kyber-weave docs validate .
```

Fix by rule id, most common first:

- **`KW-DOC-SPEC-004`** — the catalog is missing a row, or the value is a typo. The
  diagnostic names the nearest match when one is plausible; trust it.
- **`KW-DOC-SPEC-003`** — a type-specific key is missing. Either supply it, or reconsider
  whether the `doc-type` guess was right. A file that cannot name a `component` is usually
  not an `architecture` document.
- **`KW-DOC-SPEC-006`** — duplicate `id`. Two files claimed the same slug; path-derived
  ids collide when two directories hold the same filename. Qualify with the parent
  directory.
- **`KW-DOC-SPEC-005`** — `source-root` does not exist. Usually a stale path from a
  previous layout. Drop it, and drop `code-refs` with it.

## 4. Add code-refs last, selectively

Only after the corpus validates clean. This is the step that needs real judgment, and it
is worth doing on the documents that matter rather than all of them.

Good candidates: architecture documents for a component, runbooks that operate a named
service, references describing a public API.

Poor candidates: onboarding prose, meeting notes, anything narrative. A document with no
`code-refs` is completely valid — an empty claim is better than a false one.

Then:

```bash
kyber-weave docs drift .
```

Every `KW-DOC-DRIFT-001` means the symbol does not exist. Correct it or remove it; never
silence it by widening configuration.

## 5. Promote out of draft

Walk the corpus a component at a time. When someone who knows that component confirms the
frontmatter is true, set `status: current` and `last-reviewed` to that date.

A corpus that stays entirely `draft` still works — everything is retrievable, uniformly
demoted. That is a stable resting place, not a failure.

## What not to do

**Do not widen the ontology to make errors disappear.** Adding a doc-type to
`.kyber-weave/kyber-weave.yml` because six files do not fit converts a closed vocabulary
into a text field, which is the exact failure the ontology prevents.

**Do not retrofit the archive.** `archive` is an excluded path segment by default because
an archived plan is not current guidance. Leave it out of scope.

**Do not fabricate `last-reviewed`.** A date nobody stood behind makes every freshness
check meaningless.
