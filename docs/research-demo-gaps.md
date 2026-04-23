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

### Mobile project-template picker is wrong

`lib/screens/hub/project_create_sheet.dart` — the `_TemplatePickerSheet`
currently calls `client.listTemplates()`, which returns YAML files under
`team/templates/{agents,prompts,policies}/`. Blueprint §6.1 says
`project.template_id` is a foreign key to another **project** row (with
`is_template=1`). Today the mobile UI is writing a YAML path into that FK
column, which is type-confused.

**Fix:**
1. Add `listProjects(isTemplate: true)` to `hub_client.dart`.
2. Rewrite `_TemplatePickerSheet` to list project templates: show
   `name`, `goal`, and `parameters_json` keys as an input form.
3. On pick, POST `template_id` + `parameters_json` (bound values) to the
   new project. Separately, keep the agent-YAML picker for
   `on_create_template_id`.

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

## Deferrable (not demo-blocking)

- **P3.2–3.4 cross-host A2A.** The demo can run single-host with many
  spawns; multi-host A2A is a robustness story, not a demo story. Ship
  after demo lands.
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
