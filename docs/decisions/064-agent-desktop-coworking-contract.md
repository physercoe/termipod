# 064. Agent desktop co-working contract

> **Type:** decision
> **Status:** Accepted (2026-08-02, director) — extracted from
> [agent-desktop-coworking.md](../discussions/agent-desktop-coworking.md)
> before implementation, per the ADR-059/063 lesson: pin the invariants
> at decision tier before there is code to drift from them. Accepted
> ahead of the fleet start (issue #494) so workers build against a
> settled consent posture; D5's native-format clause was rewritten at
> acceptance after it read as banning TermiPod's own formats, which it
> never did
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026.731 main (`91153552`)
> (`desktop/src/state/ui_policy.ts:155-176`,
> `desktop/electron/src/browserbridge.ts:389-605`,
> `hub/internal/server/mcp_desktop_ui.go:66-131`,
> `desktop/src/state/documents.ts:31-42`)

**TL;DR.** The AI agent is a **first-class user of the desktop's
local-work tabs** — Read, Author, Inspect, Compare, Replay, Record — by
operating on the **entities each tab renders, plus pointing and a
consented navigation verb — never by synthetic input** (ADR-062's "the
click is the user's" stands). Entity tools live **where the entity
lives** (desktop-owned → bridge surfaces like `author_*`; hub-owned →
the ADR-033 hub catalog; never both). Consent is a four-class ladder —
`read | annotate | navigate | write` — where desktop-entity writes are
authorized by an **approval card granting a per-(agent, target, session)
lease, never by bearer scope**, and hub-entity writes keep the hub's own
governance. Every write obeys one **like-a-user contract**: validated
before commit, landing live in the open editor, undoable, attributed,
revertible, with degradation stated honestly — and a surface's focus
fields must be truthful **before** its write verbs ship (no writing
blind). Fleet/Projects stay hub-native; Terminal, Settings, and Vault
are permanently out of scope.

---

## 1. Context

The local-work tabs hold the user's desk work; agents can already see
(`ui_get_focus`, `ui_screenshot`) and point (`ui_highlight`) but can
make or change almost nothing: Author has no document tool and three of
five editors ignore external writes; Read's entities have full hub CRUD
(`native_tools.go:193-356`) but a pull-only, local-wins client; Replay's
entities are REST-complete with zero MCP tools; Compare/Record have no
entities yet. Four of twelve policy rows reserve focus fields that are
never populated (`uiContext.ts:29-33`). `mobile_navigate` exists with no
desktop twin, and UIRef chips deliberately never open tabs
(`uiRefFocus.ts:71-76`). The kimi relay is read-token-pinned by design
(`browser_bridge_stdio.mjs:52-66`), yet the kimiweb arm must
participate. Today no bridge tool mutates app state without a user
click; the first one sets precedent. Editing a **document entity**
through a governed tool is not UI actuation — the editor merely renders
the entity — so this contract composes with, rather than amends,
[ADR-062](062-desktop-ui-as-agent-addressable-entity.md);
[ADR-030](030-governed-actions-and-propose-verb.md) governs the verbs.

## 2. Decision

**D1 — the co-worker frame.** An agent operates a tab by (a) reading
and writing the **entities** the tab renders, (b) **pointing**
(`ui_highlight`), and (c) **navigating** the user's view on request
(D3). It never emits synthetic input into the workbench — no click,
type, or scroll outside the already-bounded browser-webview tools; the
drawio iframe and every other same-renderer surface stay invisible to
CDP-class tools. In scope: `read`, `author`, `debug` (Inspect),
`compare`, `replay`, `record`. Hub-native surfaces (`fleet`,
`projects`) are served by the existing hub catalog. **Excluded
permanently:** `terminal` (the user's authenticated shells — agent
shell work happens in the agent's own spawned session), `settings` and
`vault` (the consent surface itself; operability there would let an
agent grant itself capabilities). Exclusions are structural: no policy
bit, no lease, no tool may reach them.

**D2 — tools live where the entity lives.** Desktop-owned entities
(Author documents, Inspect tabs/roots, the compare-wall arrangement)
get **bridge surfaces**; hub-owned entities (references + annotations,
datasets/episodes/series, decision records, everything Fleet/Projects)
get **hub MCP tools** in the ADR-033 registries. An entity's surface is
never duplicated on both sides; for hub entities the desktop's
obligations are *transport* (timely hub→desktop propagation) and
*rendering* (attribution, revert affordances), not tools. First frozen
bridge family: `author_read` / `author_apply` / `author_render` /
`author_guide`, with kind dispatch internal (new kinds add adapters,
never tool names). Tool metadata follows
[ADR-063](063-mcp-version-negotiation-and-adoption.md) D5: write verbs
are never annotated read-only.

**D3 — the consent ladder.** Four classes, extending the shipped
`read | annotate | action` taxonomy (`mcp_desktop_ui.go:66-108`):

- **read** (focus, snapshots, entity reads incl. `author_read`): gated
  by the UI-sharing toggle, always audited.
- **annotate** (`ui_highlight`): toggle + per-surface policy bit +
  rate limit + audit (unchanged).
- **navigate** (new — `desktop_open` over the UIRef grammar): routes
  immediately like annotate — toggle + a per-surface `navigate` policy
  bit + rate limit + audit; user-dismissable and non-creating beyond
  what the UIRef names; structurally unable to address excluded
  surfaces. It actuates *attention*, never entities.
- **write** (entity mutation): on the desktop side, a first
  `author_apply`-class call raises an approval card granting a
  per-(agent, target, session) **lease** (allow once / allow this
  target this session), revoked by toggle-off, session end, or explicit
  revoke — in the reserved `desktopGrantKind` namespace
  (`mcp_desktop_ui.go:118-131`). On the hub side, existing governance
  (tiers, roles, `propose`, worker allow-lists) applies unchanged —
  no second gate. **Bearer scope never substitutes for the card, and
  the card never widens bearer scope** — which is exactly how the
  read-pinned kimiweb arm participates.

**D4 — the like-a-user contract, and write-not-blind.** A committed
write is indistinguishable in consequence from a careful user edit: it
lands **live** in the open surface through an imperative adapter (not a
remount, once the adapter exists); it is **undoable** (native undo
stacks where they exist, plus a bounded pre-apply snapshot ring where
they don't); it is **attributed** (a visible chip naming the agent —
for hub entities, `created_by_kind`/author rendered distinctly) and
**revertible**; degradation is stated honestly in the tool result
(editor closed → store-only; adapter missing → remount fallback,
named). Propose-shaped entities (decision records) keep their
propose-only agent posture — acceptance is the user's click. And a
surface's reserved focus fields (Read tabs, Inspect selection, compare
picks, replay episode/cursor, record target) must be **populated
truthfully before that surface's write verbs ship** — an agent must
never be asked to co-work blind.

**D5 — native grammars, validated commits, vendored knowledge.** Every
write is parsed and validated with the kind's own machinery **before**
the store is touched — atomic: committed or refused-with-diagnosis,
never a degraded document (the table silent-empty class is the banned
anti-pattern); refusals return the validator's message plus current
state so the engine's own tool-error loop self-corrects; automatic
repair is limited to mechanical fixes, each reported. Agents write each
entity's **existing** format — the same bytes the human editor reads
and writes (mxGraph XML, JSON Canvas 1.0, Excalidraw scene JSON, figure
registry sources, `TableData`, CSL-shaped reference fields). Formats
TermiPod itself defines are native formats too: `TableData`
(`desktop/src/state/table.ts`) and the figure registry sources are ours
and are written directly. What is forbidden is a **second, agent-only
dialect** layered over the real one — it doubles the parsers, and the
two drift the first time the editor gains a field the translator does
not. Data of ours that belongs inside a third-party container rides as
**namespaced extension fields** (`x-termipod.*` in JSON Canvas —
`desktop/src/state/canvas.ts`), never as a fork of the container's
grammar. Guides serve those grammars and vocabularies. Third-party
logic and data (next-ai-draw-io validators, op engine, shape
libraries — Apache-2.0) are vendored with license headers and a NOTICE
entry.

## 3. Consequences

Easier: one consent story and one apply contract across every tab and
every assistant arm (kimiweb, hub-spawned, remote-via-tunnel); per-tab
capability grows by adding entity surfaces and adapters, not new
architectures; hub-side gaps close with plain catalog additions
(`datasets_*`); attribution and revert become uniform user expectations;
Compare/Record inherit a ready-made model (propose → accept).

Harder / forbidden: no synthetic workbench input, ever — "operate like
a user" is satisfied by entities + pointing + navigation or not at all;
no entity surface duplicated across hub and bridge; write verbs blocked
on focus truthfulness for their surface; desktop writes without a live
lease refuse even for action-token holders; per-editor adapters must be
maintained (a load-once regression breaks D4 visibly — parity tests pin
each); excluded surfaces stay excluded even when a lease exists for
something else.

## References

- Code: `desktop/electron/src/browserbridge.ts`,
  `desktop/src/state/ui_policy.ts`, `desktop/src/state/uiRefFocus.ts`,
  `hub/internal/server/mcp_desktop_ui.go`,
  `hub/internal/server/native_tools.go`,
  `desktop/src/state/documents.ts`, `desktop/src/state/library.ts`,
  `desktop/src/state/annotations.ts`
- Related: [ADR-030](030-governed-actions-and-propose-verb.md),
  [ADR-033](033-tool-catalog-naming-and-registration.md),
  [ADR-053](053-hub-reference-library-entity.md),
  [ADR-059](059-agent-browser-bridge.md),
  [ADR-062](062-desktop-ui-as-agent-addressable-entity.md),
  [ADR-063](063-mcp-version-negotiation-and-adoption.md)
- Fed by: [agent-desktop-coworking.md](../discussions/agent-desktop-coworking.md);
  executed by [agent-desktop-coworking.md](../plans/agent-desktop-coworking.md)
  and [desktop-compare-wall-and-decisions.md](../plans/desktop-compare-wall-and-decisions.md)
