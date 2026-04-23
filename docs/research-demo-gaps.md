# Research-Demo Gaps

The MVP (`blueprint.md` §9 Phase 4) is the end-to-end **research demo**:

> user writes a directive → steward decomposes → fleet executes runs across
> hosts → briefing agent summarizes overnight → user reviews on phone

Everything in this document is scoped against that demo. Items here are the
delta between *what's shipped* and *what a user needs to run the demo from
their phone end-to-end*. If a feature doesn't advance this demo, it belongs
in `blueprint.md` §10 (Non-goals) or gets deferred — not listed here.

## Status

**Shipped (P0–P2, most of P3):**
- Projects / plans / plan_steps / schedules / runs / documents / reviews schema
- Multi-mode host-runner (ACP, stdio, pane)
- Plan-step executor (llm_call, shell, mcp_call, human_decision, agent_spawn)
- Hub MCP server + host-runner MCP gateway
- AG-UI broker + SSE stream + structured input
- Mobile AgentFeed, AgentCompose, plan viewer, workflows tab, triage (Inbox),
  project / task / plan / run / review detail screens, templates browser
- Team settings: schedules, budgets, audit log

## Demo-blocker gaps

### P4.1 — built-in project templates — **PARTIAL v1.0.138**

Seed path added: `seedBuiltinProjectTemplates` in
`hub/internal/server/init.go` inserts `is_template=1` project rows on first
init (idempotent via `INSERT OR IGNORE`). The `ablation-sweep` row ships
with `parameters_json = {model_sizes, optimizers, iters}` and
`on_create_template_id = agents.steward`, matching the locked demo choice.

Still open:
- Ship `reproduce-paper`, `write-memo`, `benchmark-comparison` templates if
  the demo ever needs more than one entry point.
- Project-create picker does not yet render `parameters_json` as an input
  form — selecting the ablation-sweep template still doesn't prompt for
  parameter values. Lands with the mobile parameter-form wedge.

### Mobile project-template picker — **DONE v1.0.134**

Fixed in commit `aff41c1`. `_TemplatePickerSheet` now calls
`listProjects(isTemplate: true)` and returns a project-row id, which is
what `project.template_id` expects per blueprint §6.1. Server handler
`GET /v1/teams/{team}/projects?is_template=true|false` is the new filter.

Still open: the picker does not yet render `parameters_json` as an input
form — selecting a template does not prompt the user for parameter values.
That lands with the P4.1 seed wedge (when the first template with
parameters exists to drive the UI).

### P4.2 — steward decomposition recipe — **DONE v1.0.137**

`hub/templates/prompts/steward.v1.md` now carries a concrete
"Decomposition recipe: ablation sweep" section that names the exact MCP
tool sequence (`plan.instantiate` → `a2a.invoke` × N on `worker.ml` →
collect → hand off to briefing). `plan.instantiate`, `plan.advance`, and
`a2a.invoke` are listed in the steward's tool set.

Still open: plan outlines stored as data (rather than inline in the
prompt). For the single demo template this is fine; revisit if more
templates land.

### P4.3 — briefing agent + overnight schedule — **PARTIAL v1.0.137**

Templates shipped:
- `hub/templates/agents/briefing.v1.yaml` — writer role, docs + reviews
  capabilities, spawn.descendants=0.
- `hub/templates/prompts/briefing.v1.md` — runbook writes a Goal / What
  ran / Plot / Takeaway / Caveats doc, calls `documents.create` +
  `reviews.request` so it surfaces in the mobile Inbox.
- Also shipped: `hub/templates/agents/ml-worker.v1.yaml` +
  `ml-worker.v1.md` — GPU-host worker that executes one A2A
  `train(config)` task, writes a `runs` row, attaches a trackio URI.

Still open:
- Overnight cron schedule is not auto-seeded on project creation. The
  steward spawns the briefing agent directly via `agents.spawn` at
  end-of-sweep for now.

## Degrades-demo gaps

### P3.1 — trackio host-runner consumption

Schema fields `runs.trackio_host_id` and `runs.trackio_run_uri` exist, and
`POST /v1/teams/.../runs/<id>/metric_uri` is wired.

**Progress:**
- P3.1a — hub metric-digest storage — **DONE v1.0.141**. Migration 0014
  adds `run_metrics(id, run_id, metric_name, points_json, sample_count,
  last_step, last_value, updated_at)` with UNIQUE(run_id, metric_name).
  `PUT /v1/teams/{team}/runs/{run}/metrics` atomically replaces a run's
  full digest set; `GET .../metrics` returns rows for sparkline rendering.
  Bodies are ≤~64 KiB per row — the poller splits metric families across
  rows. Hub stores digests only (blueprint §4 data-ownership law) — bulk
  time-series stay on the host.
- P3.1b — host-runner trackio poller — **DONE v1.0.142**. New package
  `hub/internal/hostrunner/trackio/` reads trackio's local SQLite store
  directly (one `{project}.db` per project, metrics live as JSON blobs
  keyed by step). Host-runner grows a `--trackio-dir` flag, a 20s poll
  loop, and uses `GET /v1/teams/{team}/runs?trackio_host=<host>` to list
  runs it should scrape. Each run's series is downsampled to ≤100 points
  (uniform stride, endpoints preserved) and PUT to the hub digest
  endpoint. `runs.trackio_run_uri` is canonicalised as
  `trackio://<project>/<run_name>`. **v1.0.144** ships the parallel
  wandb offline-mode reader at `hub/internal/hostrunner/wandb/`, a
  `--wandb-dir` flag, and an independent 20s poll loop keyed off the
  `wandb://<project>/<run-dir>` URI scheme. **v1.0.145** ships the
  TensorBoard tfevents reader at `hub/internal/hostrunner/tbreader/`
  (minimal hand-rolled TFRecord + protobuf decoder, no
  `google.golang.org/protobuf` or tensorflow/tensorboard deps), a
  `--tb-dir` flag, and a third 20s poll loop keyed off `tb://<run-path>`.
  All three readers feed the same digest endpoint; a host may enable
  any combination. **v1.0.146** unifies them under a shared
  `metrics.Reader` interface (`Scheme() string`, `Read(ctx, uri) (map[string]Series, error)`)
  at `hub/internal/hostrunner/metrics/`. The poll loop in
  `metrics_poll.go` has zero per-vendor branching — it lists host runs,
  filters by `reader.Scheme() + "://"`, and PUTs digests. Downsampling
  moved to `metrics.Downsample`. New backends just implement the two
  methods and plug in at runner startup.
- P3.1c — mobile sparkline UI reading the digest — **DONE v1.0.147**.
  `HubClient.getRunMetrics(runId)` pulls
  `GET /v1/teams/{team}/runs/{run}/metrics`; `RunDetailScreen` renders
  one row per metric — name, headline `last_value`, a hand-painted
  `CustomPainter` sparkline, and `step N · M samples`. Digest-only
  surface per blueprint §4 (bulk series stay on host); users chase full
  curves via the "Metric dashboards" link below.

### P3.2–3.4 — cross-host A2A (MVP-critical per 2026-04-23 lock)

Architecture decision locked 2026-04-23: multi-host is required for the
MVP demo (steward on VPS, worker on GPU host). A2A is no longer deferrable.

**Progress:**
- P3.2a — host-runner A2A server serves agent-cards — **DONE v1.0.133**
  (commit `30ca8ce`). New `--a2a-addr` / `--a2a-public-url` flags on
  host-runner. Card at `/a2a/<agent-id>/.well-known/agent.json` per
  A2A v0.3.
- P3.3a — hub A2A directory (register cards across hosts) — **DONE v1.0.139**.
  `PUT /v1/teams/{team}/hosts/{host}/a2a/cards` (host-runner push, replaces
  the whole set atomically) + `GET /v1/teams/{team}/a2a/cards?handle=...`
  (steward lookup). Host-runner pushes every 30s (change-hashed) whenever
  `--a2a-addr` is set. Table `a2a_cards` (migration 0013).
- P3.3b — reverse-tunnel relay on the hub — **DONE v1.0.140**. Hub exposes
  `ANY /a2a/relay/{host}/{agent}/*` (unauthed per A2A v0.3 peer spec),
  plus two host-runner endpoints: `GET /v1/teams/{team}/hosts/{host}/a2a/tunnel/next`
  (long-poll, ≤25s) and `POST .../tunnel/responses`. In-memory broker
  `TunnelManager` routes req/resp envelopes between them. Host-runner
  `a2a.RunTunnel` dispatches received envelopes through the local
  `a2a.Server.Handler()` so relayed calls hit the exact same routes a
  direct peer would. NAT'd hosts can now receive A2A calls end-to-end.
  Follow-ups tracked: A2A peer auth (per-agent tokens) is still open.
- P3.3c — card URL rewrite to relay — **DONE v1.0.148**. The directory
  now rewrites each card's `url` field to
  `<hub_public_url>/a2a/relay/<host>/<agent>` at list time, so off-box
  peers dial the hub relay instead of the NAT'd host-runner URL the host
  pushed. Hub gains `--public-url` / `Config.PublicURL`; falls back to
  the request Host header when unset (fine for single-host dev, brittle
  when the directory is scraped remotely).
- P3.2b — A2A task endpoints (send / get / cancel) — **PARTIAL v1.0.143**.
  JSON-RPC 2.0 handler for `message/send`, `tasks/get`, `tasks/cancel`
  at the agent URL root (`POST /a2a/<agent-id>`). In-memory `TaskStore`
  keeps per-agent state with terminal-state freeze so a late completion
  after a cancel can't flip state back. `Dispatcher` is an interface
  with `NoopDispatcher` as the default. Still open: concrete
  dispatcher that delivers the submitted message into the agent's
  `InputRouter` (producer="a2a") and harvests the reply — lands with
  runner integration after the parallel poller agents merge.
- P3.4 — cross-host A2A smoke (two host-runners under one hub) — OPEN.

Plus AG-UI `a2a.invoke` / `a2a.response` event kinds surfaced on the
calling agent's stream (§5.4).

## Deferrable (not demo-blocking)

- **iOS TestFlight / App Store distribution.** Android APK is sufficient
  for the demo; TestFlight is a distribution follow-up.

## Explicitly off-roadmap

These were previously listed as roadmap items but do not advance the
research demo and are retired from the README roadmap:

- Hybrid xterm / raw PTY mode — polling works for the demo; interactive
  apps are the agents' concern, not the director's surface.
- Mosh support — transport robustness is nice-to-have, not demo-blocking.
- Agent output pattern monitoring (terminal-side Notify tab) — hub-level
  AG-UI events already surface agent state (idle, errored, waiting) in a
  structured way; a second, terminal-side pattern matcher is redundant.
- Local echo — mobile input latency is dominated by network RTT, which
  local echo doesn't change for submitted lines; defer.
- Cursor alignment — glyph-width calibration is a polish item.

All five are kept in the session memory as "deferred" so they don't
recur as loop picks.
