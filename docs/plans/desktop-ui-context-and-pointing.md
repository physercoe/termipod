# Desktop UI context + pointing — shared user↔agent awareness

> **Type:** plan
> **Status:** Proposed (2026-07-29) — wedges D1–D4 below; D1 is the
> shippable slice. Builds on the agent browser bridge (W1–W3 shipped,
> `docs/plans/desktop-agent-browser-bridge.md`).
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `598e46ce`; kimi-code 0.28.1
> verified on-host (macOS arm64)

**TL;DR.** Let agents know *what the user is looking at* in the desktop app
— a curated, structured focus snapshot (`ui_get_focus`) — and let the user
*point* at anything on screen — a rect-select annotation overlay whose crop
rides the next message to the agent. Same rule as the browser bridge:
**curated state, never shell CDP**. The focus state already lives in the
renderer stores; the delivery channels (discovery-file injection for local
spawns, the A2A reverse tunnel for remote agents) already exist from the
bridge work. D1 ships the snapshot, D2 the gated screenshot, D3 the
overlay, D4 element-resolved pointing.

## 1. Context and grounding

### 1.1 The gap

An agent cooperating with the user is currently blind to the user's side of
the screen. "Continue from what I'm reading", "why is this failing?", "fix
THIS" all require the user to re-type context the app already knows: which
surface is active, which tab, which agent session, what file and selection
is in Inspect, which terminal pane is in front. The browser bridge (W1–W3)
gave agents the *webtab* content; it deliberately cannot see the `app://`
shell — and should never get CDP to it (the shell holds hub tokens, the
vault UI, and the Attention dock; shell access would let an agent approve
its own consent cards).

### 1.2 The insight that makes this cheap

The shell's focus state is **structured data in renderer stores**, not
pixels: the active job/surface (`desktop/src/state/workbench.ts` — fleet /
projects / read / author / debug / compare / replay), each surface's tab
list (e.g. `ReadSurface.tsx` `openWebTab`, :1896), the focused
session/agent, Inspect's file + selection, the terminal pane focus. Reading
it *structurally* means secrets are absent **by construction** — the
snapshot carries ids/paths/urls, never message bodies or vault material —
instead of masked after the fact.

### 1.3 Delivery channels already exist (the bridge payoff)

- **Same-host agents**: the bridge's per-spawn MCP injection
  (`~/.termipod/browser-bridge.json` discovery file → stdio relay,
  all four engine families) — a second tool set on the same server needs
  no new transport.
- **Remote agents**: the W3 hub relay — the desktop registers a `hosts`
  row (`capabilities.browser_bridge`) and long-polls the A2A reverse
  tunnel; `hub/internal/server/mcp_browser_bridge.go` routes
  `browser.invoke` envelopes with approval cards, session grants, and
  revoke. A sibling `desktop.invoke` envelope reuses every line of that
  machinery.
- **User→agent messages with attachments**: `postAgentInput` already
  accepts `InputAttachments` (`desktop/src/hub/client.ts:395`), and the
  AgentCompanion compose box already has an "attach context on send"
  concept (`desktop/src/ui/AgentCompanion.tsx:66`). The annotation crop
  is one more attachment kind.
- **Prior art**: Claude Code's screenshot paste + IDE selection context,
  Cursor's @-selection, VS Code Copilot Vision, kimi web's composer image
  paste (verified live on-host, 0.28.1 — clipboard `kind === "file"`
  items attach as `paste-<ts>.png`). None resolve a pointed region to
  *structure*; over a webtab, the bridge's AX snapshot (`@eN` refs) can —
  TermiPod's twist (§3.4, D4).

## 2. Goals / non-goals

**Goals**

- Agents (local or hub-relayed) can answer "what is the user looking at"
  as structured JSON, toggle-gated, secret-free by construction.
- Agents can request a screenshot of the desktop window — per-call
  approval, sensitive surfaces refused.
- The user can rect-select any region of the app (webview guests
  included) and send the crop + note to an agent in one gesture.
- Everything auditable the way bridge actions are (ring + hub mirror).

**Non-goals**

- CDP/debugger access to the `app://` shell webContents (the rejection
  stands — same reasons as the bridge: self-approval, token/vault
  exposure, full-authority IPC).
- Continuous screen streaming / always-on watcher (snapshot on demand
  only; nothing leaves the machine unprompted).
- Mobile-side UI context (no embedded desktop to point at; the *agent*
  side works from anywhere since delivery is hub-relayed).
- Driving the shell UI (click/type into TermiPod itself) — read-only
  awareness, no actuation.

## 3. Design

### 3.1 Components

```
┌ renderer (zustand stores: workbench, surfaces, session, inspect) ─┐
│  focus publisher (throttled, allowlisted fields)                  │
└──────────────┬───────────────────────────────────────────────────┘
               │ IPC push (desktopui_focus)
┌──────────────▼───────────────────────────────────────────────────┐
│ Electron main: desktopui.ts                                       │
│  - focus cache (last pushed snapshot)                             │
│  - capturePage (shell window / guest webContents)                 │
│  - tool handlers on the bridge server + desktop.invoke dispatch   │
│  - annotation overlay orchestration (D3)                          │
└──────────────┬───────────────────────────────────────────────────┘
               │ same channels as the bridge: stdio relay (local),
               │ A2A reverse tunnel kind "desktop.invoke" (remote)
┌──────────────▼───────────────────────────────────────────────────┐
│ hub: mcp_desktop_ui.go — desktop_ui_invoke{host_id, tool, args}   │
│  (generalizes mcp_browser_bridge.go's gate/grant/revoke helper)   │
└──────────────────────────────────────────────────────────────────┘
```

### 3.2 Capability A — `ui_get_focus` (agent → user context)

Renderer-side publisher: subscribes to the stores, pushes a compact
snapshot on change (≥500 ms throttle, only when the bridge-class sharing
toggle is on) over a new IPC channel. Main caches the last snapshot and
serves it as a tool. Shape (allowlisted per surface — the allowlist IS the
privacy review):

```json
{
  "surface": "read",
  "tab": { "kind": "web", "title": "…", "url": "arxiv.org/abs/…" },
  "agent": { "id": "ag_…", "handle": "kimi-1" },
  "inspect": { "path": "src/foo.ts", "selection": [42, 58] },
  "terminal": { "pane_id": "%12" },
  "captured_at": "2026-07-29T12:00:00Z"
}
```

- Fields are ids/paths/urls/line numbers only. No message bodies, no
  vault content, no settings values. When the vault surface (or any
  surface without an allowlist entry) is active, the snapshot degrades to
  `{ "surface": "vault" }` — existence, not content.
- Read class: available wherever the tool set is injected, no approval.
- Follow-ups in the same wedge if cheap: `ui_get_selection` (the user's
  current text selection in Inspect/transcript, bounded chars).

### 3.3 Capability B — `ui_screenshot` (agent → user screen, gated)

`webContents.capturePage` of the shell window (or a named guest),
downscaled (max ~1568px edge, PNG) — the same size discipline
`browser_screenshot` uses. This is the **most sensitive artifact the app
can emit** (a frame of everything the user sees), so:

- **Per-call approval, always** — a hub `desktop_action` attention card
  (reuse the W3 parking/grant machinery, but NO `option_id: "session"`
  escape: screenshots never get a standing grant). The card shows which
  region/surface is requested.
- **Refused outright** while the vault detail pane is open or the active
  surface lacks an allowlist entry (`SURFACE_SENSITIVE`).
- Audited like every bridge action (ring + hub mirror, `via` stamped).

### 3.4 Capability C — pointing (user → agent): the annotation overlay

The high-trust direction needs no approval — the user initiates:

1. Trigger: an "Ask agent" button in the AgentCompanion compose box (D3;
   a global hotkey is an open question, §7).
2. A translucent overlay covers the window; the user drags a rect.
   `<webview>` guests are separate webContents painted in the shell
   layout, so a rect over a guest captures from THAT guest's
   `capturePage` region; over the shell, from the shell's.
3. The crop lands in the compose box as an image attachment with an
   optional note; send → `postAgentInput` with `InputAttachments`
   (existing path, no hub changes).
4. **D4, element-resolved pointing**: when the rect is over a webtab
   guest, the bridge's AX snapshot maps the region center to an `@eN`
   ref — the message then carries both the image AND a structured
   pointer (`{ tab_id, ref: "@e42", role: "button", name: "Deploy" }`),
   so "fix this button" is unambiguous to the agent AND actionable via
   the existing `browser_click { ref }`.

### 3.5 Delivery + hub tool surface

- Local spawns: `ui_get_focus` / `ui_screenshot` join the bridge server's
  tool list (read/action classification as above); no hostrunner change —
  the injection already hands every spawn the read token and opted-in
  spawns the action token.
- Remote agents: tunnel kind `desktop.invoke` with the same envelope and
  response shapes as `browser.invoke`; hub-native `desktop_ui_invoke`
  `{host_id, tool, args}` tool mirroring `browser_invoke` — the
  capability check key becomes `desktop_ui` in the registered host row's
  capabilities (registered alongside `browser_bridge`), the gate/grant/
  revoke helper in `mcp_browser_bridge.go` generalizes to both kinds
  (one helper, two kind constants).
- Revoke: the same Settings → Remote driving rows cover both kinds (an
  agent revoked there is refused at the desktop dispatch regardless of
  kind).

### 3.6 Settings + consent surface

Settings → Assistant gains a "UI context sharing" sub-toggle next to the
bridge toggle (default **off**): no toggle, no publisher, no tools, no
tunnel registrations. The blurb states plainly what is shared (surface,
tab, focused agent, file path + selection) and what is never shared
(message bodies, vault, settings values).

### 3.7 Failure / edge behavior

- Overlay canceled (Esc) → nothing attached, no event.
- Rect entirely over a sensitive surface → capture refused with a hint,
  user re-selects.
- Agent calls `ui_screenshot` with the toggle off / no desktop online →
  the same `hosts_list` hint style as `browser_invoke`.
- Focus snapshot stale (renderer busy) → `captured_at` tells the agent
  how old the answer is; the tool never blocks waiting for a render.

## 4. Wedges

**D1 — focus snapshot (this plan's shippable slice).** Renderer publisher
+ IPC + main cache; `ui_get_focus` on the bridge server; `desktop.invoke`
tunnel kind + hub `desktop_ui_invoke` (read class only); Settings toggle +
blurb; unit tests (publisher allowlist — a non-allowlisted surface never
emits fields; snapshot shape; tunnel dispatch) + hub tool tests mirroring
`mcp_browser_bridge_test.go`.

**D2 — gated screenshot.** `ui_screenshot` with the `desktop_action`
per-call card (no session grant), vault/sensitive refusal, size caps,
audit; AttentionDock card branch.

**D3 — annotation overlay.** Rect-select overlay (shell + guest regions),
compose-box image attachment, `postAgentInput` integration, Esc/cancel
path. The only real UI work in the plan.

**D4 — element-resolved pointing.** Rect-over-webtab → `@eN` ref via the
bridge snapshot; structured pointer rides the attachment payload.

## 5. Testing

- **Unit (node --test, electron-free)**: publisher allowlist matrix
  (every surface id → emitted fields or degradation); snapshot
  throttle/coalescing; `desktop.invoke` dispatch incl. unknown tool,
  revoked agent, sensitive-surface refusal; envelope shapes.
- **Hub (go test)**: `desktop_ui_invoke` validation + capability gate +
  read routing + `desktop_action` park/approve/timeout + no-session-grant
  rule, mirroring the browser_invoke suite.
- **E2E (Playwright)**: D1 — toggle on, open a webtab, `ui_get_focus`
  over the stdio relay returns the surface + tab; toggle off ⇒ refused.
  D3 — synthesize a drag over a fixture region, assert the compose box
  holds an attachment.

## 6. Risks

- **Privacy regression by allowlist creep** — every new surface field is
  a privacy decision; the allowlist lives in ONE file with a comment
  header stating the rule (ids/paths/urls only) and the unit test fails
  on undeclared surfaces.
- **Screenshot sensitivity** — mitigated by per-call-only approval +
  vault refusal + audit; revisited only with evidence.
- **Overlay UX on multi-window / multi-display** — D3 scopes to the
  main window's bounds; guests in separate windows are out of the
  rect-select until D4.
- **Tunnel traffic class proliferation** — `browser.invoke` +
  `desktop.invoke` share one dispatcher on the desktop; hub-side helper
  stays generic so a third kind costs ~zero.

## 7. Open questions

1. Should UI-context sharing be its own toggle or fold into the bridge
   toggle? (Proposal: own toggle — different privacy posture; a user may
   share the browser but not the shell state.)
2. Focus-snapshot field allowlist — exact per-surface field set gets its
   review at D1 implementation; the test matrix (§5) is the enforcement.
3. Global hotkey for the annotation overlay (D3) or compose-button only?
   (Proposal: compose-button for D3, hotkey once it's loved.)
4. Does `ui_get_selection` (bounded text selection) belong in D1 or D2?
   (Proposal: D1 if the transcript/Inspect selection APIs are clean, else
   D2 — it's the single most useful field for "explain this".)
