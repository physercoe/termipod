# 061. Blob lifetime — a declared class per blob, expiry-only deletion

> **Type:** decision
> **Status:** Accepted (2026-07-30, director) · Amended 2026-07-30 (adds D-8,
> the backup path; re-anchors the standing justification on D-4 — see below) —
> occasioned by
> [`plans/replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md) §7
> (W4b-2), which would be the first browsing-frequency writer of large bytes
> into the blob store. **The load-bearing justification is D-4, not W4b-2**:
> ADR-058 §4 keeps job artifacts host-local and the SFTP flavour (`be796b3e`)
> already serves remote bytes without the hub, so W4b-2 may never write a blob
> at all — whereas teleport's handoff parts are unreferenced permanent garbage
> *today*, which is a shipped problem this ADR fixes. Amends
> [ADR-057](057-session-teleport.md) D-3, which accepted that those parts
> "linger" and deferred a blob GC path to T3. Companion to
> [ADR-058](058-host-job-surface.md): this ADR is the lifetime half of 058 §4's
> deferred remote-fetch decision.
> **Audience:** contributors · maintainers
> **Last verified vs code:** origin/main `b4e901f0` (as accepted) · **built
> 2026-07-30**: migration 0071 (`class`, `expires_at`, the partial index),
> `storeBlob`'s class/TTL parameters + D-5 collision upsert, the expiry sweeper
> (`blob_sweep.go`), teleport parts flipped to `derived` with a 24h TTL, and
> D-8's backup exclusion. D-6's byte cap remains deferred, as written.

**TL;DR.** The hub's blob store has no DELETE, no TTL, no owner column and no
team column: everything ever written to it is permanent, and at least four
reference *shapes* point into it, only one of which is a foreign key. That was
survivable while every writer was either small (a 64 KiB event payload) or rare
(a teleport bundle). It stops being survivable at the first writer that is
large *and* frequent *and* reproducible — a per-episode `.rrd` export. This ADR
adds a **declared class** to each blob (`owned` — someone else's row is the
referrer, lifetime unchanged; `derived` — reproducible, carries `expires_at`), a
sweeper that deletes **expired `derived` blobs only**, and the rule that on a
content-address collision the **longest lifetime wins**. There is deliberately
**no DELETE endpoint** and deliberately **no reference-counting GC**: the class
is declared by the writer, never inferred, and `owned` blobs keep exactly
today's semantics.

## Context

### What the store is today

`blobs` (migration `0001_initial.up.sql:205-211`):

```sql
CREATE TABLE blobs (
    sha256     TEXT PRIMARY KEY,
    scope_path TEXT NOT NULL,
    size       INTEGER NOT NULL,
    mime       TEXT NOT NULL,
    created_at TEXT NOT NULL
);
```

- **Content-addressed and deduped.** `storeBlob` writes bytes to
  `<DataRoot>/blobs/<aa>/<bb>/<sha>`, skipping the write if the file exists,
  and records the row with `INSERT OR IGNORE` (`handlers_blobs.go:32-54`).
  Same bytes from two unrelated callers are **one row and one file**.
- **25 MiB per blob**, whole body buffered (`maxBlobBytes`,
  `handlers_blobs.go:21`; `io.ReadAll(http.MaxBytesReader(…))`, line 61).
- **No `team_id`.** Not merely team-global as ADR-057 D-3 put it — there is no
  team scoping at all, and `GET /v1/blobs/{sha}` is mounted outside the team
  route (`server.go:868`; the desktop client's own comment reads "NOT
  team-scoped by path", `client.ts:762`). Access is capability-style: knowing
  an unguessable sha is the authorization.
- **Nothing deletes.** `command grep -rn "DELETE FROM blobs"` over `hub/`
  returns nothing; there is no TTL column, no sweeper, no admin path.
  `handleGetBlob` reads bytes from the row's stored `scope_path`, not from a
  recomputed `blobPath(sha)` (`handlers_blobs.go:82-93`) — so that column is
  load-bearing for reads today.

### At least four reference shapes, one foreign key

Any deletion policy has to know who points at a blob. The four below are
the load-bearing ones; the count is a floor, not a census (see the note
after the table).

| # | Referrer | Shape | FK? |
|---|---|---|---|
| 1 | `run_images.blob_sha` | column | **yes** — `REFERENCES blobs(sha256)` (`0017_run_images.up.sql:23`) |
| 2 | `artifacts.uri` / `artifacts.sha256` | `'blob:sha256/<hex>'` text (`0019_artifacts.up.sql:24-25`) | no |
| 3 | externalized agent-event payloads | `blob:sha256/<sha>` **inside** the payload JSON, replacing any string leaf over 64 KiB (`payload_externalize.go:33,38,80`) | no |
| 4 | teleport handoff parts + manifest | `manifest_sha` inside `host_commands.args_json` / `result_json` (`handlers_teleport.go:254`) | no |

The enumeration is deliberately open-ended: `payload_externalize.go`'s
own comment names **A2A file parts and document references** as further
writers of the `blob:sha256/` scheme (the reference-library and artifact
attach tools in `native_tools.go` / `hubmcpserver/tools.go` record such
URIs wherever an agent cites one), and `mcp_more.go:549,617` accepts a
**`hub-blob://<hex>` alias** spelling that any future reference walk
must also know. Under this ADR unknown referrers are **safe by
construction** — `owned`-by-default, no inference, no refcount — and are
fatal only to the reference-walking GC this ADR rejects. That asymmetry
is much of the argument.

Shape 3 is the reason a naive "delete blobs nobody references" sweeper is
unsafe: the referrer is a substring buried at arbitrary depth in an opaque JSON
column, and getting the walk wrong silently destroys transcript content that
exists nowhere else.

Shape 4 is worse in a different way, and it is already true in shipped code:
those blobs have **no durable referrer at all**. The manifest sha lives only in
`host_commands` rows, and host commands cascade-delete with their host
(`handlers_hosts.go:267`). A teleport's parts become unreachable garbage the
moment that row goes — permanent bytes with nothing pointing at them.

### What forces the question now

ADR-057 D-3 priced the linger honestly and deferred the fix: *"a blob-GC /
DELETE path is deferred (T3), noted here so the omission is deliberate, not
forgotten."* That price was set for teleport's access pattern — a rare,
deliberate, user-initiated move, one bundle each.

J8 W4b-2 changes the pattern, not the mechanism. The surface is a **paged
episodes table where clicking a row is the cheap action**, each export is a
multi-camera episode decoded end to end (tens of megabytes), and the transport
would be the same chunked-manifest path. Browsing thirty episodes writes
roughly a gigabyte of permanently undeletable bytes into a hub whose own
dataset code states the opposite rule about this very feature: *"The episodes
table is not stored at all. It is proxied per request, windowed and capped,
because a 50k-episode listing is bulk data and bulk data does not live on the
hub (blueprint §4)"* (`handlers_datasets.go:17-25`).

There is also a new *kind* of object here. Every blob written today is either
irreplaceable (an event payload, an uploaded image, a run figure) or cheap. A
`.rrd` is the first that is **large and regenerable** — losing one costs a
re-export, not data. The store has no way to say that, which is the actual gap.

## Decision

### D-1 — Every blob has a declared class; the class decides lifetime

Add to `blobs`:

```sql
ALTER TABLE blobs ADD COLUMN class      TEXT NOT NULL DEFAULT 'owned';
ALTER TABLE blobs ADD COLUMN expires_at TEXT;  -- NULL = never
CREATE INDEX idx_blobs_expiring ON blobs(expires_at) WHERE expires_at IS NOT NULL;
```

Two classes, and only two:

- **`owned`** — some other row is the referrer (shapes 1–3 above). Lifetime is
  **exactly today's**: permanent, deleted only if a future ADR builds a
  reference-graph GC. `DEFAULT 'owned'` means the migration is semantically a
  no-op for every existing blob and every existing writer.
- **`derived`** — reproducible from inputs the hub still has (a dataset id + an
  episode index; a session's engine-state, post-flip). Carries a non-NULL
  `expires_at`. Losing it is a cache miss.

The partial index is what keeps the sweeper's scan proportional to the number
of expiring blobs rather than to the store.

### D-2 — The class is declared by the writer, never inferred

`POST /v1/blobs?class=derived&ttl_seconds=<n>` (absent `class` ⇒ `owned`, so
every existing caller is unchanged). `storeBlob` takes the class and TTL as
parameters.

Inference is rejected outright — not deferred. Any rule of the form "large, or
this mime, or from this endpoint ⇒ cacheable" is a rule that eventually
classifies an irreplaceable event payload as disposable, and the failure is
silent and delayed. A writer knows whether it can regenerate its bytes; the
store cannot know.

A `ttl_seconds` ceiling belongs in config with a conservative default (7 days
is the right order for an export cache); a `derived` upload with no TTL is a
**400**, not an implicit forever.

### D-3 — Deletion is by expiry only. There is no DELETE endpoint

A periodic sweeper deletes rows where `class='derived' AND expires_at < now`,
and the file with each row. It follows `runStoreMaintenance`
(`store_maintenance.go:58-70`, ADR-045 D4) as its pattern — a loop with an
env-overridable interval and a disable flag — rather than inventing a scheduler.

Note what that neighbour already tells us: freed sqlite pages return to the OS
only via incremental auto-vacuum, and `blobs` lives in `hub.db`, where
`auto_vacuum=NONE` makes `incremental_vacuum` "a documented no-op"
(`store_maintenance.go:28-31`). So deleting blob **rows** will not shrink
`hub.db`; the freelist absorbs them. That is fine, and worth saying so nobody
looks for a size drop: the reclamation that matters here is the **file** unlink,
and a blob row is tiny next to the bytes it names.

No public `DELETE /v1/blobs/{sha}` — and this is a safety property, not an
omission. The store is **deduped by content address**, so a caller who deletes
"their" blob may be deleting the only copy of bytes another row owns: an
uploaded image and an exported artifact that happen to be byte-identical are
one row. Expiry-only deletion makes that class of accident unreachable.

Three implementation constraints, all from code that already exists:

- The sweeper must delete the file at **`blobPath(sha)`**, recomputed, and not
  at the row's stored `scope_path`. That column holds an absolute path captured
  at write time; if `DataRoot` ever moved, it names something else. (Reads
  trust it today — `handlers_blobs.go:82-93` — which is a separate pre-existing
  fragility this ADR notes but does not fix.)
- Delete the **row first, then the file**, and tolerate a missing file. The
  reverse order leaves a row promising bytes that are gone, which reads as
  corruption; this order leaves at worst an orphan file, which the next write
  of the same content silently reuses.
- **The sweeper and `storeBlob` must serialize per sha.** `storeBlob` is
  stat-file → skip-write → `INSERT OR IGNORE` (`handlers_blobs.go:32-54`);
  a re-upload of identical bytes concurrent with the sweep of that same
  sha — which is exactly the re-export-after-TTL path this ADR's first
  consumer creates — can see the file, skip the write, and insert its row
  after the sweeper has unlinked the bytes: a row promising bytes that are
  gone, the precise symptom the previous bullet's ordering exists to
  prevent. Serialize them (one lock, or a delete transaction that the
  writer's upsert excludes), or have the writer re-stat **after** its row
  upsert and rewrite the file if it vanished. A grace window alone does
  not close this: the uploader refreshes `expires_at` (D-5) only after
  its file check, so the row can still be expiry-eligible mid-write.

One read-side semantic, stated so callers don't invent it: **expiry marks
sweep eligibility, not read refusal** — an expired-but-not-yet-swept
`derived` blob still serves on GET, and a caller sees 404 only once the
sweeper has actually run. Refusing reads at `expires_at` would buy nothing
(the bytes are still there) and would turn sweep cadence into user-visible
behaviour.

### D-4 — Teleport handoff parts become `derived` (amends ADR-057 D-3)

The pack/unpack parts and manifest upload as `class=derived` with a short
TTL — where "short" has a floor named by the mechanism itself: unpack
polls for up to 15 minutes (`handlers_teleport.go:35-36`) and
resume-on-source can stretch a retry past one poll window, so the TTL
must comfortably exceed both. **24 hours** is the right order — it costs
nothing (the linger today is *forever*) and no legitimate reader exists
beyond the teleport that wrote them. This is not a concession — it is
what they always were. After the row flip the
target has untarred them; before it, the rollback is resume-on-source. Nothing
reads a part again, and nothing can: the only referrer is a `host_commands` row
that cascade-deletes.

This closes the specific hole ADR-057 D-3 recorded and deferred, and it
converts shape 4 from "permanent bytes with no referrer" into "cache entries
with a deadline."

### D-5 — On a content-address collision, the longest lifetime wins

The trap that makes this ADR non-trivial. `INSERT OR IGNORE` on a sha that
already exists is today a silent no-op. With classes it must not be:

- Existing `owned`, new `derived` ⇒ **stays `owned`, `expires_at` stays NULL.**
  Never demote.
- Existing `derived`, new `owned` ⇒ **promote to `owned`, clear `expires_at`.**
- Both `derived` ⇒ `expires_at = max(existing, new)`.

Without this rule, an event payload that happens to be byte-identical to a
cached export gets swept when the export's TTL lapses — a data-loss bug whose
trigger is a hash collision in the *intended* sense (same bytes), so it will
happen exactly when two features legitimately produce the same content, and it
will look like corruption rather than expiry.

### D-6 — TTL bounds staleness, not disk. A byte cap is deferred, and named

Expiry alone does not bound the store: a user browsing fast enough writes
faster than the sweeper reclaims. A per-class byte budget with oldest-first
eviction is the actual bound, and it is **deferred** — TTL first because it is
correct and small; the cap when a real disk-pressure report exists. Recorded
here so the limitation is deliberate: after this ADR, `derived` growth is
bounded in *time*, not in *bytes*.

### D-7 — The hub is still not the place for these bytes

This ADR makes the remote path *survivable*; it does not make it the default.
J8 W4b-1 (host and desktop are the same machine ⇒ the export never leaves the
host, the desktop opens the path) stays the preferred route, and remains the
route for the overwhelmingly common single-box research setup. The blob path is
for the genuinely-remote case, where the alternative is not "fewer bytes" but
"the feature does not work."

Stated plainly because the data-ownership law is only load-bearing if a
sanctioned exception stays an exception.

### D-8 — The backup path must learn that `blobs/` mutates (amendment, 2026-07-30)

Found after acceptance, and it is the one place where introducing deletion
breaks working code rather than merely leaving something undone.

`Backup` raw-copies the `blobs/` tree, justified in its own comment as
*"Both are raw-copyable (immutable / rarely written)"* (`backup.go:91-94`).
This ADR's sweeper ends that premise. Two consequences, of different
severity:

1. **A live backup can fail outright.** `addDir` walks with
   `filepath.WalkDir`, which reads a directory and *then* stats each entry,
   and its callback returned any error unconditionally — so a blob unlinked
   mid-walk (ENOENT at the stat, or at `addFile`'s open) failed the whole
   archive. One expired cache blob at the wrong moment would take down an
   operator's backup, with a message pointing at a file that no longer
   exists. **Fixed ahead of the sweeper**: `addDir` and `addFile` now skip
   `fs.ErrNotExist` and only that — a permission or I/O error still fails
   the backup, because "some bytes were unreadable" must never be downgraded
   to a successful archive. Truncation mid-copy needs no handling: a blob
   file is written once under its content address and never rewritten in
   place (`storeBlob`, `handlers_blobs.go:32-54`).
2. **`derived` blobs get archived, where no sweeper can reach them.** An
   archive is a place expiry does not run, so cache bytes captured inside
   one are permanent again — the exact property this ADR exists to remove,
   reintroduced through the back door. The sweep window is where it bites:
   a teleport of a large engine-state is `derived` for 24 hours (D-4), and
   every backup taken in that window carries its parts. **Obligation on the
   implementation**, not yet dischargeable: once `class` exists, the backup
   walk must skip `class='derived'`. They are reproducible by definition, so
   excluding them costs nothing and shrinks archives. Marked with a
   `TODO(ADR-061 D-8)` at the call site so it is found by the migration that
   makes it possible.

The general lesson, worth more than the fix: **a store that acquires
deletion invalidates every consumer that assumed append-only.** `blobs/` had
two such consumers — `backup.go`'s raw copy, and `payload_externalize.go`'s
promise that externalized transcript leaves are "durable and in backup.go"
(`payload_externalize.go:23`). The second is the reason D-1 makes `owned`
the default rather than something a writer opts into: a transcript payload
must not become disposable by omission.

## Consequences

- **Existing behaviour is untouched.** `DEFAULT 'owned'` plus "absent `class` ⇒
  `owned`" means every current writer, row and blob keeps today's semantics.
  Nothing that exists becomes deletable.
- **New surface is one migration, two query params, one sweeper.** No new
  endpoint, no blob-store restructuring, no change to the four reference
  shapes.
- **ADR-057 D-3's deferred T3 blob-GC item is partly satisfied** (D-4) and
  partly still open: `owned` blobs remain permanent by design.
- **Still forbidden, explicitly:** reference-counting GC over shapes 2–3. It
  needs a walk of `artifacts.uri` plus every externalized payload leaf, and a
  wrong walk destroys transcript content. That is a separate ADR with its own
  evidence, not a corollary of this one.
- **Blob reads can now 404 where they previously could not.** By construction
  only for `derived` blobs, which are reproducible by definition — so the
  caller's recovery is "ask for the export again." Callers of `derived` blobs
  must treat 404 as *stale cache*, not as an error; that obligation is the cost
  of the class.
- **Team scoping is still absent** and this ADR does not add it. Retention and
  isolation are separate questions, and conflating them would have made a small
  migration into a large one; noted so the omission is visible.

## Alternatives considered

- **Infer the class from mime, size, or endpoint.** Rejected — D-2. The
  failure mode is silently classifying irreplaceable bytes as disposable, and
  it surfaces a TTL later, far from the cause.
- **A public `DELETE /v1/blobs/{sha}`.** Rejected — D-3. Content addressing
  plus dedup means one caller's delete can destroy another owner's only copy.
  Expiry-only removes the whole class of accident, and no caller has asked for
  explicit deletion.
- **Reference-counted GC now, no class column.** Rejected: of the known
  reference shapes only one is a foreign key, one is a substring inside
  opaque JSON, and the enumeration itself is a floor (two spellings, plus
  writers the walk would have to discover). A refcount built on that is a
  data-loss bug waiting for an edge case, and it is a strictly larger job
  than the wedge that forced the question.
- **Keep the export desktop-side only (W4b-1) and never route `.rrd` through
  the hub.** Adopted as the *default* (D-7) but rejected as the whole answer:
  remote datasets are the case the SSH-forward wedge exists for, and a
  local-only export has to be rewritten the moment it lands.
- **A separate cache table instead of a column on `blobs`.** Rejected: two
  stores addressing the same content would each need the collision rule from
  D-5 *and* a cross-store version of it, and the dedup that makes the store
  cheap would break across the boundary.

## References

- Code: `hub/internal/server/handlers_blobs.go` (store, cap, read path) ·
  `hub/migrations/0001_initial.up.sql:205-211` (schema) ·
  `hub/internal/server/payload_externalize.go` (shape 3) ·
  `hub/migrations/0017_run_images.up.sql:23` (shape 1, the only FK) ·
  `hub/migrations/0019_artifacts.up.sql:24-25` (shape 2) ·
  `hub/internal/handoff/transport.go` + `hub/internal/hostrunner/teleport_commands.go`
  (shape 4) · `hub/internal/server/server.go:868` (route, un-team-scoped)
- ADRs: [057](057-session-teleport.md) D-3 (amended by D-4) ·
  [058](058-host-job-surface.md) §4 (the host-local artifact default this
  ADR's remote path is the sanctioned exception to) ·
  [045](045-hub-storage-scaling.md) D4 (`store_maintenance.go` — the loop
  pattern D-3 follows, and the reason row deletes don't shrink `hub.db`) ·
  [`spine/blueprint.md`](../spine/blueprint.md) §4 (hub owns metadata, hosts
  own bytes — the law D-7 keeps load-bearing)
- Plan: [`plans/replay-datasets-episodes.md`](../plans/replay-datasets-episodes.md)
  §7 (W4b-2, the forcing function)
