# Doc spec — the contract every doc honors

> **Type:** axiom
> **Status:** Current (2026-05-29)
> **Audience:** contributors (humans + AI agents)
> **Last verified vs code:** v1.0.763
> **Freshness:** contract

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
> **Last verified vs code:** v1.0.316
> **Freshness:** contract (or: rolling | snapshot — see §6.1 below; optional)
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

### 6.1 Freshness contract (`Freshness:` field — optional)

Not every doc has the same obligation to track code. Three freshness
contracts; the field is optional, with sensible per-primitive
defaults (below) when omitted.

| Value | Semantics | Lint behaviour |
|---|---|---|
| `contract` | Doc is a live mirror of code. Drift IS wrongness. | `lint-docs.sh` **fails CI** when the `Last verified` gap exceeds `STALE_THRESHOLD`. |
| `rolling` | Doc is the current statement, but drift is acceptable. | `lint-docs.sh` warns at threshold (current behaviour for all docs). |
| `snapshot` | Doc captures a moment in time; later drift doesn't make it wrong. | `lint-docs.sh` skips the drift check. |

**Default when omitted, by primitive:**

| Primitive | Default |
|---|---|
| AXIOM, VISION | `rolling` |
| PLAN (Proposed / In flight) | `contract` |
| PLAN (Done / Deferred / Cancelled) | `snapshot` |
| DECISION (Proposed) | `contract` |
| DECISION (Accepted) | `rolling` |
| DECISION (Superseded / Deprecated) | `snapshot` |
| REFERENCE, TUTORIAL | `contract` |
| HOW-TO | `rolling` |
| DISCUSSION | `snapshot` |
| ARCHIVE | `snapshot` |

The author MAY override the default by writing the field
explicitly. Authors elect to escalate a doc to `contract` when they
want CI to defend it — i.e. when a reader (human or agent) would
make wrong decisions based on a stale version.

**Phased gating.** The strict FAIL behaviour for `contract` applies
only to docs whose authors have **explicitly** written
`Freshness: contract` in the status block. Docs whose default would
be `contract` (per the table above) but haven't yet been
re-verified stay at WARN — same as pre-§6.1 behaviour. This avoids
a big-bang CI break when the field rolls out. As authors touch each
doc and confirm it's truly a contract, they add the explicit
declaration; the strict tier grows incrementally.

Rationale + alternatives considered:
[discussions/doc-freshness-maintenance.md](discussions/doc-freshness-maintenance.md).

### 6.2 Verifiable claims (`<!-- verify ... -->` markers)

Inside the body of `contract` and `rolling` docs, load-bearing
factual claims (file paths, function names, expected file counts,
migration slots) SHOULD be tagged with an inline HTML comment:

```markdown
The reload loop in `policy.go:65` <!-- verify symbol hub/internal/server/policy.go reload -->
reads the file on every mtime change.
```

`scripts/lint-doc-anchors.sh` walks every doc, extracts the
markers, and verifies each claim against the current code. Marker
kinds (MVP):

| Kind | Args | Check |
|---|---|---|
| `file` | `<path>` | File exists |
| `no-file` | `<glob>` | No file matches |
| `symbol` | `<file> <name>` | `\b<name>\b` appears in `<file>` |
| `glob` | `<pattern> <expected_count>` | Exactly N files match |

The markers are HTML comments — invisible in rendered Markdown.
Line refs in prose (`policy.go:65`) stay as navigation hints, but
the authoritative check is symbol presence (lines drift on every
edit; symbols don't).

Markers ARE the new bar for "this claim is fresh." When all
markers in a doc verify, `lint-docs.sh`'s stamp drift is the only
remaining signal — and that's the signal `Freshness:` gates. The
two layers compose: stamp says "the human re-read this recently";
markers say "the cited facts still exist."

### 6.3 Per-primitive update rules

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

## 7. Term consistency — the glossary contract

`docs/reference/glossary.md` is the canonical definition for every
project-specific term that has more than one possible meaning, or
whose meaning isn't obvious cold. It exists because at ~200K LOC of
docs+code the project has accumulated enough term collisions
(*session*, *resume*, *fork*, *kind*, *transcript*, *agent*, …) that
without a fixed reference, both human and AI contributors hallucinate
plausible-but-wrong meanings and ship bugs grounded in the wrong
mental model. The 2026-04-30 claude-code resume bug was a direct
artifact of this — *session* meant two different things in two
adjacent layers, and nothing pinned the boundary.

### The contract

Three rules. CI enforces #1 and #2; #3 is review-discipline.

1. **First-use linking.** When a doc uses a load-bearing
   project-specific term, the **first occurrence** in that doc must
   either (a) link to the glossary entry — `[hub session](reference/glossary.md#hub-session)` (path adjusts per source-doc location)
   — or (b) define the term inline in a sentence. Casual reuses
   downstream don't need to repeat the link.
2. **No new term without a glossary entry.** Any commit that
   introduces a new project-specific term in code or docs must add
   the term to `glossary.md` in the **same commit**, with at minimum
   a one-line def, a *Distinguish from* line if the term has known
   collisions, and a canonical link.
3. **Disambiguate when ambiguous.** If a sentence reads ambiguously
   without a qualifier, add the qualifier. *Session* almost always
   needs to be *hub session* or *engine session*; *kind* almost
   always needs to be *agent kind*, *event kind*, *input kind*, or
   *attention kind*. The glossary's §12 index is the canonical list
   of pairs that need disambiguation.

### What counts as "load-bearing"

A term is load-bearing in a doc when:
- The doc's correctness depends on the reader resolving the term to
  the right meaning. ("This event lands in the **transcript**" is
  load-bearing — the reader has to know whether you mean
  `agent_events` or the engine record.)
- The term has a glossary entry that explicitly calls out a
  collision (any entry with a *Distinguish from* line).
- The term names a schema column, table, type, or protocol field
  ("`engine_session_id`" needs the linked def the first time it
  appears in a discussion doc).

A term is **not** load-bearing when it's used colloquially in prose
where the surrounding sentence resolves the ambiguity ("the user's
session timed out") or in a context where the project-specific
meaning is obviously not in play.

### Adding a new term

1. Pick the canonical spelling. Lowercase-hyphenated for multi-word
   identifiers (`hub-session-id`); natural English for prose terms
   (*hub session*, *engine record*).
2. Place it in the right §1–§11 section of the glossary. If none
   fits, propose a new section in the same commit.
3. Write one line of def. Don't repeat the canonical doc — link to
   it. The glossary is for fast lookup, not depth.
4. Add a *Distinguish from* line if the term collides with anything
   in the glossary's §12 index, and extend §12 with the new pair.
5. The CI lint (§9 below) will reject the PR if a new bolded term
   appears in a doc with no matching glossary entry.

## 8. The contract for new docs

When you add a doc, walk this checklist:

- [ ] One primitive (§2). If it's two, write two files.
- [ ] Status block at top (§3). All five required lines.
- [ ] `Freshness:` field (§6.1) — explicit if you want to override
      the per-primitive default. Add `<!-- verify ... -->` markers
      (§6.2) inline for load-bearing factual claims.
- [ ] Lives in the right directory (§5).
- [ ] Filename obeys §4 — lowercase-hyphens, no qualifiers, no dates.
- [ ] H1 matches filename.
- [ ] If it's a DECISION, has a number and an immutable identity.
- [ ] If it supersedes another doc, the supersedee's status is updated
  to Superseded and links forward.
- [ ] Cross-references use relative paths from the file's location.
- [ ] Load-bearing project terms link to glossary on first use (§7).
- [ ] Any new project-specific term added to the glossary in the
      same commit (§7).

When you find an existing doc that violates this, fix it in a separate
PR with `docs:` prefix. Don't bundle doc reorgs into feature commits.

## 9. CI lints

Three scripts run on every push, all fast (pure bash + grep + awk +
python3):

**`scripts/lint-docs.sh`** — structural rules from §3, §5, §6:

1. The 5-line status block is present at the top of every doc
   (excluding `archive/`, `screens/`, `logo/`).
2. Discussion docs marked `Status: Resolved` link to either a
   `decisions/NNN-*.md` ADR or a `plans/*.md` plan in their first
   30 lines (the durable forward pointer that makes Resolved
   meaningful).
3. Every internal markdown link of the form `[label](relative-path)`
   resolves to an existing file relative to the source doc's
   location.
4. **Stale-doc gate** (§6.1). Per-doc `Last verified vs code:`
   compared to current `pubspec.yaml` version. Behaviour gated by
   the doc's `Freshness:` field (or its per-primitive default when
   absent):
   - `contract` → drift > `STALE_THRESHOLD` **fails CI**
   - `rolling` → drift > `STALE_THRESHOLD` warns (non-failing)
   - `snapshot` → no drift check

**`scripts/lint-doc-anchors.sh`** — verifiable-claim rules from §6.2:

1. Every `<!-- verify KIND ARGS -->` marker is well-formed (legal
   kind, right number of args).
2. Each marker's claim is checkable against the current code:
   - `file <path>` → file exists
   - `no-file <glob>` → no file matches
   - `symbol <file> <name>` → `\b<name>\b` appears in `<file>`
   - `glob <pattern> <count>` → exactly `<count>` files match
3. Any broken anchor fails CI. Tag-only lookup, not deep code
   analysis — drift on rename / move / delete is what this catches.

**`scripts/lint-glossary.sh`** — term-consistency rules from §7:

1. Every glossary entry has a one-line def line right after its `###`
   heading (no orphan headings).
2. Every term in `glossary.md`'s §12 *Index of "easy to confuse with"
   pairs* points to a real entry above (no dangling cross-refs).
3. Spelling-variant detection: for each entry term, scan all docs
   for known-bad alternate spellings (CamelCase / hyphenated /
   underscored / pluralised) and flag them. Drift catches itself
   here.
4. New-term gate: if a PR adds a bolded term in a doc that has no
   matching glossary entry, lint fails with a pointer to §7.

Layer-3 anti-drift (per-tag AI-native re-verification) is a
follow-up tracked in
[`discussions/doc-freshness-maintenance.md`](discussions/doc-freshness-maintenance.md)
§5; the three linters above are the load-bearing piece.

---

## 10. Open questions about this spec

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

## 11. References

- Diátaxis framework: https://diataxis.fr/
- ADR pattern (Michael Nygard, 2011): https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
- Now / Next / Later roadmap: https://www.prodpad.com/blog/the-no-bullshit-product-roadmap/
- Keep a Changelog: https://keepachangelog.com/
