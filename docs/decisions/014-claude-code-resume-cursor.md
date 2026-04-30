# 014. Claude-code resume threads `--resume <session_id>`

> **Type:** decision
> **Status:** Accepted (2026-04-30)
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Claude-code's CLI exposes `--resume <session_id>` for
cross-process conversation continuity, mirroring gemini-cli's
`--resume <UUID>` (ADR-013) and codex's `thread/resume` JSON-RPC
(ADR-012). Until v1.0.349 the hub never threaded any of these flags
for claude-code, so every "Resume" tap on a paused session spawned a
fresh engine session — same hub transcript window, brand-new claude
context. This ADR pins the fix: capture the engine `session_id` from
the `session.init` event into a new `sessions.engine_session_id`
column, and on `POST /sessions/{id}/resume` splice
`--resume <session_id>` into `backend.cmd` before calling `DoSpawn`.
The captured cursor is engine-neutral; codex and gemini can land
their own resume wiring into the same column in follow-up wedges.

## Context

The bug surfaced from device-test feedback on v1.0.348-alpha: a user
paused a claude-code steward, tapped Resume, and the new agent had no
memory of prior turns even though the mobile transcript looked
contiguous. Investigation traced it to two distinct concepts of
"session" colliding:

- **Hub `sessions.id`** — the transcript window the mobile UX shows.
  Stable across `pause → resume`. `agent_events.session_id` stamps
  every event with this id so the chat backfill survives an agent
  swap.
- **Engine `session_id`** — claude-code's own conversation cursor,
  stored under `~/.claude/projects/...`. Each fresh `claude` process
  mints a new one and emits it in the `system/init` stream-json
  frame. `claude --resume <id>` is the only way to reattach.

Per-engine resume support before this ADR:

| Engine | Resume API | Wired in hub? |
|---|---|---|
| Claude (M2 stream-json) | `--resume <id>` argv flag | **No** — fresh session every spawn |
| Codex (app-server JSON-RPC) | `thread/resume` RPC, `ResumeThreadID` field | Driver plumbing exists; hub-side capture/persist not wired |
| Gemini (exec-per-turn) | `--resume <UUID>` argv flag | Driver-internal cursor; hub-side cross-restart capture not wired |

`StdioDriver.legacyTranslate` already lifts `session_id` from claude's
init frame into the `session.init` agent_event payload
(`hub/internal/hostrunner/driver_stdio.go:295`). The cursor was being
captured, just never persisted or threaded.

## Decision

D1. **Persist the engine session id on the session row.** Add
`sessions.engine_session_id TEXT` (migration `0033`). Engine-neutral —
the field name doesn't bake in any vendor's terminology, so codex
(`threadId`) and gemini (`session_id`) can populate it from their own
capture paths without schema change.

D2. **Capture from `session.init` events.** When
`POST /agents/{id}/events` accepts an event with
`kind=session.init && producer=agent`, the hub extracts
`payload.session_id` and `UPDATE sessions SET engine_session_id = ?`
where the live session pointed at this agent. Side-effect lives in
`captureEngineSessionID` next to `touchSession` so future engines
can land their capture paths in one place. Errors from the side-
effect can't fail the event insert — worst case is a cold-start
resume, the pre-ADR-014 baseline.

D3. **Splice on resume, not on first spawn.** `handleResumeSession`
reads `engine_session_id` alongside `spawn_spec_yaml`. When the dead
agent's `kind=claude-code` and a cursor is captured, the handler
calls `spliceClaudeResume(specYAML, cursor)` to rewrite
`backend.cmd` with `--resume <id>` directly after the `claude` binary
token. The first spawn never splices (no prior session to resume),
matching what users expect.

D4. **Keep `sessions.spawn_spec_yaml` un-spliced.** The handler
splices only the in-flight `spawnIn.SpawnSpec` it passes to
`DoSpawn`; it never `UPDATE`s the sessions row's stored spec.
Successive resumes therefore always splice from a clean cmd.
Without this discipline, repeat resumes would stack `--resume`
flags on the persisted spec and the first-spawn-on-fresh-session
path would inherit a stale cursor on the next session reuse.

D5. **YAML-node rewrite, not text-regex.** `spliceClaudeResume`
walks the parsed yaml.v3 document tree to `backend.cmd`, mutates the
scalar value, and re-marshals. Robust against template whitespace
quirks and cmd-string single-vs-double-quote variations, and
preserves the rest of the spec untouched. Falls through to the
unmodified spec on parse error or missing key — better a cold-start
resume than a 500.

D6. **Idempotent and self-healing.** The rewriter strips any prior
`--resume <other>` flag pair before splicing the current one, so an
operator who hand-edits a template with a stale id doesn't poison
subsequent resumes. The cmd is normalised to single-flag form on
each pass.

D7. **Claude-only for this wedge.** `spliceClaudeResume` short-
circuits when `tokens[0]` isn't `claude` (or an absolute path ending
in `/claude`). Codex resumes via `thread/resume` JSON-RPC — wiring is
in `AppServerDriver.ResumeThreadID` waiting for the same capture
path to feed it. Gemini resumes via its own driver-internal
`SetResumeSessionID` — also wired structurally, also waiting for a
hub-side feeder. ADR-015/016 (TBD) will close those, reusing
`sessions.engine_session_id`.

## Consequences

- Every `POST /sessions/{id}/resume` for claude-code now produces a
  spawn whose `agent_spawns.spawn_spec_yaml` carries the spliced
  `--resume <id>` flag. Audit trail is grep-able: searching for
  `--resume` across spawn rows shows which resumes successfully
  threaded a cursor and which fell back to cold-start.
- Sessions whose first init was emitted before the v1.0.349 deploy
  have a NULL `engine_session_id`; their first resume is still a
  cold-start. The next init after redeploy populates the column,
  and subsequent resumes are warm.
- The capture is best-effort. If the hub crashes between the event
  insert and the cursor UPDATE the next event-publish will retrigger
  via the next session.init (whenever the agent restarts) or fall
  back to cold-start. We don't add a retry loop.
- Cross-engine reuse: gemini-cli's `init` event payload also carries
  a `session_id` field of identical shape, so once we wire its
  capture path the same `captureEngineSessionID` hook works
  unchanged. Codex's `threadId` is delivered on a different frame
  (the `thread/started` JSON-RPC notification) — that needs its own
  capture path but the destination column is shared.

## Fork is explicitly cold-start

Resume threads the engine cursor; **fork mints a fresh one**. The
two operations look adjacent in the mobile UX (both spawn a new
agent against an existing session row) but their relationship to
engine state is opposite:

| Op | Hub session | Engine session_id | First-spawn argv |
|---|---|---|---|
| Resume | reused (status: paused→active) | same as last spawn | `--resume <captured_id>` |
| Fork | new row, scope+title copied | **never inherited** | cold-start, no `--resume` |

Why fork must not inherit:

- **Engine session stores aren't multi-writer.** Claude's
  `~/.claude/projects/<cwd>/<sid>.jsonl`, gemini's
  `<projdir>/.gemini/sessions/<uuid>`, and codex's CLI thread
  store all assume a single live attacher. Two parallel sessions
  resuming the same id race writes; the archived source's
  "frozen" state stops being frozen.
- **Archive semantics.** A user who archived the source said "this
  conversation is done." A fork that keeps writing to the same
  engine session lies about the archive.
- **Multiple forks.** Two forks of one archive both pointing at
  the same engine id corrupt each other on the next turn.

`handleForkSession` (`handlers_sessions.go:342`) already enforces
this implicitly by writing `spawn_spec_yaml = NULL` on the new
row, but the boundary deserves a defensive guard test
(`TestSessions_ForkDoesNotInheritEngineSessionID`) so a future
change that tries to "helpfully" inherit the cursor fails loudly.

**Status (MVP).** Fork is currently shipped as an experimental
primitive: the new session is created cold, and continuity comes
from the user reading back through the source's transcript (still
queryable by `session_id`). Productising fork — automated
distillation, system-prompt injection, "fork from turn N" —
is deferred. See [discussion: fork & engine-side context
mutations](../discussions/fork-and-engine-context-mutations.md)
for the open shape.

## Files

- `hub/migrations/0033_sessions_engine_session_id.up.sql` — column add
- `hub/internal/server/handlers_sessions.go` —
  `captureEngineSessionID` helper + resume-handler splice
- `hub/internal/server/handlers_agent_events.go` — invocation site
- `hub/internal/server/resume_splice.go` — `spliceClaudeResume`,
  `rewriteClaudeResumeFlag`, `findScalar`
- `hub/internal/server/resume_splice_test.go` — splice unit tests
- `hub/internal/server/handlers_resume_engine_session_test.go` —
  end-to-end capture + resume tests + fork-no-inherit guard
- `hub/internal/server/context_mutation.go` —
  `detectContextMutation` per-engine vocabulary (OQ-4 input-side)
- `hub/internal/server/context_mutation_test.go` — detector unit
  tests
- `hub/internal/server/handlers_context_marker_test.go` —
  end-to-end input-route marker emission tests
- `docs/discussions/fork-and-engine-context-mutations.md` — design
  space for fork productisation + mutation observability

## Cross-links

- ADR-009 — agent state & identity (resume-from-paused contract).
- ADR-010 — frame profiles as data (where `session_id` originates in
  the translator).
- ADR-012 — codex app-server (peer engine, JSON-RPC `thread/resume`).
- ADR-013 — gemini exec-per-turn (peer engine, argv `--resume`).

## Open

- **OQ-1.** Codex `threadId` capture path. Driver field exists
  (`AppServerDriver.ResumeThreadID`); hub-side feeder is the gap.
- **OQ-2.** Gemini cross-restart cursor seed. Driver method exists
  (`ExecResumeDriver.SetResumeSessionID`); same gap as codex.
- **OQ-3.** Reconcile-driven respawn. Today reconcile only marks an
  agent crashed; it doesn't relaunch. If we ever add that path it
  needs to consult `engine_session_id` the same way `handleResume
  Session` does.
- **OQ-4 (PARTIAL — input-side resolved v1.0.349).** Engine-side
  context mutations vs hub transcript. Each engine ships interactive
  commands that mutate its session state *without telling the hub*:
  claude `/compact`, `/clear`, `/rewind`; gemini `/compress`; codex's
  analogous primitives.

  **Model pinned:** the hub transcript is an **operation log** — it
  records what was said, requested, and triggered, not the engine's
  literal context state. Engine mutations are observable on the
  hub's *input* path (we see the user's slash command); they're
  *not* observable on the engine's *output* path (none of the
  engines emit a frame announcing the mutation in stream-json
  today). The hub records what it can see, marks where it can't.

  **Shipped v1.0.349 (input-side resolution).** When the input
  route receives `kind=text` whose body matches a per-engine
  context-mutation slash command, the hub emits a typed
  `agent_event` immediately after the input row (`producer=system`,
  `kind ∈ {context.compacted, context.cleared, context.rewound}`).
  Mobile renders these as inline operation chips so the transcript
  reads as an operation log: `[user] /compact` → `[system] context
  compacted`. Per-engine vocabulary lives in
  `detectContextMutation` (`hub/internal/server/context_mutation.go`):
    - claude-code: `/compact`, `/clear`, `/rewind`
    - gemini-cli: `/compress`, `/clear`
    - codex: TBD (vocabulary not yet audited; emission is a no-op)

  **Still open (pushed to OQ-4b):**
  - **Engine-emitted markers.** When an engine *does* surface a
    compaction/clear event in stream-json (claude's
    `system/compact_boundary` in some SDK builds; gemini's frame
    on `/compress`), the frame profile (ADR-010) should lift it to
    the same typed kind so engine-initiated mutations get a marker
    too. Today only user-initiated ones do.
  - **Cursor invalidation on `/clear`.** Should `/clear` set
    `sessions.engine_session_id = NULL` so a future resume cold-
    starts? Today claude reuses the same id post-`/clear`, so
    `--resume` would land on a cleared engine while the hub
    transcript holds full history — wrong for both directions.
  - **`/rewind` as hub primitive.** Should `/rewind` map to a
    hub-side "rollback to seq=N" that mints a fork from that point
    instead of mutating the live session? See
    `discussions/fork-and-engine-context-mutations.md` §4.3.
- **OQ-5.** Fork productisation (deferred from MVP). Today fork
  creates a bare session shell; spawning into it cold-starts the
  engine. The user-expected behaviour ("fork retains the gist of
  the source") needs either summarisation-into-system-prompt or
  archive-time distillation. Both engineering paths are
  cross-engine because all three accept a system prompt at spawn
  time, but the choice between LLM-driven summary, last-K verbatim
  injection, and steward-authored distillation hasn't been made.
