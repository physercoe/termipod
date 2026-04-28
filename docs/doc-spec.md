# Doc spec — the contract every doc honors

> **Type:** axiom
> **Status:** Current (2026-04-28)
> **Audience:** contributors (humans + AI agents)
> **Last verified vs code:** v1.0.308

**TL;DR.** This file defines what a doc *is* in this repo, what every
doc must declare, where it lives, and how it's named. Adopting it
gives readers (you, future-you, Claude, and any contributor) a
30-second answer to "what is this file, do I trust it, does it
apply to me right now?"

Read this file first if you're about to add or move a doc.

---

## 1. Why a spec?

Docs accumulate during scaling. Without a spec, three rots set in:

1. **Authority drift** — canonical and exploratory docs sit at the
   same visual weight; readers can't tell which ones to trust.
2. **Lifecycle blur** — shipped, in-flight, deferred, and superseded
   docs coexist with no signal saying which is which.
3. **Naming entropy** — files get named after events ("redesign",
   "audit") or for the moment they were created ("v2"), not for the
   stable concept they document.

The spec is the cheapest fix for all three. One status block + a
naming rule + a primitive type per file, applied consistently.

---

## 2. The seven primitives

Every doc is exactly one of these. Mixing primitives in one file is
how doc systems rot — split or pick.

| # | Primitive | Authority | Lifecycle | Intent | Example |
|---|---|---|---|---|---|
| 1 | **AXIOM** | Canon | Forever (slow) | Why X exists | `blueprint.md` |
| 2 | **VISION** | Canon | Quarter-scale | Where we're going | `roadmap.md` |
| 3 | **PLAN** | Canon | Time-bound | What's next | `plans/cache-first-cold-start.md` |
| 4 | **DECISION** (ADR) | Canon | Append-only | Why we chose X | `decisions/002-mcp-consolidation.md` |
| 5 | **REFERENCE** | Canon | Continuous | Lookup | `reference/hub-mcp.md` |
| 6 | **HOW-TO** | Canon | Continuous | Do this task | `how-to/install-host-runner.md` |
| 7 | **DISCUSSION** | Exploration | Resolves or fades | Think aloud | `discussions/positioning.md` |

Two operational adjuncts — supporting types, not primitives:

- **TUTORIAL** — learning-oriented walkthrough (Diátaxis layer 1).
  "Your first agent spawn, end-to-end."
- **ARCHIVE** — superseded primitives, frozen for archaeology.
  Read-only. Never edited after the move.

### Choosing the right primitive

If you're about to write a doc, walk this decision tree:

1. **Is it always-true architectural content?** → AXIOM.
2. **Does it answer "where are we going?"** → VISION.
3. **Is it a time-bound work unit with a target?** → PLAN.
4. **Does it record a concrete decision and its consequences?** → DECISION.
5. **Is it lookup material — schemas, names, vocab?** → REFERENCE.
6. **Does it tell someone how to do a specific task?** → HOW-TO.
7. **Are you exploring open questions?** → DISCUSSION.

If the answer is "two of these," you have two docs, not one.

---

## 3. The status block — what every doc must declare

Top of every file. Light prose. No YAML frontmatter — these are
human-read, and YAML would be ceremony for ceremony's sake.

```markdown
# Title

> **Type:** axiom (or: vision | plan | decision | reference | how-to | discussion | tutorial | archive)
> **Status:** Current (2026-04-28) — see status vocab below
> **Audience:** contributors (or: operators | end-users | principal | reviewers)
> **Last verified vs code:** v1.0.308
> **Supersedes:** decisions/005-old-name.md (only if applicable)

**TL;DR.** One or two sentences. What this doc tells you, in plain language.

---

[body]
```

**Status vocabulary, by primitive:**

| Primitive | Allowed statuses |
|---|---|
| AXIOM | Current · Superseded |
| VISION | Current · Revised · Achieved |
| PLAN | Proposed · In flight · Done · Deferred · Cancelled |
| DECISION | Proposed · Accepted · Superseded · Deprecated |
| REFERENCE | Current · Stale (needs update) |
| HOW-TO | Current · Stale |
| DISCUSSION | Open · Resolved (→ link to ADR or plan) · Dropped |
| TUTORIAL | Current · Stale |
| ARCHIVE | Archived (frozen at vX.Y.Z) |

**The 30-second rule.** A reader who lands on a file should be able
to answer in 30 seconds: *what is this, do I trust it, does it apply
to me right now?* The status block is what makes this possible.

---

## 4. Naming spec

Names are an interface. They contract with future readers about what's
behind a path, with zero qualifiers.

### Directory naming — 8 rules

1. **Lowercase only.** No camelCase, no Title Case.
2. **Hyphens for multi-word.** `how-to/`, never `how_to/` or `howto/`.
3. **One word per primitive.** Reflects exactly one of: `spine`,
   `plans`, `decisions`, `reference`, `how-to`, `discussions`,
   `tutorials`, `archive`. No `agent-discussions/` or similar mixes.
4. **No version markers** in dir names (`v2/`, `current/`, `new/`).
5. **No qualifiers** (`active-`, `done-`, `legacy-`). Status lives in
   the file's status block, not the path.
6. **Grep-friendly.** Full words, not abbreviations (`reference/` not
   `ref/`).
7. **Stable.** Once chosen, don't rename — it breaks every
   cross-reference.
8. **Plural for collections, singular for conceptual containers.**
   `decisions/` (many ADRs), `archive/` (one frozen pool).

### File naming — 8 rules

1. **Lowercase with hyphens.** Same as dirs.
2. **Name the topic, not the doc style.** `blueprint.md` ✓ —
   `blueprint-design-doc.md` ✗.
3. **No version markers in the filename.** Versioning lives in the
   status block (`Last verified vs code: v1.0.308`), not the name.
4. **No date markers in the filename.** `audit-2026-04-23.md` ✗ —
   that goes in the status block.
5. **No primitive-leak qualifiers.** Don't suffix `-plan` or `-spec`
   or `-design` — the directory the file lives in already says what
   primitive it is.
6. **ADRs use `NNN-name.md`** with 3-digit zero-padded numbers,
   sequential. `001-locked-candidate-a.md`. Numbering is the only
   ordering signal that survives renames; it's the ADR's identity.
7. **Plans don't get numbers** — they're parallel work units, not a
   sequential record. The status header sorts them ("In flight" vs
   "Done").
8. **How-tos and tutorials use task-shape.** `install-host-runner.md`,
   `run-the-demo.md`, `setup-your-first-agent.md`. The reader's verb
   leads.

### H1 inside the file — match the filename

Filename `agent-lifecycle.md` → H1 `# Agent lifecycle`. Sentence case,
no trailing colon. The H1↔filename round-trip is a cheap sanity check.

---

## 5. Directory layout

```
docs/
├── README.md                       Index — where to start, by reader role
├── roadmap.md                      Vision + Now/Next/Later + phases
├── doc-spec.md                     This file
│
├── spine/                          AXIOM — always-true architectural content
├── reference/                      REFERENCE — schemas, vocab, API surface
├── how-to/                         HOW-TO — task-oriented runbooks
├── decisions/                      DECISION — append-only ADRs (NNN-name.md)
├── plans/                          PLAN — active and recent work units
├── discussions/                    DISCUSSION — open exploration
├── tutorials/                      TUTORIAL — learning-oriented walkthroughs
└── archive/                        ARCHIVE — superseded, frozen
```

Each directory holds exactly one primitive. A file in `spine/` is
always an AXIOM; a file in `plans/` is always a PLAN; etc.

`README.md` and `roadmap.md` and `doc-spec.md` live at the top level
because they're orientation docs — readers find them before they need
to navigate into a primitive.

---

## 6. Lifecycle rules

### AXIOM
- Updated when architecture changes
- Status moves Current → Superseded only when a successor doc exists
  and is linked

### VISION
- Reviewed each quarter; bumped status year-by-year
- The roadmap is itself a VISION; it changes but old versions aren't
  preserved (use git history)

### PLAN
- Created when work is committed (Proposed)
- Moves to In flight when work begins
- Moves to Done when the wedge ships, with a version tag in the status
- Moves to Deferred or Cancelled if abandoned — the file stays for
  archaeology

### DECISION (ADR)
- Append-only. Once Accepted, the file is immutable except for status
  changes (→ Superseded or → Deprecated)
- Superseding ADRs link forward; the original keeps its number
- Numbers are dispense-on-creation. Don't reserve, don't skip.

### REFERENCE / HOW-TO / TUTORIAL
- Updated whenever the underlying surface changes
- The status moves to Stale when the doc has drifted from the code
  (a reader's signal that the file may be wrong)

### DISCUSSION
- Open while the question is live
- Resolves to a linked DECISION (preferred) or fades (Dropped)
- Resolved discussions stay in `discussions/` — they document the
  process that led to the ADR; they're not ADRs themselves

### ARCHIVE
- One-way move; archived files are frozen at the point of move
- The original directory's link is removed (no dangling reference)
- Archived files keep their old name as historical artifact, even if
  it violates current naming rules

---

## 7. The contract for new docs

When you add a doc, walk this checklist:

- [ ] One primitive (§2). If it's two, write two files.
- [ ] Status block at top (§3). All five lines.
- [ ] Lives in the right directory (§5).
- [ ] Filename obeys §4 — lowercase-hyphens, no qualifiers, no dates.
- [ ] H1 matches filename.
- [ ] If it's a DECISION, has a number and an immutable identity.
- [ ] If it supersedes another doc, the supersedee's status is updated
  to Superseded and links forward.
- [ ] Cross-references use relative paths from the file's location.

When you find an existing doc that violates this, fix it in a separate
PR with `docs:` prefix. Don't bundle doc reorgs into feature commits.

CI runs `scripts/lint-docs.sh` on every push, which enforces three
rules from this spec:

1. The 5-line status block is present at the top of every doc
   (excluding `archive/`, `screens/`, `logo/`).
2. Discussion docs marked `Status: Resolved` link to either a
   `decisions/NNN-*.md` ADR or a `plans/*.md` plan in their first
   30 lines (the durable forward pointer that makes Resolved
   meaningful).
3. Every internal markdown link `[text](path.md)` resolves to an
   existing file.

Layer-2 anti-drift signals (stale-doc reports, touched-area reports
on PRs, ADR backlinks from spine/reference) are follow-ups; the
linter is the load-bearing piece.

---

## 8. Open questions about this spec

Carrying these forward — answer when they come up:

- **Per-doc audience matters when?** Today most docs are
  contributor-facing; if Termipod ever ships end-user docs they'd
  want their own subtree.
- **Do we want frontmatter for tooling?** Currently no — the prose
  status block is enough for human reads. If we ever build doc-site
  generation it might pay to add YAML.
- **Diátaxis fit.** TUTORIAL/HOW-TO/REFERENCE/EXPLANATION (axiom +
  discussion) is roughly Diátaxis. We layer DECISION and PLAN on top
  for the engineering process. If Diátaxis purity ever matters
  (publishing to docs.termipod.dev), the mapping is:
  - Diátaxis tutorial → our TUTORIAL
  - Diátaxis how-to → our HOW-TO
  - Diátaxis reference → our REFERENCE
  - Diátaxis explanation → our AXIOM + DISCUSSION

---

## 9. References

- Diátaxis framework: https://diataxis.fr/
- ADR pattern (Michael Nygard, 2011): https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
- Now / Next / Later roadmap: https://www.prodpad.com/blog/the-no-bullshit-product-roadmap/
- Keep a Changelog: https://keepachangelog.com/
