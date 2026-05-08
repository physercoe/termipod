# 021. ACP capability surface — resume, auth, mode/model, image inputs

> **Type:** decision
> **Status:** Proposed (2026-05-08)
> **Audience:** contributors
> **Last verified vs code:** v1.0.405

**TL;DR.** Our M1 (`ACPDriver`) currently implements the minimum ACP
handshake: `initialize` → `session/new` → `session/prompt` →
`session/cancel`, plus a single inbound request handler for
`session/request_permission`. ACP defines a wider surface and gemini-cli's
own `agentCapabilities` in `initialize` advertises it: `loadSession` (resume),
`promptCapabilities` (image / audio / embeddedContext), four
authentication methods, and per-session model + mode switches. This ADR
pins which of those gaps we close before MVP and how — Phase 1
(`session/load` + `authenticate`), Phase 2 (`session/set_mode` +
`session/set_model`), Phase 4 (image content blocks). Phase 3 (the
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
2. Family-default from `agent_families.yaml` (`gemini-cli` defaults
   to `gemini-api-key` if `GEMINI_API_KEY` env is non-empty, else
   `oauth-personal`).
3. First non-interactive method in the agent's `authMethods` list.

Interactive flows (`oauth-personal` with no cached creds) cannot
complete in a daemon — the agent needs a browser callback we
can't surface. When the chosen method is interactive AND the
agent has no cached creds, the driver fails fast with a recognizable
error message and emits an `attention` event surfacing the
auth-method options to the principal so they can pick a different
method or set creds and retry. The hub does NOT proxy the OAuth
URL through mobile — that's a Phase 5 concern (browser callback
infrastructure).

### D4. Phase 2 — `session/set_mode` + `session/set_model`.

Both are new outbound RPCs from `ACPDriver.Input`, dispatched from
new `kind=set_mode` and `kind=set_model` input shapes accepted by
`POST /agents/{id}/input`. Mobile renders a small picker in the
steward header sourced from the `available_commands_update` /
`current_mode_update` / `current_model_update` notifications we
already capture (v1.0.403). Selecting an option fires the input;
the agent emits a fresh `current_mode_update` / `current_model_update`
notification confirming.

If the agent doesn't support the picker (older builds, non-gemini
ACP engines), the available list will be empty and the picker won't
render — same fall-back-to-no-UI pattern the slash command picker
uses.

### D5. Phase 4 — image content blocks.

`session/prompt`'s `prompt` array becomes heterogeneous:

```json
{
  "prompt": [
    {"type": "image", "mimeType": "image/png", "data": "<base64>"},
    {"type": "text",  "text": "what's in this screenshot?"}
  ]
}
```

Mobile's existing image-attach flow (`image_picker` →
`flutter_image_compress`) already produces compressed bytes; the
hub receives them on a new `images: [{mimeType, data}]` field of
the input shape and the driver inserts the matching content blocks
ahead of the text. We use base64-inline (`data:`) rather than URI
references because the latter would require either hub-side hosting
(HTTP server) or fs-capability support (Phase 3). Inline blocks
work without either.

`embeddedContext` (resource references) is a smaller follow-up that
piggybacks on the same `session/prompt` content-array work; landed
together since the wire shape is identical with `type: "resource"`.

Audio support is *not* part of Phase 4 — there's no mobile audio
capture infrastructure and no operator demand. The driver will
forward `type: "audio"` blocks if the input layer ships them, but
the hub doesn't fabricate a mobile UI for it.

### D6. Capability-gated dispatch. Don't blindly send.

Each new outbound method checks `agentCapabilities` from the cached
`initialize` response before dispatching:

- `session/load` requires `loadSession: true`. Fall back to
  `session/new` otherwise.
- `set_mode` requires the mode to be in `session/new.modes.availableModes`.
- `set_model` requires the modelId to be in `models.availableModels`.
- Image blocks require `promptCapabilities.image: true`. When false,
  the driver strips images from the prompt and emits a system event
  noting the agent can't accept them — the mobile UI shows a
  warning chip rather than silently dropping the attachment.

Capability-gating is the load-bearing invariant that lets the same
driver target future ACP agents (claude-code SDK ACP, Zed's own
agent) without a per-engine fork. Adding a Tier-2 engine means a new
steward template + family entry; no driver changes.

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
