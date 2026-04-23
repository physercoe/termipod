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

### P4.1 — built-in project templates

No project rows with `is_template=1` are seeded. The blueprint calls for four
canonical templates so the demo has a real on-ramp:

- `reproduce-paper` — inputs: paper URL + reference implementation; outputs:
  reproduction report + training curves
- `ablation-sweep` — inputs: base config + ablation axes; outputs: sweep
  summary + per-run metrics
- `write-memo` — inputs: topic + target length; outputs: reviewable memo doc
- `benchmark-comparison` — inputs: models + benchmark name; outputs: leaderboard

**Concretely required:**
1. Seed data in `hub/internal/server/init.go` (or an idempotent
   `seedBuiltinProjectTemplates` helper) that inserts four `projects` rows
   with `is_template=1`, `kind='goal'`, `goal` set to a parameterized
   string, `parameters_json` describing the inputs, and
   `on_create_template_id` pointing at `agents/steward.v1.yaml`.
2. Handler + mobile client call `listProjects(is_template=true)` — a
   filter that already exists server-side but isn't exposed on the client.
3. The project-create template picker must use that list, not the YAML
   template list. See next item.

### Mobile project-template picker — **DONE v1.0.134**

Fixed in commit `aff41c1`. `_TemplatePickerSheet` now calls
`listProjects(isTemplate: true)` and returns a project-row id, which is
what `project.template_id` expects per blueprint §6.1. Server handler
`GET /v1/teams/{team}/projects?is_template=true|false` is the new filter.

Still open: the picker does not yet render `parameters_json` as an input
form — selecting a template does not prompt the user for parameter values.
That lands with the P4.1 seed wedge (when the first template with
parameters exists to drive the UI).

### P4.2 — steward decomposition recipe

`hub/templates/prompts/steward.v1.md` is a generic "spawn agents, be
concise" prompt. It does not contain the concrete decomposition recipe the
demo needs: read `project.goal` + the template's plan outline, call
`plan.instantiate`, advance phases, spawn workers for each step.

**Fix:**
1. Rewrite `steward.v1.md` with a step-by-step decomposition recipe that
   names the exact MCP tools (`plan.instantiate`, `plan.advance`,
   `agents.spawn`) and the order to call them.
2. Each built-in project template ships a *plan outline* the steward reads
   — a short YAML/markdown sketch stored on the template project's
   `config_yaml` or a sibling `plans/<template>.v1.yaml`.

### P4.3 — briefing agent + overnight schedule

No `briefing.v1.yaml` template exists. No cron schedule that fires the
briefing is seeded. The demo hinges on the user waking up to a summary
document they can review on their phone.

**Fix:**
1. Ship `hub/templates/agents/briefing.v1.yaml` and
   `hub/templates/prompts/briefing.v1.md`.
2. When a project is created from a built-in template, the
   `on_create_template_id` plan includes an `overnight` schedule that
   runs the briefing agent daily at e.g. 06:00 local.
3. Briefing agent's output is a document + an auto-requested review, so it
   shows up in the mobile Inbox as a pending approval.

## Degrades-demo gaps

### P3.1 — trackio host-runner consumption

Schema fields `runs.trackio_host_id` and `runs.trackio_run_uri` exist, and
`POST /v1/teams/.../runs/<id>/metric_uri` is wired. Host-runner does not
yet poll trackio for metrics, so runs in the demo show no training curves.

**Fix:** host-runner polls the configured trackio HTTP endpoint for a run
and attaches a metric URI on heartbeat.

### P3.2–3.4 — cross-host A2A (MVP-critical per 2026-04-23 lock)

Architecture decision locked 2026-04-23: multi-host is required for the
MVP demo (steward on VPS, worker on GPU host). A2A is no longer deferrable.

**Progress:**
- P3.2a — host-runner A2A server serves agent-cards — **DONE v1.0.133**
  (commit `30ca8ce`). New `--a2a-addr` / `--a2a-public-url` flags on
  host-runner. Card at `/a2a/<agent-id>/.well-known/agent.json` per
  A2A v0.3.
- P3.2b — A2A task endpoints (send / get / cancel) on host-runner — OPEN.
- P3.3 — hub A2A directory (register cards) + reverse-tunnel relay — OPEN.
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
