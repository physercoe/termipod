# 021. ACP capability surface — resume, auth, mode/model, image inputs

> **Type:** decision
> **Status:** Accepted (2026-05-08) — Phase 1 shipped (v1.0.410–v1.0.413); Phase 2 shipped (v1.0.420–v1.0.424); Phase 4 pending
> **Audience:** contributors
> **Last verified vs code:** v1.0.424

**Amendment (2026-05-08).** Cross-engine survey after the original
draft showed Phases 2 and 4 are not actually M1-only: claude-code's
stream-json content array, codex's app-server `turn/start` input
array, and gemini-cli's ACP all accept image content blocks; only
gemini's `gemini -p` argv-only path can't carry them. Mode/model
runtime switching is M1-only at the protocol layer, but the mobile
picker UX should still be cross-engine — non-M1 engines route the
selection through "edit template + respawn" rather than a `set_*`
RPC. Amendments update D4 (Phase 2 picker branches by capability)
and D5 (Phase 4 lands per-driver, not just M1); plan §3 gains
matching wedges. Original D-numbers preserved so cross-references
in the plan still resolve.

**TL;DR.** Our M1 (`ACPDriver`) currently implements the minimum ACP
handshake: `initialize` → `session/new` → `session/prompt` →
`session/cancel`, plus a single inbound request handler for
`session/request_permission`. ACP defines a wider surface and gemini-cli's
own `agentCapabilities` in `initialize` advertises it: `loadSession` (resume),
`promptCapabilities` (image / audio / embeddedContext), four
authentication methods, and per-session model + mode switches. This ADR
pins which of those gaps we close before MVP and how — Phase 1
(`session/load` + `authenticate`), Phase 2 (mode + model picker —
runtime via M1, respawn via others), Phase 4 (image content blocks
across all drivers that accept content arrays). Phase 3 (the
`fs/*` and `terminal/*` client capability surface) is deferred to
post-MVP.

## Context

Real-world device testing through v1.0.391–v1.0.405 surfaced three
recurring frictions that all trace back to a too-narrow ACP
implementation:

- **Resume.** Each spawn-restart begins a fresh gemini session. The
  user's "do you remember our first turn?" test (logged in the
  v1.0.405 trace) confirmed the agent has no continuity across
  restarts. The hub-side cursor column `sessions.engine_session_id`
  exists (ADR-014) but the M1 path neither captures into it nor
  splices a resume call out of it.
- **Authentication.** The M1 path assumes pre-authenticated agents —
  it relies on disk-cached OAuth tokens that the daemon decrypts via
  the user's keychain. When the host-runner's environment lacks
  desktop-session vars (`DBUS_SESSION_BUS_ADDRESS`,
  `XDG_RUNTIME_DIR`) the daemon hangs silently. v1.0.402's per-call
  handshake budget covers slow startup, but the underlying assumption
  is fragile: agents whose *only* path to creds is the explicit
  `authenticate` RPC are unsupported today.
- **Mode and model lock-in.** gemini's `session/new` response carries
  `modes.availableModes` (`default` / `autoEdit` / `yolo` / `plan`)
  and `models.availableModels` (8 variants). Today the only way to
  switch is to edit the steward template's `Yolo: true` flag and
  respawn. Mobile users see the model name but can't pick.
- **Input shape lock-in.** `agentCapabilities.promptCapabilities` from
  gemini reports `{ image: true, audio: true, embeddedContext: true }`.
  Mobile already has `image_picker` + `flutter_image_compress`
  infrastructure, but the prompt array sent on `session/prompt` is
  text-only, so a screenshot can only reach the agent as a fs
  reference (which the agent then has to read with its own
  `read_file` tool — slower, costlier, and roundtrips the file
  contents twice).

The full ACP surface as of `@google/gemini-cli@0.41.2` and the
spec at `agentclientprotocol.com`:

| Direction | Method | Implemented? |
|---|---|---|
| Client → Agent | `initialize` | yes |
| Client → Agent | `authenticate` | **no** — Phase 1 |
| Client → Agent | `session/new` | yes |
| Client → Agent | `session/load` | **no** — Phase 1 |
| Client → Agent | `session/prompt` | yes (text-only inputs) |
| Client → Agent | `session/cancel` | yes |
| Client → Agent | `session/set_mode` | **no** — Phase 2 |
| Client → Agent | `session/set_model` | **no** — Phase 2 |
| Agent → Client | `session/update` | partial (subtypes covered through v1.0.404) |
| Agent → Client | `session/request_permission` | yes |
| Agent → Client | `fs/read_text_file` · `fs/write_text_file` | **no** — Phase 3 (post-MVP) |
| Agent → Client | `terminal/create` · `terminal/output` · `terminal/wait_for_exit` · `terminal/release` | **no** — Phase 3 (post-MVP) |

`promptCapabilities` (image / audio / embeddedContext) is structural
not method-shaped — it's an extension of the existing
`session/prompt` content array, addressed in Phase 4.

The four phases sort by user-visible value vs. implementation cost.
Phase 1 buys cross-restart continuity (the most-asked feature on the
M1 thread) and unbreaks auth-required engines. Phase 2 is small and
high-visibility — a model picker users can see. Phase 4 is mostly
mobile work. Phase 3 (client capabilities) is genuine architectural
work that crosses sandbox boundaries; deferring it is honest.

## Decision

### D1. Scope = Phases 1, 2, 4 for MVP. Phase 3 explicitly post-MVP.

The four-phase split is not arbitrary — it divides ACP's surface by
*architectural reach*. Phases 1, 2, 4 stay inside the existing M1
driver and the mobile renderer. Phase 3 (`fs/*`, `terminal/*`) makes
the host-runner an active participant in tool execution rather than
a passive bridge — that intersects directly with the deferred
sandbox/egress-proxy work in `feedback_post_mvp_sandbox`. Bundling
them confuses the boundary; deferring Phase 3 lets the sandbox plan
shape the client-capability surface when both land.

### D2. Phase 1a — `session/load` for resume.

When the hub spawns an M1 agent for an existing session AND
`agentCapabilities.loadSession` was true on the last `initialize`,
the driver calls `session/load` with the persisted sessionId
*instead* of `session/new`. On failure (sessionId stale on the
agent's disk, or `loadSession: false`) it falls back to
`session/new` and the user starts fresh, same as today.

The persistence column is `sessions.engine_session_id` — already
introduced by ADR-014 for claude-code. The M1 capture path is
analogous: `session.init` events that the driver *already* synthesizes
on `session/new` (line `driver_acp.go:230` family) carry the
sessionId; the hub-side capture in `captureEngineSessionID`
(`hub/internal/server/handlers_agent_events.go`) extends to claim
it for ACP agents too. No new column.

`session/load` returns the same shape as `session/new` plus a stream
of historical `session/update` notifications that replay the
conversation. The driver consumes those as it would normal updates
*but* tags them with `replay: true` in the resulting `agent_event`
payload so the mobile transcript can dedupe against the cached
events it already has. `replay:true` events still hit the streaming
aggregator (v1.0.404) so the transcript fold-up stays consistent —
they just don't bump the displayed turn counter or notification
state.

### D3. Phase 1b — `authenticate` after `initialize`.

The driver inspects the `initialize` response. If `authMethods` is
non-empty AND the agent advertises `requiresAuthentication: true`
(or, where that field is absent, if `session/new` fails with a
recognizable auth error), the driver calls `authenticate` with one
methodId. Method selection precedence:

1. Steward-template-declared `auth_method:` — explicit operator
   choice; no inference.
2. Family-default from `agent_families.yaml`. **For `gemini-cli`,
   the default is `oauth-personal`** — Google account login, with
   credentials cached at `~/.gemini/oauth_creds.json` after a
   one-time `gemini auth` run on the host. This matches the
   single-user-developer case (the most common termipod
   deployment) and avoids forcing API-key procurement up front.
   Service-account / shared-host deployments override the family
   default by setting `auth_method: gemini-api-key` (or
   `vertex-ai`) on the steward template.
3. First non-interactive method in the agent's `authMethods` list.

Why OAuth as default: `oauth-personal` is structurally interactive,
but in the daemon path the agent uses *cached* tokens from a prior
`gemini auth` and never opens a browser. The interactive flow only
triggers on first run or token expiry. Defaulting to API-key
instead would force every operator to mint a Google AI Studio key
even when a personal Google login is already on the host.

When the chosen method needs an interactive flow AND there are no
cached creds (first run, expired refresh token, or — most commonly
— a daemon spawned in an env that can't reach the keychain), the
driver fails fast with a recognizable error and emits an
`attention` event surfacing the auth-method options to the
principal so they can pick a different method or run
`gemini auth` and retry. The hub does NOT proxy the OAuth URL
through mobile — that's a Phase 5 concern (browser callback
infrastructure).

### D4. Phase 2 — mode + model picker, capability-branched.

The mobile picker is cross-engine. The wire path it uses depends on
what the engine supports:

| Engine path | Wire mechanism |
|---|---|
| **ACP M1** (gemini-cli `--acp`, future claude-code SDK ACP) | Live `session/set_mode` / `session/set_model` RPCs from `ACPDriver.Input`. Picker change applies in-session. |
| **M2 stream-json (claude)** + **M2 app-server (codex)** | No protocol-level switch exists — `--permission-mode` and `--model` are flag-time only. Picker change requires a respawn: hub edits the active spec's `backend.cmd`, stops the agent, spawns a fresh one with new flags. The session row stays put so the transcript is continuous; the `engine_session_id` resume cursor (ADR-014 / W1.1) keeps conversation context across the respawn. |
| **M2 exec-per-turn (gemini-cli `-p`)** | Per-turn argv. Driver stashes the override (`d.NextTurnModel` / `d.NextTurnMode`) and applies it to the next subprocess argv. No restart needed because gemini already spawns fresh per turn. |

Capability declaration lives on the agent_families.yaml entry —
each family declares one of `runtime_mode_switch: rpc | respawn |
per_turn_argv | unsupported`. The hub uses this to route the
picker selection. Mobile renders the picker identically across all
families; only the latency feedback differs ("applying…" → instant
on rpc, ~few-second on respawn).

Outbound RPC dispatch from `ACPDriver.Input` is gated by the
`agentCapabilities` cached at handshake — mode list comes from
`session/new.modes.availableModes`, model list from
`session/new.models.availableModels`. Unknown ids → driver returns
a typed error (mobile renders as a snackbar).

If neither RPC nor respawn nor per-turn argv is available, the
picker doesn't render — same fall-back-to-no-UI pattern the slash
command picker uses.

### D5. Phase 4 — image content blocks across all content-array drivers.

Image inputs land per-driver, not just M1. Three of our four drivers
already accept content-array inputs at the protocol level — they
just disagree on the exact wire shape for an image block. The hub
exposes one cross-engine input shape and each driver maps it to its
native form:

```
mobile / hub input shape:
  POST /agents/{id}/input
  { "kind": "text", "body": "...", "images": [
      {"mime_type": "image/png", "data": "<base64>"}
  ]}
```

| Driver | Native image-block shape |
|---|---|
| `StdioDriver` (claude M2 stream-json) | `{"type":"image","source":{"type":"base64","media_type":<mime>,"data":<b64>}}` — Anthropic SDK shape. |
| `AppServerDriver` (codex M2 app-server) | `{"type":"input_image","image_url":"data:<mime>;base64,<b64>"}` — OpenAI responses-API shape. |
| `ACPDriver` (gemini M1 + future ACP engines) | `{"type":"image","mimeType":<mime>,"data":<b64>}` — ACP shape. |
| `ExecResumeDriver` (gemini M2 exec-per-turn) | Capability-gate strip — gemini's `-p` argv has no inline-image affordance. Driver drops images and emits a `kind=system` event noting incompatibility; mobile shows a warning chip and the text portion still goes through. |

Capability gating uses each engine's reported support — claude's
`anthropic-vision` flag in stream-json init, codex's
`promptCapabilities.image` field, ACP's
`agentCapabilities.promptCapabilities.image`. Engines that report
false → strip + warn.

We use base64-inline (`data:` URLs / inline data fields) rather
than URI references because the latter would require either
hub-side hosting (HTTP server) or `fs/*` client capability support
(Phase 3, deferred). Inline blocks work without either, at the
cost of repeating the bytes in each turn.

`embeddedContext` (ACP-only `type: "resource"`) is a smaller
follow-up that piggybacks on the same content-array work;
non-ACP engines don't have an equivalent so this stays M1-scoped
within Phase 4.

Audio support is *not* part of Phase 4 — there's no mobile audio
capture infrastructure and no operator demand. Drivers will
forward audio blocks if the input layer ships them, but the hub
doesn't fabricate a mobile UI for it.

### D6. Capability-gated dispatch. Don't blindly send.

Each new outbound feature checks engine capabilities before dispatch:

- **`session/load`** requires `loadSession: true` on the cached ACP
  capabilities. Fall back to `session/new` otherwise.
- **Mode / model picker** routes by the family's
  `runtime_mode_switch:` declaration (D4 amendment). RPC path
  additionally requires the target id to be in the cached
  `availableModes` / `availableModels`.
- **Image blocks** route by per-driver capability flag (D5
  amendment): claude/codex check engine init flags, ACP checks
  `promptCapabilities.image`, exec-per-turn always strips. False →
  driver strips images and emits a `kind=system` event so mobile
  surfaces a warning chip rather than silently dropping the
  attachment.

Capability-gating is the load-bearing invariant that lets one mobile
UI target every engine without per-engine UI hardcoding. Adding a
new ACP engine means a new steward template + agent_families.yaml
entry; no driver changes. Adding a new non-ACP engine means a new
driver branch in the per-driver fan-out (D5 table) but no mobile
changes.

### D7. Mobile shape — additive, not breaking.

All new mobile behaviors are gated on either a server-emitted event
type (`replay:true` → renderer dedupe) or a payload field (`images`
→ attach). APKs from before Phase 1 stay compatible:

- A 391-era mobile sees Phase 1's `replay:true` events, doesn't
  recognize the flag, and renders them as normal events — harmless
  visual duplication on resume, which the user can clear by
  scrolling to top.
- A 391-era mobile sees Phase 2's mode picker source events but
  has no widget to render the picker — same as today's
  `available_commands_update` (system-kind, hidden in non-verbose
  mode).
- A 391-era mobile cannot generate Phase 4's image-input shape, but
  text-only prompts continue to work unchanged.

This means Phase 1+2 ship without forcing an APK update. Phase 4
*requires* a new APK to send images, but legacy APKs can still
receive image-content responses from the agent (they fall back to
`kind=raw` for non-text response blocks today, which is acceptable
until users opt in to a new build).

## Consequences

**Becomes possible:**

- Resume across spawn restart — the conversation feels continuous
  even when the agent process changes. Closes the most-asked gap on
  the M1 thread.
- Auth-required engines that don't pre-cache creds (claude-code SDK
  in some configs, hypothetical Vertex AI flow) become supportable
  without driver changes.
- Mode and model picking from mobile, gated by what the agent
  itself advertises — no per-engine UI hardcoding.
- Screenshot-led prompts (the demo flow "ask gemini about this
  graph") become a one-tap operation rather than a fs-shuffle.

**Becomes harder:**

- Three more outbound methods + image-block input mean the M1
  driver's surface area roughly doubles. Each new method gets a
  capability-gate check that future engines might not implement
  consistently — there's a real risk of carrying engine-specific
  branches inside the driver. Mitigation: every gate goes through a
  helper (`d.capCheck("loadSession")` etc.) that logs a single line
  on miss; carrying engine-specific branches is forbidden by D6.
- Replay handling on `session/load` doubles the volume of events
  coming through `handleNotification` on resume. Mobile's offline
  cache must reconcile against `replay:true` events without
  re-notifying the user (no "1 new turn" badge for replayed turns).
  Tracking is in the plan doc.

**Becomes forbidden:**

- Per-engine special cases inside the M1 driver. New engines extend
  the agent_families.yaml entry + steward template; if an engine's
  ACP behavior is so divergent that yaml + capability gates can't
  carry it, that engine gets its own driver (the M2/M3 escape
  hatch) rather than corrupting M1's neutrality.

## References

- ADR-013 (gemini exec-per-turn) — supersedes itself toward this
  ACP path; D1 amendment dated 2026-05-07 already names M1 as the
  preferred shape.
- ADR-014 (claude-code resume cursor) — `sessions.engine_session_id`
  column we extend to ACP, plus the splice-on-resume pattern.
- ADR-011 (turn-based attention) — `request_approval` MCP path
  remains the way ACP stewards surface principal-level decisions;
  Phase 1's auth-prompt event reuses this surface.
- `feedback_post_mvp_sandbox` — Phase 3 (`fs/*`, `terminal/*`)
  defers here because the client-capability surface is shaped by
  the sandbox boundary.
- ACP spec — `agentclientprotocol.com` (Zed's open spec).
- Implementation lands as the wedges in `plans/acp-capability-surface.md`.
