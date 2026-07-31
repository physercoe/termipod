# MCP 2026-07-28 — do our servers need updating, and what can we borrow?

> **Type:** discussion
> **Status:** Resolved (2026-07-31) — contract extracted to
> [ADR-063](../decisions/063-mcp-version-negotiation-and-adoption.md),
> work scoped in [mcp-2026-07-28-compat.md](../plans/mcp-2026-07-28-compat.md)
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026.730.1231-alpha (`7ab1462a`); spec facts
> verified 2026-07-31 against the published 2026-07-28 revision
> **Freshness:** snapshot

**TL;DR.** The MCP 2026-07-28 revision (published four days ago, the
largest since launch) makes the protocol stateless, replaces
server-initiated round-trips with Multi Round-Trip Requests, adds
header routing and cacheable lists, hardens auth, formalizes an
extensions framework, and deprecates roots/sampling/logging. Auditing
our four hand-rolled servers against it: **nothing breaks today**
(engines still speak ≤ 2025-11-25, and the spec keeps old revisions
alive ≥ 12 months), but our version ceiling is 2025-11-25 with a
silent-downgrade-to-2024-11-05 behavior on unknown versions — the
exact pattern that already caused the v1.0.649 agy incident, re-armed
for the day engines ship 2026-07-28 clients. Beyond compat, four new
features map cleanly onto problems our lanes already fight (blocking
approval calls, body-parsing audit, host jobs, tool UIs). Resolution:
a small additive compat wedge now (plan lane U), borrows folded into
their owning lanes (plan lane B), and the negotiation/adoption
contract pinned as ADR-063.

---

## 1. What changed in 2026-07-28

- **Stateless core.** `initialize`/`initialized` and `Mcp-Session-Id`
  are retired; each request carries protocol version, client identity,
  and capabilities in `_meta`
  (`io.modelcontextprotocol/protocolVersion|clientInfo|clientCapabilities`).
  A new optional `server/discover` RPC provides up-front capability
  discovery — and is the blessed **stdio backwards-compat probe**.
  Explicit tool-minted handles replace hidden session state.
- **Multi Round-Trip Requests (MRTR).** Server-initiated
  `elicitation/create`, `sampling/createMessage`, and `roots/list`
  (which required held-open streams) are replaced: a tool needing
  mid-call input returns `resultType: "input_required"` with
  `inputRequests` + opaque `requestState`; the client retries the
  original call with `inputResponses`. Every result now carries
  `resultType` (`"complete"` normally).
- **Header routing** (Streamable HTTP): `Mcp-Method` and `Mcp-Name`
  are required on POSTs so gateways/WAFs can route and meter without
  parsing JSON bodies.
- **Cacheable lists.** `tools/list`, `prompts/list`,
  `resources/list`, `resources/read` gain `ttlMs` + `cacheScope`
  (`"public"`/`"private"`).
- **Authorization hardening.** RFC 9207 `iss` validation, credentials
  bound to their issuer, Dynamic Client Registration deprecated in
  favor of Client ID Metadata Documents (CIMD).
- **Extensions framework.** Tasks moves from experimental core to the
  `io.modelcontextprotocol/tasks` extension (poll `tasks/get`, new
  `tasks/update`); notifications consolidate into one
  `subscriptions/listen` stream; **MCP Apps** (SEP-1865, stabilized
  2026-01) — servers returning interactive UIs rendered inside chat
  clients — and Enterprise Managed Authorization join the framework.
- **Deprecations.** Roots, sampling, logging, and the legacy HTTP+SSE
  transport — all with a ≥ 12-month offramp. Publication explicitly
  breaks nothing for 2025-11-25 implementers.

## 2. What we ship (audit at `7ab1462a`)

Everything is **hand-rolled JSON-RPC** — no MCP SDK anywhere in the
tree. Four servers, three protocol-blind byte-pump relays
(`hub-mcp-bridge`, `mcp-uds-stdio`, `browser_bridge_stdio.mjs`), zero
MCP client code (the engines are the only clients).

| Server | Anchor | Version posture | Caps / content |
|---|---|---|---|
| Hub `/mcp/{token}` (`termipod-hub`) | `hub/internal/server/mcp.go` | default `2024-11-05` (`:64`), allow-list {2024-11-05, 2025-03-26, 2025-06-18, 2025-11-25} (`:77-82`), echo-if-known-else-downgrade | tools only; text-only results (`mcpResultJSON` stringifies); no annotations (non-spec keys `short`/`concurrency_safe`/`side_effecting`/`tier` instead); plain HTTP POST, batches → `-32700` |
| `hub-mcp-server` stdio daemon (no live spawn path) | `hub/internal/hubmcpserver/run.go:56-78` | same four, same negotiation | only server emitting `listChanged:false`; text-only |
| Host-runner UDS gateway (`termipod-host`) | `hub/internal/hostrunner/mcp_gateway.go:282-303` | same four, same negotiation | tools only; **ships dot-named tools** (`host.ping`, `hub.*`) unfiltered while its siblings filter them |
| Desktop browser bridge (`termipod-browser`) | `desktop/electron/src/browserbridge.ts:1458` | **no allow-list — blindly echoes any `protocolVersion`** (own pin `2025-06-18`) | only server with resources caps and real `image` content blocks; loopback POST, dual read/action bearer tokens |

Cross-cutting: the three Go allow-lists are **triplicated** (comments
point at `server/mcp.go` as canon; no shared constant); **zero**
`MCP-Protocol-Version` header handling on either HTTP transport;
`ToolSpec.ReadOnly` exists but is never rendered as `readOnlyHint`;
authority tools return JSON stringified into text blocks
(`structuredContent` unused); `docs/reference/hub-mcp.md` names a
nonexistent `hub-mcp` binary and claims no spec version.

## 3. Compat analysis — the re-armed incident

Our negotiation semantics (one function, copied three times):
*echo if in the set, else silently downgrade to 2024-11-05*. The
helper exists because of the v1.0.649 incident (`docs/changelog.md`):
agy 1.0.1 sends `2025-11-25` and treats a downgrade as fatal — the
transport died and the agent fell back to filesystem crawling. A
2026-07-28 engine client will hit the same wall on all three Go
servers. The desktop bridge has the complementary failure: it will
*claim* any revision (blind echo) while implementing none of its
semantics — a client believing MRTR works will send `inputResponses`
into a server that has never heard of them.

Neither failure exists **today** (no engine ships a 2026-07-28 client
yet). Both are one engine release away. The fix set is small and
purely additive — plan lane U.

## 4. Borrow analysis — what the new spec gives our existing problems

1. **MRTR ↔ approval-gated tools.** `ui_screenshot`'s per-call consent
   and the browser-bridge approval cards **block the `tools/call`**
   while an attention card waits (the D3 hub leg's timeout machinery
   manages exactly this). MRTR is the spec-shaped replacement:
   `input_required` + `requestState`, engine re-drives. Version-gated
   on engine adoption; our flow is already shaped like it internally.
2. **Header routing ↔ governed actions.** Our relays are byte pumps;
   stamping `Mcp-Method`/`Mcp-Name` on the internal HTTP legs lets
   hub and desktop classify read-vs-action, ring-audit, and
   rate-meter without parsing bodies. Shippable now — internal legs
   only, no client dependency.
3. **Tasks extension ↔ ADR-058.** The host-job surface (detached
   exec, delivered-on-list queue, polling) is nearly isomorphic to
   `io.modelcontextprotocol/tasks`. Aligning job kinds with the
   extension lets engines' native task UIs drive host jobs for free
   once they support it.
4. **MCP Apps ↔ the Companion.** Two directions: the Companion
   renderer can *host* tool-shipped UIs — extending
   [desktop-companion-vision-parity.md](../plans/desktop-companion-vision-parity.md)
   D-1's "vendor's UI when it exists" from engines to tools — and our
   bridge could *ship* approval/annotation UI into other MCP hosts.
   Needs a webview-sandbox security review; the webtab
   partition-policy infrastructure is the right foundation.
5. **`subscriptions/listen` ↔ the sharing toggle.** Toggling UI
   sharing changes tool availability silently today
   (`listChanged:false` everywhere); the consolidated stream is the
   standard fix, when clients listen.
6. **Anti-borrow, already right:** we never built on roots, sampling,
   or logging — the hub deliberately 204s roots notifications. Nothing
   to unwind.

## 5. Options considered

- **Do nothing until engines move.** Rejected: the downgrade path is
  a known-fatal pattern with a production precedent, and every compat
  item is additive — waiting buys nothing and risks a scramble.
- **Adopt an MCP SDK.** Rejected for now: four small hand-rolled
  servers with distinct auth/transport shapes (URL-token HTTP, UDS,
  loopback dual-bearer) would each fight an SDK's opinions; the spec
  delta is a handful of fields and one method. Revisit if we ever
  implement MRTR server-side in earnest.
- **Additive compat wedge + contract ADR + lane-folded borrows.**
  Chosen — see the plan and ADR-063.

## 6. Resolution

Proceed per [mcp-2026-07-28-compat.md](../plans/mcp-2026-07-28-compat.md)
(lane U = compat, lane B = borrows folded into owning lanes), under
the negotiation/adoption contract of
[ADR-063](../decisions/063-mcp-version-negotiation-and-adoption.md).
Related:
[ADR-033](../decisions/033-tool-catalog-naming-and-registration.md)
(dot-name filtering rationale),
[ADR-058](../decisions/058-host-job-surface.md) (tasks alignment),
[ADR-059](../decisions/059-agent-browser-bridge.md) /
[ADR-062](../decisions/062-desktop-ui-as-agent-addressable-entity.md)
(bridge posture), [hub-mcp.md](../reference/hub-mcp.md) (needs refresh).
