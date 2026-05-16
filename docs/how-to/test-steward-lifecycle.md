# Test the steward-driven project lifecycle (write + A2A)

> **Type:** how-to
> **Status:** Current (2026-05-16)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.607 (Scenarios 12-16 cover v1.0.592-v1.0.599 ships; Scenario 17 added 2026-05-16 for the agents.fanout/gather/reports.post orchestration triad; Scenario 7.5 failure-mode note refreshed for the v1.0.592 `request_project_steward` registration fix; older scenarios pinned to v1.0.500)

**TL;DR.** Step-by-step QA walkthrough proving the floating
steward overlay can drive a full research-project lifecycle —
read, edit, create, write, delegate via A2A — using the
`seed-demo --shape lifecycle` portfolio. Companion to
[`test-agent-driven-prototype.md`](test-agent-driven-prototype.md)
(read-only); this one covers the write verbs and exercises A2A.
Wedge plan:
[`docs/plans/steward-lifecycle-walkthrough.md`](../plans/steward-lifecycle-walkthrough.md).

Each scenario isolates a single MCP write verb so when something
fails you know exactly what to report. Tasks given to agents are
deliberately tiny (one-line summaries, single-step plans, fake
artifact URIs) — this is a fast-check on the surface, not a
realistic experiment run.

---

## Pre-conditions

The principal's stipulation is *"assume the hub and hosts has
configured."* That implies the rest:

1. **Hub running**, ≥ v1.0.500-alpha (chassis floor: D10 hero
   overrides, AFM-V1 artifact body schema, phaseless override key).
2. **Mobile build** ≥ v1.0.500-alpha installed; Settings →
   Experimental → **Steward overlay** is ON. (Overlay backfill is
   5-turn-targeted from v1.0.499; the compact `PhaseBadge` replaces
   the inline ribbon from v1.0.500. Several scenarios below check
   those.)
3. **At least one host** in `connected` state. Two hosts ideal
   for the honest A2A test (worker on a different host than the
   steward).
4. **Lifecycle seed loaded** — run on the hub host:

   ```bash
   hub-server seed-demo --shape lifecycle --reset
   ```

   Should print 5 project IDs (`research-idea-demo`,
   `research-litreview-demo`, `research-method-demo`,
   `research-experiment-demo`, `research-paper-demo`). If the
   command says `seed-demo: skipped (already exists)`, run it
   again with `--reset` so each scenario starts from known state.
5. **Worker engine available** on the chosen host (claude-code,
   codex, gemini-cli, or kimi-code — see
   `../reference/steward-templates.md` §"Per-engine capability
   matrix" for which engines speak which driving mode). Kimi-code
   additionally needs `kimi login` to have run interactively in
   the operator's shell — out-of-band auth, no flag-time fallback
   (ADR-026 W3). Confirm with `host-runner self-check` if available.
6. **Policy permits `agents.spawn`** without manual approval.
   Default test-team policy permits.

If any of the above is missing, the first scenario will fail
before any writes happen — fix the precondition and retry.

---

## Conventions

- Each scenario has **Goal**, **Steps**, **Expected**, **Failure
  modes**.
- "Type: …" means: with the steward overlay panel open, type the
  quoted line into the chat input and tap send.
- "Verify in UI" steps assume the steward navigates you there at
  the end of its action (`mobile.navigate`). If it doesn't, tap
  the destination yourself — the action was the test, the nav
  is a convenience.
- Wall-time targets assume CPU-only host. Add ~30 s if the
  steward engine is cold (first call).

---

## Scenario 0 — conjure a project (`projects.create` + `mobile.navigate`)

**Goal:** prove the steward can spin up a new lifecycle project on
director demand — the load-bearing first turn of the
"Agent-driven mode" demo script (see
[`discussions/agent-driven-mobile-ui.md`](../discussions/agent-driven-mobile-ui.md)
§11). Every later scenario can run against this conjured project OR
against the lifecycle-seed portfolio; this scenario guarantees the
former path works.

**Steps:**

1. Open the overlay puck → tap to expand.
2. Type: `Set up a research project to compare sparse vs dense
   attention on long-context retrieval. Use the research template,
   start at the idea phase, and take me to it.`

**Expected:**

- Within ~15 s, the steward replies in chat with a confirmation
  ("Created research project … taking you there now"). The reply
  may be a single bubble or a brief chain of bubbles + a
  `mobile.intent` pill.
- The overlay emits `mobile.navigate(uri="termipod://project/<new_id>")`;
  the underlying screen flips to the new project's Overview.
- The new project's name is intelligible (`sparse-vs-dense-attention`
  or similar — exact slug doesn't matter; the steward picks).
- The project is parked at the `idea` phase. The `PhaseBadge` above
  the pill bar reads `Idea · 1/5 ›`; tap to expand the underlying
  ribbon and confirm the four upcoming phases (v1.0.500 — was a
  full inline ribbon before). The chassis-hydrated `scope-ratified`
  acceptance criterion is visible.
- The Documents tile (v1.0.483) is present on the idea-phase
  Overview even with no documents yet.

**Verify across surfaces:**

- Projects tab (top-level) lists the new project alongside the
  lifecycle seed.
- Activity tab on the project shows a `project.create` audit row
  attributed to the steward.

**Failure modes:**

- Steward replies "I don't have permission" → policy gate on
  `projects.create`; verify with `policy.list` against the test
  team config.
- Steward replies but the UI doesn't navigate → `mobile.intent` SSE
  was dropped. Check `lookupSessionForAgent` is populated (the
  v1.0.479 fix). Pull-to-refresh and re-issue.
- Project appears in the list but with a strange name or wrong
  template → the steward picked the wrong tool. File the chat
  transcript + the MCP tool call sequence so the `projects.create`
  description in `tools.go` can be tightened.
- Documents tile missing on Overview → v1.0.483 regression; check
  `shortcut_tile_strip.dart`'s idea-phase tile list.

**Wall-time target:** ≤ 20 s end-to-end (create → navigate → screen
paints). Mostly steward turn time.

---

## Scenario 1 — read smoke (`projects.list`)

**Goal:** confirm the steward can introspect the lifecycle seed
before exercising any write verb.

**Steps:**

1. Open the overlay puck → tap to expand.
2. Type: `What projects do I have?`

**Expected:** within ~10 s, the steward replies in chat with a
text bubble listing **all five** lifecycle-seed projects:
`research-idea-demo`, `research-litreview-demo`,
`research-method-demo`, `research-experiment-demo`,
`research-paper-demo`. May include other projects if the team
isn't fresh — the seeded five MUST appear.

**Failure modes:**

- Reply omits one of the five → seed didn't fully run; re-run
  `seed-demo --shape lifecycle --reset` and retry.
- Reply lists no projects → steward isn't authenticated to the
  team. Check Settings → Hub for team id.
- Reply hangs > 30 s → steward engine cold or stuck. Inspect
  hub logs for a `projects.list` MCP tool call; if absent, the
  steward never dispatched.

---

## Scenario 2 — edit a project goal (`projects.update`)

**Goal:** prove the steward can mutate an existing project field.

**Steps:**

1. Type: `Set the goal of research-idea-demo to "evaluate sparse attention for sub-1B language models on long-context retrieval".`
2. Wait for steward reply + a navigate to the idea project's
   detail screen.
3. Read the goal field on the Project Detail header.

**Expected:**

- Steward chat reply confirms the update ("Updated the goal of
  …").
- Project Detail header shows the new goal exactly.
- Activity tab on that project has a fresh audit row attributed
  to the steward.

**Failure modes:**

- Steward replies "I don't have permission" → policy gate;
  check `policy.read` and adjust for test environment.
- Goal text in UI doesn't update → mobile cache didn't
  invalidate. Pull-to-refresh; if still stale, file a
  follow-up wedge against the project provider's invalidation
  path. The write itself succeeded — the bug is the UI
  refresh.
- 4xx in hub logs on `PATCH /projects/{id}` → API contract
  drift; the MCP tool description in `tools.go` must match the
  hub's accepted fields.

---

## Scenario 3 — create a document (`documents.create`)

**Goal:** prove `create` write verb on the simplest entity type
(plain document, not a typed-W5a section).

**Steps:**

1. Type: `Add an idea memo to research-idea-demo titled "sparse-attention hypothesis" with body "Hypothesis: sparse attention beats dense at sub-250M params on long-context retrieval. Need ablation."`
2. Wait for steward reply + navigate to the project's documents
   list.
3. Tap the new document; verify title + body match what you
   said.

**Expected:**

- Steward reply confirms creation with the new doc id (or a
  human label).
- Documents tab on `research-idea-demo` shows the new entry at
  the top.
- Document body matches the dictated text exactly.

**Failure modes:**

- Steward asks "what kind?" → tool description for
  `documents.create` doesn't make `kind` optional or default
  obvious. Either supply `kind=memo` in the prompt, or fix the
  description.
- Document appears but body is empty → the steward sent
  `title` but no `body` field. Check the audit row.
- Document never appears in the list → cache invalidation gap;
  pull-to-refresh; if it shows up after refresh, file a
  follow-up against the documents provider.

---

## Scenario 4 — create a plan with steps (`plans.create` + `plans.steps.create`)

**Goal:** chain two write verbs in a single user request — the
real test of multi-step composition.

**Steps:**

1. Type: `Draft a 3-step method plan on research-method-demo: step 1 "literature scan", step 2 "ablation table on retrieval task", step 3 "scaling sweep at 125M / 350M / 1B".`
2. Wait for the steward to reply + navigate to the project's
   plan widget.

**Expected:**

- Steward chat reply confirms the plan + 3 steps with their ids
  (or a numbered summary).
- Plan widget on the method-demo project shows exactly 3 steps
  in the dictated order.
- Each step's status pill shows the default state (typically
  `pending` / `todo`).

**Failure modes:**

- Steward creates the plan but only 1 step → it batched
  `plans.steps.create` but only one call landed. Check audit
  for repeated tool calls. Often a model latency issue; retry.
- Steps appear out of order → the steward fired
  `plans.steps.create` in parallel without ordering. Either the
  tool description should mention an `order` field, or the
  steward should be instructed to sequence them.
- Plan appears with 0 steps → step inserts errored; check hub
  logs for `INSERT INTO plan_steps` failures (FK, unique
  constraint).

---

## Scenario 5 — edit a plan step (`plans.steps.update`)

**Goal:** confirm `update` works on a child entity, not just
the project root.

**Steps:**

1. Type: `Mark step 1 of the method plan on research-method-demo as done.`
2. Wait for the steward reply + navigate to the plan widget.

**Expected:**

- Step 1's status pill flips to `done` / `complete` / `✓`
  (whichever the UI uses for that state).
- Step 2 + 3 remain pending.
- Activity tab shows an audit row for the step update.

**Failure modes:**

- Steward updates the wrong step → it picked by partial match
  ("step 1" vs "literature scan"). Worth noting; the
  description for `plans.steps.update` may need disambiguation
  guidance.
- Steward replies "step not found" → `plans.steps.list` returned
  empty; either Scenario 4 didn't actually land, or the
  steward looked up the wrong plan. Re-run Scenario 4.
- UI status pill doesn't change → cache invalidation gap on
  the plan provider; same follow-up flow as Scenario 2.

---

## Scenario 6 — log a run + attach artifact (`runs.create` + `runs.attach_artifact`)

**Goal:** exercise the experimental-loop write surface.

**Steps:**

1. Type: `Log a run on research-experiment-demo with seed=42, then attach a metric-chart artifact named "demo-curve" pointing at file:///tmp/curve.json.`
2. Wait for the steward reply + navigate to the project's runs
   tab.

**Expected:**

- Runs tab shows a new row with seed=42.
- Tapping the run reveals one attached artifact named
  "demo-curve" of kind `metric-chart` whose URI is
  `file:///tmp/curve.json`. The kind chip renders as `chart`
  (the closed-set label — wave 2 W1, v1.0.489). Legacy
  `eval_curve` is still accepted by the hub but silently remaps
  to `metric-chart`; prefer the new slug when prompting.
- The artifact row exists even though the file does not — by
  design (per Q2 in the wedge plan). Bytes-on-disk is a
  follow-up wedge.
- Optional: if the artifact's URI were a real `blob:sha256/…`
  served by the hub (not the case here), tapping the row would
  open a kind-specific viewer (PDF / tabular / image / code-bundle
  / audio / video / canvas-app). Wave 2 W2–W6 + canvas-viewer
  shipped these viewers through v1.0.498; the `file://` URI here
  surfaces "unsupported scheme" instead, which is also correct.

**Failure modes:**

- Run appears but artifact doesn't → the steward forgot to
  call `runs.attach_artifact` after `runs.create`. Common
  failure for chained writes; worth instructing the user (or
  the steward template) to phrase as a sequence.
- 400 on `runs.create` → required field missing
  (`project_id`); the tool description should make this
  clearer.
- Artifact has wrong kind / mime → the steward picked the
  wrong enum. Check `runs.attach_artifact` description for
  the kind list.

---

## Scenario 7 — A2A delegation (`agents.spawn` + `a2a.invoke`)

**The load-bearing scenario.** The whole multi-agent positioning
rests on the steward delegating to a different agent and
surfacing the reply.

**ADR-025 (v1.0.564+) note.** The general steward is **blocked
from `agents.spawn` against project-bound workers** per W9. This
scenario exercises an *unbound* worker spawn (no `project_id`)
so the general steward can still drive it end-to-end. For
project-bound spawn delegation (the canonical ADR-025 chain),
run Scenario 7.5 below.

**Goal:** end-to-end A2A: steward spawns an unbound worker on a
host, sends it a tiny task via `a2a.invoke`, receives the reply,
and surfaces it back in the overlay chat as a steward bubble.

**Steps:**

1. Pick the worker's `<host-id>` from the Hosts tab → **HUB**
   section (the indented children under the HubTile, v1.0.499
   grouping). Personal-only bookmarks won't carry an A2A relay
   target — only HUB-registered hosts can host workers.
2. Type: `Spawn an unbound worker on host <host-id> called "summarizer-1" (no project binding), give it the goal "summarize a project goal in one line", and have it write a one-line title for research-method-demo.`
3. Wait. This scenario is allowed up to **90 s** — spawn +
   engine warm + A2A round-trip.
4. Read the steward's reply in the overlay chat.

**Expected:**

- Steward chat shows a system row indicating `agents.spawn`
  fired (worker handle visible somewhere — overlay or audit).
  The worker's `project_id` column is NULL (no binding).
- Within ~30–60 s of the spawn, the steward posts a final text
  bubble containing the **worker's one-line title** for the
  method project. The text is short, on-topic, and clearly
  *not* the steward's own paraphrase of the project goal.
- Activity tab → project `research-method-demo` shows an A2A
  invoke event (the worker isn't bound to the project; the
  spawn event lands on the team-scope audit feed instead).
- Hosts tab → the chosen host shows an extra running agent
  (the worker) for at least the duration of the call.

**Failure modes (and what each one tells you):**

- **403 from W9 gate** — the prompt didn't make `project_id`
  empty enough. Re-run with an explicit "no project binding"
  instruction, and double-check the rendered spawn YAML doesn't
  carry a stray `project_id:` from a template.
- **No spawn event in audit** → policy gated `agents.spawn` or
  it 4xx'd. Hub logs will say which. Pre-condition 6 should
  cover this; if not, the policy needs adjusting for test.
- **Spawn succeeded, no A2A reply within 90 s** → A2A relay
  isn't reachable from the steward's host to the worker's
  host. Inspect:
  - `hub/.../audit_events` for an `a2a.message_sent` row;
  - the worker host's logs for a relay handshake;
  - `team/a2a/cards` for the worker's registered URL.
- **A2A reply arrived in hub but not in overlay chat** →
  the worker reply landed as a JSON-RPC envelope to the
  steward, but the steward didn't post a follow-up text
  frame on its own session. This is the
  `surface-A2A-reply-in-overlay` gap — file a follow-up.
  The walkthrough verifies the wire path; the surfacing path
  is the work.
- **Reply appears but is the steward's paraphrase, not the
  worker's text** → hard to tell apart on a quick read; check
  the audit row's payload for the actual `parts[].text` from
  the worker. If there is no worker reply at all, the chat
  bubble is the steward hallucinating success — that's a
  failure of this scenario, regardless of how plausible the
  text reads.
- **Worker hangs** → worker engine is cold or stuck on
  approval. Hub `attention_items` will tell you which.

---

## Scenario 7.5 — Project steward delegation chain (ADR-025)

**The canonical ADR-025 delegation flow.** Exercises every layer
of the accountability chain end-to-end: principal → general
steward → request_project_steward → director consent →
project steward → worker. Verifies the W9 gate AND the W4 +
W7 + W8 surfaces in a single pass.

**Pre-condition:** the demo project `research-method-demo` has
**no** project steward yet (run `hub-server seed-demo --shape
lifecycle --reset` if a previous run left one behind).

**Steps:**

1. From the home tab, open the General steward overlay.
2. Type: `Spawn a coder worker on host <host-id> called "summarizer-2" inside project research-method-demo. Have it write a one-line title for the method-demo project.`
3. Wait ~10 s.

**Expected (general steward branch):**

- The general steward replies that it can't directly spawn a
  project-bound worker (per ADR-025 D2). It calls
  `request_project_steward({project_id, reason})` instead.
- A `project_steward_request` attention item appears in the
  Me tab, severity `major`, summary "Spawn a project
  steward: …".

4. Tap the attention item. **Expected:** the W7 host-picker
   sheet opens prefilled with the general steward's
   suggestion (host_id). Tap **Spawn steward**.
5. **Expected:** SnackBar `Project steward spawned (<id>)`. The
   attention item closes.
6. Pop back to the general steward overlay. The steward should
   recognise the project now has a steward and either:
   - retry the request as an A2A to the new project steward, OR
   - tell you to open the project Agents tab and ask the
     project steward directly.

7. Open Projects → `research-method-demo` → Agents → tap **Ask
   steward** FAB → land in the project steward's session.
8. In the project steward's chat, type:
   `Spawn the summarizer-2 worker on host <host-id> as a coder. Have it write a one-line title for this project.`
9. Wait up to 90 s for spawn + engine warm + A2A.

**Expected (project steward branch):**

- The project steward calls `agents.spawn` with `project_id` in
  the spawn YAML. The W9 gate ALLOWS it (caller == project
  steward).
- The new worker row carries `project_id =
  research-method-demo`'s id and `kind = claude-code`
  (workers don't start with `steward.`).
- A `scope_kind='project'` session row opens for the worker
  (W8). It does NOT appear in the global Sessions screen —
  Sessions filters worker sessions to the project Agents tab
  (verify by popping to Sessions and looking for it).
- Project Agents tab shows the steward AND the worker. Tap
  the worker → agent_config_sheet → confirm `Ask steward to
  reconfigure` CTA renders.
- Within ~30-60 s the project steward posts the worker's reply
  text in its chat.

**Failure modes:**

- **W9 gate rejects the project steward's spawn** → check
  `projects.steward_agent_id` matches the project steward's
  id. If they don't match, W3's bind step didn't run; re-spawn
  the project steward.
- **request_project_steward never fired** → the general
  steward's prompt update didn't take. Confirm the deployed
  `steward.general.v1.md` includes the
  `## Project work — delegate to the project steward`
  section (added in v1.0.573-alpha). **Common cause until
  v1.0.592:** the tool was registered in the dispatcher but
  missing from `tools/list` (`mcpToolDefsExtra()` skipped
  the entry), so claude-code returned "No such tool available"
  and the steward fell back to a generic `request_approval` +
  `agents.spawn` sequence (which the W9 gate then rejected
  with `general steward must delegate via request_project_steward`).
  Fixed in v1.0.592. Verify via the catalog directly:
  `tools/list` on the steward's MCP path should include
  `request_project_steward`. The
  `TestEveryDispatcherCaseAdvertised` safety test (also
  v1.0.592) makes regressions of this class fail in CI.
- **No worker session in project Agents tab** → DoSpawn's
  auto-open for project_id didn't fire. Check hub logs for
  `INSERT INTO sessions ... scope_kind='project'`. If the
  worker row exists but no session, the auto-open guard
  (W8) regressed.

---

## Scenario 8 — composed end-to-end ("steward, drive the demo")

**Goal:** stress-test the surface in one freeform request.

**Steps:**

1. Reset the seed: `hub-server seed-demo --shape lifecycle --reset`
2. Re-open the app + overlay.
3. Type one paragraph:
   *"Walk me through the method-demo project: update its goal
   to 'sparse attention ablation', add a one-line idea memo,
   draft a 3-step method plan, mark step 1 done, and log a
   demo run with seed=7 plus a fake eval_curve artifact. End
   on the runs tab so I can see the result."*
4. Watch.

**Expected:**

- Steward executes 5 distinct write tool calls in order.
- Each step appears in the audit log (Activity tab) with a
  steward attribution.
- Final navigation lands you on the project's runs tab with
  the new run + artifact visible.
- Total wall-time ≤ 3 minutes on a typical CPU host.

**Failure modes:**

- Steward does only the first action then asks "should I
  continue?" → fine; reply "yes, continue." and re-test the
  multi-step path. May indicate a steward-template tweak is
  needed (see `templates/steward.general.v1.yaml` if it
  exists).
- Steward executes them out of order (e.g. logs the run
  before drafting the plan) → semantically OK, the verbs all
  succeeded; note the order for the wedge close-out.
- Steward partially succeeds and stops without explanation →
  one of the verbs hit an error it couldn't recover from.
  Check audit + hub logs to find which.

---

## Scenario 9 — overlay turn-count backfill (v1.0.499)

**Goal:** prove the overlay panel restores **the last 5 user turns**
of chat after the app is closed and re-opened, regardless of how
tool-heavy the recent turns were. Catches the regression class the
v1.0.499 fix addressed: the prior event-budget rule could surface 1–2
turns on chats with heavy tool calls.

**Steps:**

1. Continuing from Scenario 0 / 1 (or any session with ≥ 5 prior
   user turns), background the app or force-stop it from the
   launcher.
2. Re-open the app. The puck is visible immediately; tap to expand.
3. Scroll the overlay chat history up.

**Expected:**

- The overlay shows **at least 5** of your prior user messages
  (and the steward's replies + any `mobile.intent` pills) — not
  one, not two, not the whole transcript. Tool-call frames are
  collapsed out of the display, so what you see is the
  conversational shape only.
- The newest message is at the bottom; the chat reads
  oldest → newest. The system note "Showing cached history
  (offline)" may appear if the device was offline; otherwise the
  hub re-sync is silent.
- Sending a new message appends below the restored history.
  Older messages may be evicted as the rolling window
  (`_overlayMessageCap = 15`) fills.

**Failure modes:**

- Only 1–2 turns visible after restart → tool-heavy conversation
  is hitting the event-budget cap. Either the upstream raised the
  ceiling and you're on a stale build (verify mobile version), or
  `_backfillTurnTarget` regressed. Check
  `lib/widgets/steward_overlay/steward_overlay_controller.dart`
  constants.
- Chat is blank → backfill RPC failed silently. The first frame
  on the live SSE will populate it; sending a message confirms.
- More than ~15 messages visible → the message cap regressed.
  Acceptable to leave as Pinned-bug for follow-up if it's only
  cosmetic.

---

## Scenario 10 — director composes tiles + hero on a phaseless project (v1.0.499)

**Goal:** prove the Customize affordance works for manually-created
projects (no template, no phase). The v1.0.499 fix landed two paths
that this exercises:

- Mobile: the Customize row's `phase.isEmpty` early-return is gone;
  the sheet opens.
- Hub: `resolveOverviewWidget` honors `overrides[""]` (empty-string
  phase key); the picked hero actually renders.
- Sheet save: `PhaseTileEditorSheet._save` pops with the updated
  body and the chain bubbles it back so the strip rebuilds
  immediately (the customize-save callback fix).

**Steps:**

1. From the Projects tab, tap **+ New project** (NOT through the
   steward). Enter just a name — leave the steward template and
   on-create template blank. Tap Save.
2. Open the new project. The `PhaseBadge` is NOT visible (no
   phases). The Overview body shows the chassis default
   `task_milestone_list` hero + the `[Outputs, Documents]` tile
   strip + a "Customize shortcuts" row at the bottom of the strip.
3. Tap **Customize shortcuts**. The phase-tile editor sheet opens.
4. Toggle a tile off (e.g. drop `Documents`). Pick a different
   hero from the picker chips (e.g. `recent_artifacts`). Tap
   **Save**.

**Expected:**

- Sheet closes. The Overview body re-renders **without a manual
  refresh**: the tile you dropped is gone, the new hero is
  visible.
- Re-opening the project (or pulling down to refresh) shows the
  same state — the override persisted, not just optimistically
  reflected.
- The Activity tab on the new project shows a `project.update`
  audit row attributed to the user (not the steward) listing
  `phase_tile_overrides_json` and `overview_widget_overrides_json`
  in the changed-fields list.

**Failure modes:**

- Tapping Customize does nothing → v1.0.499 mobile fix regressed
  (`_CustomizeTilesRow._open` early-returns on empty phase).
- Sheet saves but the strip rebuilds with the same tiles → the
  callback plumb broke (`onProjectChanged` not firing). The hub
  PATCH still landed; reload the project to confirm.
- Sheet saves but the new hero never appears, even after reload →
  hub `resolveOverviewWidget` no longer consults `overrides[""]`.
  Check `handlers_projects.go`.

---

## Scenario 11 — fault injection (the failure-mode validations)

The walkthrough's value is its failure modes. Before declaring
this wedge done, deliberately break **three** subsystems and
confirm each surfaces the documented failure mode:

1. **Stop the worker host.** Re-run Scenario 7. Expected:
   "Spawn succeeded, no A2A reply within 90 s." If the user
   sees a different failure surface (e.g. silent timeout, no
   error), the failure mode in this doc needs a fix.

2. **Disconnect the hub from the mobile.** Mid-walkthrough,
   kill the mobile's wifi for ~10 s and reconnect. Expected:
   the SSE auto-reconnect from v1.0.479 kicks in; the steward
   stream doesn't permanently die.

3. **Send a malformed write request.** Type:
   `Update project research-idea-demo with budget=banana.`
   Expected: the steward reports the validation error in
   chat. (4xx on `projects.update` should not crash the
   overlay.)

If any of the three fault-injection scenarios surfaces a
*different* failure than this doc says it should, fix the doc
or fix the surface — don't ship the wedge with a stale failure
guide.

---

## Scenario 12 — Project Agents detail sheet has SessionInitChip + overflow (v1.0.594)

**Goal:** confirm the project Agents detail sheet now matches
the Session-chat surface.

**Steps:**

1. Open Projects → `research-method-demo` → Agents.
2. Tap a live project steward row (after spawning one per
   Scenario 7.5). The `_AgentDetailSheet` opens.

**Expected:**

- Header row shows: handle · mode chip · status chip · (overflow
  menu icon — three dots) · close (X).
- A second row directly below the header shows a
  `SessionInitChip` if the agent has emitted `session.init`:
  engine kind pill (`claude`) + model pill (`opus 4.7`) +
  permission mode pill + tools count + mcp-server count. Tap
  opens the session-details sheet.
- The overflow menu (three dots) carries: **View agent config**
  (opens `showAgentConfigSheet`), **Pause/Resume** (if live +
  has pane), **Respawn** (if spec available), **Terminate** /
  **Delete** (state-aware).
- No flat row of action buttons below the header — those
  collapsed into the overflow in v1.0.594.

**Failure modes:**

- **SessionInitChip never appears:** agent hasn't emitted
  `session.init` yet (cold spawn). Send the agent one input,
  wait ~3 s, reopen the sheet.
- **Overflow menu missing pause/resume:** agent is dead (status
  terminated/failed/crashed) or paneless — expected.

---

## Scenario 13 — Spawn-steward sheet engine row reads YAML mode + model (v1.0.597)

**Goal:** confirm the engine info row reflects what the YAML
actually configures.

**Steps:**

1. Open the Library tab → Templates → tap `steward.v1.yaml` →
   editor. Change `driving_mode: M2` to `driving_mode: M4`. Save.
2. Pop back to Home → tap **Spawn steward** card.
3. Pick `steward.v1.yaml` in the template dropdown.

**Expected:**

- Engine info row reads `Claude Code` + `M4 · opus 4.7 · JSONL
  tail · MCP permission gate`. Pre-v1.0.597 it would have read
  `Claude Code · opus-4-7 · stream-json · MCP permission gate`
  — hardcoded regardless of YAML.
- Repeat for `steward.codex.v1.yaml`, `steward.gemini.v1.yaml`,
  `steward.kimi.v1.yaml`: each engine row shows the correct
  label + driving-mode-aware transport hint. Kimi-code used to
  fall through to "Unknown engine" / "kind=kimi-code"; now has
  its own entry.

**Cleanup:** revert the `driving_mode` edit on `steward.v1.yaml`
or reset via `Restore built-in` so other scenarios start from
known state.

---

## Scenario 14 — Project-bound steward workdir isolation (v1.0.595)

**Goal:** prove two project stewards on the same host no longer
collide on `~/hub-work`.

**Steps:**

1. Reset the seed: `hub-server seed-demo --shape lifecycle --reset`.
2. Spawn a project steward on `research-method-demo` via the
   project's Agents tab "Spawn project steward" CTA.
3. Spawn a second project steward on
   `research-experiment-demo` from its Agents tab — same host.
4. On the host, run: `ls -d ~/hub-work/*/*` (or `find ~/hub-work
   -maxdepth 3 -name .mcp.json`).

**Expected:**

- Two distinct `~/hub-work/<pid8>/<handle>` directories appear,
  each with its own `.mcp.json` and `.claude/settings.local.json`.
- Neither overwrites the other. Pre-v1.0.595 both project
  stewards (using bundled `steward.v1.yaml` with hardcoded
  `default_workdir: ~/hub-work`) would have shared `~/hub-work`
  and silently overwritten each other's per-spawn config —
  the root cause of the "phantom kimi steward" symptom where
  taps on one project's steward strip routed to a different
  project's session.

**Failure modes:**

- **Both stewards still in `~/hub-work` directly:** the project
  steward template wasn't refreshed. Check
  `hub/templates/agents/steward.v1.yaml` (or the team-overlay
  copy under `<DataRoot>/team/templates/agents/`) — should NOT
  carry `default_workdir:` (left to launcher to auto-derive).
  Re-init the hub or delete the team-overlay file to fall
  back to the bundled v1.0.595+ shape.

---

## Scenario 15 — Template scaffold tools + scope filter (v1.0.596-598)

**Goal:** exercise the agent-driven template-authoring loop
end-to-end.

**Pre:** the principal token must have MCP access (default for
the test team).

**Steps:**

1. Open the General Steward overlay.
2. Type: *"Author a new worker template for a 'reading-list-curator'
   role. Use the scaffold tool to make sure the schema is right."*
3. Wait for the steward to call `templates.agent.scaffold(kind=worker)`.
4. Watch the chat — the scaffold body should appear in a
   tool_result card, then the steward customises it and calls
   `templates.agent.create(name=reading-list-curator.v1.yaml,
   content=<modified>)`.

**Expected (scaffold flow):**

- The scaffold result carries: `{category, suggested_name,
  content}` where `content` includes all schema-mandated fields
  (template, version, driving_mode, backend.{kind, model, cmd,
  permission_modes}, default_role, default_capabilities, skills,
  default_channels) and NO persona-specific carryover (no
  `agents.coder`, no `display_label: "Coder"`, etc.).
- The steward modifies in place and writes back via `.create`.
  No "I can't author a YAML template" or improvised non-schema
  output.

5. Edit the new YAML: add `applicable_to:\n  template_ids:
   [research-project.v1]\n` at the top level. Save.
6. Open Projects → `research-method-demo` → tap **Spawn worker**
   FAB → template picker.

**Expected (applicable_to filter):**

- The picker shows: every template with no `applicable_to:`
  (team-shared) PLUS `reading-list-curator.v1.yaml` (because
  it's scoped to `research-project.v1`, which
  `research-method-demo`'s `template_id` matches).
- Open Projects → `research-paper-demo` → Spawn worker picker.
  Expected: `reading-list-curator.v1.yaml` does NOT appear
  (different project template).

**Failure modes:**

- **`templates.agent.scaffold` returns "tool not found":**
  catalog registration regressed. The dispatcher safety test
  `TestEveryCatalogEntryHasTier` should fail in CI before this
  reaches you — if it didn't, the test is broken.
- **Picker shows the scoped template in every project:** the
  filter wasn't applied. Check `lib/services/template_filter.dart`
  imports in `spawn_agent_sheet.dart` and `plan_create_sheet.dart`.

---

## Scenario 16 — Library tab collapsible + search (v1.0.599)

**Goal:** verify the new Library UX affordances.

**Steps:**

1. Open Settings → Library (or the Settings entry that opens
   `TemplatesScreen`).
2. On the Templates tab, tap the chevron next to "agents" —
   expected: section collapses; chevron rotates from `expand_more`
   to `chevron_right`; tile count next to the section name
   stays visible.
3. Tap the search icon in the AppBar.

**Expected:**

- AppBar title swaps to a `TextField` with hint
  "Search templates and engines". Type `steward`.
- Templates tab filters to rows where the name or category
  contains `steward`. The agents section forces open (search
  overrides collapse).
- Switch to the Engines tab while search is active. Same query
  filters by family + bin + supports — only families whose
  fields contain `steward` appear (likely none — try `claude`
  or `gemini` for a hit).
- Tap the close icon in the AppBar — search clears, prior
  collapse state restored (your earlier collapse of "agents"
  is still in effect).

**Failure modes:**

- **Search box doesn't autofocus:** check the
  `TextField(autofocus: true)` flag in `templates_screen.dart`.
- **Collapse state lost on tab swap:** the `_collapsed` set
  must live on `_TemplatesScreenState`, not on the inner body
  builder. Regression from v1.0.599's design.

---

## Scenario 17 — Fan-out / fan-in orchestration (`agents.fanout` + `agents.gather` + `reports.post`)

**The orchestrator-worker primitive.** Exercises the
fan-out/fan-in pattern (`hub/internal/server/mcp_orchestrate.go`)
end-to-end: project steward spawns N trivial workers under one
`correlation_id`, each worker posts a typed `reports.post` on
completion, and the steward's `agents.gather` long-poll returns
the consolidated result list. The whole loop is parallel — the
steward doesn't drive each worker by hand.

**Pre-condition:** the demo project `research-method-demo` has a
live project steward (run Scenario 7.5 first, or `hub-server
seed-demo --shape lifecycle --reset` followed by an
`@steward.<pid8>` ensure flow). Without a project steward the
fan-out's spawns will hit the W9 gate.

**Steps:**

1. Open the project steward's overlay chat for
   `research-method-demo`.
2. Type: `Fan out two trivial workers under correlation_id "demo-fanout-1". Worker "title-1" writes a 5-word title for this project; worker "summary-1" writes a one-sentence summary. After both report back, gather the results and post them as a single text reply in chat.`
3. Wait up to **3 min** — two spawns + two engine warms + two
   trivial outputs + the gather long-poll.
4. Read the steward's reply in the overlay.

**Expected:**

- Steward chat shows a system row referencing `agents.fanout`
  (two worker handles visible — `title-1`, `summary-1`). Their
  `project_id` column matches `research-method-demo`.
- Within ~90-120 s of the fanout, both workers' rows in the
  project Agents tab show `worker_report` activity (visible
  via Activity tab → filter to project, OR via the per-agent
  detail sheet's transcript view).
- The steward posts a final text bubble containing **both
  workers' outputs** — the 5-word title from `title-1` AND the
  one-sentence summary from `summary-1`. The text is short,
  on-topic, clearly two distinct contributions.
- Hub activity feed shows one `agents.fanout` row and one
  `agents.gather` row tagged with `correlation_id=demo-fanout-1`.

**Failure modes (and what each one tells you):**

- **Steward calls `agents.spawn` twice instead of `agents.fanout`** —
  the orchestration primitive isn't being discovered. Check that
  `orchestrationToolDefs()` is in the steward's MCP catalog
  (`templates.{cat}.list` from the steward should show
  `agents.fanout` / `agents.gather` / `reports.post`). If missing
  it's a tool-advertisement bug.
- **Fanout returns `error: spawn ok but input post failed`** for
  one or both workers → the worker spawned but its first
  `input.text` event didn't land. Check
  `hub/.../audit_events` for the spawn row + a missing
  matching `input.text` row. Common cause: session not auto-opened
  (verify `auto_open_session: true` on the fanout spawn path).
- **`agents.gather` times out (~10 min)** → one or both workers
  never posted `reports.post`. Their transcripts will show the
  worker waiting on something (often an approval). Pre-condition
  6 (auto-allow tier ≥ moderate) should cover this; otherwise
  the worker is parked on a tool-permission prompt.
- **Gather returns partial results (`done: false` for one worker)** —
  one worker terminated/crashed before posting. The session in
  the Sessions list shows it as paused; the agent's transcript
  has the engine-side stderr.
- **Steward reply doesn't include both worker texts** → the
  steward gathered the reports but paraphrased instead of
  surfacing the raw `report.summary_md` field. Inspect the
  gather response payload in the steward's session for the
  actual worker text and report this as a steward-prompt
  follow-up (the orchestration mechanism worked; the rendering
  is the gap).
- **`agents.fanout` denied by W9 gate** — the project steward
  isn't bound to `research-method-demo` (or you're testing in
  the general steward's overlay by mistake). Run Scenario 7.5
  first to materialize the project steward.

**Why the trivial tasks:** the scenario tests the
orchestration plumbing, not the work content. A 5-word title +
one-sentence summary keeps each worker's turn cheap enough that
the gather long-poll resolves in seconds, isolating "did
fanout/gather wire correctly" from "did the workers do good
work".

---

## When you're done

- Tick the checkboxes in
  [`docs/plans/steward-lifecycle-walkthrough.md`](../plans/steward-lifecycle-walkthrough.md)
  done-criteria section.
- File any gap surfaced (missing `documents.update`, weak tool
  description, A2A reply not surfacing in chat, cache
  invalidation lag) as its own follow-up wedge or memory entry.
  Don't silently fix-and-forget — the gaps are the value.
- Update `docs/changelog.md` with what you actually verified on
  the build under test.
- For a clean second run on a different host (done-criterion 4),
  reset the seed first.

---

## Why these specific scenarios (rationale for future contributors)

- **W1 read** — sanity-checks the steward → hub auth before any
  writes can possibly succeed. Fastest-failing precondition.
- **W2 edit (project)** — single-tool, single-row mutation.
  Smallest possible write footprint.
- **W3 create (document)** — adds a row; tests cache
  invalidation on a list view.
- **W4 plan + steps** — chained writes. Catches the "steward
  fires write 1 but forgets write 2" failure class, which is
  the dominant write bug pattern empirically.
- **W5 edit (step)** — child-entity update. Catches the "steward
  edits the wrong child" disambiguation bug.
- **W6 run + artifact** — exercises the experimental loop's
  central write path. Different shape from documents.
- **W7 A2A** — the multi-agent positioning anchor. If this
  scenario fails, the system has a positioning lie, regardless
  of how green W1–W6 are.
- **W8 composed** — ensures the verbs compose under one
  freeform user request, the way the principal will actually
  use it.
- **W9 overlay turn-count backfill** — protects the v1.0.499 fix.
  The overlay's "rolling 5 turns" only matters under restart
  pressure with a tool-heavy chat history; without an explicit
  scenario, regressions in `_backfillTurnTarget` or
  `_overlayMessageCap` would be invisible to a green run-through.
- **W10 director composes tiles + hero (phaseless)** — protects
  the v1.0.499 phaseless-customize fix end-to-end (mobile open
  guard + hub empty-phase override lookup + save-callback plumb).
  The customize-sheet path bypasses the steward, so it's the one
  surface the steward-driven scenarios can't catch by themselves.
- **W11 fault injection** — protects this doc from rotting. The
  failure modes ARE the value of the walkthrough; verify them
  empirically.
