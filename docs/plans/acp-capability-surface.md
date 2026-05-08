# ACP capability surface

> **Type:** plan
> **Status:** In progress (2026-05-08) — Phase 1 (W1.1–W1.4) shipped v1.0.410–v1.0.413; Phase 2/4 not yet started
> **Audience:** contributors
> **Last verified vs code:** v1.0.413

**TL;DR.** Implementation plan for ADR-021 (ACP capability surface).
Three phases scoped for MVP — Phase 1 (`session/load` + `authenticate`),
Phase 2 (mode + model picker, runtime via M1 / respawn via others),
Phase 4 (image content blocks across all content-array drivers).
Phase 3 (`fs/*` + `terminal/*` client capabilities) deferred to
post-MVP per ADR-021 D1. Total: 12 wedges across 3 phases (revised
upward from 8 after ADR-021's cross-engine amendment), each
independently shippable.

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

### Phase 1 — Resume + authenticate ✅ SHIPPED

All four wedges landed 2026-05-08 across versions v1.0.410–v1.0.413.
Killing and respawning a gemini M1 steward now reattaches to the
prior conversation when the agent advertises `loadSession`, falls
back cleanly to `session/new` on stale cursors, dispatches
`authenticate(methodId=oauth-personal)` by default for gemini-cli,
and surfaces auth failures as typed `attention_request` events
instead of silent hangs. Mobile drops replay duplicates so the
transcript reads continuous across the restart.

#### W1.1 — Persist + capture ACP `session/new` cursor ✅ v1.0.410

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

#### W1.2 — `session/load` on respawn ✅ v1.0.411

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

#### W1.3 — Mobile dedupe for `replay:true` ✅ v1.0.412

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

#### W1.4 — `authenticate` after `initialize` ✅ v1.0.413

Driver-side. After `initialize` returns and we see non-empty
`authMethods`, decide whether auth is needed:

- If the agent's `initialize` response sets
  `requiresAuthentication: true` (or, in its absence, if
  `session/new` returns an `auth_required`-shaped error), call
  `authenticate` before retrying.
- Method selection: precedence as ADR-021 D3 — template-declared
  override, then family-default (**`oauth-personal` for
  gemini-cli**), then first non-interactive method as fallback.

The default targets the single-user-developer path: `gemini auth`
once on the host, after which `~/.gemini/oauth_creds.json` carries
cached tokens that the daemon uses without opening a browser.
Service-account / shared-host deployments override via
`auth_method: gemini-api-key` (or `vertex-ai`) on the steward
template; no host-runner env diff required.

If the chosen method is interactive AND the agent has no cached
creds (first run, expired refresh, keychain unreachable), fail
fast with a typed `attention` event so the principal sees the
auth-method options and can pick one (or run `gemini auth` on the
host and retry).

**Files:** `driver_acp.go` (auth branch + retry-after-auth-fail
ladder), `agent_families.yaml` (`gemini-cli` family default
`auth_method: oauth-personal`), `templates/agents/
steward.gemini.v1.yaml` (`auth_method:` field — optional, falls
through to family default when omitted).

**Tests:** (a) `authMethods` empty → auth skipped. (b) Default
picks `oauth-personal` for gemini-cli when no template override
is set. (c) Template `auth_method: gemini-api-key` overrides the
default. (d) Interactive method without cached creds → `attention`
event with method options.

**Done when:** a gemini M1 agent on a host with cached OAuth creds
authenticates cleanly without GEMINI_API_KEY in env. A host
without cached creds produces a clear attention-driven failure
mode rather than the silent hang of pre-v1.0.402.

**Version:** v1.0.413.

### Phase 2 — Mode + model picker (cross-engine, capability-routed)

ADR-021 D4 (amended) splits Phase 2 into one cross-engine input
contract + per-engine routing branches. Mobile UI is identical
across families; only the wire path differs.

#### W2.1 — `runtime_mode_switch` family declaration + hub routing

Hub-side. Each entry in `agent_families.yaml` declares one of
`runtime_mode_switch: rpc | respawn | per_turn_argv | unsupported`.
The hub's `POST /agents/{id}/input` accepts new input kinds
`set_mode` and `set_model` and routes them based on the active
agent's family declaration:

- `rpc` → forward to the driver (W2.2).
- `respawn` → enqueue a respawn with the current spec mutated;
  hub stops the agent and starts a fresh one with new flags.
  Conversation continuity rides on `engine_session_id` (W1.1).
- `per_turn_argv` → stash on the driver as `NextTurnMode` /
  `NextTurnModel`; applied to the next subprocess argv (W2.4).
- `unsupported` → 422 with a typed error mobile renders as
  "this engine doesn't support runtime switching."

**Files:** `agent_families.yaml` (declarations: claude=respawn,
codex=respawn, gemini-cli=rpc, gemini-cli-exec=per_turn_argv),
`handlers_agent_input.go` (accept set_mode/set_model + routing
switch), `runner.go` (respawn-with-spec-mutation helper).

**Tests:** (a) each family routes to the right path. (b) invalid
mode_id for `rpc` family → 404. (c) `unsupported` family → 422.

**Version:** v1.0.420.

#### W2.2 — `session/set_mode` + `session/set_model` ACP driver dispatch

Driver-side, M1-only. `ACPDriver.Input` gains two cases that map to
ACP RPCs against the cached sessionId. Validated against the
cached `availableModes` / `availableModels` from `session/new` /
`session/load`.

**Files:** `driver_acp.go`. Two driver tests covering the dispatch
+ unknown-id validation.

**Done when:** ACP M1 routing path lights up — gemini agent flips
mode without spawning a new process.

**Version:** v1.0.421.

#### W2.3 — Respawn-with-mutated-spec for claude/codex

Hub-side, non-ACP path. The `respawn` branch from W2.1 needs a
helper that:

1. Reads the active spawn spec.
2. Mutates `backend.cmd` — e.g. replaces `--model claude-3-5-sonnet`
   with `--model claude-3-7-opus`.
3. Calls `pause` on the agent (clean stop).
4. Calls `spawn` with the mutated spec.
5. The new agent re-attaches to the same session row via the
   resume cursor (ADR-014); transcript stays continuous.

**Files:** `runner.go` (`respawnWithSpecMutation`),
`steward_template_mutator.go` (new — small string-edit helper for
the common `--model X` and `--permission-mode X` flag forms;
falls back to a typed error when the spec doesn't have the
expected flag shape so we don't corrupt unfamiliar templates).

**Tests:** (a) claude `--model` flag mutation. (b) codex
`--approval-policy` flag mutation. (c) no-op when the flag isn't
in the spec → return typed error rather than respawn-with-no-change.

**Done when:** mobile picker selection on a claude steward
respawns the agent with the new model and the next prompt uses
the new model.

**Version:** v1.0.422.

#### W2.4 — `NextTurnModel` / `NextTurnMode` for gemini-exec

Driver-side, exec-per-turn-only. `ExecResumeDriver` gains two
fields that the next `runTurn` consults when building argv. No
in-process handshake needed — gemini already spawns fresh per turn.

**Files:** `driver_exec_resume.go` (fields + argv splice).

**Tests:** (a) NextTurnModel set → next argv has `--model X`.
(b) NextTurnModel cleared after one turn (sticky behavior is a
follow-up).

**Done when:** mobile picker selection on a gemini exec-per-turn
agent applies on the next prompt without restarting.

**Version:** v1.0.423.

#### W2.5 — Mobile mode + model picker UI

Mobile-side, cross-engine. Read the available lists from the
agent's most recent `current_mode_update` / `current_model_update`
notifications (already captured as `kind=system` per v1.0.403).
Render two small chips in the steward header that open a
bottom-sheet picker on tap. Tapping an option fires `set_mode` /
`set_model` to the hub; the routing branch decides what happens.

Latency feedback differs by family: `rpc` → instant; `respawn` →
a "respawning…" spinner over the agent strip until the new
lifecycle.started arrives; `per_turn_argv` → a small "applies on
next turn" hint.

**Files:** `agent_feed.dart` (header chips + picker sheet),
`hub_client.dart` (input wrappers), one widget test per family
behavior.

**Done when:** mobile users can flip mode/model on any of the
four engine paths from the steward header.

**Version:** v1.0.424 (APK rebuild).

### Phase 4 — Image content blocks (cross-engine)

ADR-021 D5 (amended) makes image inputs land per-driver, not just
M1. One cross-engine hub input shape, four driver-specific
mappings.

#### W4.1 — Hub input contract for `images: []`

Hub-side. `POST /agents/{id}/input` accepts a new optional
`images: [{mime_type: "image/png", data: "<base64>"}]` field
alongside the existing `body`. Validation:

- mime_type in allowlist (`image/png`, `image/jpeg`, `image/webp`,
  `image/gif`).
- data is valid base64 and decodes to ≤5 MiB per image.
- Up to 3 images per request (gemini per-prompt limit; the lower
  bound across our engines).

Field is plumbed through to `Driver.Input` payload alongside
`body`. Drivers that don't know about images ignore the field
(forward-compatible).

**Files:** `handlers_agent_input.go` (validation + plumbing),
`hub_client.dart` (Dart-side request shape — but no UI yet; UI
lands in W4.5).

**Tests:** (a) valid images → 201 + plumbed to driver. (b) bad
mime type → 400. (c) >5 MiB → 400. (d) >3 images → 400.
(e) malformed base64 → 400.

**Version:** v1.0.430.

#### W4.2 — Claude (StdioDriver) image content blocks

Driver-side. `buildStreamJSONInputFrame`'s `text` branch becomes a
content-array builder that inserts image blocks ahead of the text:

```json
{ "type": "image",
  "source": {"type": "base64", "media_type": "<mime>", "data": "<b64>"} }
```

Capability gate: claude's `system/init` frame includes
`anthropic-vision` (or model-implied — Claude Sonnet 4+ all
support vision). If the active model doesn't, strip + emit
`kind=system` warn event.

**Files:** `driver_stdio.go` (content-array builder), test for
the wire shape.

**Version:** v1.0.431.

#### W4.3 — Codex (AppServerDriver) image content blocks

Driver-side. `startTurn`'s `input: [...]` array gains image
blocks in OpenAI responses-API shape:

```json
{ "type": "input_image",
  "image_url": "data:<mime>;base64,<b64>" }
```

Capability gate: codex's app-server returns vision support in its
init capabilities — exact field name verified in implementation.

**Files:** `driver_appserver.go` (startTurn signature accepts
`images []` and inserts blocks), one test.

**Version:** v1.0.432.

#### W4.4 — ACP (ACPDriver) image content blocks

Driver-side. `Input("text")`'s prompt-array build inserts ACP
image blocks ahead of text:

```json
{ "type": "image", "mimeType": "<mime>", "data": "<b64>" }
```

Capability gate: `agentCapabilities.promptCapabilities.image`
from the cached `initialize` response (already parsed).

**Files:** `driver_acp.go` (prompt-array builder), one test.

**Version:** v1.0.433.

#### W4.5 — gemini-exec (ExecResumeDriver) capability-gate strip

Driver-side. exec-per-turn passes the prompt as `gemini -p
"<text>"` argv with no inline-image affordance. The driver:

- Strips images from the input payload.
- Emits one `kind=system` event per stripped image:
  `payload={engine: "gemini-exec", reason: "no inline image
  support — switch to gemini M1 (--acp) for multimodal turns"}`.
- Lets the text portion proceed normally.

**Files:** `driver_exec_resume.go` (strip + warn), one test.

**Version:** v1.0.434.

#### W4.6 — Mobile image-attach UI

Mobile-side. The composer already has an attach button that
currently routes images to fs upload. Add a second branch: when
the active agent's family declares image-input support (a new
`prompt_image: true` flag on the family entry, populated by W4.2-
W4.4), the attach button inlines the image into the next prompt's
`images` field instead of uploading to fs.

UI: a small thumbnail strip above the text field, removable taps,
capped at 3 images per prompt. Compression: existing
`flutter_image_compress` path, target 1024px max dimension and
70% quality. Base64 the result and send.

**Files:** `agent_compose.dart` (attach branch), `hub_client.dart`
(image field on input — partially done in W4.1), one widget test.

**Done when:** tapping attach → picking a screenshot → typing
"what's in this?" → sending lands a multimodal turn that the
agent describes. Verified across claude, codex, and gemini M1.
Gemini M2 (exec-per-turn) shows the warning chip.

**Version:** v1.0.435 (APK rebuild).

---

## 4. Phase order, dependency graph

```
Phase 1:  W1.1 → W1.2 → W1.3
                W1.4 (independent)

Phase 2:  W2.1 → W2.2 (M1 RPC path)
                W2.3 (claude/codex respawn — independent of W2.2)
                W2.4 (gemini-exec per-turn argv — independent)
          W2.5 (mobile UI; needs ≥1 of W2.2/W2.3/W2.4 to demo)

Phase 4:  W4.1 → W4.2, W4.3, W4.4 (each engine independent)
                 W4.5 (gemini-exec strip — independent)
          W4.6 (mobile UI; needs W4.1 + ≥1 of W4.2/W4.3/W4.4)
```

Three phases, each with a fan-out of per-engine wedges that can
ship in parallel. Phase 1 chain has the longest critical path
(three sequential wedges); Phase 2 and 4 are mostly fan-out so
they ship faster once the contract wedge (W2.1 / W4.1) lands.

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

**Phase 2 happy paths (one per engine path).**

*M1 RPC (gemini --acp):* Pick `yolo` from chip → chip flips within
~1s → next tool call auto-approves.

*Respawn (claude / codex):* Pick a different model from chip →
"respawning…" spinner over the steward strip for ~3-5s →
new lifecycle.started arrives → next prompt uses the new model.
Transcript continuity preserved by `engine_session_id` resume.

*Per-turn argv (gemini exec-per-turn):* Pick a different model →
"applies on next turn" hint shows briefly → next prompt's
gemini subprocess invokes with the new `--model` flag.

**Phase 4 happy paths.**

*Claude:* Attach → pick screenshot → "describe this" → send →
claude's response references the image content correctly.

*Codex:* Same flow → codex describes the image.

*Gemini M1:* Same flow → gemini-cli (ACP) describes the image.

*Gemini M2 (exec-per-turn):* Attach → pick screenshot → send →
text portion goes through; warning chip appears: "this engine
doesn't support inline images — switch to gemini M1 for
multimodal turns."

---

## 7. References

- ADR-021 — design decision; this plan implements it.
- ADR-014 — `sessions.engine_session_id` precedent for resume.
- ADR-013 (amended 2026-05-07) — gemini-cli ACP family entry.
- ADR-011 — `request_approval` / attention surface for the auth
  failure path in W1.4.
- `spine/blueprint.md` §5.3.1 — M1/M2/M4 mode taxonomy.
- `feedback_post_mvp_sandbox` — Phase 3 deferral context.
