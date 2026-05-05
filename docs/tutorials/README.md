# Tutorials

> **Type:** axiom
> **Status:** Current (2026-05-05)
> **Audience:** new contributors + new directors
> **Last verified vs code:** v1.0.351

**TL;DR.** Learning-oriented walk-throughs per Diátaxis. Each tutorial
takes you from zero to a working understanding of one part of the
system by *doing* — not by reading reference docs first. Pair with the
how-tos under [`../how-to/`](../how-to/) for task-oriented runbooks
and the spine docs under [`../spine/`](../spine/) for the architectural
foundations.

---

## What's here

| # | Tutorial | Outcome | Time |
|---|---|---|---|
| 00 | [Getting started](00-getting-started.md) | Hub + mobile + first project + steward, end to end | ~60 min |
| 01 | [Author a project template](01-author-a-project-template.md) | A custom YAML project template you wrote, instantiated, visible in mobile | ~45 min |
| 02 | [Build a worker agent](02-build-a-worker-agent.md) | A worker template + prompt; steward spawns it; you observe in mobile | ~45 min |

---

## Reading order

If you're brand-new, do them in order — each one builds on the last.
If you already have a hub running, skip 00 and pick up at 01.

These tutorials assume Linux / macOS / WSL2 + a clean shell. Windows
native is untested.

For the cold-start install + dev workflow, see
[`../how-to/local-dev-environment.md`](../how-to/local-dev-environment.md).

---

## What's *not* here

- **How-to runbooks.** Use the
  [`../how-to/`](../how-to/) directory: `install-hub-server.md`,
  `install-host-runner.md`, `run-the-demo.md`, etc. Those are
  task-oriented — you go to them when you need to do a specific
  thing.
- **Reference material.** Use [`../reference/`](../reference/):
  `architecture-overview.md`, `database-schema.md`,
  `api-overview.md`, etc.
- **Architecture explanations.** Use [`../spine/`](../spine/):
  `blueprint.md`, `protocols.md`, `data-model.md`, etc.

Per the [Diátaxis](https://diataxis.fr) framework, those four
audiences (learning / doing / looking up / understanding) need
different docs.

---

## Open follow-ups

Beyond the three starter tutorials, see
[`../plans/doc-uplift.md` §9](../plans/doc-uplift.md):

- A `tutorials/03-first-contribution.md` — for external contributors
  walking through their first PR
- Translated tutorials (zh / ja) — post-MVP

If anything in a tutorial diverges from how the system actually
behaves when you try it, that's a doc bug — file an issue or open a
PR fixing the doc.
