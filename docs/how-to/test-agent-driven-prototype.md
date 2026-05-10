# Test the agent-driven mobile prototype

> **Type:** how-to
> **Status:** Current (2026-05-10)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.464

**TL;DR.** Step-by-step QA walkthrough for the v1.0.464-alpha
agent-driven mobile prototype: a persistent floating steward
overlay that can navigate the app to any in-app destination. Read-
only verbs at this stage — the steward navigates, you tap to edit
or approve. Follow each scenario in order; each one isolates a
specific layer (overlay shell, ensure-spawn, URI dispatch, intent
SSE) so when something fails you know what to report.

The wedge spans
[ADR-022](../decisions/022-observability-surfaces.md)-adjacent code
plus the agent-driven UI discussion at
[`discussions/agent-driven-mobile-ui.md`](../discussions/agent-driven-mobile-ui.md).
Test scope = §7 of that doc, **read-only verbs only**.

---

## Pre-conditions

Set up before testing:

1. **Hub running** with a current build (≥ v1.0.464-alpha).
2. **Phone or emulator** with the v1.0.464-alpha APK installed.
3. **At least one host registered** — Hosts tab shows at least
   one row with status `connected`. (The general steward needs
   a host to spawn on. Two hosts is ideal to match the demo
   script in `agent-driven-mobile-ui.md`, but one is enough for
   most scenarios below.)
4. **Team configured** — Settings → Hub shows a team id.
5. **Active connection** — the Hosts tab is not empty and the
   hub indicator shows green.

If any of the above is missing, complete it before running the
scenarios. The overlay does not appear when the hub is
unconfigured — that is by design.

---

## Scenario 1 — overlay shell appears and is draggable

**Goal:** confirm the overlay puck is mounted at the app root and
persists across all five tabs.

**Steps:**

1. Open the app. After the hub config loads, a teal circular
   puck (steward avatar) should appear near the bottom-right of
   the screen, above the bottom navigation bar.
2. Switch tabs: Projects → Activity → Me → Hosts → Settings.
   The puck should stay visible on every tab in the same place.
3. Push a sub-route: tap a project on the Projects tab to open
   Project Detail. The puck stays visible.
4. Drag the puck around with your finger. It should move smoothly
   and stay where you released it. Try the corners of the screen
   — it should clamp inside the screen edges.

**Expected:** puck visible on every tab + every pushed route, drag
works, position survives tab switches.

**Failure modes to look for:**
- Puck missing on one tab → overlay-host wrapping is broken.
- Puck appears but isn't draggable → gesture handling is wrong.
- Puck flickers or disappears on push → overlay is mounted per-
  route instead of at root.
- Puck overlaps an essential UI control unrecoverably → drag
  clamping is misconfigured.

---

## Scenario 2 — open the chat panel, see the steward connect

**Goal:** confirm the panel opens, the controller calls
`ensureGeneralSteward`, and the SSE stream attaches.

**Steps:**

1. Tap the puck. A half-height chat panel should slide up from
   the bottom with a "Steward" header + close (×) button + an
   empty transcript area + an input field at the bottom.
2. Watch for the spinner. The first time you open the panel on
   a fresh team, the controller calls `POST /steward.general/ensure`
   which may spawn a new general steward if none exists. Allow
   ~5–15 seconds.
3. Once connected, the spinner is replaced by an empty-state
   message ("Tell the steward what you want to see…").

**Expected:** panel opens, spinner appears briefly, empty-state
prompt shows.

**Failure modes:**
- Panel never opens → puck tap handler dropped.
- Spinner spins forever → ensure-spawn 4xx/5xx; check hub logs
  for `mobile/intent` or `steward.general/ensure` errors.
- Panel opens but shows "Hub not configured" error → the host
  precondition wasn't met.
- Panel opens but the empty-state never replaces the spinner →
  SSE attach failed; check hub `agent_events` stream URL.

---

## Scenario 3 — talk to the steward (no navigation yet)

**Goal:** confirm bidirectional text flow before exercising the
intent path.

**Steps:**

1. With the panel open, type into the input: `Hello, are you
   there?`
2. Tap send (or press the keyboard's enter). Your message should
   appear on the right side of the transcript with a teal-tinted
   bubble.
3. Wait for the steward's reply. Within 10–30 seconds, a
   left-aligned message with the steward's response text should
   appear in the transcript.

**Expected:** user message appears immediately on send; steward
reply streams in within ~30s.

**Failure modes:**
- Send button does nothing → input handler is wired wrong.
- User message appears but no reply ever lands → SSE not
  attached, or the steward agent isn't running, or the engine
  is hung.
- Reply appears but is duplicated multiple times → SSE replay
  window isn't being filtered correctly.

---

## Scenario 4 — single-segment navigations

**Goal:** confirm `mobile.navigate` round-trips for top-level tab
URIs.

For each line below, in sequence:

1. Type the prompt into the chat.
2. Wait for the steward to respond + navigate.
3. Confirm the app moved to the expected destination.
4. A brief snackbar at the bottom should read **Steward → \<label>**.
5. Note the result before moving to the next prompt.

| # | Prompt | Expected destination |
|---|---|---|
| 4a | "Take me to Projects" | bottom-nav highlights Projects (tab 0) |
| 4b | "Switch to the Activity tab" | bottom-nav highlights Activity (tab 1) |
| 4c | "Open Hosts" | bottom-nav highlights Hosts (tab 3) |
| 4d | "Go to Me" | bottom-nav highlights Me (tab 2) |
| 4e | "Open Settings" | bottom-nav highlights Settings (tab 4) |

**Expected:** each navigation lands on the named tab; transcript
gains a system row "Steward → \<tab>"; snackbar appears for ~2s.

**Failure modes:**
- Navigation happens but no snackbar → ScaffoldMessenger lookup
  failed; report which tab was the source.
- Snackbar appears but tab does not change → uri_router dispatch
  bug; capture the URI from the system row's footnote.
- Steward replies in text but never navigates → MCP tool not
  exposed or not invoked; check hub logs for `mobile.navigate`
  tool calls.

---

## Scenario 5 — push-route navigations (Insights view)

**Goal:** confirm `mobile.navigate` can push a fullscreen route,
not just switch tabs.

**Steps:**

1. Type: `Show me the steward insights view.`
2. Wait for response. The app should push the **Insights**
   screen (fullscreen, app bar reads "Insights", scope banner
   shows "Stewards · \<team-id>").
3. Tap the back button — you should return to the previous
   screen + the puck should still be visible.

**Variants to try in sequence:**

| # | Prompt | Expected destination |
|---|---|---|
| 5a | "Show steward insights" | InsightsScreen, scope=team_stewards |
| 5b | "Open the team insights view" | InsightsScreen, scope=team |
| 5c | After (5a or 5b): "Go back to projects" | Projects tab, Insights popped |

**Failure modes:**
- "Show insights" navigates to home but not Insights → URI
  resolved to `me` instead of `insights`; check the system row
  footnote.
- Insights screen pushes but fields are empty → scope id
  resolution failed (check qp.id / teamId fallback path).

---

## Scenario 6 — project deep navigation

**Goal:** confirm the steward can resolve a project name → URI
→ Project Detail push.

**Pre-condition:** at least one project exists. If none, run the
research-demo seed first (`make seed-demo`) or create a project
manually before this scenario.

**Steps:**

1. Type: `What projects do I have?`
2. Steward should list the projects (text only, no navigation
   yet).
3. Pick one — say its name is "Foo". Type: `Open project Foo.`
4. The app should push Project Detail for that project. Snackbar
   reads "Steward → Project: Foo".

**Failure modes:**
- Steward navigates to wrong project → its prompt's project-list
  step didn't run, or it picked by partial match. Report the
  prompt + the project list it had.
- "Project not found in cache" toast → mobile's hub state
  doesn't have this project loaded. Pull-to-refresh on Projects
  tab and retry.

---

## Scenario 7 — overlay collapse during steward action

**Goal:** confirm intents fire even when the chat panel is
collapsed (puck-only).

**Steps:**

1. Open the panel.
2. Type: `In about 20 seconds, take me to the Activity tab.` (or
   say "wait 15 seconds then go to activity") — the steward's
   replies are non-deterministic, so use a phrasing that gets a
   delayed action.
3. Tap × on the panel to collapse it back to puck mode.
4. Wait for the steward's delayed action.

**Expected:** the navigation still fires (Activity tab opens) and
a snackbar appears even though the chat panel is closed.

**Failure modes:**
- Navigation never fires while collapsed → SSE listener is in
  the chat widget instead of the controller; intents are dropped
  on collapse.
- Snackbar doesn't appear → ScaffoldMessenger context resolves
  to a stale one.

---

## Scenario 8 — invalid URI graceful failure

**Goal:** confirm the dispatcher handles unknown shapes without
crashing.

**Steps:**

1. Type: `Navigate me to a page that doesn't exist.`
2. The steward may attempt a URI that the v1 dispatcher doesn't
   know — or may decline.
3. Check that the app does **not** crash, hang, or freeze.

**Expected:** either the steward declines politely (text-only
response), or it fires an unknown URI and the chat shows a
"Steward could not navigate to \<uri>" system row. Either is
acceptable; what's not acceptable is a crash or no feedback.

---

## Scenario 9 — multi-host (foldable / tablet view)

**Goal:** efficiency check on the multiplexing-screen claim from
the discussion doc.

**Pre-condition:** running on a foldable phone unfolded, or a
tablet, with two hosts registered.

**Steps:**

1. Open the panel.
2. Type: `Show me both my hosts.`
3. Steward should navigate to Hosts (single navigate; can't show
   "both" simultaneously yet — that's a future verb).
4. Type: `Now show me the insights view.`
5. Steward navigates to Insights with hosts visible by tab below
   in the IndexedStack.

**This scenario is the manual-vs-agent-driven efficiency anchor.**
On a manual run, the same five tab transitions take ~10 taps and
~30s. On the agent-driven run, you should hit the same five
destinations in 5 spoken/typed lines, ~60-90s, but with hands
free. Note your timings — the prototype's success criterion is
the user-perceived efficiency win.

---

## Scenario 10 — write attempts must NOT succeed

**Goal:** confirm the read-only constraint holds. The prototype
must not let the steward edit, approve, ratify, or spawn anything.

**Steps:**

1. Type: `Approve the latest attention item for me.`
2. Type: `Create a new project called Bar.`
3. Type: `Ratify the first deliverable.`

**Expected:** for each, the steward responds in text but does
**not** mutate state. The Activity tab does not show new audit
events for these actions (other than the `mobile.intent` audit
rows, which only record navigations).

**Failure modes:**
- Write occurs → bug. Per the discussion doc this is post-
  prototype; the steward template must not call write tools at
  this stage.
- Steward claims it did the action but didn't → cosmetic bug in
  the prompt (the steward over-promised); report so we can
  tighten the system prompt.

---

## What to capture when reporting

For any failure, please include:

- **Scenario # + step #** that failed.
- **Exact prompt** you typed.
- **Steward's text reply** (copy from the chat).
- **Snackbar content** (if any) when navigation fired.
- **System row footnote** in the chat (the URI the steward
  tried to navigate to). Copy verbatim.
- **Hub logs** from the time of the failure if you have shell
  access. Look for `mobile.intent` or `steward.general/ensure`
  lines.
- **Screen recording** if the failure mode is visual/timing.

The intent URI in the system row is the highest-leverage piece —
it tells us whether the steward picked the right destination
(grammar issue) or the dispatcher mis-routed it (router issue).

---

## Known limitations of the v1 prototype

- **Read-only.** No edits, approvals, ratifications, or spawn
  actions yet. By design.
- **No state digest mobile → steward.** The steward does not see
  what page you're currently on; it speaks blind. If you ask
  "open the next one," the steward has no context.
- **Single (general) steward only.** Domain stewards inside a
  project are not bound to the overlay yet.
- **Position resets on app restart.** The puck moves back to
  bottom-right on every cold start (no shared_preferences yet).
- **Voice via system IME only.** Tap the microphone on your
  keyboard; the chat input is just text.

These are known + documented in
[`discussions/agent-driven-mobile-ui.md`](../discussions/agent-driven-mobile-ui.md)
§5 (open questions for ADR-023).

---

## References

- [Discussion — agent-driven mobile UI](../discussions/agent-driven-mobile-ui.md)
- [Glossary — steward](../reference/glossary.md#steward)
- [Glossary — general steward](../reference/glossary.md#general-steward)
- [How-to — install host-runner](install-host-runner.md)
- [How-to — install hub server](install-hub-server.md)
