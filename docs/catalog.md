---
id: catalog
title: Component and owner catalog
doc-type: reference
status: draft
owner: 'dpalfery'
last-reviewed: 2026-08-04
---

# Component and owner catalog

This table is the **authoritative vocabulary** for the `component` and `owner`
frontmatter keys. A document naming a component with no row here fails
`KW-DOC-SPEC-004`. The check exists so that components cannot be invented one
document at a time until nobody can say how many there are.

One row per component that genuinely exists. TerraGraph is roughly four thousand lines,
which is three real units and a product name — not a row per package. `internal/graph`
holds the model the loader produces, `internal/text` the vectorizer retrieval uses, and
`internal/render` the output both entry points share; none is a component anyone would ask
a question about on its own.

| Component | Type | Source root | Overview | Detailed documentation | Owner | Last reviewed | Status |
|---|---|---|---|---|---|---|---|
| TerraGraph | Product | `.` | The whole tool: index Terraform, serve it to agents. | [README](../README.md) | dpalfery | 2026-08-04 | draft |
| Loader | Library | `internal/tfhcl` | Walks the repo, discovers roots, parses HCL, and extracts reference edges from expression traversals. | — | dpalfery | 2026-08-04 | draft |
| Retrieval | Library | `internal/index` | BM25 corpus, identity-weighted ranking, the four relationship queries, and the snapshot host. | — | dpalfery | 2026-08-04 | draft |
| Interfaces | Entry points | `cmd` | The `terragraph` CLI and the `terragraph-mcp` server — deliberately separate binaries. | — | dpalfery | 2026-08-04 | draft |

## How the columns are read

Only **Component** (index 1) and **Owner** (index 6) are parsed, counting the empty
cell produced by the leading pipe. The other columns are for human readers and may
be reworded freely. Moving either parsed column requires a matching
`ontology.catalog` override in `.kyber-weave/kyber-weave.yml`.
