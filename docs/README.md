# termipod docs

> **Type:** axiom
> **Status:** Current (2026-04-28)
> **Audience:** anyone landing in `docs/` for the first time
> **Last verified vs code:** v1.0.308

**TL;DR.** The index. Tells you where to start based on what you came
to find. If you're going to add a doc, read [`doc-spec.md`](doc-spec.md)
first — it defines what every doc must declare and where it lives.

---

## Where to start

**I'm a contributor and want to ship something.**
1. [`roadmap.md`](roadmap.md) — what's in flight, what's next
2. [`spine/`](spine/) — the architecture you'll build against
3. [`plans/`](plans/) — pick up an active wedge or write a new one

**I'm an operator and want to install / run / test.**
1. [`how-to/install-hub-server.md`](how-to/install-hub-server.md)
2. [`how-to/install-host-runner.md`](how-to/install-host-runner.md)
3. [`how-to/run-the-demo.md`](how-to/run-the-demo.md)
4. [`how-to/release-testing.md`](how-to/release-testing.md)

**I'm trying to understand a piece of the system.**
1. Start with [`spine/blueprint.md`](spine/blueprint.md) for the
   architecture
2. [`spine/information-architecture.md`](spine/information-architecture.md)
   for mobile IA
3. [`spine/agent-lifecycle.md`](spine/agent-lifecycle.md) for how
   agents are born / live / die
4. [`spine/sessions.md`](spine/sessions.md) for the session ontology
5. [`reference/vocabulary.md`](reference/vocabulary.md) when a term
   is unfamiliar

**I'm wondering "why did we do X?"**
- [`decisions/`](decisions/) — append-only ADRs. Browse the index
  there; numbers are stable and the status field tells you whether
  a decision is still current.

**I'm exploring an open question.**
- [`discussions/`](discussions/) — pre-decision exploration, mixed
  lifecycle. Status header on each file says whether the question
  is open, resolved, or dropped.

---

## Doc structure

```
docs/
├── README.md                       this file
├── roadmap.md                      where we're going (vision + Now/Next/Later)
├── doc-spec.md                     contract every doc honors
│
├── spine/                          axioms — always-true architecture
├── reference/                      schemas, vocab, API surface
├── how-to/                         task-oriented runbooks
├── decisions/                      append-only ADRs (NNN-name.md)
├── plans/                          active and recent work units
├── discussions/                    open exploration
├── tutorials/                      learning-oriented walkthroughs (TBD)
└── archive/                        superseded, frozen
```

Each directory holds exactly one type of doc. The contract is in
[`doc-spec.md`](doc-spec.md).

---

## Canonical docs (the spine)

These four are the architectural foundation. Everything else cites
back to them.

| Doc | Topic |
|---|---|
| [`spine/blueprint.md`](spine/blueprint.md) | Architecture, axioms, ontology, protocol layering |
| [`spine/information-architecture.md`](spine/information-architecture.md) | Mobile IA — six axioms, role ontology, entity × surface matrix |
| [`spine/agent-lifecycle.md`](spine/agent-lifecycle.md) | How an agent is born, lives, spawns, dies |
| [`spine/sessions.md`](spine/sessions.md) | Session ontology — the conversational primitive that survives respawn |

---

## Conventions

- **Status block** at the top of every file. See `doc-spec.md` §3.
- **Naming:** lowercase-hyphens, no version markers, no dates in
  filenames. See `doc-spec.md` §4.
- **One primitive per file.** A file is exactly one of: axiom,
  vision, plan, decision, reference, how-to, discussion, tutorial,
  archive. See `doc-spec.md` §2.
- **No mixed concerns.** If a file would be "the architecture AND the
  runbook AND open questions," it gets split into three.
- **English only.** Per project convention, all docs are in English.

---

## Adding or moving a doc

Read [`doc-spec.md`](doc-spec.md) first. The 30-second checklist:

1. Pick the right primitive — §2 of the spec
2. Add the status block — §3
3. Put it in the right directory — §5
4. Name it per §4
5. Cross-references use relative paths

Reorgs go in their own commits prefixed `docs:` so feature commits
stay clean.
