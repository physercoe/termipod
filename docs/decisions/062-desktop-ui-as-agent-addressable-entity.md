# 062. Desktop UI as agent-addressable entity

> **Type:** decision
> **Status:** Proposed (2026-07-30) — extracted during review of
> [`plans/desktop-ui-context-and-pointing.md`](../plans/desktop-ui-context-and-pointing.md)
> (PR #475) **before its first wedge ships**, per the ADR-059 lesson: state
> the invariants at decision tier before there is an implementation to
> drift from them. The plan derives from this ADR.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `598e46ce`
> (`desktop/src/state/workbench.ts:36-54`,
> `desktop/electron/src/webtab_policy.ts:35-76`,
> `desktop/electron/src/browserbridge.ts` / `browserbridge_host.ts`,
> `hub/internal/server/payload_externalize.go`)

**TL;DR.** TermiPod's desktop is an agent workbench: agents are half the
userbase of its UI, so the UI is a **shared entity with two native
consumers**, not a human artifact with an agent side-channel bolted on.
That premise fixes the design space: (identity) anything the user or an
agent points at is a **UIRef** — a compact, secret-free reference that
joins the user's attention to the entity graph agents already reach
through their own tools; (projection) the renderer stores are the one
canonical UI state, and agents get a schema'd projection of it — never
scraping, never shell CDP; (policy) one per-surface table declares what
each representation may carry, the same shape as `webtab_policy`;
(representation) the entity is served semantic / structural / visual —
structure when it exists, pixels for the residue, and vision's consent
friction lives in the table, not in blanket ceremony; (symmetry) both
parties can point, neither can drive; (lifecycle) focus is observable,
not only pollable; (persistence) UI state is host-local and ephemeral —
the hub relays it and never stores it.

## Context

The browser bridge (ADR-059, W1–W3 shipped) gave agents the *webtab*
guests. The user's side of the screen — which surface is active, which
tab, which file and selection, which episode cursor — remained invisible,
so collaboration keeps paying a re-typing tax ("continue from what I'm
reading", "why is THIS failing"). PR #475 proposes closing that gap; this
ADR states what the closing must be *derived from*, because the obvious
incremental framing ("the bridge grows two more tools") gets the
ontology backwards for this particular app.

Three facts drive the derivation:

- **The desktop's surfaces mostly render agent-accessible hub entities.**
  The workbench's nine jobs (`workbench.ts:36-54`) show runs, episodes,
  datasets, files, diffs, sessions — things agents can already fetch
  through hub tools under their own authorization. A focus reference is
  therefore not a data export; it is a **join key** into a tool space
  the agent already holds. This is why a reference can be ~tens of
  tokens, secret-free by construction, and still be maximally useful.
- **The shell's focus state is structured data in renderer stores**, not
  pixels — reading it structurally makes secrets absent by construction
  instead of masked after the fact.
- **The house already has the policy pattern.** `webtab_policy.ts` rows
  (ADR-059 D-1) made "one reviewable line per partition" the way a
  surface opts into agent visibility. Lifting that to shell surfaces is
  a repetition of a proven shape, not an invention.

"First-class" does **not** mean raw access. Agents don't get raw SQL to
the hub DB even though hub entities are first-class; they get entities.
The shell-CDP rejection (ADR-059 §Context, plan §1.1) stands unchanged —
a designed interface is what first-class *means*.

## Decision

### D-1 — One canonical state, projected — never scraped

The renderer stores (zustand: workbench, surfaces, session, inspect) are
the canonical UI state. The agent-facing view is a **schema'd projection**
of that state pushed over IPC and served from the Electron main process —
the same state the human's pixels render, at a different fidelity. No
capability may bypass the projection to read the shell (no shell
`webContents.debugger`, no DOM scraping of `app://`): the shell holds hub
tokens, the vault UI, and the Attention dock, and shell access would let
an agent approve its own consent cards. Guests stay reachable only
through the bridge's existing gates (ADR-059 D-1–D-6).

### D-2 — The UIRef is the unit of reference, in both directions

Every focus answer, pointing gesture, highlight target, and audit entry
cites a **UIRef**: a compact structured reference — surface id, the
rendered entity's ids (run / episode / dataset / document / project),
path + selection where the surface shows files, tab + AX ref where it
shows a webtab guest, rect where only geometry exists. Fields are ids,
paths, URLs (fragment-stripped, ADR-059 D-5), and coordinates — never
content.

Refs flow **both ways**. The user's side emits them via focus and the
annotation overlay. Agents emit them in replies, and the transcript
renders an agent-emitted ref as a **clickable chip that focuses that
surface when the user clicks** — the agent directs attention, the user
actuates. The ref grammar is client-agnostic on purpose: mobile has no
desktop to point at, but nothing in a UIRef assumes Electron, so a
future mobile client can emit its own.

### D-3 — One per-surface policy table governs every representation

A single registry file — one row per surface, the `webtab_policy`
pattern — declares what the entity serves, per representation:

- `snapshot`: the exact field allowlist the semantic projection may emit
  for this surface (empty = existence only, `{surface: "…"}`);
- `capture`: `allow | refuse` — whether pixels of this surface may ever
  be captured;
- `highlight`: `allow | refuse` — whether an agent may render a pointing
  annotation over it.

The table **is** the privacy review: adding a surface or a field is a
one-line diff in one file, matrix-tested (an undeclared surface fails
the unit test), and nothing else in the pipeline makes policy decisions.
Vault refuses everything, always. The columns are independent because
the sensitivities are: `settings` may declare its existence in a
snapshot yet must refuse pixel capture (a capture shows its values).

### D-4 — Three representations, one entity; vision is a peer

The entity is served at three fidelities: **semantic** (UIRef +
projected fields), **structural** (AX/DOM — exists over webtab guests
only, via the bridge's snapshot machinery), **visual** (pixels via
`capturePage`, downscaled). The serving rule: **structure when it
exists, pixels for the residue** — rendering bugs, layout questions,
"why does this look wrong" are answerable only by pixels, which is what
makes vision a peer representation rather than a fallback.

Vision's consent friction lives **in the policy table, not in blanket
ceremony**: capture of `capture: allow` surfaces may be offered under a
session grant once the card machinery supports it; full-window capture,
or anything intersecting a `capture: refuse` surface, stays per-call
approval, always, with no session escape. First implementations ship
per-call for everything — loosening is then a policy-row + card-kind
change inside this frame, never a redesign. Every capture is audited.

### D-5 — Pointing is symmetric; actuation is not

Both parties get deixis. The **user** points at the agent: rect-select
overlay, crop + optional structural ref into the agent's composer —
user-initiated, so no approval. The **agent** points at the user:
`ui_highlight` — an ephemeral, visibly-attributed, dismissible
annotation over a `highlight: allow` surface or AX element, and
ref-chips in the transcript (D-2). Highlights are **non-actuating
annotation**: no approval card; consent is the sharing toggle plus the
policy bit; every call is audited like an action (ring + hub mirror).

Neither party drives the other's half: agents never click/type into the
shell (D-1), and agent-emitted refs never auto-focus a surface — the
click is the user's.

### D-6 — Focus is observable; tools are the portable floor

The entity has a lifecycle, so consumers may **subscribe**: focus is
exposed as an MCP resource (change-notified, throttled, ref-sized
payloads) where the client supports resource subscriptions, and as a
pull tool everywhere (the portable floor — subscription support must be
capability-detected, never assumed). Ambient per-turn injection of UI
context into prompts is rejected: it spends tokens on turns that need no
grounding and shares more than the conversation asks for. A
subscription's notifications are bounded by the size of a UIRef; the
*agent* still decides when the context matters.

### D-7 — UI state is host-local and ephemeral; the hub relays, never stores

Hub = metadata, hosts = bytes: focus snapshots, captures, and highlights
are host-local state. Remote delivery rides the W3 reverse tunnel behind
the shipped consent stack (bridge toggle + sharing toggle +
Remote-driving opt-in; per-kind session grants; revoke covers reads),
and the hub **never persists** a focus snapshot or a capture as a hub
entity — no `ui_state` table, no capture archive. What crosses the hub
is audited (ring always; hub mirror per the shipped W3 posture) and
size-governed by the existing payload externalization boundary. There is
nothing to sweep, back up, or expire, because nothing is stored.

## Consequences

- The plan's capabilities become corollaries: `ui_get_focus` = the
  semantic projection (D-1/D-2); the annotation overlay and D4 pointing
  = user-side deixis (D-5); `ui_screenshot` = the visual representation
  under the `capture` column (D-3/D-4); the hub relay = D-7. New
  capabilities (highlight, ref-chips, subscriptions) are the previously
  missing halves of D-5/D-6, not scope creep.
- Adding a surface to agent visibility is a one-row review (D-3) — the
  same property ADR-059 D-1 bought for embedded webs.
- The tiered-capture evolution (D-4) is pre-decided: nobody needs to
  re-litigate the consent architecture to make vision more fluent later;
  they edit a row and add a card kind.
- A UIRef in an audit entry, transcript chip, or attention card is
  stable documentation of *what was shared* — the reference, not the
  content.
- Rejecting hub persistence (D-7) means no cross-session "what was the
  user looking at yesterday" — accepted: that is surveillance-shaped,
  and the transcript already records what was deliberately shared.

## Alternatives considered

- **Shell CDP / generic UI automation for agents.** Rejected (again —
  ADR-059 said it for guests' sake; D-1 says it for the shell's):
  self-approval of consent cards, token and vault exposure, and
  full-authority IPC one `Runtime.evaluate` away.
- **Ambient context injection (IDE-assistant style, every turn).**
  Rejected (D-6): token cost on turns that need no grounding,
  over-sharing by default, and the first-priority client (kimi web)
  exposes no system-prompt seam anyway — an MCP tool/resource is the
  native channel.
- **Screenshot-first (stock computer-use posture).** Rejected: pixels
  are the *residue* representation here (D-4). Structure is cheaper,
  precise, secret-free by construction, and joins the agent's existing
  tool space (D-2); a screenshot answers only what structure cannot.
- **Per-capability ad-hoc consent** (each tool invents its gate).
  Rejected (D-3): consent must be *scope*-shaped (which surfaces, which
  representations, one table) or every new capability re-opens the
  privacy argument from zero.
- **Hub-persisted UI state** (focus history as a hub entity). Rejected
  (D-7): hub = metadata law, surveillance shape, and ADR-061 just spent
  a whole decision keeping the blob store free of exactly this kind of
  accreting byproduct.

## References

- Code: `desktop/src/state/workbench.ts:36-54` (the nine jobs) ·
  `desktop/electron/src/webtab_policy.ts` (the policy-row pattern D-3
  lifts) · `desktop/electron/src/browserbridge.ts` /
  `browserbridge_host.ts` (gates, audit, tunnel — the machinery D-4/D-7
  reuse) · `hub/internal/server/payload_externalize.go` (the size
  boundary D-7 leans on)
- Plan: [`plans/desktop-ui-context-and-pointing.md`](../plans/desktop-ui-context-and-pointing.md)
  (derives from this ADR; wedges D1–D6)
- Related ADRs: [059](059-agent-browser-bridge.md) (guest-side gates;
  the CDP rejection this extends to the shell) ·
  [021](021-acp-capability-surface.md) (capability detection precedent
  for D-6) · [030](030-governed-actions-and-propose-verb.md) (the
  approval-ladder shape D-4's tiering reuses) ·
  [061](061-blob-lifetime.md) (why D-7 refuses accreting byproduct)
