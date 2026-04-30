# Fork & engine-side context mutations

> **Type:** discussion
> **Status:** Partially resolved (2026-04-30) — input-side mutation
> markers shipped in v1.0.349 (ADR-014 OQ-4); fork productisation
> + engine-emitted markers + cursor invalidation on `/clear` remain
> deferred from MVP
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Two related problems land on the same surface — the
boundary between hub session and engine session. (1) **Fork** today
is a bare session shell; spawning into it cold-starts the engine,
which means the user-expected "this fork picks up where the source
left off" behaviour isn't real. (2) Each engine ships interactive
context commands (claude `/compact` `/clear` `/rewind`, gemini
`/compress`, codex equivalents) that mutate engine-side state
*without telling the hub*. The hub's `agent_events` log keeps a
complete transcript; the engine's view diverges silently. **Status
(v1.0.349):** ADR-014 pinned the model — *the hub transcript is an
operation log* — and shipped the input-side observable: when the
user types one of these slash commands, the hub emits a typed
`context.<verb>` marker as a system-producer agent_event so the
mobile transcript shows where the engine context truncated. Fork
productisation, engine-*emitted* markers (when the engine's own
stream surfaces the mutation), and cursor invalidation on `/clear`
remain deferred. This doc collects the shape for the next wedge.

---

## 1. Two layers of "session"

The hub and each engine both use the word "session" to mean
different things:

| Layer | What it tracks | Lifecycle | Storage |
|---|---|---|---|
| Hub `sessions.id` | Mobile transcript window — the chat the user sees | Stable across pause→resume; archive→fork mints a new one | `sessions` table; `agent_events.session_id` stamps every event |
| Engine `session_id` (claude / gemini) or `threadId` (codex) | Engine's own conversation cursor — what `--resume` reattaches to | One per engine process incarnation; `--resume` re-binds; `/clear` may reset; `/compact` rewrites in place | claude `~/.claude/projects/<cwd>/<sid>.jsonl`; gemini `<projdir>/.gemini/sessions/<uuid>`; codex CLI thread store |

ADR-014 wires the resume primitive: capture engine cursor on
`session.init`, splice `--resume <id>` on `handleResumeSession`.
Fork stays cold by design (cursor never inherited from source).

What ADR-014 does *not* address: every engine's interactive control
plane that lets the user mutate engine-side context out-of-band.

---

## 2. The engine-side commands

A non-exhaustive matrix of mutations the user can trigger inside a
running agent:

| Command | Engine | Effect on engine context | Effect on engine `session_id` | Hub observability |
|---|---|---|---|---|
| `/compact` | claude | summarises history into a synthetic system message; drops literal turns | unchanged | none — no marker frame in stream-json |
| `/clear` | claude | resets conversation to empty | (TBD — may reuse id or mint new) | none |
| `/rewind` | claude | drops trailing N turns | unchanged | none |
| `/compress` | gemini | analogous to `/compact` | unchanged | TBD — may emit a frame |
| ? | codex | TBD — app-server may surface mutation methods | TBD | TBD |

Two consequences of this opacity:

**A. Resume after compact is a partial rehydration.** A user
`/compact`s, the agent quietly carries on with summarised context.
A later pause + resume threads `--resume <id>` and the engine
reattaches to its compacted state. The hub transcript still renders
the full pre-compact history. From the user's POV the chat looks
contiguous, but the model's actual context is much shorter than
what the screen shows. This is fine for casual use and
load-bearing-wrong for "reviewing what the agent decided based on."

**B. Clear is silently destructive.** If `/clear` keeps the engine
`session_id` stable (claude's behaviour as of 2026-04-30 is
unverified; needs confirmation), a resume would land on an empty
engine that the hub renders as if the prior turns still informed
it. The user thinks they're continuing a conversation; the engine
thinks it just woke up.

**C. Rewind is the hub's natural primitive misplaced.** Rewind is
"go back to turn N and try again from there." That's exactly what
**fork from a specific seq** would do at the hub level — except
rewind happens engine-side, leaving the hub transcript intact while
the engine truncates. The two operations should probably be the
same primitive.

---

## 3. Fork today vs fork as users expect it

### What the code does (v1.0.349)

`handleForkSession` (`hub/internal/server/handlers_sessions.go:342`):

- requires source `status='archived'`
- copies `scope_kind`, `scope_id`, `title` to a new `sessions` row
- writes `worktree_path = NULL`, `spawn_spec_yaml = NULL`,
  `engine_session_id = NULL`
- lands `paused` (or `active` if attached to an idle agent)

A subsequent spawn into the fork uses whatever spec the user picks;
the engine starts cold. The hub transcript from the source is
queryable by the source's `session_id` but doesn't appear in the
fork's chat.

### What users expect (informally)

- "Fork should remember the gist of the source."
- "Fork should let me try a different approach from a specific
  point in the source conversation."
- "Fork should preserve files / decisions / agreements from the
  source so I don't repeat onboarding."

None of those are wrong, none of them are what fork does today.
Hence the experimental status pinned in ADR-014.

---

## 4. Design space

### 4.1 Productising fork's context-carryover

Three engineering paths, all cross-engine because every engine
accepts a system prompt at spawn time:

**Option A: LLM-driven summary at fork time.** The fork handler
runs a summariser pass over the source's `agent_events`, producing
a single "Background context" block injected into the new spawn's
`context_files["CLAUDE.md"]` (or equivalent). Pros: fully
automatic, summary tuned to the user's question at fork time.
Cons: extra LLM call, latency on fork, summariser quality varies.

**Option B: Last-K verbatim injection.** The fork handler grabs
the last K turns from the source's `agent_events` and dumps them
verbatim into the new spawn's system prompt. Pros: no
summarisation needed, deterministic. Cons: doesn't scale to long
sources; K is a magic number.

**Option C: Steward-authored distillation at archive time.** When
a session is archived, the steward writes a "what we decided / where
we left off" block to a new `sessions.distillation` column. Fork
inlines that block into the new spawn's system prompt. Pros: high
quality, human-curated, archive-as-checkpoint maps to user mental
model. Cons: requires steward UX for distillation, only useful if
the steward actually authored one before archive.

**Option D: Fork from turn N.** Hub-level rewind: the fork copies
the source's `agent_events` up to seq=N as the new session's
backfill, then injects a "you're continuing this conversation"
prompt. The engine sees only the new spawn but the user sees the
full pre-fork history rendered as if the engine had said it. Pros:
maps cleanly to "rewind to here and try again." Cons: the engine's
real context only contains what's in the system prompt, so any
follow-up question that references mid-source content will fail.

These aren't mutually exclusive — the eventual answer is probably
C as the default with D as a power-user escape hatch.

### 4.2 Modelling engine-side mutations

**Shipped v1.0.349 — input-side markers.** The hub watches its
own input route. When `kind=text` arrives with a body matching a
known per-engine slash command (`/compact` `/clear` `/rewind` for
claude, `/compress` `/clear` for gemini), the input handler emits
a follow-up `agent_event` row with `producer=system` and
`kind ∈ {context.compacted, context.cleared, context.rewound}`.
The marker lands one seq behind the user's `input.text` so the
transcript reads like an operation log. Detection lives in
`detectContextMutation`; emission in
`maybeEmitContextMutationMarker` next to `captureEngineSessionID`.

This handles the common case (user types the command in chat) but
not engine-initiated mutations: claude's auto-compact when the
context window fills, or gemini's `/compress` triggered from a
lower layer. Those still need the engine to surface the mutation
in stream-json before the hub can mark them — option α below.

**Option α: Marker frames.** Patch each engine's frame profile to
emit a typed `agent_event` (e.g. `kind="context.compacted"`) when
the engine performs a mutation, IF the engine surfaces it in
stream-json. Lets the hub render "context compacted here" inline
in the transcript and lets a future resume decide whether to honour
or roll back. Cost: cross-engine instrumentation, several engines
don't surface the event today.

**Option β: Hub-mediated mutations only.** Disable engine-side
slash commands entirely (impossible for claude — `/compact` is part
of the bin's interactive REPL, no flag to gate it) or route them
through a hub primitive (`POST /sessions/{id}/compact` that emits
a marker frame and then calls the engine). Cost: every engine has
its own command surface; we'd need per-engine adapters and we'd
break the user's mental model of "claude works the way it works."

**Option γ: Snapshot + diff.** Periodically dump the engine's
local session JSONL alongside the hub's `agent_events` and diff on
resume. If they've diverged, surface a banner. Cost: tight
coupling to engine storage layout that vendors are free to change.

**Option δ: Accept the divergence; document it.** The MVP answer.
Hub transcript is the source of truth for *the audit log*; engine
state is the source of truth for *what the model actually
thinks*. They drift; users learn this. ADR-014's existing capture +
splice is enough for the resume primitive; mutations stay
unobservable until a user-facing problem motivates a deeper
treatment.

### 4.3 The unification temptation

Rewind (engine-side) and fork-from-turn-N (hub-side) have the same
shape. If we ever ship D from §4.1, it would naturally absorb
claude's `/rewind` semantics — except `/rewind` runs in the live
process, while D mints a new agent. They couldn't be the same
implementation, but they could share UX vocabulary.

---

## 5. Recommendation (for now)

**MVP (shipped):** ADR-014 + the defensive guard test. Fork stays
cold, engine mutations stay unobserved, divergence is documented.

**Next wedge candidates** (none committed):

1. Inventory what each engine's mutation commands actually do — in
   particular whether `session_id` survives `/clear`. This is a
   one-day investigation that would materially change OQ-4 in
   ADR-014.
2. Pick a fork productisation path from §4.1. C (archive-time
   distillation) feels right but requires steward UX work.
3. Marker frames for `/compact` (Option α). Smallest surface,
   highest visibility-per-effort if any engine actually surfaces
   the event in stream-json.

---

## 6. Cross-links

- ADR-014 — claude-code resume cursor (the primitive this builds
  on); fork section + OQ-4/OQ-5.
- ADR-009 — agent state & identity (defines fork as
  resume-from-archive primitive at the hub level).
- ADR-010 — frame profiles as data (where marker-frame translation
  would land).
- `discussions/transcript-ux-comparison.md` — adjacent — how the
  mobile renders the transcript today; relevant to how
  context-mutation markers would surface to users.
