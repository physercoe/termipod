# 060. Datasets as a hub entity; episode bytes stay on the host

> **Type:** decision
> **Status:** Proposed (2026-07-29) — records the entity model and the
> byte-residency/media posture that shipped as J8 W1–W5
> ([`plans/replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md),
> #446–#470 + the SFTP media follow-up `be796b3e`), extracted to decision
> tier while the plan is still In-progress: the decisions below are what
> the remaining wedges (W4b) and the sibling environments plan build on,
> and migration comments + a snapshot-bound plan are not a citable home
> for them. Nothing here changes shipped behaviour.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `589a01b0`
> (`hub/migrations/0068_datasets.up.sql`, `0069_runs_dataset.up.sql`,
> `hub/internal/server/handlers_datasets.go`,
> `desktop/electron/src/media_policy.ts`, `mediascheme.ts`,
> `desktop/electron/src/rerun_policy.ts`)

**TL;DR.** Embodied-AI data enters the product as **one new first-class
entity — `Dataset`** (project-scoped like runs, migration 0068) whose hub
row holds a **name, a location, and a folded digest — never bytes**.
**Episodes are deliberately not an entity**: the episodes table is proxied
from the host per request, windowed and capped, and an episode
materializes hub-side only when something references it. Bytes reach the
renderer exclusively through the **privileged `termipod-media://` scheme**,
whose two flavours — `file` (local disk) and `sftp` (ranged reads over a
live SSH session's SFTP channel) — share one allowlist posture and cannot
cross. Provenance is two cheap columns wired for the future:
**`run.dataset_id`** (0069, app-layer enforced, sniffed-but-never-auto-
written) and **`env_ref`** (opaque, unvalidated, reserved so provenance
accumulates before the Environment registry exists). External viewers
integrate as **one policy-registry row + a managed local process**
(`rerunweb`), never as a new surface; long host-side computations are
[ADR-058](058-host-job-surface.md) jobs. The blueprint §4 data-ownership
law (hub owns metadata, hosts own bytes; axiom A2) governs throughout —
this ADR records how J8 instantiates it, not the law itself.

## Context

Runs were first-class; the data they train on and roll out against was
not — nothing in the product could say "a dataset exists", let alone
open one. J8 added the Replay job over LeRobot-format datasets
(v2.1/v3.0): multi-camera video, proprioception/action series, language
annotations; datasets routinely reach 50k episodes and hundreds of GB,
and frequently live on a **remote** training host reachable only over
the user's SSH session.

Three prior decisions constrain the shape: the blueprint's A2 (bulk data
cannot flow through a bounded-VPS hub), ADR-038 (entity digests as the
hub-side summary shape), and ADR-050 (J-jobs are desktop-only). The
no-surprise-scans posture is inherited from the Inspect job: nothing
walks a user's disk or re-reads a dataset without an explicit act.

## Decision

### D-1 — `Dataset` is a hub entity: name + location + digest, never bytes

`datasets` (0068) is project-scoped like runs (team reached through the
project, no own `team_id`): `{id, project_id, host_id, root_path,
source local|sftp|hf, format, env_ref, digest_json +
digest_schema_version + digest_ts, fingerprint_json, timestamps}`.
Registration is an **explicit act** ("Open in Replay" / REST POST),
idempotent on `(project_id, host_id, root_path)` — never a crawler. The
digest is ADR-038-shaped, **computed host-side, stored hub-side**,
schema-versioned so a bump refolds instead of reinterpreting old bytes;
LeRobot's own `stats.json` is folded, not recomputed. Refresh is
**manual**, with a stat-only fingerprint (`meta/` files/bytes/mtime)
driving a "digest may be stale" indicator — the one-syscall honesty
pattern, because a v3.0 refold is not free and a background crawl is
exactly the surprise this design refuses.

### D-2 — Episodes are not materialized hub-side

A 50k-episode listing is bulk data wearing a metadata costume
(`handlers_datasets.go` header). The episodes table and per-episode
feature series are **proxied from the host on demand** — windowed,
capped, request/response under the 60-second dataset-verb bound — keyed
by `(dataset_id, episode_index)`. An episode becomes a hub row **only
when something references it** (an eval rollout, a J6 record): a
`robot.episode` element pointing at `(dataset, index)`, bytes on the
box. Anything that cannot fit the 60s bound is an
[ADR-058](058-host-job-surface.md) job, not a loosened verb.

### D-3 — Bytes reach the renderer only via `termipod-media://`, two flavours, one posture

Episode video never rides hub HTTP or renderer `fetch`. The privileged
`termipod-media://` protocol (registered on the default session only,
range-serving) has exactly two flavours:

- **`file`** — local absolute path, extension-allowlisted
  (`media_policy.ts`); `mediaPathOf` requires `host === 'file'` so no
  other flavour can alias into disk reads.
- **`sftp`** — `?s=<sshSessionId>&p=<posixAbsPath>`: ranged reads over
  the SFTP channel of a **live SSH terminal session the user already
  opened**. Parasitic by design: no stored credentials, no new network
  path, no daemon on the host — the media stream has exactly the
  user's session's lifetime and authority, and dies with it. Paths are
  posix-normalized before the same extension allowlist.

Flavours share one `rangedResponse` implementation and one allowlist
posture; adding a third flavour means adding a scheme host and its
policy function, reviewable in one file. The player's remote source is
a **persisted connection id per dataset** resolved to a live session at
render time — a dead connection degrades to a picker, never to a
credential prompt.

### D-4 — Provenance is columns wired before their consumers exist

- **`run.dataset_id`** (0069): plain TEXT, no FK (SQLite ALTER
  limitation, 0005 precedent), enforced at the application layer —
  `handleUpdateRun` refuses cross-project links, dataset delete clears
  the column. The config sniff (`run_dataset_hint.go`) **only ever
  proposes**; the write is an explicit act, because a wrong edge sends
  someone to watch the wrong robot.
- **`env_ref`** — opaque `"family:env_id@version"`, nullable-by-default
  and deliberately unvalidated, on runs, datasets and the episode
  element. The Environment registry is a sibling plan
  ([`plans/environments-and-embodiments.md`](../plans/environments-and-embodiments.md));
  reserving the field now means provenance later **resolves** into rows
  instead of being backfilled by guesswork.

### D-5 — External viewers are a registry row + a managed process, not a surface

The Rerun viewer integrates as: one `rerunweb` partition row in
`webtab_policy.ts` — **deliberately identical to `kimiweb`**
(non-persistent, loopback-pinned, bridge-read-only), so "another web UI
is one registry row" cannot quietly widen policy — plus a desktop-owned
launch policy (`rerun_policy.ts`: pinned binary discovery, loopback
`--bind`, absolute-`.rrd`-only `isRecordingPath`). No viewer code is
vendored, no `.rrd` writer is bespoke (plan §11 decision 3): export
integrates LeRobot's own exporter host-side under a pinned
`(lerobot, rerun-sdk)` pair, as an ADR-058 job kind.

## Consequences

- The hub stays byte-free for embodied data: worst case per dataset is
  one digest JSON. What W4b-2's remote `.rrd` fetch does about transient
  bytes is being decided in the in-flight blob-lifetime ADR — that
  decision composes with this one (bytes in the blob store are a
  *sanctioned exception with a deadline*, never dataset storage).
- A remote dataset is exactly as reachable as the user's own SSH access
  to it — no new trust anchor, but also: no live session, no video.
  The picker states this honestly rather than auto-connecting.
- `source: 'sftp'` datasets whose host has no host-runner return an
  honest 501 for digest/series (parquet decode stays host-side, and a
  TS re-implementation was rejected); video still works (D-3 needs only
  SFTP). Recorded, not scheduled.
- Registration idempotency means "Open in Replay" is safe to mash; the
  cost is that moving a dataset root creates a new identity — accepted,
  a moved root *is* a different location claim.
- `env_ref` accumulates unvalidated strings until the registry lands —
  by design; consumers must treat it as a hint until then.

## Alternatives considered

- **Hub-materialized episode index.** Rejected (D-2): 50k rows × N
  datasets of derivable data on a bounded VPS, permanently stale vs the
  host's parquet. The host proxies; the hub stores what it owns.
- **A dataset crawler / auto-registration.** Rejected (D-1):
  no-surprise-scans. Walking a researcher's disk uninvited is how tools
  get uninstalled.
- **Client byte-range slicing of v3.0 concatenated mp4** (plan §11
  decision 1). Rejected: `<video>` needs a valid container at episode
  boundaries; host-side `ffmpeg -c copy` extraction with an LRU cache
  keeps the media protocol dumb.
- **Port-forward for remote video** (serve media over the SSH-forward
  wedge). Rejected: nothing on the training host serves media on a
  port; SFTP ranged reads need zero host-side setup and inherit the
  session's exact authority.
- **Bespoke `.rrd` writer in TS.** Rejected (plan §11 decision 3): a
  custom writer inherits the SDK↔viewer lock-step problem twice over.
- **A separate `episodes` hub table for provenance.** Rejected: the
  element system already models "a thing something referenced";
  episodes ride it as `robot.episode` instead of adding an entity whose
  unreferenced rows would be D-2's rejected index by another name.

## References

- Code: `hub/migrations/0068_datasets.up.sql` (entity + identity index)
  · `hub/migrations/0069_runs_dataset.up.sql` (provenance edge) ·
  `hub/internal/server/handlers_datasets.go` (60s verbs, windowed
  proxying) · `hub/internal/hostrunner/datasetmeta/` (host-side reader)
  · `desktop/electron/src/media_policy.ts` + `mediascheme.ts` (flavours,
  allowlist, ranged serving) · `desktop/electron/src/ipc/ssh.ts`
  (`openSftpMedia`) · `desktop/electron/src/rerun_policy.ts` +
  `desktop/electron/src/webtab_policy.ts` (D-5)
- ADRs: [038](038-per-run-event-digest.md) (digest shape) ·
  [050](050-desktop-workbench-delivery-model.md) (desktop-only J-jobs) ·
  [058](058-host-job-surface.md) (long host computations) ·
  [`spine/blueprint.md`](../spine/blueprint.md) §4 (the
  data-ownership law; axiom A2)
- Plans: [`plans/replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md)
  (§3 model, §11 decisions) ·
  [`plans/environments-and-embodiments.md`](../plans/environments-and-embodiments.md)
  (`env_ref` consumer)
