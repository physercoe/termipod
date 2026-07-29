# 058. Host job surface — long-running host-side computations

> **Type:** decision
> **Status:** Proposed (2026-07-29) — resolves task #160, the W4b blocker in
> [`plans/replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md)
> (.rrd export needs a computation the request/response dataset verbs cannot
> carry). Written before any job code exists so the mechanism lands in the
> right layer instead of inside a feature wedge.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `589a01b0`
> (`datasetVerbTimeout` `hub/internal/server/handlers_datasets.go:40`,
> `tickCommands` `hub/internal/hostrunner/runner.go:503`,
> `chunkBundle` `hub/internal/hostrunner/teleport_commands.go:122`,
> `storeBlob` `hub/internal/server/handlers_blobs.go`)

**TL;DR.** Some host-side computations the Replay plan needs — LeRobot .rrd
export (decodes every frame of an episode), ffmpeg episode extraction
(plan §11), digest refresh on very large v3.0 datasets — cannot ride the
synchronous dataset verbs, which are bounded at 60 seconds by design
(`datasetVerbTimeout`: "a wedged host must fail the request, not hold a hub
connection open"). A host job is therefore **a new family of allowlisted
`host_commands` kinds executed detached**: submission, delivery, status,
result, and cancellation all reuse the existing pull-only command queue
(migration 0002) — no new transport, no hub-initiated connection, NAT-safe
as today. What is genuinely new is small: job kinds run in a **goroutine
with a single-flight guard** instead of inline on the poll tick (which is
serial, and it shares the single main-loop goroutine with the spawn,
reconcile and idle ticks — an inline job would starve not just
pause/resume/teleport but spawn launches and status reconciliation for
its whole duration), a **`progress_json` heartbeat** on the command row, a
**`job_cancel` kind**, and a **host-side artifact cache with LRU eviction**
shared with §11's ffmpeg extraction. Artifacts stay on the host
(data-ownership law: hosts own bytes); v1 returns a host-local path, which
is exactly what the local-first Replay consumer needs. Remote artifact
fetch, when the SSH-forward wedge lands, reuses teleport's chunked-manifest
blob transport — the pattern already exists. Explicitly out of scope: a
generic "run this command" kind, job persistence across host-runner
restarts, queues/priorities/scheduling, and modeling jobs as runs.

## Context

- **The bound is real and correct.** Dataset verbs go hub→host through
  `s.tunnel.enqueueHostVerb` with `datasetVerbTimeout = 60s`
  (`handlers_datasets.go:40,541`) and 504 past it. Loosening it would trade
  a design gap for held-open hub connections — the exact failure family
  ADR-057's T1c fix closed (a 30-second client timeout stacked on
  15-minute commands stranded work and broke cancel). Long work must not
  ride request/response at all.
- **The Replay plan implies at least three long computations.** W4b .rrd
  export via the pinned `(lerobot, rerun-sdk)` pair (plan §11 — integrate
  LeRobot's own rerun path, no bespoke writer); host-side ffmpeg episode
  extraction with an LRU cache (§11, not yet built); digest refresh on
  large v3.0 datasets already flirts with the 60s bound (full parquet
  metadata walks). Three known consumers is past the threshold where a
  shared surface stops being speculative.
- **A desktop-side exporter was considered and rejected** (task #160's
  other branch). The pinned `(lerobot, rerun-sdk)` environment lives where
  datasets and training live — the host. A desktop exporter means Python
  environment discovery on the user's machine (a support matrix that exists
  nowhere in the desktop today) and soft-degrades to "missing" for exactly
  the users the feature targets; and once remote datasets arrive it becomes
  decode-every-frame-over-the-wire, so the rewrite is mandatory, not
  hypothetical. The #394 soft-degrade pattern was designed for host-side
  missing deps; keep it there.
- **The substrate mostly exists.** `host_commands` (migration 0002) is a
  pull-only hub→host work queue with kind/args/status/result/error columns
  and pending→delivered→done|failed lifecycle; ADR-057 already extended it
  with long-running kinds (`session_handoff_pack`/`unpack`). Teleport also
  built chunked artifact transport: `chunkBundle` splits a bundle into
  ≤25 MiB parts in the content-addressed blob store behind a manifest.
  What does not exist: detached execution (`tickCommands`,
  `runner.go:503`, runs commands serially inline, on the same
  single main-loop goroutine as the spawn/reconcile/idle ticks),
  progress reporting, cancellation of an in-flight command, and any
  artifact lifecycle on the host. Note the teleport kinds already
  run long *inline* today — a multi-minute `session_handoff_pack`
  blocks the whole loop, a cost ADR-057 implicitly accepted for a
  rare, deliberate, user-initiated operation. Jobs arrive at browsing
  frequency, which is why detached execution is the line item here;
  migrating the teleport kinds onto the job executor afterwards is a
  natural follow-up, not a prerequisite.

## Decision

### 1. Jobs are allowlisted `host_commands` kinds — never a generic runner

Each job type is its own command kind with its own typed args, validated
host-side: `dataset_export_rrd`, `dataset_extract_episode` (§11), later
`dataset_digest_refresh` for oversized datasets. There is deliberately no
`job_run(argv)` kind: the command queue is reachable by anyone who can
write hub rows for a host, and a generic kind would turn it into a remote
exec surface. Adding a job type means adding a kind to the host-runner's
switch — the same review surface as `session_handoff_pack`.

Path-shaped args go through the same guards the synchronous verbs use
(`resolveDataPath` traversal checks on the fully-expanded string —
`datasetmeta` convention), and outputs may only be written under the job
cache root (§4).

### 2. Job kinds execute detached, with a single-flight guard

`runCommand` keeps handling classic kinds inline. Job kinds dispatch to a
job executor that runs the work in a goroutine and returns immediately, so
the poll tick keeps servicing pause/resume/capture/teleport. The executor
holds an in-memory map `command_id → {cancel func, started_at}` and a
per-host concurrency cap of **one running job** in v1. Backlog semantics
follow the shipped delivery contract: the list endpoint flips `pending →
delivered` **on read** (`handlers_commands.go:86-88`, `created_at`
order), so a job kind pulled while another runs cannot be "left pending"
— the executor accepts it as `delivered` and holds it in an in-memory
FIFO until the slot frees. A crash while queued is already covered by
the restart reconciliation in §3 (startup fails the runner's own
`delivered` job rows), so the in-memory queue adds no new loss mode.

The job body for `dataset_export_rrd`: probe the pinned
`(lerobot, rerun-sdk)` environment (capability probe, #394 soft-degrade —
a missing env fails the job with a typed, actionable error, it does not
half-run); run the exporter as a subprocess with a per-job ceiling
(default 30 min) enforced by context; write the .rrd under the job cache
root; report `{path, bytes, sha256}` in `result_json`.

### 3. Status and progress ride the command row; cancel is a command

- `host_commands` gains a nullable `progress_json` column (additive
  migration). The running job PATCHes it at least every 30 s — coarse
  `{phase, done, total}` — and that patch doubles as the liveness
  heartbeat. Statuses are unchanged (`delivered` = running for job kinds;
  `done`/`failed` terminal), so every existing consumer of the lifecycle
  keeps working; the lifecycle-flip sweep problem (task-board W2 lesson)
  is avoided by not flipping anything.
- Desktop-facing surface: submit is a dataset-scoped hub endpoint (e.g.
  `POST /teams/{team}/datasets/{id}/export` → team-scoped through the
  project, inserts the command row, returns the command id); status is a
  team-scoped read of that row (id, status, progress, result, error). The
  desktop polls; nothing holds a connection (the T1c class is structurally
  impossible here).
- `job_cancel` is a new classic (inline) kind whose args name the target
  command id; the executor calls the mapped cancel func, the job's own
  goroutine PATCHes `failed` with `error: "cancelled"`. Cancelling a job
  that already finished is a no-op success — idempotent, like `pause` on a
  dead pane.
- Restart semantics, v1: the in-memory map dies with the host-runner. On
  startup the runner lists its own `delivered` job-kind rows (the list
  endpoint already filters by status) and PATCHes them
  `failed / "lost: host-runner restarted"`. Callers resubmit. The hub
  additionally sweeps job rows whose progress heartbeat is older than
  5 minutes to `failed / "host stopped reporting"` — covers a host that
  died entirely. No persistence, no resume: an export is cheap to redo
  relative to the machinery resume would cost.

### 4. Artifacts stay on the host; fetch is a follow-up with a named pattern

Job outputs land under one host-runner-owned cache root
(`~/.termipod/hostrunner/jobcache/<kind>/…`) with LRU eviction (default
cap 20 GiB; in-flight and just-reported artifacts pinned). This is the
same cache §11's ffmpeg episode extraction needs — one LRU, two
consumers, which is the point of doing this outside the wedge.

The hub never stores job artifacts (blueprint data-ownership law: the hub
owns names/events/metadata, hosts own bytes — a multi-GB .rrd through hub
disk would break that and teleport's 25 MiB chunks were transient
relocation, not storage). v1 returns the host-local path, which fully
serves the local-first Replay consumer: the desktop hands
`result.path` to the W4a Rerun manager (`isRecordingPath` already
requires absolute + `.rrd`) exactly as it would a user-picked file. For
the remote case, fetch reuses teleport's chunked-manifest transport
(`chunkBundle` / `storeBlob`, ≤25 MiB parts) or a channel over the
now-landed SSH-forward/SFTP primitives (`be796b3e`) — decided in that
wedge, against patterns that already exist. Any bytes that do transit
the hub on that path are `class=derived` with a TTL per
[ADR-061](061-blob-lifetime.md) — transient relocation, never storage.

### 5. Explicitly out of scope

- Generic exec kind (§1).
- Job persistence, resume, retries, priorities, cross-host jobs, queues
  beyond the natural `created_at` order.
- Jobs as runs: runs are user-visible experiment records with metrics and
  Compare semantics; jobs are plumbing. No `runs` rows, no run events.
- Any hub-side artifact storage for jobs.

## Consequences

- W4b unblocks with a caller for W4a's so-far inert IPC handlers: Replay
  "Export to Rerun" → submit → poll progress → open local .rrd.
- §11 ffmpeg extraction lands later as just another kind + the shared LRU —
  no new mechanism.
- The 60s verb bound stays honest; nothing long ever rides it again.
- The host-runner poll loop gains its first detached execution path — the
  single-flight guard and startup reconciliation are new invariants tests
  must pin (job kind never runs inline; two submits never run
  concurrently; restart fails delivered job rows).
- Costs accepted: a lost host-runner loses running jobs (resubmit); one
  additive migration; the desktop learns one poll loop.

## References

- Code: `hub/internal/server/handlers_datasets.go:40` (60s bound),
  `hub/internal/server/handlers_commands.go` + `hub/migrations/0002` (queue),
  `hub/internal/hostrunner/runner.go:503` (serial inline tick),
  `hub/internal/hostrunner/teleport_commands.go` (chunked manifests),
  `hub/internal/server/handlers_blobs.go` (content-addressed store),
  `desktop/electron/src/rerun_policy.ts` (W4a consumer).
- Related: [ADR-057](057-session-teleport.md) (long-running command kinds,
  chunk transport, T1c timeout lesson), plan
  [`replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md)
  §11 + W4b, task #160.
