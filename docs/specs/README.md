---
id: specs/index
title: Specification index
doc-type: index
status: draft
owner: dpalfery
last-reviewed: 2026-09-30
---

# Specification index

This directory is the active specification directory for TerraGraph.

The documentation root is `docs`, declared by `.kyber-weave/kyber-weave.yml`. The repository has no root `AGENTS.md` config registry, so this file is the specification index those artifacts resolve through.

One feature occupies one subdirectory and contains `requirements.md`, `design.md`, and `tasks.md`. A specification records intent. It is not current guidance, and nothing outside this directory should cite it as how the tool behaves today.

Completed specifications move to `docs/specs/archive/<feature>/`. The archive is never current guidance. Closeout updates this table when that move happens.

| Feature | Phase status | Requirements | Design | Tasks |
|---|---|---|---|---|
| [bicep-support](bicep-support/requirements.md) | Approved 2026-09-30 (awaiting final approve-and-execute) | [requirements](bicep-support/requirements.md) | [design](bicep-support/design.md) | [tasks](bicep-support/tasks.md) |
