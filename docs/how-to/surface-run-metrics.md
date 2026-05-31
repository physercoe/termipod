# Surface training curves on mobile (trackio / wandb / TensorBoard)

> **Type:** how-to
> **Status:** Current (2026-05-31)
> **Audience:** testers, agents (stewards/workers), contributors
> **Last verified vs code:** v1.0.755

**TL;DR.** A run's loss/accuracy curves appear on the mobile **Runs**
tab as inline sparklines only when two things are true: the host-runner
polls the tracker's local store (trackio polling is **on by default**),
and the run row is **linked** to the tracker via `trackio_run_uri`
(+ host). The hub never stores bulk series — it holds a downsampled
digest, and the full interactive dashboard stays on-host (blueprint §4
data-ownership law). This guide explains the fields, the agent recipe,
and how to seed test data.

---

## What the link fields mean

A run is connected to its metrics by two columns (set at
`runs.create` or later with `runs.update`):

- **`trackio_run_uri`** — canonical form `trackio://<project>/<run_name>`.
  - **`<project>`** — the trackio *project* name. Trackio keeps one
    SQLite file per project at `<TRACKIO_DIR>/<project>.db` (default
    `TRACKIO_DIR` is `~/.cache/huggingface/trackio`). It is exactly the
    `project=` you pass to `trackio.init(...)`.
  - **`<run_name>`** — the run's name *within* that project: the
    `name=` in `trackio.init(project=..., name=...)`. It's stored in the
    trackio `metrics` table's `run_name` column, which the poller
    queries.
  - Other trackers: wandb uses `wandb://...`, TensorBoard uses
    `tb://<run-path>`.
- **`trackio_host_id`** — the **hub host id** of the machine where the
  worker logged (its trackio DB is on that host's local disk; the
  host-runner there is what reads it). **Optional:** if the run has an
  `agent_id` and you leave the host blank, the hub fills it from that
  agent's host. So an agent normally only needs to supply
  `trackio_run_uri`.

> The two are independent: `<project>`/`<run_name>` locate the data
> *inside* a host; `trackio_host_id` says *which* host. A wrong host id
> means the right machine never polls the run.

## Operator prerequisite (one-time)

The trackio poller is **on by default** — no flag needed. It resolves
trackio's own default dir (`$TRACKIO_DIR` → `~/.cache/huggingface/trackio`).
Override with `--trackio-dir <path>` or disable with `--no-trackio`
(see [install-host-runner.md](install-host-runner.md#the-trackio-metric-digest-poller-on-by-default-blueprint-65)).

**wandb and TensorBoard are opt-in.** Unlike trackio they have no
default location the host-runner can resolve reliably — their only
"defaults" are `$WANDB_DIR` / `$TENSORBOARD_LOGDIR`, which the
host-runner *daemon* usually does not inherit from the worker's shell.
So you must name the dir explicitly: `--wandb-dir <path>` (a wandb
offline-run root) or `--tb-dir <logdir>`. Once a dir is set, the rest of
this guide applies unchanged — link the run with the matching URI
scheme: `wandb://<run-dir>` or `tb://<run-path>` instead of
`trackio://<project>/<run_name>` (the run column `trackio_run_uri` holds
any of the three; the scheme picks the reader).

## Agent recipe (the normal path)

1. **Create the run.** `runs.create({project_id})` — and, if you
   already know the names, pass `trackio_run_uri` and `agent_id`.
2. **Log from the worker** using trackio as usual:
   ```python
   import trackio
   trackio.init(project="proj-a", name="run-1")
   for step in range(100):
       trackio.log({"loss": loss, "acc": acc}, step=step)
   ```
3. **Link the run** (if not set at create — common, because the run name
   is often chosen at `trackio.init` time):
   ```
   runs.update({ run: "<run_id>", trackio_run_uri: "trackio://proj-a/run-1" })
   ```
   Omit `trackio_host_id` — the hub derives it from the run's agent.
4. Within ~20 s the host-runner pushes the downsampled digest; the
   mobile Runs tab renders the sparkline. The full dashboard opens via
   the metric-URI link (external, on-host).

Typo'd a field? Fix it with `runs.update` — **no need to recreate the
run** (that was the old limitation).

## Seeding test data directly (for review without a real training job)

Trackio's store is a plain SQLite file with one table — you can write
rows directly, then link the run. Either approach works because the
poller only reads `metrics(run_name, step, metrics)`.

**Option A — via trackio (recommended; creates the file + schema):**
```python
import trackio
trackio.init(project="proj-a", name="smoke-1")
for s in range(50):
    trackio.log({"loss": 2.0 - s/50, "acc": s/50}, step=s)
```

**Option B — direct SQLite insert** (when trackio isn't installed). The
schema (per trackio's storage docs) is:
```sql
-- file: ~/.cache/huggingface/trackio/proj-a.db
CREATE TABLE IF NOT EXISTS metrics (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp TEXT,
  run_name  TEXT,
  step      INTEGER,
  metrics   TEXT  -- JSON, e.g. {"loss":1.23,"acc":0.7}
);
INSERT INTO metrics (timestamp, run_name, step, metrics) VALUES
  (datetime('now'), 'smoke-1', 0, '{"loss":2.0,"acc":0.0}'),
  (datetime('now'), 'smoke-1', 1, '{"loss":1.8,"acc":0.1}'),
  (datetime('now'), 'smoke-1', 2, '{"loss":1.6,"acc":0.2}');
```
Then link: `runs.update({ run: "<run_id>", trackio_run_uri: "trackio://proj-a/smoke-1" })`.
Only numeric JSON values render as curves; strings/arrays are skipped.

## Where it shows + why the hub holds so little

The host-runner reads the SQLite read-only, downsamples each scalar to
≤100 points, and PUTs the digest to the run. Mobile renders inline
sparklines (Runs tab) plus a launch-out link to the external dashboard.
Per blueprint §4 and [forbidden-patterns.md](../spine/forbidden-patterns.md)
(#9), **metrics live on the host**; the hub stores only the run's URI
and the small digest — never the bulk time-series.

## Troubleshooting "mobile shows nothing"

- **Run not linked** — `trackio_run_uri` empty on the run row. Set it
  with `runs.update`.
- **Wrong host** — `trackio_host_id` points at a host that doesn't have
  the DB. Clear it and let the agent-derivation fill it, or set the
  correct host.
- **Poller disabled** — someone passed `--no-trackio`, or the trackio
  dir is non-default and `--trackio-dir` wasn't set.
- **Name mismatch** — `<project>`/`<run_name>` in the URI must match the
  `project=`/`name=` the worker logged under, exactly.
- **No numeric metrics yet** — the worker hasn't logged, or only logged
  non-scalar values.
