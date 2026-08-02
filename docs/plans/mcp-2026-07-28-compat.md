# MCP 2026-07-28 compat + borrows

> **Type:** plan
> **Status:** In progress (2026-08-01) — **lane U shipped** (U1–U10) with
> **B2**; lanes B1/B3/B4/B5 stay with their owning lanes. See §5 for the
> three places the spec text disagreed with this plan's reading and what
> was built instead
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026-08-01 — lane U implemented against the
> published 2026-07-28 changelog (SEP-2575 / 2322 / 2549 / 2243 / 2106)
> **Freshness:** contract

**TL;DR.** Execute
[ADR-063](../decisions/063-mcp-version-negotiation-and-adoption.md)
against the four hand-rolled MCP servers: **lane U** is the additive
compat wedge (shared version set + 2026-07-28, desktop allow-list,
`MCP-Protocol-Version` headers, `resultType`, `ttlMs`/`cacheScope`,
`server/discover`, `_meta` tolerance, annotations dual-publish,
`structuredContent`, doc refresh) — all shippable now, nothing gated
on engines. **Lane B** routes each borrow to the lane that owns the
problem it solves (MRTR → browser-bridge approvals, headers → relays,
tasks → ADR-058, MCP Apps → companion plan, `subscriptions/listen` →
sharing toggle). Investigation record:
[mcp-2026-07-28-adoption.md](../discussions/mcp-2026-07-28-adoption.md).

---

## 1. Lane U — the compat wedge (additive, no engine dependency)

Ordered; U1–U7 are one Go PR + one desktop PR.

- **U1 — one shared version set (ADR-063 D1) + add `2026-07-28`.**
  New tiny package `hub/internal/mcpver`: the supported set, the floor
  constant, and one `Negotiate(clientAsk string) string` — semantics
  exactly ADR-063 D2. The three copies
  (`server/mcp.go:64-95`, `hubmcpserver/run.go:56-78`,
  `mcp_gateway.go:282-303`) collapse onto it;
  `mcp_protocol_negotiation_test.go` moves with it and gains the
  2026-07-28 row + a shared fixture file the desktop test also reads.
- **U2 — desktop bridge allow-list.** Replace the blind echo at
  `browserbridge.ts:1458` with the mirrored set + floor fallback;
  parity test against the U1 fixture (ADR-063 D1). Node tests pin the
  refusal shape for an unknown ask.
- **U3 — `MCP-Protocol-Version` headers.** Both HTTP transports
  (hub `handleMCP`, bridge `startBridgeServer`): read the request
  header when present (it wins over a body-less guess, is validated
  against the set), and set the negotiated version on every response.
- **U4 — `resultType` stamping (ADR-063 D3).** The result builders —
  `mcpResultText`/`mcpResultJSON`/`mcpResultError` (hub),
  `run.go:311-330` (daemon), `mcpGWResultText` (gateway), the bridge's
  result literals — gain `resultType: "complete"` unconditionally.
- **U5 — cacheable lists.** `tools/list` (and the bridge's
  `resources/list`) gain `ttlMs` + `cacheScope: "private"`; TTL from
  one constant (suggest 300 000 ms — catalogs change only on sharing
  toggle / hook reinstall).
- **U6 — `server/discover`** on all four servers: serverInfo,
  capabilities, and the supported version list (from U1/U2).
- **U7 — per-request `_meta` tolerance.** When
  `io.modelcontextprotocol/protocolVersion` / `clientInfo` are present
  on a request, they feed negotiation (no prior `initialize`
  required — the hub HTTP transport is already per-request) and the
  audit line (alongside `x-tp-agent-id`).
- **U8 — annotations dual-publish (ADR-063 D5).** Render
  `annotations.readOnlyHint` from `ToolSpec.ReadOnly` and `title` from
  the display name across all catalogs; `destructiveHint` only where
  a registry key genuinely means destructive. Custom keys stay.
- **U9 — `structuredContent`.** Authority tools that build JSON
  results emit it as `structuredContent` **in addition to** the
  stringified text block (the text fallback stays — the
  `run.go:273-275` client-rendering caveat still holds for older
  clients).
- **U10 — hygiene.** Batch rejection answers `-32600` (hub currently
  `-32700`); `docs/reference/hub-mcp.md` refresh (no `hub-mcp` binary,
  `ping` exists, no tool count in initialize, states the ADR-063
  posture + version set); ADR-033 gets a one-line pointer to ADR-063
  D5 (wire names frozen).

## 2. Lane B — borrows, routed to their owning lanes

- **B1 — MRTR-shaped approvals** → the browser-bridge/ui-context
  lane. Design the approval flow's external shape as
  `input_required` + `requestState` behind a version gate, keeping
  today's blocking behavior for ≤ 2025-11-25 clients. The D3 hub-leg
  seams (`uicapture_hub.ts`) already isolate the wait — the refactor
  is contained. **Gate: first engine client that negotiates
  2026-07-28.**
- **B2 — relay header stamping** → shippable with lane U: the three
  relays add `Mcp-Method`/`Mcp-Name` (parsed from the single JSON-RPC
  object they already frame) on their internal HTTP legs; hub and
  desktop hosts may then classify/meter/ring-audit without body
  parsing. Internal-only; no client dependency.
- **B3 — tasks extension alignment** → ADR-058's lane. When engines
  ship the `io.modelcontextprotocol/tasks` client side, expose host
  jobs under the extension (poll `tasks/get` ≈ delivered-on-list
  queue). Author as an ADR-058 amendment at that point; nothing to
  build now.
- **B4 — MCP Apps** → the companion plan
  ([desktop-companion-vision-parity.md](desktop-companion-vision-parity.md)):
  a follow-up wedge exploring (a) the Companion renderer hosting
  tool-shipped UIs (D-1's vendor-first principle extended to tools)
  and (b) our bridge shipping approval/annotation UI into other MCP
  hosts. Requires a webview-sandbox security review on the webtab
  policy pattern **before** any prototype renders foreign UI.
- **B5 — `subscriptions/listen` / catalog-change truthfulness** →
  the sharing-toggle owner (desktop bridge): when clients adopt the
  stream, emit tool-list-changed on toggle flips instead of today's
  silent mutation; until then U5's short TTL bounds the staleness.

## 3. Non-goals

Adopting an MCP SDK (rejected in the discussion §5); implementing MRTR
server-side before any client can drive it; MCP OAuth/CIMD (our auth
is bearer-env over local transports; revisit with the ADR-059 remote
leg); renaming any wire-visible tool (ADR-063 D5); building on roots /
sampling / logging (ADR-063 D4).

## 4. Review anchors

- The U1 negotiation semantics must stay byte-compatible for the four
  existing versions — `mcp_protocol_negotiation_test.go` is the
  contract; the agy downgrade-fatal comment (`mcp.go:67-76`) must
  survive the move.
- U4/U5/U9 are response-shape changes visible to **all** engines at
  once — run the M2/M4 smoke set per family (claude, codex, kimi-ts,
  agy) before merge; agy 1.0.1 is the canary for unknown-field
  tolerance.
- U8's `destructiveHint` mapping is conservative-by-default: when in
  doubt, omit — a wrong `readOnlyHint=true` invites client
  auto-approve of a mutating tool (permission-relevant, treat as a
  security review item).
- The bridge's dual-token read/action split must be unaffected by
  U2/U3/U6 (`browserbridge.ts:1583-1590` scope checks precede method
  dispatch).
- CI blind spot: desktop node tests run manually
  (`node --test src/state/*.test.ts src/ssh/*.test.ts` plus the
  electron suite) per wedge.

## 5. Deltas found while building (2026-08-01)

Three places where the published 2026-07-28 text disagreed with this
plan's reading. All three were resolved toward the spec.

- **Server identity moved.** The revision took `serverInfo` *out* of
  the handshake body and into every result's
  `_meta["io.modelcontextprotocol/serverInfo"]` (SEP-2575) — a
  stateless client never sees an initialize response to read it from.
  U6 as written ("serverInfo, capabilities, and the supported version
  list") could not be satisfied without it, so all four servers now
  stamp identity on every result. The handshake keeps its body copy
  for 2024/2025-era clients.
- **`resources/read` is cacheable too.** U5 named `tools/list` and the
  bridge's `resources/list`; SEP-2549's `CacheableResult` covers
  `resources/read` as well (and `prompts/list` /
  `resources/templates/list`, which we do not serve). The bridge's
  `ui://focus` read carries `ttlMs`/`cacheScope` accordingly.
- **`ping` is gone in 2026-07-28** (SEP-2575), which U10's doc note
  did not say. We keep serving it — the revisions our engines actually
  speak have it, and a 2026-era client simply never calls it.

- **`structuredContent` is object-only on our wire** (review
  amendment, 2026-08-02). The 2025 revisions already know the key —
  typed as an object (`{ [key: string]: unknown }`); SEP-2106's
  any-JSON widening is 2026-07-28-only. Additive-first covers unknown
  keys, not known keys with a widened type, so version-blind responses
  attach `structuredContent` only for object-shaped results; the list
  tools' arrays stay text-only (`mcpwire.AttachStructuredContent`)
  until responses version-branch.

One deliberate divergence, flagged rather than taken: the revision
answers an unsupported client-declared version with
`UnsupportedProtocolVersionError` (`-32022`), where
[ADR-063](../decisions/063-mcp-version-negotiation-and-adoption.md) D2
says serve at the floor. D2 is the accepted contract and its reasoning
holds — our responses are additive, so a floor-served answer is *also*
a valid 2026-07-28-shaped one, and serving beats refusing. Erroring
instead would be an ADR-063 amendment, not an implementation choice.

Also noted, not fixed: the desktop bridge answers a sharing-toggle
refusal on `resources/read` with `-32002`. The revision renumbered
*resource-not-found* from `-32002` to `-32602`, which this is not — it
is a policy refusal, and `-32000`–`-32019` stays implementation-defined
and grandfathered. Left alone deliberately.

## 6. Acceptance

A 2025-11-25 client (agy fixture) and a 2024-11-05 client negotiate
exactly as today (regression corpus green); a synthetic 2026-07-28
client gets its version echoed, can call `server/discover`, sees
`resultType`/`ttlMs`/`cacheScope`/`annotations`/`structuredContent`
on the wire, and can send `_meta`-only requests without `initialize`;
an unknown future version (`2027-01-01`) is answered with the floor on
**all four** servers including the desktop bridge; the Go and TS
version sets fail CI if they diverge from the shared fixture; relay
HTTP legs carry `Mcp-Method`/`Mcp-Name` and the hub audit line shows
them.
