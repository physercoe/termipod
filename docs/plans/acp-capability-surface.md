# ACP capability surface

> **Type:** plan
> **Status:** Proposed (2026-05-08)
> **Audience:** contributors
> **Last verified vs code:** v1.0.405

**TL;DR.** Implementation plan for ADR-021 (ACP capability surface).
Three phases scoped for MVP — Phase 1 (`session/load` + `authenticate`),
Phase 2 (`session/set_mode` + `session/set_model`), Phase 4 (image
content blocks). Phase 3 (`fs/*` + `terminal/*` client capabilities)
deferred to post-MVP per ADR-021 D1. Total: 8 wedges across 3 phases,
each independently shippable.

---

## 1. What we have today (v1.0.405 baseline)

| Surface | State |
|---|---|
| `ACPDriver` outbound methods | `initialize` · `session/new` · `session/prompt` · `session/cancel` |
| `ACPDriver` inbound handlers | `session/update` (10 subtypes) · `session/request_permission` |
| `ACPDriver` cap awareness | None — `agentCapabilities` from `initialize` response is parsed but not stored or consulted |
| Resume cursor column | `sessions.engine_session_id` (ADR-014) — populated for claude only |
| Steward template auth declaration | None — agents are assumed pre-authenticated |
| Mode / model declaration in templates | `Yolo: true` flag forces yolo at spawn time; no runtime switch |
| Mobile prompt input shape | Text-only: `body: <string>` |
| Mobile image attach | Exists for chat composer; routes to fs upload, NOT into `session/prompt` content array |

What this gives us: a working M1 path that handshakes and runs single-
session turns. What's missing is everything that lets the agent be
*used continuously*: resume, auth selection, mid-session controls,
non-text inputs.

---

## 2. Vocabulary

- **ACP capabilities** — the JSON object the agent returns in
  `initialize.agentCapabilities` declaring what it supports.
  Persisted in driver memory after handshake.
- **Capability gate** — a runtime check before dispatching an
  optional method (`d.capCheck("loadSession")`). Returns false if
  the cached capabilities don't include the feature; the driver
  takes the fall-back branch instead of erroring.
- **Replay event** — a `session/update` notification arriving in
  response to `session/load`'s history catch-up rather than from
  live agent activity. Tagged `replay: true` in the resulting
  `agent_event` payload so mobile renderers can dedupe.
- **Phase** — a logically independent slice. Phases ship as
  independent versions; nothing in a later phase depends on a
  later phase's runtime behavior (only on the same architectural
  shape).

---

## 3. Wedges

Eight wedges across three phases. Sized so each is a 1-2 day push and
ships its own version bump.

### Phase 1 — Resume + authenticate

#### W1.1 — Persist + capture ACP `session/new` cursor

Hub-side. Extend `captureEngineSessionID`
(`hub/internal/server/handlers_agent_events.go`) to recognize ACP
`session.init` events the same way it recognizes claude's. The M1
driver already emits `session.init` with `session_id` in the
lifecycle.started payload (driver_acp.go:230). Move it to a real
`session.init` event so the existing capture path picks it up.

**Files:** `driver_acp.go` (separate `session.init` from
`lifecycle.started`), `handlers_agent_events.go` (case-broaden the
capture to any `kind=session.init && producer=agent`), one migration
no-op (column already exists). One test: capture happens for
`kind=gemini-cli` agents and persists the UUID.

**Done when:** spawning a gemini M1 agent populates
`sessions.engine_session_id` exactly once per spawn.

**Version:** v1.0.410.

#### W1.2 — `session/load` on respawn

Driver-side. `ACPDriver.Start` checks if `d.ResumeSessionID` is
non-empty (set from `sessions.engine_session_id` by `launch_m1.go`).
If yes AND `agentCapabilities.loadSession` was true on prior runs
(family-default for gemini-cli; cached on the family entry), call
`session/load` instead of `session/new`. On error, log the failure
mode and fall back to `session/new` so the user still gets a
session, just a fresh one.

**Replay handling:** `session/load` sends a stream of historical
`session/update` notifications. The driver tags them with `replay:
true` in the emitted `agent_event` payload. `_collapseStreamingPartials`
on mobile is unaffected (events still chain by `message_id`); the
new flag is for downstream cache-dedupe.

**Files:** `driver_acp.go` (Start branch + replay flag), `launch_m1.go`
(plumb `ResumeSessionID` through from `agents.thread_id_json`), new
field on `ACPDriver`.

**Tests:** (a) Start with `ResumeSessionID` set calls `session/load`,
not `session/new`. (b) `session/load` failure falls back to
`session/new`. (c) Replay-tagged events round-trip the flag intact.

**Done when:** killing and respawning a gemini M1 steward returns
to the same conversation thread; the agent's first reply
acknowledges prior turns.

**Version:** v1.0.411.

#### W1.3 — Mobile dedupe for `replay:true`

Mobile-side. Offline-snapshot cache for the agent's transcript
already carries the events from before the restart. After restart,
the `session/load` replay re-emits them. Without dedupe the user
sees every event twice.

In `agent_feed.dart`'s event ingest, drop incoming events flagged
`replay:true` whose `(producer, kind, message_id, seq)` tuple is
already present in the visible list. Cheap: events are already
sorted by `seq`, so the dedupe is an O(log n) lookup against a
sorted view.

**Files:** `agent_feed.dart` (ingest filter), one widget test.

**Done when:** a resume with cached transcript shows no duplicate
bubbles.

**Version:** v1.0.412 (forces APK rebuild — first APK-touching
wedge of the plan).

#### W1.4 — `authenticate` after `initialize`

Driver-side. After `initialize` returns and we see non-empty
`authMethods`, decide whether auth is needed:

- If the agent's `initialize` response sets
  `requiresAuthentication: true` (or, in its absence, if
  `session/new` returns an `auth_required`-shaped error), call
  `authenticate` before retrying.
- Method selection: precedence as ADR-021 D3 — template-declared,
  then family-default, then first non-interactive.

If the chosen method is interactive AND the agent has no cached
creds, fail fast with a typed `attention` event so the principal
sees the auth-method options and can pick one (or set
`GEMINI_API_KEY` and retry).

**Files:** `driver_acp.go` (auth branch), `agent_families.yaml`
(`gemini-cli` default-method declaration), `templates/agents/
steward.gemini.v1.yaml` (`auth_method:` field, optional).

**Tests:** (a) `authMethods` empty → auth skipped. (b) Method
selection precedence honored. (c) Interactive method without
cached creds → `attention` event with method options.

**Done when:** a gemini M1 agent without `GEMINI_API_KEY` and
without DBus access produces a clear attention-driven failure
mode rather than the silent hang of pre-v1.0.402.

**Version:** v1.0.413.

### Phase 2 — Mode + model picker

#### W2.1 — `session/set_mode` + `session/set_model` driver dispatch

Driver-side. Two new input kinds accepted by `ACPDriver.Input`:

- `kind=set_mode`, payload `{mode_id: <string>}` → calls
  `session/set_mode` with the cached sessionId.
- `kind=set_model`, payload `{model_id: <string>}` → calls
  `session/set_model`.

Both validated against the cached lists from `session/new` /
`session/load` response. Unknown ids → driver returns a typed
error (mobile renders as a snackbar).

**Files:** `driver_acp.go` (dispatch), `handlers_agent_input.go`
(validation: accept the new kinds, require the matching id field).

**Tests:** (a) valid mode_id → outbound RPC. (b) unknown mode_id →
404 (mode not in availableModes). (c) post-success the next
`current_mode_update` notification arrives and confirms.

**Done when:** `curl POST /agents/.../input -d
'{"kind":"set_mode","mode_id":"yolo"}'` flips the running gemini
agent into yolo without restarting the spawn.

**Version:** v1.0.420.

#### W2.2 — Mobile mode + model picker UI

Mobile-side. Read the available lists from the agent's most recent
`session/update` notifications (we already capture them as
`kind=system, payload.sessionUpdate=current_mode_update`). Render
two small chips in the steward header — current mode, current
model — that open a bottom-sheet picker on tap.

Tapping an option fires the input from W2.1. Confirmation comes
back as the next `current_mode_update` / `current_model_update`
notification, which updates the chip.

**Files:** `agent_feed.dart` (header chips + picker sheet),
`hub_client.dart` (input wrappers), one widget test.

**Done when:** mobile users can flip a running gemini agent
between gemini-2.5-pro and gemini-3-flash-preview from the steward
header without restarting.

**Version:** v1.0.421 (APK rebuild).

### Phase 4 — Image content blocks

#### W4.1 — `session/prompt` heterogeneous content array

Driver-side. The text-only assumption hard-coded into Input("text")
becomes an array build:

```go
prompt := []map[string]any{}
for _, img := range payload["images"].([]any) { ... insert image block ... }
prompt = append(prompt, map[string]any{"type": "text", "text": body})
```

Capability-gated: if `promptCapabilities.image` is false on the
cached capabilities, the driver strips images and emits a
`kind=system` event noting the agent can't accept them. Mobile
shows a warning chip; the text portion still goes through.

**Files:** `driver_acp.go` (prompt-array builder),
`handlers_agent_input.go` (accept new `images: [{mime_type, data}]`
field, validate base64).

**Tests:** (a) `images` field in input shape produces an image
block ahead of text. (b) capability gate strips images when agent
doesn't support them. (c) malformed base64 → 400 from the hub
input handler.

**Done when:** a curl with an image base64 produces a multimodal
prompt that gemini reasons about.

**Version:** v1.0.430.

#### W4.2 — Mobile image-attach to prompt

Mobile-side. The composer already has an attach button that
currently routes images to fs upload. Add a second branch: when
the active agent's family supports `promptCapabilities.image`, the
attach button inlines the image into the next prompt's `images`
field instead of uploading to fs. UI: a small thumbnail strip
above the text field, removable taps, capped at 3 images per
prompt (gemini's per-prompt limit per the docs).

Compression: reuse the existing `flutter_image_compress` path,
target 1024px max dimension and 70% quality. Base64 the result and
send.

**Files:** `agent_compose.dart` (attach branch), `hub_client.dart`
(image field on input), one widget test.

**Done when:** tapping attach → picking a screenshot → typing
"what's in this?" → sending lands a multimodal turn that gemini
describes.

**Version:** v1.0.431 (APK rebuild).

---

## 4. Phase order, dependency graph

```
W1.1 → W1.2 → W1.3
              W1.4 (independent)

W2.1 → W2.2

W4.1 → W4.2
```

Three independent chains. Phase 1 chain has the longest
critical path (W1.1 must precede W1.2 must precede W1.3) but each
wedge is small.

Phase 2 and Phase 4 don't depend on Phase 1 — they could ship
first if priority shifts. Recommended order is 1 → 2 → 4 because
Phase 1's user-visible value (resume) is highest.

---

## 5. Out-of-scope (and why)

- **Audio content blocks.** No mobile capture infrastructure; no
  user demand. Driver tolerates them in case the input layer ever
  ships them, but no mobile UI work.
- **`fs/*` and `terminal/*` client capabilities (Phase 3).**
  Architectural scope creep — these change the host-runner from
  passive bridge to active tool host, and that intersects with
  the deferred sandbox/egress-proxy plan. Tracked but unscheduled
  per ADR-021 D1.
- **Browser-callback OAuth flow.** Interactive `oauth-personal`
  with no cached creds requires a callback URL the daemon can
  reach. We could proxy through mobile but that's a Phase 5
  concern; for MVP, interactive OAuth produces an attention event
  rather than a working flow.
- **Multi-engine ACP families beyond gemini-cli.** claude-code
  SDK ACP is the next obvious candidate but the SDK doesn't ship
  a stable ACP daemon yet (verified 2026-05-07). When it does,
  ADR-021 D6 says it lands as a steward template + family entry,
  no driver changes. Tracked in `feedback_no_short_board`.

---

## 6. Verification — end-to-end happy paths

For each phase, an operator-runnable scenario:

**Phase 1 happy path.**
1. Spawn a gemini M1 steward with `GEMINI_API_KEY` set.
2. Send "remember: my favorite color is blue".
3. Stop the steward (host-runner kills the agent).
4. Tap Resume.
5. Send "what's my favorite color?" → agent answers "blue".

**Phase 2 happy path.**
1. With a running gemini M1 agent in `default` mode.
2. Tap mode chip → pick `yolo`.
3. Confirmation chip flips to yolo within ~1s.
4. Send a prompt that triggers a tool call → no permission popup
   surfaces (yolo auto-approves).
5. Tap mode chip → flip back to `default`.
6. Next tool call → permission popup surfaces again.

**Phase 4 happy path.**
1. With a running gemini M1 agent.
2. Tap attach → pick a screenshot.
3. Thumbnail appears in composer.
4. Type "describe this" → send.
5. Agent's first text response references the image content
   correctly.

---

## 7. References

- ADR-021 — design decision; this plan implements it.
- ADR-014 — `sessions.engine_session_id` precedent for resume.
- ADR-013 (amended 2026-05-07) — gemini-cli ACP family entry.
- ADR-011 — `request_approval` / attention surface for the auth
  failure path in W1.4.
- `spine/blueprint.md` §5.3.1 — M1/M2/M4 mode taxonomy.
- `feedback_post_mvp_sandbox` — Phase 3 deferral context.
