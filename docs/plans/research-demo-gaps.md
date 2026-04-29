# Research-demo gaps

> **Type:** plan
> **Status:** In flight (P0–P3 shipped; P4 backend feature-complete; hardware run remaining)
> **Audience:** contributors
> **Last verified vs code:** v1.0.314

**TL;DR.** The detailed P4 demo tracker. Pairs with `../roadmap.md` —
roadmap is the high-level Now/Next/Later view; this doc is the
demo-scoped delta of what's shipped vs what's needed. Locked target
is Candidate A per `../decisions/001-locked-candidate-a.md`.

The MVP (`../spine/blueprint.md` §9 Phase 4) is the end-to-end research demo:

> user writes a directive → steward decomposes → fleet executes runs across
> hosts → briefing agent summarizes overnight → user reviews on phone

Everything here is scoped against that demo. Items are the delta
between *what's shipped* and *what a user needs to run the demo from
their phone end-to-end*. If a feature doesn't advance this demo, it
belongs in `../spine/blueprint.md` §10 (Non-goals) or gets deferred.

## Status

**Shipped (P0–P2, most of P3):**
- Projects / plans / plan_steps / schedules / runs / documents / reviews schema
- Multi-mode host-runner (ACP, stdio, pane)
- Plan-step executor (llm_call, shell, mcp_call, human_decision, agent_spawn)
- Hub MCP server + host-runner MCP gateway
- AG-UI broker + SSE stream + structured input
- Mobile AgentFeed, AgentCompose, plan viewer, workflows tab, triage (Me tab),
  project / task / plan / run / review detail screens, templates browser
- Team settings: schedules, budgets, audit log

## Demo-blocker gaps

### P4.1 — built-in project templates — **DONE for Candidate A (v1.0.138 + v1.0.152)**

Seed path added: `seedBuiltinProjectTemplates` in
`hub/internal/server/init.go` inserts `is_template=1` project rows on first
init (idempotent via `INSERT OR IGNORE`). The `ablation-sweep` row ships
with `parameters_json = {model_sizes, optimizers, iters}` and
`on_create_template_id = agents.steward`, matching the locked demo choice
(`../decisions/001-locked-candidate-a.md`).

**v1.0.152:** the project-create sheet now renders a template's
`parameters_json` as an input form. After picking a template whose
`parameters_json` is a non-empty object, a second sheet prompts for each
key. Type inference: `[int]`/`[num]`/`[string]` → comma-separated field,
bare scalars → typed field, anything else → raw-JSON textarea validated
on submit. The populated map is sent as `parameters_json` on create.

Out of scope for the demo (deferred):
- `reproduce-paper`, `write-memo`, `benchmark-comparison` templates —
  Candidate A only needs `ablation-sweep`. Per ADR-001 the other
  candidates aren't on the MVP path.

### Mobile project-template picker — **DONE v1.0.134**

Fixed in commit `aff41c1`. `_TemplatePickerSheet` now calls
`listProjects(isTemplate: true)` and returns a project-row id, which is
what `project.template_id` expects per blueprint §6.1. Server handler
`GET /v1/teams/{team}/projects?is_template=true|false` is the new filter.

Parameter form now lands on top: see P4.1 v1.0.152 note above.

### P4.2 — steward decomposition recipe — **DONE v1.0.137**

`hub/templates/prompts/steward.v1.md` now carries a concrete
"Decomposition recipe: ablation sweep" section that names the exact MCP
tool sequence (`plan.instantiate` → `a2a.invoke` × N on `worker.ml` →
collect → hand off to briefing). `plan.instantiate`, `plan.advance`, and
`a2a.invoke` are listed in the steward's tool set.

Still open: plan outlines stored as data (rather than inline in the
prompt). For the single demo template this is fine; revisit if more
templates land.

### P4.3 — briefing agent + overnight schedule — **DONE v1.0.153**

Templates shipped:
- `hub/templates/agents/briefing.v1.yaml` — writer role, docs + reviews
  capabilities, spawn.descendants=0.
- `hub/templates/prompts/briefing.v1.md` — runbook writes a Goal / What
  ran / Plot / Takeaway / Caveats doc, calls `documents.create` +
  `reviews.create` so it surfaces in the mobile Me tab.
- Also shipped: `hub/templates/agents/ml-worker.v1.yaml` +
  `ml-worker.v1.md` — GPU-host worker that executes one A2A
  `train(config)` task, writes a `runs` row, attaches a trackio URI.

Scheduling exposure closed in **v1.0.153**: the steward can now call
`schedules.create(project_id, template_id, trigger_kind='cron', cron_expr)`
directly to attach a nightly briefing (plus `schedules.{list,update,
delete,run}` for full lifecycle). Auto-seeding at project creation is
still not wired — the steward authors the cron explicitly per project,
which is the desired behaviour under the owner-authority model.

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
- P3.2b — A2A task endpoints (send / get / cancel) — **DONE v1.0.149,
  response loop closed in follow-up**.
  JSON-RPC 2.0 handler for `message/send`, `tasks/get`, `tasks/cancel`
  at the agent URL root (`POST /a2a/<agent-id>`). In-memory `TaskStore`
  keeps per-agent state with terminal-state freeze so a late completion
  after a cancel can't flip state back. Concrete `a2aHubDispatcher`
  extracts text parts from incoming messages and POSTs them to the hub's
  `/v1/teams/{team}/agents/{agent}/input` endpoint — same audit path as
  phone/web input — and the local `InputRouter` delivers them to the
  driver. The follow-up landed both halves of the loop: input now
  carries `producer="a2a"` (agent_events CHECK widened to accept it,
  migration 0015), and driver output events are tapped via an
  `AgentEventPoster` wrapper so `producer="agent"` text chunks append to
  task history (state `submitted` → `working`) and a
  `lifecycle`/`phase=stopped` flips the task to `completed`. One task
  per agent at a time; a second `message/send` while the first is live
  cancels the prior correlation. No turn-complete signal is available
  from all drivers, so tasks stay on `working` until the driver stops
  or the peer issues `tasks/cancel`.
- P3.4 — cross-host A2A smoke (two host-runners under one hub) —
  **DONE v1.0.157**. `TestTunnel_RelayTwoHosts_RoutesPerPath` in
  `hub/internal/server/tunnel_a2a_test.go` spins up two concurrent
  long-poll tunnels (host-cpu and host-gpu) against one hub and
  verifies that `/a2a/relay/{host}/...` routes purely on the path
  segment — host-A's relay call cannot reach host-B's tunnel queue.
  Anti-fanout invariant is checked via per-tunnel hit counters. Real
  two-VPS smoke is still useful for network-level validation (TLS,
  public URL rewrite, NAT traversal) but the core routing contract is
  now covered by CI.

### P4.4 — steward MCP tool parity — **DONE v1.0.156**

The steward's decomposition recipe in `hub/templates/prompts/steward.v1.md`
referenced tools (`a2a.invoke`, `agents.spawn`, `runs.create`,
`post_message`, `plan.instantiate`, `plan.advance`) that the MCP server
didn't expose — so the steward couldn't drive the demo end-to-end under
the principal/director UX model.

Shipped in `hub/internal/hubmcpserver/tools.go` (originally a
standalone daemon at `cmd/hub-mcp-server`; consolidated into the
hub's in-process MCP at `/mcp/<token>` in v1.0.297–298 via
`internal/server/mcp_authority.go` so spawned agents reach it through
the bridge — single `.mcp.json` entry, single symlink to install):
- `a2a.invoke(handle, text)` — looks up the agent card from the team
  directory, then POSTs a JSON-RPC `message/send` to the relay URL.
  Unblocks steward→worker handoff.
- `runs.create(project_id, ...)` — lets the steward reserve a run row
  before delegating via A2A.
- `agents.spawn(child_handle, kind, spawn_spec_yaml, ...)` — spawns
  workers / the briefing agent; returns 202 + attention_id when policy
  tiers the request.
- `channels.post_event(channel, type, parts, [project])` — unifies
  `post_message` / `post_excerpt`; works for both project-scope and
  team-scope channels.

Steward + briefing prompts updated to call the shipped tool names
(`reviews.create` not `reviews.request`, `runs.get` not `runs.read`,
`channels.post_event` not `post_message`).

**v1.0.151:** added `plans.steps.create`, `plans.steps.list`,
`plans.steps.update` MCP wrappers so the steward can author and advance
multi-phase plans directly. Steward recipe updated to lay down one
`plan_steps` row per phase via these tools and patch status as each
phase runs.

**v1.0.153:** added `schedules.{list,create,update,delete,run}` MCP
wrappers. Closes the scheduling infra gap called out in the
steward-CEO audit (`../discussions/ux-steward-audit.md` §2) and unblocks P4.3
overnight cron authoring without relying on the mobile UI. Steward
prompt updated to list the schedules surface.

**v1.0.154:** added `tasks.{list,create,update}` MCP wrappers so the
steward can break a project goal into trackable units of work directly
(distinct from plan_steps; these surface in the mobile project view).
Closes audit-queue row 2.

**v1.0.155:** added `project_channels.create` and `team_channels.create`
so the steward can author a new channel before posting. Previously
`channels.post_event` required the channel to already exist (created
by {{principal.handle}} from the mobile UI), which broke the
author-then-announce flow. Closes audit-queue row 3.

**v1.0.156:** added `projects.update` and `hosts.update_ssh_hint`.
`projects.update` patches mutable fields (goal, parameters_json,
budget_cents, policy_overrides_json, steward_agent_id,
on_create_template_id) — create-time fields (kind, template_id,
parent_project_id) remain immutable by design. `hosts.update_ssh_hint`
accepts an object and stringifies to `ssh_hint_json`; the hub rejects
secret keys (password/private_key/passphrase/secret/token) per §4.
Closes audit-queue row 4 — **P4.4 is now fully DONE**.

Closed since this doc was written:
- Explicit `request_approval` / `request_select` (formerly
  `request_decision`) / `request_help` MCP tools all shipped. v1.0.338
  converted the three async kinds from long-poll to turn-based
  delivery: the call returns immediately with `awaiting_response`,
  the agent ends its turn, and the principal's reply lands as a
  fresh user turn via `input.attention_reply`. See
  [`reference/attention-kinds.md`](../reference/attention-kinds.md)
  for the authoring guide.

Plus AG-UI `a2a.invoke` / `a2a.response` event kinds surfaced on the
calling agent's stream (§5.4).

## Dress-rehearsal harness (no-GPU)

The demo's end-to-end path (worker writes metrics → host-runner polls →
hub digests → mobile sparkline) takes hours of nanoGPT training on a
GPU host. Two harness tools let a reviewer exercise the same pipeline
on a laptop in seconds:

**`hub-server seed-demo` (v1.0.169)** inserts a ready-to-browse
`ablation-sweep-demo` project with 6 completed runs, synthetic loss
curves, a briefing document, a pending review, and one open attention
item. Pure DB writes; no vendor file is produced. Reviewers can open
the mobile app, land on the project, and tap through every P4 surface
immediately.

```
hub-server seed-demo --data ./hub-data
```

**`hub/cmd/mock-trainer` (v1.0.170)** writes a real trackio SQLite or
wandb-offline JSONL file with a configurable synthetic training curve.
The host-runner's existing trackio / wandb readers consume these
outputs unchanged — the same code path a real worker exercises. Use it
to validate reader + poller + digest end-to-end without touching a
GPU.

```
# trackio
mock-trainer --vendor trackio --dir /tmp/trackio \
  --project ablation-sweep-demo --run size384-lion \
  --size 384 --optimizer lion --iters 1000

# wandb
mock-trainer --vendor wandb --dir /tmp/wandb \
  --project ablation-sweep-demo --run run-abc \
  --size 256 --optimizer adamw --iters 1000
```

Then POST a run to the hub with `trackio_run_uri` set to the printed
URI and point host-runner at the same `--dir` so it picks the file up.

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
