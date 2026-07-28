# 057. Host-to-host session teleport

> **Type:** decision
> **Status:** Accepted (2026-07-28) — director approval after wedge T1
> shipped (T1a #415–#419, T1b #420, T1c #421, T1d #423) and was
> implementation-reviewed (fixes `f1d63f48` pack/unpack guards,
> `eb8fb2c4` secret-bearing refusal + uncancelable commit point + client
> timeout budget). Refines Part 2 of
> [`plans/env-profiles-and-session-teleport.md`](../plans/env-profiles-and-session-teleport.md)
> where code facts contradict it (see §Decisions). Builds on
> [ADR-014](014-claude-code-resume-cursor.md)'s pause-first resume ordering
> and reuses [ADR-056](056-env-secret-host-envelopes.md)'s host keys for the
> secret-re-seal path (re-seal itself deferred: T1 refuses secret-bearing
> teleports up front).
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `eb8fb2c4` (T1 shipped: pack/unpack
> commands, portable paths, hub orchestration, desktop UI; T2/T3 open)

**TL;DR.** Teleport moves a live session's agent from one host to another
without breaking the conversation. The transcript never moves — it lives on
the hub, stamped by `session_id` — so the only things that must physically
relocate are the two byte-stores the hub does **not** own: the **git worktree**
(the agent's working files) and the **engine-state directory** (the engine's
own on-disk session store, e.g. claude's `~/.claude/projects/<slug>/<uuid>.jsonl`
or kimi's `~/.kimi-code/sessions/<wd>/<session>/`). Teleport is therefore
**`resumePausedSession` re-targeted to a different host, after those two stores
are handed off.** The handoff rides the **existing `host_commands` pull queue**
(the same channel that carries pause/resume/capture/terminate) as two new
command kinds — `session_handoff_pack` on the source, `session_handoff_unpack`
on the target — so no host-reachable RPC and no new transport are introduced.
The engine-state directory is snapshotted by **reusing the host-side
`local_log_tail` path resolvers** (which already know each engine's on-disk
layout), tarred, split into ≤25 MiB parts over the **existing content-addressed
blob store** behind a small **chunked manifest**, and reassembled on the target.
Ordering is **pause-first (ADR-014)**: every step before the row flip is
rollback-safe by simply resuming on the source. T1 covers **git-worktree
sessions** for **claude-code and kimi-code-ts** and assumes both hosts **share a
git remote**; non-worktree workdir tars are T2; hub-relayed bare-repo transport
and host-offline auto-failover are T3.

## Context

- Part 1 (env profiles + host-sealed secrets) is fully shipped (#396–#414).
  Part 2 — host-to-host session teleport — is the second control-plane
  primitive from the cloud-agent comparison (Claude web `--teleport`, Codex
  cloud), and is the remaining scope of the accepted plan.
- **Data-ownership law** ([blueprint](../spine/blueprint.md)): the hub owns
  names + events + metadata; **hosts own bytes**. A session's conversation is
  metadata-adjacent (it is `agent_events`, hub-stored). Its working files and
  the engine's private session store are **bytes on the host** — the only
  state teleport must move.
- **Resume already exists and is 90 % of teleport.** `resumePausedSession`
  (`hub/internal/server/handlers_sessions.go:599`) respawns a paused session's
  agent, carrying forward `worktree_path`, `spawn_spec_yaml`, and the
  per-engine resume cursor (`engine_session_id`, spliced by
  `spliceClaudeResume`/`spliceACPResume`/`spliceAntigravityResume`,
  `handlers_sessions.go:666-685`). It is hard-pinned to the **same** host at
  `handlers_sessions.go:691` (`HostID: deadHostID.String`). Teleport is this
  function with that one field overridden — **after** the two byte-stores are
  present on the target.
- **The host-runner is pull-only** (`runner.go:36-42`, `0002_host_commands`):
  it polls `/v1/teams/{team}/agents/spawns` for spawns and
  `/v1/teams/{team}/hosts/{host}/commands?status=pending` for host commands,
  applies them locally, and PATCHes `result_json`/`status` back. There is no
  hub→host socket. Any teleport work a host must perform has to be **enqueued
  and polled**, exactly like the existing commands.

## Decision

### D-1 — Teleport is re-targeted resume; the transcript never moves

`POST /v1/teams/{team}/sessions/{id}/teleport {target_host_id}`. The session's
`agent_events` stay on the hub keyed by `session_id`, so the conversation view
is continuous across the move. Teleport pauses the source agent, relocates the
worktree + engine-state to `target_host_id`, then runs the existing resume
path with `HostID` = target and `worktree_path` = the target-side path. The
row flip (`current_agent_id`, `host_id`, `status='active'`) is the commit
point.

### D-2 — Handoff rides the `host_commands` queue as two new kinds

No new channel, no host-reachable RPC, and the dormant `plan_executor`
(`hostrunner/plan_executor.go`, zero production callers) is **not** used. Two
new `host_commands.kind`s, each with structured `args_json` and `result_json`:

- **`session_handoff_pack`** (enqueued on the **source** host):
  1. In the worktree: commit any WIP on the session branch (`hub/<handle>` by
     default, `worktree.go:49-52`) and **push** it to the shared remote;
     capture the resulting `head_sha`.
  2. Snapshot the engine-state directory for this `engine_session_id` (see
     D-4), `tar` it, split into ≤25 MiB parts, upload each as a blob, and
     assemble a **manifest** (D-3).
  3. Return `{branch, head_sha, remote, manifest_sha, engine, workdir}` in
     `result_json`.
- **`session_handoff_unpack`** (enqueued on the **target** host):
  1. Fetch `branch` from the shared remote and `git worktree add` it at the
     target-derived worktree path (reusing `EnsureWorktree`, `worktree.go:35`).
  2. Fetch the manifest's blob parts, verify `tar_sha256`, untar the
     engine-state into the target's engine store — **remapping the workdir path
     segment** (claude's cwd→slug encoding; kimi's `workspaces.json` wd→wdID
     lookup — D-4).
  3. Return `{worktree_path, engine_session_id}` (the values the re-targeted
     resume needs) in `result_json`.

The hub enqueues via the existing `enqueueHostCommand`
(`handlers_commands.go:200`) and polls the row's `status`/`result_json`.

### D-3 — Transport: a chunked manifest over the existing blob store

The blob store (`handlers_blobs.go`) is content-addressed by sha256, caps a
blob at **25 MiB** (`maxBlobBytes`, `handlers_blobs.go:21`), buffers the whole
upload (`io.ReadAll`), and is **team-global with no DELETE and no TTL**. Engine
stores and (T2) workdir tars can exceed 25 MiB, so teleport ships a **manifest
blob**:

```json
{ "v": 1, "tar_sha256": "<hex>", "total_size": 12345678,
  "parts": ["<sha256-part-0>", "<sha256-part-1>", …] }
```

The tar stream is split into ≤25 MiB parts, each uploaded as a normal blob; the
manifest itself is a blob. The target fetches parts **sequentially** (bounded
memory on both hosts), concatenates, verifies `tar_sha256`, then untars. No new
endpoint and no blob-store change. Because blobs have no DELETE today, teleport
engine-state parts **linger** — acceptable and consistent with the hub already
retaining the equivalent transcript; a blob-GC / DELETE path is deferred (T3),
noted here so the omission is deliberate, not forgotten.

### D-4 — Engine-state paths reuse the host-side resolvers, NOT YAML globs

The plan sketched declaring per-family `state_paths` globs in
`agent_families.yaml`. **Rejected**: the path logic is non-trivial and already
implemented in Go, host-side, in `hub/internal/drivers/local_log_tail/*/pathresolver.go`
(the hostrunner package already imports these — `launch_m4_*.go`). It is not a
static glob:

- **claude-code**: `~/.claude/projects/<slug>/<session-uuid>.jsonl`, where
  `<slug>` is the cwd with every `/` → `-` (`claude_code/pathresolver.go:24-35`).
- **kimi-code-ts**: `~/.kimi-code/sessions/<wdID>/<session>/{state.json,agents/*/wire.jsonl}`,
  where `<wdID>` is resolved from the cwd via `<store>/workspaces.json`
  (`kimi_code/pathresolver.go:20-132`).

A YAML glob would **duplicate and inevitably diverge** from this. Teleport
instead calls the resolver to enumerate the session's files for `pack`, and on
`unpack` re-derives the target paths from the target workdir (the same
resolvers, run with the target's HOME/cwd) — so the cwd→slug / cwd→wdID remap
is done by the one authority that already gets it right. `state_paths` is thus
a **host-runner capability keyed by engine family**, not family YAML data. T1
implements the resolver-backed snapshot for claude-code and kimi-code-ts; other
families slot in as their resolvers are taught the reverse (enumerate-for-pack)
direction (T2/T3).

### D-5 — Ordering is pause-first; every pre-flip step is resume-on-source

Per ADR-014, the session store is append-only with a single appender, so the
safe rollback at every step is "resume on the source, exactly as it was."
Orchestration order:

1. **Validate** — session is `paused` **or** `active`-and-idle (mid-turn →
   `409 busy`); target host online + family-capable + reachable (reuse
   `checkSpawnHostReachable`, `handlers_agents.go:979`, plus a capability
   check against the target host's advertised `Agents` map,
   `capabilities.go:26-45`); this is a **worktree** session (T1 —
   non-worktree is T2).
2. **Pause** the source agent if it was active-idle (reuse the pause command).
3. **Pack** on source (D-2) → manifest + branch head.
4. **Unpack** on target (D-2) → target worktree_path.
5. **Flip**: re-targeted resume (D-1) spawns on the target and atomically
   updates the sessions row. **This is the commit point.**
6. **Cleanup** (best-effort, post-commit): `RemoveWorktree` on the source
   (`worktree.go:79`; safe because WIP was committed+pushed, so the tree is
   clean).

A failure at steps 1–4 leaves the source authoritative: **resume on source**
and return the teleport as failed. Nothing is destroyed on the source until
after the target is confirmed live (step 6).

### D-6 — T1 assumes a shared git remote; hub relay is T3

`pack` pushes and `unpack` fetches the session branch through a **git remote
both hosts can reach** (e.g. the project's origin). This is the realistic case
for the director's fleet (a VPS steward + a GPU box that both reach GitHub) and
matches the plan's T1 framing. Hosts that share **no** remote need a hub-side
bare-repo relay — explicitly **T3**, not T1. `pack`/`unpack` surface a clear
`no_shared_remote` error so the precondition fails loudly rather than
corrupting state.

### D-7 — Secret-bearing sessions require a client re-seal

Env-secret envelopes (ADR-056) are sealed to the **source** host's key and
cannot be opened on the target. A teleport of a secret-bearing session
therefore needs the **initiating client** to re-resolve the profile's
`secret_refs` and re-seal to the **target** host key (ADR-056 D-6), attaching a
fresh `env_secret_envelope` to the re-targeted spawn. **Headless / steward
teleport of a secret-bearing session is impossible by design** — the hub holds
no openable secrets. T1 **refuses** (`409`) a teleport of a session whose
profile carries `secret_refs` unless the request carries a target-sealed
envelope; the desktop teleport action performs the re-seal (reusing the E3b-3
seal path) before calling the endpoint.

### D-8 — Snapshot lifecycle & the active-worktree invariant

The sessions row's unique index `idx_sessions_active_worktree` on
`(team_id, worktree_path)` for `status IN ('active','paused')`
(`0030_sessions_status_rename`) holds across teleport because the target
worktree path differs from the source's (different host home). The row flip
(step 5) rewrites `worktree_path` to the target's in the same statement that
sets `host_id`, so the invariant is never violated mid-flight. Engine-state and
workdir bytes on the **source** become garbage after step 6; their blobs are
retained (D-3).

## Consequences

- **New surface is minimal**: two `host_commands` kinds, one REST endpoint, a
  chunked-manifest helper, and a resolver-backed engine-state snapshot. No new
  transport, no host-reachable RPC, no blob-store change, no `agent_families`
  schema change.
- **Teleport of an idle worktree session across two hosts sharing a remote
  works end-to-end in T1** for claude-code and kimi-code-ts, driven from the
  desktop session menu with `pausing → packing → transferring → spawning →
  verifying` phases.
- **Deferred (recorded, not scope):** non-worktree workdir tar relay + the
  remaining families (T2); hub bare-repo relay, host-offline auto-teleport
  policy, drain mode, blob GC/TTL, and streaming transfer (T3).
- **Risk — partial handoff**: a crash between `pack` and the flip leaves pushed
  branch + uploaded blobs but an unmoved session; the rollback (resume on
  source) is clean, and the pushed branch/blobs are harmless orphans (branch is
  the session's own `hub/<handle>`; blobs are content-addressed and dedup).

## Slicing (Part 2 wedges)

- **T1** — worktree sessions, claude-code + kimi-code-ts, shared-remote git
  handoff. Internally: **T1a** host `pack`/`unpack` command kinds + chunked
  manifest transport + resolver-backed engine-state snapshot (host-side, local
  Go-testable); **T1c** the teleport endpoint + pause-first orchestration +
  re-targeted resume + rollback (hub-side, local Go-testable); **T1d** the
  desktop session-menu action + progress + secret re-seal (D-7).
- **T2** — non-worktree sessions (workdir tar over the same manifest transport,
  256 MB compressed cap per the plan) + remaining families' snapshot direction;
  mobile action.
- **T3** — host-offline auto-teleport policy, drain mode, hub bare-repo relay
  (D-6), blob GC/TTL (D-3), streaming transfer, telemetry.

## Alternatives considered

- **A dedicated hub→host teleport socket.** Rejected: violates the pull-only
  NAT-friendly design (`0002_host_commands` rationale); the command queue
  already provides enqueue + result-report + status.
- **Wiring the dormant `plan_executor` generic shell-exec.** Rejected: a
  general host-exec primitive is a far larger security surface than four typed
  teleport verbs; the typed `host_commands` kinds keep the host's job auditable
  and closed.
- **Declaring `state_paths` globs in `agent_families.yaml`** (the plan's
  sketch). Rejected — D-4: duplicates non-trivial resolver logic that already
  exists and would silently diverge.
- **Streaming the tar through a new endpoint.** Deferred to T3: the chunked
  manifest over existing blobs bounds memory with zero new surface; a streaming
  endpoint only matters if manifest overhead ever dominates.
