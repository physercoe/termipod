# Desktop UI context + pointing — shared user↔agent awareness

> **Type:** plan
> **Status:** Proposed (2026-07-30) — wedges D1–D5 below; D1+D2 (the LOCAL
> kimi-web loop) are the first priority, remote/hub-relayed delivery (D5)
> is second. **D1 shipped (PR #476); D2 implemented — PR pending.**
> Builds on the agent browser
> bridge (W1–W3 shipped,
> `docs/plans/desktop-agent-browser-bridge.md`). **Derives from
> [ADR-062](../decisions/062-desktop-ui-as-agent-addressable-entity.md)**
> (desktop UI as agent-addressable entity) — reframed 2026-07-30 from
> "the bridge grows two more tools" to verbs over a first-class UI
> entity: UIRef both directions, one per-surface policy table, three
> representations, agent pointing (D6). Review amendments 2026-07-30:
> relay fallback is read-token-only; stable relay copy (the
> resourcesPath pin breaks on AppImage); D5 sits behind the Remote-driving
> opt-in with the shipped W3 audit posture; session grants never cross
> tool kinds.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** the D2 wedge rebased onto origin/main
> `17abbd08` (S2/S3 split pane + ui_get_focus description) + review
> fixes: frontend + electron typecheck/build green, 256 electron
> node --test pass, lint-docs OK, the D2 Playwright spec green on
> Electron 43; kimi-code 0.28.1 verified on-host (macOS arm64)

**TL;DR.** The desktop UI is a **shared entity with two native consumers**
(ADR-062): agents are half the userbase of an agent workbench, so "what is
the user looking at" and "look at THIS" are verbs over a first-class
entity, not bolted-on tools. Concretely: agents read a structured focus
projection (`ui_get_focus`, returning a **UIRef** — the join key into the
entity graph agents already reach through their own tools), the user
points via a rect-select annotation overlay whose crop goes straight into
the agent's composer, and the agent points back (`ui_highlight`,
transcript ref-chips — D6). Same rule as the browser bridge: **curated
state, never shell CDP**. **Priority is the LOCAL loop**: the
embedded `kimi web` panel and the desktop share one machine, so the kimi
agent reads the focus snapshot through kimi-code's own MCP discovery
(user-level `mcp.json`), and the overlay injects the crop into kimi's
composer main-side (user-initiated — the bridge's kimiweb read-only
posture is untouched). Remote/hub-relayed delivery reuses the W3 tunnel
and lands second (D5). D3 is the gated screenshot, D4 element-resolved
pointing.

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

**Goals** (priority-ordered — the LOCAL kimi-web loop first, remote
hub-relayed second):

- The embedded `kimi web` agent (and the user's local kimi-code CLI) can
  answer "what is the user looking at" as structured JSON through
  kimi-code's own MCP discovery — toggle-gated, secret-free by
  construction; hub-relayed agents get the same through the W3 tunnel
  (D5).
- The user can rect-select any region of the app (webview guests
  included) and land the crop in **kimi web's composer** in one gesture —
  main-side, user-initiated, the bridge's kimiweb read-only posture
  untouched; a hub-attached agent session is the second target.
- Agents can request a screenshot of the desktop window — per-call
  approval, sensitive surfaces refused.
- Agents can *point back* (ADR-062 D-5): emit UIRefs that render as
  clickable transcript chips, and draw ephemeral attributed highlights —
  non-actuating in both cases (D6).
- Everything auditable the way the bridge is (ring always; hub mirror
  per the shipped W3 posture — actions mirrored, hub-leg reads ring-only).

**Non-goals**

- CDP/debugger access to the `app://` shell webContents (the rejection
  stands — same reasons as the bridge: self-approval, token/vault
  exposure, full-authority IPC).
- Continuous screen streaming / always-on watcher (snapshot on demand
  only; nothing leaves the machine unprompted).
- Mobile-side UI context (no embedded desktop to point at; the *agent*
  side works from anywhere since delivery is hub-relayed).
- Driving the shell UI (click/type into TermiPod itself) — awareness and
  annotation, no actuation: a highlight or ref-chip never focuses,
  scrolls, clicks, or types; the user's click is the only actuator
  (ADR-062 D-5).

## 3. Design

### 3.0 The entity model (ADR-062) — what everything below derives from

Every capability in this plan is a verb over one entity, in one of three
representations, governed by one policy table:

**The UIRef** (ADR-062 D-2) is the unit of reference — compact,
secret-free (ids/paths/fragment-stripped-urls/coordinates, never
content), and the **join key** into the entity graph the agent already
reaches through its own hub tools:

```json
{ "surface": "replay", "entity": { "dataset_id": "ds_…", "episode_id": "ep_…", "cursor": 1234 } }
{ "surface": "debug",  "path": { "file": "src/foo.ts", "selection": [42, 58] } }
{ "surface": "read",   "guest": { "tab_id": "wt_3", "ax_ref": "@e42" }, "rect": [412, 88, 640, 320] }
```

A UIRef is what `ui_get_focus` returns (wrapped with `captured_at`),
what the D4 pointer embeds, what `ui_highlight` targets, what audit
entries cite — and it flows **both directions**: agent-emitted refs
render as clickable transcript chips (D6). The grammar is
client-agnostic (nothing assumes Electron).

**The policy table** (ADR-062 D-3) is one registry file —
`desktop/src/state/ui_policy.ts`, one row per surface, the
`webtab_policy` pattern — with three independent columns: `snapshot`
(the exact field allowlist the projection may emit; empty = existence
only), `capture: allow | refuse` (may pixels of this surface ever be
captured), `highlight: allow | refuse` (may an agent annotate over it).
The table IS the privacy review; a surface with no row degrades to
existence-only everywhere and the unit test fails on undeclared
surfaces. Vault refuses everything, always.

**Three representations, one serving rule** (ADR-062 D-4): semantic
(UIRef + projected fields — §3.2), structural (AX/DOM, webtab guests
only — §3.4 D4), visual (pixels — §3.3). **Structure when it exists,
pixels for the residue** — and the hub relays but never stores any of
them (ADR-062 D-7).

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
serves it as a tool. The snapshot is the **semantic projection** of the
§3.0 entity: a UIRef plus the fields the surface's `ui_policy` row
`snapshot` column allows — the row, not the pipeline, is where the
privacy decision lives. Shape:

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
- **Split panes** (`desktop-shell-split-pane.md` S3, shipped after D1): with
  a split on screen the snapshot gains `secondary` (the pinned pane, served
  under **its own** policy row) and `active_pane`, and `surface` becomes
  *positional* — the primary pane — so both panes are described rather than
  only the focused one. One pane emits neither field; absent means no split.
  The degrade rule above follows **focus**, not position: the user being in
  a vault pane suppresses the whole snapshot, including the other pane.
- Read class: available wherever the tool set is injected, no approval.
- Follow-ups in the same wedge if cheap: `ui_get_selection` (the user's
  current text selection in Inspect/transcript, bounded chars).

**Invocation model — the user decides WHETHER, the agent decides WHEN.**
Three layers, in order of authority:

1. **User: availability (consent).** The sharing toggle is the only
   user-side control — off means no publisher and no tool in any
   catalog, so the agent can't reach the snapshot at all.
2. **Agent: timing (reactive tool selection).** The tool sits in the
   MCP catalog and the agent calls it when the conversation warrants —
   steered by the tool description, which is where the deictic-cue
   instructions live: *call when the user references what's on their
   screen ("this", "here", "what I'm looking at", "why is this
   failing") or when grounding would materially change the answer; do
   NOT call by default on every turn* (token cost + data-minimization —
   the snapshot should only be pulled when the conversation needs it).
3. **User: explicit override.** "Look at what I'm seeing" always works,
   and the companion's compose box grows a focus chip in its existing
   attach-context row (`AgentCompanion.tsx:66`) so the user can pin the
   snapshot onto one message deliberately.

Deliberately NOT the ambient model (Claude-Code-IDE / Cursor style
auto-injection every turn): it costs tokens on turns that don't need
grounding, it shares more than the conversation asks for, and — the
decisive constraint for the first-priority target — kimi web exposes no
system-prompt seam, so an MCP tool is the kimi-code-native channel.

**Between polling and ambient sits the native shape** (ADR-062 D-6):
focus is also exposed as an MCP **resource** (`ui://focus`,
change-notified, ref-sized payloads bounded by the ≥500 ms throttle) for
clients that support resource subscriptions — a subscribed agent *sees*
focus changes without polling or per-turn injection, and still decides
for itself when the context matters. Subscription support is
capability-detected, never assumed (kimi-code's is unverified — open
question 7); the tool is the portable floor everywhere.

**Surface coverage — every main job is covered, by matrix not by
exception.** The workbench has nine jobs (`workbench.ts:37-60`): fleet,
projects, read, author, debug, compare, replay, record, terminal (+
settings + the kimi-web session panel). Each maps to a `ui_policy` row
(§3.0); the table below previews the `snapshot` column — the `capture` /
`highlight` bits get their defaults at the same D1 review (vault and
settings refuse capture; vault refuses highlight). First-cut field set
(D1 review finalizes it, open question 2):

| Surface (job) | Emitted on focus | Never emitted |
|---|---|---|
| `fleet` | focused agent `{id, handle}`, its session id | message bodies |
| `projects` | `{project_id, task_id?}` | task descriptions |
| `read` | tab list `{kind, title, url|path}`, active tab | page contents (the bridge already reads webtabs) |
| `author` | active document `{id, title}` | document body |
| `debug` (Inspect) | tabs `{kind, path}`, active `{path, selection}` | file contents |
| `compare` | the two refs/paths being compared | diff bodies |
| `replay` (J8) | `{dataset_id, episode_id?, cursor?}` | episode frames |
| `record` (J6) | `{dataset_id?}` (recording target) | captured streams |
| `terminal` | focused `{pane_id}` (+ owning agent id when known) | scrollback (the hostrunner `capture` command already covers it) |
| `settings` | `{surface: "settings"}` only | every value |
| kimi-web panel | `{surface: "kimiweb"}` + guest URL (loopback, fragment-stripped — the W2 redaction rule) | chat contents (the bridge reads the guest, read-only) |
| vault / unknown | `{surface: "…"}` only | everything |

The overlay (D2) covers the same set visually — a rect over ANY of these
surfaces captures, except the sensitive rows which refuse (§3.3's rule) —
and D4's element resolution adds structure only over webtab guests.

### 3.3 Capability B — `ui_screenshot` (agent → user screen, gated)

`webContents.capturePage` of the shell window (or a named guest),
downscaled (max ~1568px edge, PNG) — the same size discipline
`browser_screenshot` uses. This is the **most sensitive artifact the app
can emit** (a frame of everything the user sees), so:

- **Per-call approval, always** — a hub `desktop_action` attention card
  (reuse the W3 parking/grant machinery, but NO `option_id: "session"`
  escape: screenshots never get a standing grant). The card shows which
  region/surface is requested.
- **Refused outright** on sensitive surfaces (`SURFACE_SENSITIVE`) — the
  `ui_policy` row's `capture` column (§3.0), NOT "has an allowlist
  entry": the columns encode different sensitivities (settings has a
  snapshot entry that emits the surface id only, yet a *pixel* capture
  of settings shows its values — snapshot allowed, capture refused).
  Vault refuses both, always.
- Audited like every bridge action (ring + hub mirror, `via` stamped).

The friction is architecturally **tiered by the table, not blanket**
(ADR-062 D-4): v1 ships per-call for every capture, but capture of
`capture: allow` surfaces may later be offered under a per-kind session
grant — a policy-row + card-kind change inside this frame, never a
redesign. Full-window capture, or any rect intersecting a
`capture: refuse` surface, stays per-call forever.

### 3.4 Capability C — pointing (user → agent): the annotation overlay

The high-trust direction needs no approval — the user initiates. **The
full user-operation sequence** (what the user actually does, D2):

1. **Setup (once)**: Settings → Assistant → enable "UI context sharing".
   Nothing else — no config, no restart.
2. **Trigger**: the user is looking at anything — a paper in Read, a
   failing pane in Terminal, a diff in Compare — and starts the gesture
   from wherever they are (D2.1): the **status-bar crosshair chip** (next
   to the assistant-dock chip), the **command-palette entry** ("Ask agent
   — annotate a region"), or the **"Ask agent" button** in an
   AgentCompanion compose box. The chip/palette arm GLOBALLY (no
   companion origin) so the trigger exists inside the embedded kimi-web
   loop too — the kimi SPA takes no injected buttons, its read-only
   posture stands. A companion arm routes "Send to <agent>" back to that
   mount; a global arm offers the first registered bound companion. The
   palette action is also registered in the rebindable-shortcut store
   (Settings → Keyboard) with NO default chord — a shipped global hotkey
   stays open question 3. The companion does NOT need to be bound to a
   session yet for the kimi-web path.
3. **Select**: a translucent overlay covers the window; the user drags a
   rect. `<webview>` guests are separate webContents painted in the shell
   layout, so a rect over a guest captures from THAT guest's
   `capturePage` region; over the shell, from the shell's. Esc cancels
   (nothing attached, no event). A rect fully inside a sensitive surface
   is refused with a hint; the user re-selects.
4. **Target**: a small row shows the reachable targets — **"Attach to
   kimi web" first when the panel is open**, else the companion's bound
   session — plus an optional one-line note field. One click chooses.
   - kimi-web path: the crop is injected into kimi's composer (§3.5,
     main-side `DOM.setFileInputFiles`); the user adds text in kimi's own
     composer if they like and **hits send there** — the send is always
     the user's.
   - companion path: the crop appears as a thumbnail chip in the compose
     box; the user types the note and hits send → `postAgentInput` with
     `InputAttachments` (existing path, no hub changes).
5. **D4, element-resolved pointing**: when the rect is over a webtab
   guest, the bridge's AX snapshot maps the region center to an `@eN`
   ref — the message then carries both the image AND a structured
   pointer (`{ tab_id, ref: "@e42", role: "button", name: "Deploy" }`),
   so "fix this button" is unambiguous to the agent AND actionable via
   the existing `browser_click { ref }`.

The other direction (agent reads context, D1) needs **no user operation
at all** beyond the toggle: the agent calls `ui_get_focus` when it wants
grounding, and the snapshot simply arrives. The design intent is that
D1's zero-effort ambient context handles most turns, and the overlay is
the one-gesture escalation when the user wants to be specific.

### 3.4b Capability D — agent pointing (agent → user): highlights + ref-chips

Deixis is symmetric (ADR-062 D-5): a collaborating agent needs "look
here" as much as "what are you looking at". Two primitives, both
**non-actuating** — the no-driving non-goal stands:

- **Ref-chips.** An agent-emitted UIRef in a reply renders in the
  transcript as a clickable chip (`replay · ep_… @ 1234`,
  `src/foo.ts:42`); clicking focuses that surface/entity. The agent
  directs attention, **the click is the user's** — no consent machinery,
  because nothing happens until the user acts. Cost: one chip renderer +
  a UIRef→focus dispatcher (the workbench store already has the
  setters).
- **`ui_highlight { ref, note?, ttl? }`.** An ephemeral, visibly
  attributed ("kimi-1 points here"), dismissible glow over a
  `highlight: allow` surface — or over an AX element of a webtab guest
  (the D4 machinery in reverse). Renders and expires (default ~8s);
  never occludes the Attention dock or modal consent UI; never focuses,
  scrolls, clicks, or types. Consent is the sharing toggle + the policy
  bit — no approval card (it draws pixels, it takes no action with user
  authority) — but every call is **audited like an action** (ring + hub
  mirror, `via` stamped) and remote delivery sits behind the full D5
  stack like everything else.

### 3.5 Transfer mechanics — how an annotation reaches the context window

**The LOCAL kimi-web loop is the first target — two mechanisms, neither
touches the bridge's kimiweb read-only posture:**

*The kimi agent reads the user's context (D1).* kimi-code discovers MCP
servers at two levels (ADR-054 D3): user (`~/.kimi-code/mcp.json`) and
project (`<cwd>/.kimi-code/mcp.json`, deep-merged, project wins). The
desktop spawns the `kimi web` SERVER (`kimiweb.ts` — `web --no-open
--port`) but sessions pick their own workspaces in kimi's UI, so
project-level injection can't reach them; the deliverable channel is the
**user-level file**: while the sharing toggle is on, the desktop
deep-merges one additive entry (`termipod-desktop`, the same stdio relay
as the bridge) into `~/.kimi-code/mcp.json` and removes it when off. The
relay gains a fallback so a STATIC entry survives token rotation: when
`TP_BROWSER_URL` is unset it reads the discovery file itself
(`~/.termipod/browser-bridge.json`, 0o600, per-run bearer) — and exits
cleanly when the file is absent (bridge off), which kimi-code marks as a
down server, never a failure. Two constraints the fallback must pin
(review amendments):

- **Read token only.** The discovery file carries both `token` and
  `action_token`; the fallback resolves `token` and hardcodes
  `TP_BROWSER_SCOPE=read` — action scope exists ONLY on the env-injected
  path, where the hub spawn's `browser_bridge: true` opt-in decided it
  (ADR-059 D-3: scope from the bearer, opt-in per spawn). A static entry
  that could reach the action token would hand every local kimi-code
  session typing rights on webtabs with no opt-in anywhere. Pinned by a
  D1 unit test (fallback never reads `action_token`).
- **Stable relay path.** The per-spawn entry points at
  `process.resourcesPath/browser_bridge_stdio.mjs`
  (`browserbridge_host.ts:75`) — fine there because the discovery file is
  rewritten every app run, but a STATIC `mcp.json` entry pinning that
  path breaks on Linux AppImage, where `resourcesPath` is a fresh
  `/tmp/.mount_*` per launch (and quietly goes stale across updates
  elsewhere). On toggle-on (and refreshed at each app start) the desktop
  copies the relay to a stable home — `~/.termipod/bridge/` — and the
  `mcp.json` entry references that copy.

Every local kimi-code client then gets `ui_get_focus` — kimi web
sessions AND the user's own CLI (a bonus: the CLI user gets "what am I
looking at in TermiPod" too). Note the static entry exposes the bridge
server's full READ tool list, not just `ui_get_focus` — no widening: the
discovery file is user-readable by design, so any local process of the
user already holds that capability; the entry just makes it a catalog.
Hub-spawned agents already receive the tool set via the W2 per-spawn
injection.

*The user transfers context to kimi web (D2).* The `kimiweb` partition
is `bridge: 'read'` to stop AGENTS typing into an agent's chat — but the
overlay is the USER acting, and the desktop main is the authority
process for its own guest: the read-only rule binds the agent-driven
bridge, not the app's own UI. So D2 injects the crop **main-side**, with
no bridge relaxation: the kimi web SPA's composer has a real file input
behind its attach button (verified in the live 0.28.1 bundle —
`r.value?.click()`, files → attach pipeline, the same path the paste
handler feeds), so CDP `DOM.setFileInputFiles` on that input — exactly
the mechanism W2's `browser_upload_file` proves — lands the image as a
composer attachment, then the user reviews and hits send in kimi's own
UI (the send action stays the user's, which is the consent). Fallback if
the SPA's DOM shifts: write the crop to the clipboard + focus the panel,
leaving the user one Cmd+V. This tool is NEVER registered on the bridge
server — agents can't reach it.

**The hub-attached path (remote agent sessions) exists today** and was
verified against `598e46ce` — it ships second (D5):

```
overlay crop → compose attachment → POST /agents/{id}/input
  { kind: "text", body, images: [{ mime_type, data(b64) }] }
  → hub stores payload_json["images"] VERBATIM on the input event
    (handlers_agent_input.go; test pins the shape)
  → hostrunner input_router consumes the producer=user event
  → driver: extractImageInputs (image_inputs.go:25)
    → ACP drivers (M1 kimi-ts, claude-code, …): lowered to ACP
      { type: "image", mimeType, data } blocks that LEAD the prompt
      array (driver_acp.go:1704-1713), gated by the agent's declared
      promptCapabilities.image (ADR-021 W4.4, tri-state — only an
      explicit false strips the blocks; the agent sees the image as
      native multimodal input, before the note text)
```

No hub change is needed — the envelope already carries images. Two
driver-family caveats become D5 scope:

- **Pane/stdio (M2/tmux) drivers can't receive images** — the pane is a
  terminal; there is no remote image channel (the kimi TUI's clipboard
  paste is a local feature, not tmux-drivable). D5 materializes the crop
  into the agent's workdir instead (`.termipod/annotations/<ts>.png`,
  via the blob the input event already externalizes) and appends the
  path to the note text — "see attached image at <path>" — so a pane
  agent reads it with its normal file tools. If the workdir write fails,
  the driver notes the drop rather than failing silently.
- **Capability honesty**: when the ACP agent declares
  `promptCapabilities.image == false`, the same workdir materialization
  is the fallback (today those blocks are just stripped).

**UX of the transfer** (D2 detail): after the drag, a target row offers
the visible targets — the kimi-web panel first when it's open
(“Attach to kimi web”), else the companion's bound session — then the
crop appears as a thumbnail chip (delete-able, like the existing context
chips) either in kimi's own composer (injected, user sends) or in the
companion compose box. For the companion path, the sent message renders
in the transcript as an image card with the note beneath, and for D4 the
structured pointer (`@e42 · button · "Deploy"`) renders as a small
caption chip on the card.

### 3.6 Delivery + hub tool surface

- Local clients (FIRST): `ui_get_focus` / `ui_screenshot` join the bridge
  server's tool list (read/action classification as above). They reach the
  embedded kimi-web agent + the user's kimi-code CLI through the
  user-level `~/.kimi-code/mcp.json` entry (§3.5), and hub-spawned local
  agents through the existing W2 per-spawn injection — no hostrunner
  change.
- Remote agents (SECOND, D5): tunnel kind `desktop.invoke` with the same
  envelope and response shapes as `browser.invoke`; hub-native
  `desktop_ui_invoke` `{host_id, tool, args}` tool mirroring
  `browser_invoke` — the capability check key becomes `desktop_ui` in the
  registered host row's capabilities (registered alongside
  `browser_bridge`), the gate/grant/revoke helper in
  `mcp_browser_bridge.go` generalizes to both kinds (one helper, two kind
  constants).
- **Remote delivery sits behind the Remote-driving opt-in** (review
  amendment). W3's consent posture (the #474 amendments) applies
  unchanged: the tunnel loop only runs when the separate default-off
  Remote-driving toggle is on, so `desktop_ui` is only ever registered —
  and remote `ui_get_focus` only ever reachable — behind bridge toggle +
  UI-sharing toggle + Remote-driving toggle, all three. Audit matches the
  shipped W3 read posture, not a new one: hub-leg reads are ring-audited
  (`via: 'hub'`, ring-only — `shouldMirrorAudit` keeps them off the hub
  mirror); actions ring + hub mirror.
- **Session grants never cross tool kinds** (review amendment). The W3
  grant store is keyed `team|hostID|agentID` with no kind dimension
  (`mcp_browser_bridge.go:109`) — a naive "one helper, two kind
  constants" would let a browser-driving session grant silence
  `desktop_action` screenshot cards. The generalized helper keys grants
  by kind (and §3.3's rule stands above that: `desktop_action` never
  consults session grants at all — screenshots are per-call, always).
- Revoke: the same Settings → Remote driving rows cover both kinds (an
  agent revoked there is refused at the desktop dispatch regardless of
  kind — reads included, per the shipped W3 revoke rule).

### 3.7 Settings + consent surface

Settings → Assistant gains a "UI context sharing" sub-toggle next to the
bridge toggle (default **off**): no toggle, no publisher, no tools, no
tunnel registrations. The blurb states plainly what is shared (surface,
tab, focused agent, file path + selection) and what is never shared
(message bodies, vault, settings values).

### 3.8 Failure / edge behavior

- Overlay canceled (Esc) → nothing attached, no event.
- Rect entirely over a sensitive surface → capture refused with a hint,
  user re-selects.
- Agent calls `ui_screenshot` with the toggle off / no desktop online →
  the same `hosts_list` hint style as `browser_invoke`.
- Focus snapshot stale (renderer busy) → `captured_at` tells the agent
  how old the answer is; the tool never blocks waiting for a render.

## 4. Wedges

**D1 — focus snapshot + the local kimi-web read loop (first priority,
shippable slice).** The **UIRef shape + the `ui_policy` table** land
here (§3.0 — all three columns, even though only `snapshot` has a
consumer yet: the table is the frame the later wedges fill); renderer
publisher + IPC + main cache; `ui_get_focus` on the bridge server;
`ui://focus` as a subscribable MCP resource where the client supports it
(capability-detected; open question 7); **user-level `~/.kimi-code/mcp.json` injection**
(toggle-gated deep-merge, additive `termipod-desktop` entry, removed on
toggle-off) + the relay's discovery-file fallback (static entries survive
token rotation, clean exit when the bridge is off) — so the kimi-web
agent AND the user's local kimi-code CLI can ask what the user is seeing;
hub-spawned local agents get it via the existing W2 injection. Settings
toggle + blurb; unit tests (publisher allowlist — a non-allowlisted
surface never emits fields; snapshot shape; mcp.json merge/remove is
additive-only and preserves foreign keys; relay fallback matrix:
env-set / discovery-present / discovery-absent, and the fallback never
resolves `action_token`; the mcp.json entry references the stable
`~/.termipod/bridge/` relay copy, never `resourcesPath`).

**D2 — annotation overlay, kimi-web first (first priority).** Rect-select
overlay (shell + guest regions); target row — **the kimi-web panel first
when open**: main-side `DOM.setFileInputFiles` injection into the SPA
composer's file input (never a bridge tool; fallback clipboard + focus +
one Cmd+V), the user reviews and sends in kimi's own UI; companion-bound
hub session second (thumbnail chip, `postAgentInput`, transcript image
card); Esc/cancel path. The only real UI work in the plan. **D2.1
amendment**: the trigger moves into the shell chrome — a status-bar
crosshair chip + a palette entry arm GLOBALLY (no companion origin; the
compose-box button stays) so the gesture is reachable from the embedded
kimi-web loop; the target row of a global arm offers the first registered
bound companion, and the palette action is shortcut-bindable with no
default chord (OQ3's shipped hotkey stays open).

**D3 — gated screenshot.** `ui_screenshot` with the `desktop_action`
per-call card (no session grant), vault/sensitive refusal, size caps,
audit; AttentionDock card branch.

**D4 — element-resolved pointing.** Rect-over-webtab → `@eN` ref via the
bridge snapshot; structured pointer rides the attachment payload.

**D5 — remote / hub-relayed delivery (second priority).** `desktop.invoke`
tunnel kind + hub `desktop_ui_invoke` (read class first); generalized
gate/grant/revoke (per-kind grants; reachable only behind the
Remote-driving opt-in, §3.6); the §3.5 driver work — ACP image blocks ride the
existing path, pane/stdio drivers and `image:false` agents get the
workdir materialization fallback (`.termipod/annotations/<ts>.png` +
path in the note text); hub tool tests mirroring
`mcp_browser_bridge_test.go`.

**D6 — agent pointing (§3.4b).** Ref-chips: transcript renderer for
agent-emitted UIRefs + the UIRef→focus dispatcher (chips ship first —
they are pure rendering, no consent surface). `ui_highlight`: overlay
renderer (attributed, TTL, never over Attention/modal UI), policy
`highlight` bit enforcement, action-class audit; local first, remote
rides D5's generalized dispatch unchanged.

## 5. Testing

- **Unit (node --test, electron-free)**: `ui_policy` matrix — every
  surface id → emitted fields or degradation, `capture` and `highlight`
  bits enforced, undeclared surface fails; UIRef shape (round-trips,
  never carries content fields); snapshot throttle/coalescing;
  `desktop.invoke` dispatch incl. unknown tool, revoked agent,
  sensitive-surface refusal; `ui_highlight` TTL/attribution/refusal on
  `highlight: refuse`; ref-chip parse + dispatch (a chip click focuses,
  a bare render never does); envelope shapes.
- **Hub (go test)**: `desktop_ui_invoke` validation + capability gate +
  read routing + `desktop_action` park/approve/timeout + no-session-grant
  rule, mirroring the browser_invoke suite.
- **E2E (Playwright)**: D1 — toggle on, open a webtab, `ui_get_focus`
  over the stdio relay returns the surface + tab; toggle off ⇒ refused;
  the user-level mcp.json entry appears on toggle-on and is removed on
  toggle-off with foreign keys intact. D2 — synthesize a drag over a
  fixture region, assert the compose box (or the kimi-web guest's file
  input, via CDP) holds the attachment.

## 6. Risks

- **Privacy regression by allowlist creep** — every new surface field is
  a privacy decision; the policy lives in ONE file (`ui_policy.ts`,
  §3.0) with a comment header stating the rule (ids/paths/urls only)
  and the unit test fails on undeclared surfaces.
- **Highlight abuse (attention spam / fake-UI phishing)** — `ui_highlight`
  is rate-limited per agent, always attributed, TTL-bounded, never
  renders over the Attention dock or modal consent UI, and carries no
  interactive elements (a glow + a note, not a button); every call is
  audited.
- **Screenshot sensitivity** — mitigated by per-call-only approval +
  vault refusal + audit; revisited only with evidence.
- **Overlay UX on multi-window / multi-display** — D2 scopes to the
  main window's bounds; guests in separate windows are out of the
  rect-select until D4.
- **User-level mcp.json is shared territory** — a malformed merge could
  break the user's OTHER kimi-code MCP servers; the merge is
  additive-only, round-trip tested (D1), and the entry self-heals
  (removed cleanly on toggle-off / app uninstall note in the docs).
- **Tunnel traffic class proliferation** — `browser.invoke` +
  `desktop.invoke` share one dispatcher on the desktop; hub-side helper
  stays generic so a third kind costs ~zero.

## 7. Open questions

1. Should UI-context sharing be its own toggle or fold into the bridge
   toggle? (Proposal: own toggle — different privacy posture; a user may
   share the browser but not the shell state.)
2. Focus-snapshot field allowlist — exact per-surface field set gets its
   review at D1 implementation; the test matrix (§5) is the enforcement.
3. Global hotkey for the annotation overlay (D2) or compose-button only?
   **Partially resolved (D2.1)**: the trigger is no longer compose-button
   only — the status-bar chip and the command-palette entry arm globally,
   and the action is user-bindable in Settings → Keyboard. A SHIPPED
   default hotkey stays open (original proposal: hotkey once it's loved).
4. Does `ui_get_selection` (bounded text selection) belong in D1 or D2?
   (Proposal: D1 if the transcript/Inspect selection APIs are clean, else
   D2 — it's the single most useful field for "explain this".)
5. ~~Should the bridge ever gain a narrow `browser_paste_image` tool class
   for the kimiweb partition?~~ **Resolved**: no agent-tool carve-out is
   needed — D2 injects the crop main-side on the user's own gesture
   (`DOM.setFileInputFiles` into the SPA composer's file input), so the
   bridge's kimiweb read-only posture stands untouched. The send action
   stays the user's.
6. Is the user-level `~/.kimi-code/mcp.json` entry acceptable shared
   territory, or should kimi-code grow a per-session/env config channel?
   (Proposal: ship the user-level entry — additive, toggle-gated,
   round-trip safe; revisit if kimi-code upstream adds a cleaner seam.)
7. Does kimi-code (and each hub-spawned family) support MCP resource
   subscriptions? (D1 detects capability and falls back to the tool;
   verify per family at D1 — the resource is additive either way,
   ADR-062 D-6.)
