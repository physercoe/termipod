# Test the steward-driven project lifecycle (write + A2A)

> **Type:** how-to
> **Status:** Current (2026-05-16)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.607 (Scenarios 12-16 cover v1.0.592-v1.0.599 ships; Scenarios 17-20 added 2026-05-16 to close gaps from the comprehensive coverage audit: fanout/gather/reports.post, agents.list live + a2a.cards.list + terminate, templates.propose + preview approval, request_select round-trip; Scenarios 21-24 added 2026-05-16 for ADR-027 plan §11 M4 claude-code verifications: plan_approval, AskUserQuestion, /compact, pill on Stop hook + knob tuning; Scenario 7.5 failure-mode note refreshed for the v1.0.592 `request_project_steward` registration fix; older scenarios pinned to v1.0.500)

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

## Scenario 18 — Worker discovery + termination (`agents.list live=true` + `a2a.cards.list` + `agents.terminate`)

**The worker-housekeeping triad** (v1.0.606 ships +
pre-existing terminate). Without these the multi-worker
scenarios accumulate cruft and the agent's view of its peers
gets noisy. Closes the v1.0.606 + cleanup gap surfaced by the
2026-05-16 coverage audit.

**Pre-condition:** the demo project `research-method-demo`
has a live project steward AND at least one fanout from
Scenario 17 has run (so there's something to discover and
terminate). If you skipped 17, spawn a single unbound worker
the same way Scenario 7 does.

**Steps:**

1. Open the project steward's overlay chat.
2. Type: `Show me all currently-live agents on this team — call agents.list with live=true. Then show me the A2A directory via a2a.cards.list (no handle filter). Finally pick one of the fanout workers and call agents.terminate on it.`
3. Wait up to **30 s** — three tool calls plus the
   terminate's audit + a2a card removal.
4. Re-run step 2 (or just `agents.list live=true` again) and
   confirm the terminated worker is gone from the response.

**Expected:**

- First `agents.list` returns a clean roster — no
  `terminated/failed/crashed` rows. Each row carries `handle`,
  `kind`, `status`, `project_id`, `parent_agent_id`. The
  fanout workers from Scenario 17 should be visible.
- `a2a.cards.list` returns the per-handle directory; the URL
  field on each card points at the hub's `/a2a/relay/<host>/<agent>`
  path (not the host-runner direct address — confirms the
  v1.0.394+ relay rewrite).
- `agents.terminate` returns success. Activity tab shows an
  `agents.terminate` audit row with the worker's id.
- Re-running `agents.list live=true` no longer includes the
  terminated worker. Mobile project Agents tab also drops it
  (or marks it terminated, depending on hub default for the
  archive-on-terminate flag).

**Failure modes (and what each one tells you):**

- **`agents.list` still shows terminated rows** → the v1.0.606
  default-hide isn't applied. Either the hub on the test bed is
  pre-v1.0.606 or the request lost the `live=true` flag through
  the MCP layer. Verify the rendered tool args via the hub's
  audit log.
- **`a2a.cards.list` returns empty** → no host-runner has
  pushed cards yet. Check `--a2a-addr` (must not be `disabled`)
  and the host-runner logs for `a2a cards published count=N
  hash=...`.
- **Card URLs point at the worker host, not the hub relay** →
  `s.publicBase` is returning the wrong base URL. Operator
  needs to set `--public-url` so off-box clients resolve the
  cards correctly.
- **`agents.terminate` succeeds but row stays `running`** →
  host-runner didn't pick up the terminate command. The agents
  table will show `pause_state='terminating'`; the row stays
  there until the host-runner's command queue drains. Inspect
  host logs.

---

## Scenario 19 — Template proposal end-to-end (`templates.propose` + director preview + approve)

**The agent-authored template flow.** A worker proposes a new
template (e.g. a tweak to a worker prompt). The hub raises a
`template_proposal` attention item. The director sees the
v1.0.602 preview block — proposed YAML body, rationale,
status chip (NEW / revise / no change) — and approves. The
blob lands on disk under `team/templates/<cat>/`.

Closes the "approve sight-unseen" gap that motivated the
v1.0.602 preview wedge.

**Pre-condition:** any live steward or worker that the
director's overlay/chat can address. Project steward is
easiest — Scenario 7.5 covers the spawn.

**Steps:**

1. In the steward's overlay chat, type: `Propose a new prompt template under category=prompts named "demo-proposal.v1.md" with content: "You are a demo agent for {{principal.handle}}. Reply with a one-line greeting." Rationale: "smoke-test the proposal flow." Call templates.propose.`
2. Wait ~5 s for the attention to land.
3. Open the **Me** tab — a `template_proposal` attention card
   appears in the Approvals filter. Tap **Details**.
4. On the approval detail screen, verify the preview block
   above the action buttons.
5. Tap **Approve**.

**Expected:**

- Me-page card shows `template_proposal` chip, severity
  `minor`, summary `Template proposal: prompts/demo-proposal.v1.md — smoke-test the proposal flow`.
- Detail screen renders the preview block:
  - Header reads `Template proposal` with status chip = **NEW**
    (no existing template at that path).
  - `prompts/demo-proposal.v1.md` in mono on its own line.
  - `Proposed by` shows the steward's handle.
  - **Rationale** section shows `smoke-test the proposal flow.`
  - **Proposed body** renders the markdown content in a mono
    code block, scrollable.
- Approve → snackbar `Decision recorded: approve`, attention
  drops from the open list.
- A `team/templates/prompts/demo-proposal.v1.md` file lands on
  the hub data root (or run `templates.prompt.get
  name="demo-proposal.v1.md"` from any agent to verify).

**Variation: revise vs no-change chip.** Re-run the same
prompt with the SAME template body but a different name like
`coder.v1.md` (which already exists). On the detail screen the
status chip should read **no change** (body identical) or
**revise** (body differs) — depending on whether you tweaked
the content. Validates the diff hint logic in the preview
block.

**Failure modes:**

- **No `template_proposal` attention surfaces** — the
  `templates.propose` tool wasn't found in the agent's
  catalog. Check the MCP-bridge logs for `unknown tool:
  templates.propose`. v1.0.295 renamed it to `templates_propose`;
  the dispatcher accepts both as aliases.
- **Preview block renders but body is empty** — `downloadBlob`
  failed silently. Check the network tab for the
  `/v1/blobs/<sha>` request and the hub's `blobs` table for
  the sha.
- **Approve succeeds but no file lands on disk** — the
  attention-resolve code path that installs the template is
  broken (the `decide(approve)` handler is supposed to read
  the blob and PUT it under `team/templates/`). Inspect the
  hub's audit_events for an `attention.resolved` row + the
  follow-up `templates.create`/`templates.update`. If the
  follow-up is missing, file a hub follow-up.

---

## Scenario 20 — Interactive request from agent (`request_select` round-trip)

**The agent-asks, principal-answers loop.** Whole class of
attention kinds (`approval_request`, `select`, `help_request`,
`elicit`) share this shape — the steward asks the director a
question via MCP, the director answers from the Me-page card,
the steward's long-poll receives the verdict. Scenario 20
exercises the `request_select` (multi-option pick) path as
representative; the `approval_request` (yes/no) path is the
degenerate case.

**Pre-condition:** a live steward addressable from the
overlay chat. General steward is fine — `request_select` isn't
gated by the W9 project-binding rule.

**Steps:**

1. Open the steward's chat (Me FAB → tap into general
   steward, or any project steward).
2. Type: `Use request_select to ask me which of these three colors I prefer: ["red", "green", "blue"]. Wait for my answer, then say it back to me.`
3. Wait ~5 s for the attention card to land on the Me page.
4. Open the **Me** tab — a `select` attention should be in the
   Approvals filter. The Me-page card renders the three
   options as **inline buttons** (no Details drill-in needed
   — `select` is in the approvals filter and the per-option
   buttons are rendered directly).
5. Tap **green** (or any option).
6. Watch the steward chat — the steward should post a follow-up
   text bubble naming the chosen color within ~10 s.

**Expected:**

- Attention card title contains the question text. Three
  inline `OutlinedButton`s labelled `red`, `green`, `blue`
  plus a fourth `Reject` button.
- Tapping `green` → snackbar `Picked: green`, attention drops
  from the open list, audit row records `decision=approve,
  option_id=green`.
- Steward's session shows the verdict arrive (`request_select`
  long-poll resolves with `option_id=green`); the steward
  posts a final text bubble like `You picked green.`

**Failure modes:**

- **Card shows generic Approve/Reject instead of per-option
  buttons** → the `options` array isn't surviving the
  pending_payload round-trip. Inspect the
  `pending_payload_json` on the attention row.
- **Tap an option, attention resolves, but steward never
  follows up** → the steward's long-poll on
  `request_select` either timed out or isn't being awaited.
  Common cause: the steward called `request_select` and then
  bailed before reading the response. Re-prompt with explicit
  "wait for my answer."
- **`Reject` button does nothing distinct from `Approve`** →
  the inline action wiring is correct only when an
  `option_id` is passed. Reject without an option should record
  `decision=reject` (no option_id); the agent's long-poll
  resolves with the rejection.

**Why `request_select` not `approval_request`:** the select
shape exercises both the option-rendering code path and the
plain reject path in one scenario. An `approval_request`
scenario would be near-identical to Scenario 7.5's W7 host-
picker (which IS an approval round-trip — just framed as
project-steward materialization).

---

# ADR-027 M4 claude-code verifications (Scenarios 21-24)

The next four scenarios exercise the LocalLogTailDriver (ADR-027)
specific to **M4 claude-code spawns**. They're grouped here
because they share the same pre-condition (a claude-code agent
running on driving_mode M4) and the same failure-mode
diagnostic ladder (the hub's `per-spawn UDS gateway` logs +
the claude `~/.claude/projects/.../*.jsonl` tail).

**Pre-condition for 21-24:** a live project steward (or any
worker) spawned with `driving_mode: M4` against the
`backend.kind: claude-code` engine. The bundled
`steward.claude-m4.v1.yaml` template hardwires M4 — easiest
seed. Verify via `agents.get` returning `driving_mode: M4`
on the row.

Phase 2/3 adapters (gemini-cli / codex / kimi-code on their
own M4 paths) inherit the same wire shape; until they ship,
21-24 stay claude-only.

## Scenario 21 — Plan mode + ExitPlanMode → `plan_approval` card

**Goal:** confirm the plan_approval attention card renders
when claude's `ExitPlanMode` hook fires, and the principal's
verdict round-trips back to claude.

**Steps:**

1. In the M4 steward's overlay chat, type: `I want to enter plan mode. Make a plan to refactor a small file, then call ExitPlanMode when ready so I can review the plan.`
2. Wait up to **60 s** — claude warms, enters plan mode,
   produces a plan, calls `ExitPlanMode`.
3. Open the **Me** tab.

**Expected:**

- A `plan_approval` attention card appears in the Approvals
  filter. Body widget renders the plan text claude proposed
  (the `plan` field of the ExitPlanMode hook payload).
- Inline action buttons: **Approve plan** / **Reject plan**.
- Tap **Approve plan** → snackbar; attention drops from open
  list; claude's next session/prompt unblocks (visible as
  follow-up text in the steward's chat).
- Audit feed shows the `plan_approval` resolved decision.

**Failure modes:**

- **No attention surfaces** → ExitPlanMode hook didn't reach
  the host-runner's gateway. Tail
  `~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl` for
  an `ExitPlanMode` line; if present, the gateway socket
  isn't hooked. Check `hub/internal/hostrunner/launch_m4_locallogtail.go`
  log lines for `gateway listening on /tmp/termipod-host-*.sock`.
- **Attention surfaces but body is empty** → the
  `_PlanApprovalBody` widget didn't receive the `plan` field.
  Inspect `pending_payload_json` on the attention row.
- **Approve resolves attention but claude stays parked** →
  the hub's `attention.reply` long-poll path didn't fire the
  `mcp__termipod__permission_prompt` response back to claude.
  Verify the dialog_type discriminator was `plan_approval`.

## Scenario 22 — AskUserQuestion picker (operator-at-TUI)

**Goal:** verify the mobile-side surface for AskUserQuestion
hooks. **Inline picker on mobile is W8 follow-up** (deliberately
deferred per the ADR-027 D-amend-6 commit `7129dea`); today
the principal sees the request and the operator picks at the
TUI.

**Steps:**

1. In the M4 steward's chat, type: `Call AskUserQuestion to ask me to pick between options "Option A" and "Option B". Treat my response (which I'll pick from your TUI) as the answer.`
2. Wait ~5-10 s for the hook to fire.
3. Open the **Me** tab — an `approval_request` agent_event
   should be visible referencing AskUserQuestion. **No
   mobile picker.**
4. Switch to the host running claude-code (TUI). Use claude's
   keyboard picker to choose Option A.
5. Watch the steward chat for the follow-up.

**Expected:**

- Mobile sees the question text and the candidate options
  (read-only — no inline pick).
- TUI picker accepts the keystroke; the picker's
  `pickerDone` channel resolves.
- Steward posts a follow-up acknowledging the chosen option.
- Audit feed shows the in-process resolution (no
  `attention.resolved` row because mobile didn't act).

**Failure modes:**

- **No agent_event on mobile** → the `AskUserQuestion` hook
  payload didn't post to `agent_events`. Inspect the
  attentionclient logs.
- **TUI pick doesn't resolve** → operator-at-keyboard timing.
  The picker uses an in-process `pickerDone` channel; if
  Claude's TUI is unfocused, the keystroke goes nowhere.
- **Mobile shows the picker buttons (not deferred)** →
  someone shipped the W8 follow-up since the audit was
  written. Update this scenario and remove the "deferred" note.

## Scenario 23 — `/compact` → compaction card + Compact/Defer

**Goal:** confirm the PreCompact hook surfaces a typed
attention with both verdicts wired correctly.

**Steps:**

1. In the M4 steward's chat, type a long-running prompt that
   accumulates context (or just `/compact` in the TUI — same
   trigger).
2. Wait ~3-5 s for PreCompact to fire.
3. Open the **Me** tab.

**Expected:**

- A `compaction` (or `permission_prompt` with
  `dialog_type=compact`) attention card. Body widget
  (`_CompactionBody`) shows the compaction summary claude
  proposed.
- Two inline action buttons: **Compact** / **Defer**.
- **Compact** → claude proceeds with the compaction; chat
  shows the compacted state.
- **Defer** → claude resumes the prior conversation without
  compacting; the attention drops from open list with a
  decision of `defer` (or `reject` depending on wiring).

**Failure modes:**

- **Card shows generic Approve/Reject** → the dialog_type
  routing in `mcpPermissionPrompt` (W4) didn't stamp the
  right kind. Inspect the attention row's `kind` column.
- **Defer behaves like Compact (or vice versa)** → the
  decision/option_id contract isn't honored. Check the
  attention-reply path — the principal's verdict should arrive
  back at claude as the appropriate `permission` value.

## Scenario 24 — Idle / streaming pill clears on Stop hook (+ knob tuning)

**Goal:** confirm the mobile pill UI honors the LocalLogTailDriver's
Stop hook so users don't see a stuck "streaming…" indicator
after claude finishes its turn. Also tune the two timing knobs.

**Steps:**

1. In the M4 steward's chat, send any prompt that takes >2 s
   (e.g. `Write me a 200-word essay about kittens.`).
2. Watch the steward header / chat for an **idle/streaming
   pill** during generation.
3. Wait for the response to complete (claude posts the
   final text frame and the Stop hook fires).
4. Confirm the pill **clears** within ~1-2 s of the final
   text frame.

**Expected:**

- During streaming: pill shows `streaming…` (or the project's
  equivalent label).
- After final text: pill clears. Header reverts to the
  steward's idle state.

**Knob tuning (operator activity):**

- `hook_park_default_ms` defaults to **60000 ms** (60 s)
  — how long a hook payload may park without resolving
  before the driver gives up. If claude regularly hangs >60 s
  on legitimate work, bump this; if it's frequently parking
  unnecessarily, lower it.
- `idle_threshold_ms` defaults to **2000 ms** (2 s) — how
  long without a JSONL line constitutes "idle". On a slow
  machine 2 s may flap; bump to 3-4 s if the pill toggles
  visibly.
- Both knobs live in
  `hub/internal/hostrunner/launch_m4_locallogtail.go`
  (LocalLogTailDriver config struct). After tuning, restart
  the host-runner.

**Failure modes:**

- **Pill never appears** → the streaming-pill widget isn't
  reading the driver's idle/streaming state. Inspect the
  agent_events stream for `kind=lifecycle, state=streaming`
  vs `state=idle` rows.
- **Pill stays on after Stop** → Stop hook didn't reach the
  driver, OR the agent_events `state=idle` row isn't being
  published. Check the hook log payload schema in
  `docs/reference/claude-code-hook-schema.md` matches what
  the gateway emits.
- **Pill flaps during generation** → `idle_threshold_ms` is
  too tight relative to claude's actual inter-line latency.
  Bump it 1-2 s and observe.

**Why these specific verifications:** the four belong
together because they're the post-merge on-device checks called
out by ADR-027 plan §11. Mobile-side rendering ships in
v1.0.592 with the M4 driver; the actual verification was
deferred until users could run claude 2.1.x on a real host.

---

## Scenario 25 — Fleet shutdown via `hub-server shutdown-all` (ADR-028 Phase 1)

**Goal:** confirm `hub-server shutdown-all` stops every active
session on every live host, fires the `host.shutdown` verb so each
host-runner exits 0, and that `Restart=on-failure` leaves the hosts
DOWN until the operator manually starts them again. Sessions stay
at `paused` and remain resumable via the existing route.

**Pre-conditions:** the smoke-test fleet has at least **two hosts**
running under systemd (Track B install per
[`install-host-runner.md`](install-host-runner.md)), each with an
active steward session. The hub-server is reachable; `HUB_TOKEN`
holds an owner-scope bearer.

**Steps:**

1. From a third box (or the hub host itself), run:

   ```
   hub-server shutdown-all --reason "lifecycle-scenario-25"
   ```

2. Watch hub-server's stdout: it prints a per-host row showing
   `sessions_stopped`, `acked=yes`, and an empty error column.
3. On each affected host (e.g. `journalctl -fu termipod-host@<user>`):
   - Expect log line `host.shutdown received reason=lifecycle-scenario-25`.
   - Followed by `host.shutdown exiting code=0`.
   - Followed by systemd marking the unit `inactive (dead)` and **not**
     respawning it (`Restart=on-failure` only kicks on non-zero exits).
4. In mobile (or `GET /v1/teams/{team}/sessions`), confirm each prior
   active session shows `status=paused` rather than `active` or
   `closed`.
5. Inspect the audit log for the team:
   ```
   GET /v1/teams/{team}/audit?action=host.shutdown
   GET /v1/teams/{team}/audit?action=session.stop
   ```
   Each host gets one `host.shutdown` row (meta carries
   `sessions_stopped`, `force_kill`, `reason`, `acked`); each stopped
   session gets one `session.stop` row (meta carries the agent_id and
   the same reason); each terminated agent keeps its existing
   `agent.terminate` row for activity-feed continuity.
6. Bring the fleet back manually:
   ```
   sudo systemctl start termipod-host@<user>     # on each host
   ```
   Hosts heartbeat; the mobile sessions list shows **Resume** for
   each prior session. Tapping Resume re-spawns a fresh agent inside
   the same session (engine_session_id preserved when the engine
   supports it).

**Expected:**

- Each host's systemd unit exits 0 and stays `inactive (dead)` until
  the operator starts it again.
- `hub-server` itself stays up across the whole flow — never restarts
  (ADR-028 D-2 keeps hub out of the host-fleet exit loop).
- Sessions resume cleanly post-bringup; the chat AppBar shows
  Resume, not "session ended."
- `--force-kill` flag: re-run shutdown-all on one host with the flag
  and confirm the `audit_events` row's meta carries `force_kill=true`
  (visible behavior depends on the agent driver's SIGKILL handling).

**Failure modes:**

- **`acked=no` for a live host** → host-runner didn't respond within
  60s. Check the host's journald — most likely the tunnel long-poll
  was wedged. The hub-side rows still land (operator intent is what
  the audit cares about); just `systemctl restart` the host and
  re-run.
- **Host respawns after exit** → the unit's `Restart=` is set to
  `always` rather than `on-failure`. Fix the unit and re-test;
  ADR-028 D-2 requires `on-failure` so exit 0 is a true off.
- **Session ends up `closed`** → stopSessionInternal is being given a
  wrong status target. Inspect the `UPDATE sessions` in
  `hub/internal/server/stop_session.go` — it must set `status='paused'`.
- **Hub-server exited too** → the orchestrator confused itself for a
  host. Re-read ADR-028 D-2; hub-server's exit is gated only by its
  own `self-update` (Phase 2), never by `shutdown-all`.

**Why this scenario:** it's the smoke test for the
`hub-server shutdown-all` ship in v1.0.610. The exit-code-0 →
systemd-leaves-it-down contract is the load-bearing piece behind
Phase 2's `update-all` (which switches to exit 75 → systemd respawns
the new binary). Verifying the contract here catches the cliff
*before* operators run `update-all` on a real fleet.

---

## Scenario 30 — Spawn-with-task surfaces on the Tasks tab (ADR-029)

**Goal:** confirm that asking a project steward to spawn a worker
for a task materialises a row on the Tasks tab with the assignee /
assigner / time attribution, that the task auto-flips to `done` on
agent terminate, that `tasks.delete` drops a row created in error,
and that `tasks.update status='cancelled'` is sticky against the
auto-derive.

**Setup.** Land on a project's detail screen, ensure a project
steward is alive (Scenario 7.5 covers the spawn).

**Steps (happy path — inline-create + auto-derive):**

1. In the project steward's chat, send:
   `Please spawn a worker for me to "Investigate the loss spike",
   using the @critic.v1 template. Make it a real task on the
   Tasks tab.`
2. The steward calls `agents.spawn` with `task: {title:
   "Investigate the loss spike"}`. Confirm:
   - A 201 spawn response with `agent_id` + `spawn_id`.
   - `audit_events` table has a `task.create` row with
     `meta.source='spawn'`.
3. Open the project detail → **Tasks** tab. Confirm a tile reads:
   - title: "Investigate the loss spike"
   - status: `in_progress`
   - assignee chip: the new worker's handle
   - assigner attribution: the project steward's handle
   - "started 0m ago"
4. Send to the steward: `Terminate that worker — it's done.`
5. Steward calls `agents.terminate` (or `agents.patch
   status='terminated'`). Confirm on the Tasks tab the tile
   auto-flips to `done` with "done 0m ago". `audit_events` has a
   `task.status` row with `meta.source='spawn'`,
   `meta.from='in_progress'`, `meta.to='done'`.

**Coda 1 — `tasks.delete`:**

6. Ask the steward: `Actually that wasn't a real task. Please
   delete it.`
7. Steward calls `tasks.delete`. Confirm:
   - The tile disappears from the Tasks tab.
   - `audit_events` has a `task.delete` row.
   - The `agent_spawns` row that drove the task survives with
     `task_id` NULL (visible via `/v1/teams/{team}/agents/spawns`).

**Coda 2 — `cancelled` is sticky against auto-derive:**

8. Repeat steps 1-3 with a fresh inline task ("Investigate the
   memory regression").
9. Before the worker terminates, send to the steward:
   `Please cancel that task — we're not going to ship this.`
   Steward calls `tasks.update status='cancelled'`. Confirm
   the tile renders muted with a strikethrough title (Phase 2
   wedge W8 once shipped; pre-Phase 2 the tile still flips
   visually, just without the muted styling).
10. Now terminate the worker. Confirm the task **stays
    `cancelled`** — auto-derive must not overwrite it.
    `audit_events` has the `task.status` row with
    `meta.to='cancelled'`, **no follow-up `task.status` row**
    with `meta.to='done'`.

**Expected outputs:**

- `audit_events.action` values land for every transition:
  `task.create source=spawn`, `task.status source=spawn` (auto-
  derive), `task.delete`, `task.status source=steward`
  (cancel-override).
- The Tasks tab tile attribution matches the agent_spawns row's
  assignee + parent_agent_id.

**Failure modes:**

- **Tasks tab empty after spawn-with-task** → either
  `agent_spawns.task_id` wasn't stamped (check the migration ran),
  or the mobile tile isn't reading the linked task. Inspect the
  `/v1/teams/{team}/projects/{proj}/tasks` response for the
  freshly-created row.
- **Status auto-flip doesn't fire on terminate** → the W3
  `deriveTaskStatusFromAgent` helper isn't being invoked. The
  PATCH agent handler must call it on every status flip; check
  the handler logs.
- **`cancelled` gets overwritten by auto-derive** → the
  cancelled-is-sticky guard in `deriveTaskStatusFromAgent` is
  missing or the comparison is wrong.

**Why this scenario:** it's the end-to-end exercise of ADR-029
Phase 1. The four steps cover the four gaps the ADR closed
(spawn↔task linkage, status auto-derive, audit, cancelled
explicit-override). Mobile rendering of the triad lands in
Phase 2 — pre-Phase 2 the tile shows the same status flip but
without the assignee chip / assigner line / muted-cancelled
styling.

---

## Scenario 31 — Tasks tab triad + task detail surfaces (ADR-029 Phase 2)

**Goal:** confirm that after Phase 2 ships, the Tasks tab and Task
Detail screen render every ADR-029 attribution field, the worker
delivery + notification edges (W2.6–W2.9) are visible end-to-end,
and the linked-work navigation works.

**Pre-conditions:**

- Build under test is post-Phase-2 (Tasks tab `_TaskTile` extends
  to assignee chip + assigner + relative time per ADR-029 D-6).
- Project steward is live with a worker template installed
  (lit-reviewer.v1 / critic.v1 / coder.v1 work; their MCP allow-
  list now includes `tasks.complete`).
- Hub at version that includes the hub-side denormalized JOIN
  (W10) — `tasks.list` returns `assignee_handle`,
  `assignee_status`, `assigner_handle`, `started_at`,
  `completed_at`, `result_summary`.

**Steps:**

1. **Trigger a spawn-with-inline-task via the project steward.**
   Ask the steward something like *"Spawn lit-reviewer to survey
   the literature on retrieval-augmented decoding; depth=shallow."*
   The steward should call `agents.spawn` with both
   `parent_agent_id` (itself) and inline `task: {title, body_md}`.
2. **Open the project's Tasks tab.** Confirm a new tile appears
   with **all four** triad pieces rendering:
   - **Assignee chip** — `@<worker-handle>` with a green status pip
     while the worker is `running`.
   - **Assigner attribution** — "by @<project-steward-handle>" on a
     muted line beneath the title.
   - **Relative time** — "started <N>m ago" reading from
     `started_at`.
   - **No strikethrough** — title renders normally while
     `status='in_progress'`.
3. **Pull-to-refresh** the Tasks tab. The list reloads via the
   read-through cache; the freshly-spawned task may have moved
   tiles if status flipped in the meantime (W11).
4. **Tap the task tile.** The Task Detail screen opens.
5. **Verify the W9 surface elements:**
   - **Attribution block** (under the status/priority chips):
     assignee chip with status pip + "assigned by @<steward>" line +
     "started <N>m ago" line, all icons-prefixed.
   - **Linked-work pane**: "Worker session: <session-title>" with
     an **Open** button. Tap it → the receiver's `SessionChatScreen`
     opens, scrolled to the live tail.
   - **Activity timeline** (bottom): rows for `task.create`
     (source=spawn), `task.status` (todo → in_progress
     source=spawn).
6. **Wait for the worker to finish** and call `tasks.complete
   summary="..."`. The hub W2.8 stamps `result_summary`; W2.9
   fires a `task.notify` event into the steward's session.
7. **Confirm the W2.9 notification:** in the steward's chat
   surface, a `kind='task.notify'` event appears with the body
   "Task **<title>** in_progress → done." plus the result summary
   on a new line.
8. **Re-open the task detail screen.** Confirm:
   - Attribution block now shows "done <N>m ago" instead of
     "started <N>m ago".
   - Result summary panel renders below the metadata rows
     containing the worker's `tasks.complete` summary verbatim.
   - Activity timeline gained a `task.status` row (in_progress →
     done) and the latest entries are reverse-chronological.
9. **Cancel coda:** manually call `tasks.update
   status='cancelled'` from the steward's chat on a different
   task. In the Tasks tab, the cancelled task's title now renders
   **strikethrough + muted**, and the relative-time line reads
   "cancelled <N>m ago". The task detail screen's status chip row
   shows `cancelled` selected.

**Expected:**

- All triad fields populate from the hub-denormalized JOIN — no
  N+1 lookups on mobile.
- Worker session "Open" button always lands on the correct chat
  (matches by `assignee_id` against `sessionsProvider.active +
  .previous`).
- Audit timeline never empties on a transient network failure
  (`_audit` retains its prior value if `listAuditEventsCached`
  errors).
- Pull-to-refresh works in both populated and empty states.

**Failure modes:**

- **Assignee chip missing** → hub didn't denormalize. Check
  `handleListTasks` SELECT for the `LEFT JOIN agents ae` clause
  and confirm `tasks.list` returns `assignee_handle` in JSON.
- **"started <N>m ago" reads "started 0s ago" indefinitely** →
  `started_at` isn't being stamped at spawn time. The W1 migration
  added the column; the flip-on-spawn logic in `DoSpawn` should
  stamp it. Inspect the row directly: `SELECT started_at FROM
  tasks WHERE id=?`.
- **Linked-work pane reads "no live session"** even though the
  worker is alive → `sessionsProvider` cache is stale. The
  provider rebuilds on `hubProvider` invalidation; force a hub
  refresh and retry.
- **Activity timeline empty** → either the task was created
  pre-W4 audit (legacy data) or the client-side filter is wrong.
  Inspect `audit_events WHERE target_kind='task' AND
  target_id=<id>` directly.
- **Cancelled title is not strikethrough** → mobile is reading
  `status` from a stale tile payload. Pull-to-refresh; if still
  wrong, the `_TaskTile` `decoration` branch isn't reading the
  status string correctly.

**Why this scenario:** it's the end-to-end exercise of ADR-029
Phase 2. Phase 1 (W1–W7 + W2.6–W2.11) makes the data structure
right; Phase 2 (W8–W12) makes the user surface answer "who is
doing this, when did it start, what did they conclude" without
leaving the Tasks tab.

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
