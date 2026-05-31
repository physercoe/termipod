---
name: Graceful host-runner update and session resume
description: Design exploration for an orchestrated host-runner update lifecycle — drain, update, auto-resume — so that shipping a host-runner fix (e.g. the trackio metric-poller change) does not hard-kill every agent on the host and strand its sessions in a manual, principal-only resume. Documents the current behaviour verified against code (host.update fires a hard restart with no drain; agent death flips sessions to paused reactively; resume exists but is REST/principal-driven and split across two paths — SIGCONT a paused-alive agent vs respawn a dead agent's paused session; no auto-resume on reconnect; no session MCP surface so a steward cannot resume). Proposes a three-phase lifecycle (graceful drain with a checkpoint signal + bounded wait → update → auto-resume of the sessions that were running, gated by a resume-on-reconnect intent so deliberately-paused sessions are left alone) and a steward-reachable respawn, and lays out the open questions (drain timeout, auto-resume scope, in-flight turn handling, ordering vs version verification). Relates to ADR-028 (host control CLI) and ADR-034 (orchestration loop closure / lifecycle hooks).
---

# Graceful host-runner update and session resume

> **Type:** discussion
> **Status:** Open (2026-05-31) — raised after the trackio poller fix
> (v1.0.755): shipping a host-runner change restarts the host-runner,
> which hard-kills every agent on that host. Explores an orchestrated
> drain → update → resume lifecycle. Feeds a future ADR (likely an
> ADR-028 extension).
> **Audience:** contributors
> **Last verified vs code:** v1.0.756

**TL;DR.** Every host-runner upgrade (binary swap + restart) hard-kills
all agents on that host. Today the system copes *reactively*: the dead
processes flip their sessions to `paused`, and a human resumes each one
by hand — there is no graceful drain, no auto-resume, and no
steward-reachable resume. As host-runner fixes ship more often (the
trackio change is the immediate trigger), this needs to become an
orchestrated lifecycle: **drain** (signal agents to checkpoint, pause
them cleanly, wait briefly) → **update** → **auto-resume** the sessions
that were running, leaving deliberately-paused ones alone.

---

## 1. Why now

The trackio fix (v1.0.755) is a host-runner change. To deploy it, the
host-runner restarts — and on restart every agent process on that host
dies. For a single tester babysitting one worker that is an annoyance;
for a fleet of stewards + workers it is a coordinated outage with a
manual recovery. The cost scales with exactly the thing we are building
toward (many agents per host), so the ad-hoc path won't hold.

## 2. Current behaviour (verified vs code)

- **`host.update` is a hard restart, no drain.** `handleAdminHostUpdate`
  → `updateOneHost` (`handlers_admin.go:366`) just enqueues the
  `host.update` verb (download + restart). Nothing pauses or warns the
  agents first; their processes die when the runner restarts.
- **Agent death → session `paused`, reactively.** When the runner
  restart kills an agent, its session transitions to `paused`
  ("host-runner restart killed the agent process" —
  `handlers_sessions.go:15`). State that wasn't written to the
  worktree (mid-turn output) is lost.
- **Resume exists but is split and principal-only:**
  - `handleResumeAgent` (`POST /agents/{id}/resume`) — SIGCONTs a
    **paused-but-alive** agent (e.g. budget pause). Useless once the
    process is dead.
  - `handleResumeSession` (`POST /sessions/{id}/resume`,
    `handlers_sessions.go:555`) — **respawns** a fresh agent into a
    `paused` session, reusing `worktree_path` + `spawn_spec_yaml` +
    `engine_session_id` so it continues where it left off. Requires the
    session to be `paused` and to carry a spawn spec.
  - Both are REST/mobile only. There are **no session MCP tools**, and
    `agents.resume` (added v1.0.756) only does the SIGCONT path — so a
    **steward cannot bring a dead worker back**; only a human can.
- **No auto-resume on reconnect.** When the host reconnects (heartbeat),
  nothing re-spawns the sessions that were running before the update.
- **Partial substrate already exists:** the host-command queue
  (`enqueueHostCommand` pause/resume), budget-driven pause
  (`budget.go`), the reconcile pass that marks dead panes, and the
  loop-sweep escalation machinery (ADR-034) are all reusable pieces.

## 3. Proposed lifecycle

A host update should be three explicit phases instead of one hard verb.

1. **Drain.** Before issuing `host.update`, for each live agent on the
   host: (a) emit a "you are about to be paused — checkpoint now"
   signal (a lifecycle hook the agent/engine can act on), (b) enqueue a
   graceful pause, (c) wait for ack up to a bounded timeout, then
   proceed regardless. Record, per session, a **resume-on-reconnect
   intent** so phase 3 knows which sessions to bring back (vs the ones a
   human deliberately paused, which stay down).
2. **Update.** Issue `host.update` as today; the runner downloads and
   restarts. Drained agents are already paused, so the restart is no
   longer a surprise kill.
3. **Auto-resume.** On the host's next healthy heartbeat (optionally
   after verifying the reported new version), respawn the sessions
   flagged in phase 1 — reusing the existing `handleResumeSession`
   respawn core — in dependency order (stewards before the workers they
   coordinate, so a worker's escalation target is alive).

## 4. Steward reachability

Coordination shouldn't require the human. Two gaps:

- A steward can `agents.terminate` and now `agents.resume` (SIGCONT) but
  cannot **respawn** a dead worker's session. Either expose a
  session-respawn MCP tool (keyed by agent → its paused session) or
  fold respawn into `agents.resume` so one verb "brings the worker
  back" whether it was suspended or killed.
- Stewards have **no session visibility** at all (no `sessions.list` /
  `sessions.get` MCP tools), so even diagnosing "which of my workers is
  paused" is currently impossible from the agent side.

## 5. Open questions

1. **Drain timeout.** How long do we wait for an in-flight turn to
   checkpoint before forcing the pause? Fixed (e.g. 30 s), per-engine,
   or policy-driven? What does an agent actually *do* on the checkpoint
   signal — is there anything to persist beyond the worktree?
2. **Auto-resume scope.** Resume every session that was running, or only
   stewards (let stewards re-dispatch workers)? How do we distinguish
   "running, killed by the update" from "paused on purpose by the user"
   — the resume-on-reconnect intent flag, and where it lives.
3. **In-flight work.** Mid-turn output not yet written to the worktree
   is lost on kill. Is a pre-drain "flush" feasible for any engine, or
   do we accept turn-loss and rely on the engine `--resume` cursor to
   re-ask?
4. **Ordering & verification.** Resume only after the host reports the
   expected new version? Steward-before-worker ordering — derive from
   the spawn lineage (`agent_spawns.parent_agent_id`)?
5. **Scope of the trigger.** Single-host update, fleet update
   (`/v1/admin/fleet/update`), and a crashed-and-recovered host all want
   the same resume behaviour — is "resume sessions that were running
   when the host went away" the unifying rule, independent of *why* it
   went away?

## 6. Relationship to existing work

- **ADR-028 (host control via tunnel + CLI)** — the host
  shutdown/update/restart verbs live here; this lifecycle is the
  natural Phase-N extension (drain/resume around the existing update).
  The roadmap lists ADR-028 Phase 1 as *Next, not started*.
- **ADR-034 (orchestration loop closure)** — already defines lifecycle
  hooks and stall escalation; the drain "checkpoint now" signal and the
  resume ordering should reuse that machinery rather than inventing a
  parallel one.
- **`agent-lifecycle.md` / `sessions.md` (spine)** — the
  pause/respawn/archive state model this builds on.

## 7. Sources (code)

- `internal/server/handlers_admin.go:366` — `updateOneHost` (hard
  restart, no drain).
- `internal/server/handlers_sessions.go:15,555` — session→paused on
  death; `handleResumeSession` respawn core.
- `internal/server/handlers_agent_control.go` — `handleResumeAgent`
  (SIGCONT), `enqueueHostCommand` pause/resume.
- `internal/server/budget.go` — pause-on-budget (a working drain-like
  precedent).
- `internal/hubmcpserver/tools.go` — `agents.resume` (SIGCONT only);
  no session MCP tools.
