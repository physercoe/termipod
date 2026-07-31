# 063. MCP version negotiation + adoption posture

> **Type:** decision
> **Status:** Proposed (2026-07-31) — extracted from
> [mcp-2026-07-28-adoption.md](../discussions/mcp-2026-07-28-adoption.md)
> before the compat wedge ships, per the ADR-059 lesson: pin the
> invariants at decision tier before there is an implementation to
> drift from them
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** 2026.730.1231-alpha (`7ab1462a`)
> (`hub/internal/server/mcp.go:64-95`, `hub/internal/hubmcpserver/run.go:56-78`,
> `hub/internal/hostrunner/mcp_gateway.go:282-303`,
> `desktop/electron/src/browserbridge.ts:1458`)

**TL;DR.** We ship four hand-rolled MCP servers whose spec posture has
drifted three ways (triplicated Go allow-lists topping out at
2025-11-25; a desktop bridge that echoes any version unseen). This ADR
pins the contract all four honor: **(1)** one canonical supported-set
per language with pinned cross-language parity, negotiation =
echo-if-known / downgrade-to-floor for unknowns — and **never** a blind
echo; **(2)** spec adoption is **additive-first**: response-side fields
of newer revisions are stamped unconditionally, request-side semantics
are gated on the negotiated version; **(3)** deprecated features
(roots, sampling, logging, HTTP+SSE) are never adopted; **(4)**
tool-metadata truth is dual-published (spec annotations *and* our
registry keys). Wire-visible tool names are frozen regardless of what
newer revisions legalize.

---

## 1. Context

The MCP spec now revs roughly twice a year (2024-11-05 → 2025-03-26 →
2025-06-18 → 2025-11-25 → 2026-07-28), and 2026-07-28 is a
structural break: stateless core, per-request `_meta`,
`server/discover`, MRTR (`resultType`), cacheable lists, an extensions
framework, and a deprecation list. Our four servers
(hub `/mcp/{token}`, `hub-mcp-server`, the host-runner UDS gateway,
the desktop browser bridge) are hand-rolled and negotiated
independently: three carry byte-identical version sets as **separate
copies**, the fourth has no set at all. The v1.0.649 incident
(changelog; `mcp.go:67-76`) established that version handling is not
cosmetic — a strict client (agy 1.0.1) treats a silent downgrade as
fatal and the whole transport dies. Engines will ship 2026-07-28
clients on their own schedules; we control neither side of that
timing.

## 2. Decision

**D1 — one canonical set per language, parity pinned.** The supported
protocol-version set and the negotiation function live in exactly one
place per language: a shared Go package consumed by all three Go
servers, and one exported constant in the desktop bridge. The two are
kept identical by a parity test that pins both lists against the same
fixture (they cannot share a constant across languages; they can share
a test corpus). Adding a revision is a one-line change per language +
fixture update — never a three-file hunt.

**D2 — negotiation semantics.** On `initialize`: echo the client's
version if it is in the set; otherwise answer with the **floor**
(2024-11-05), never an unlisted version and never a blind echo of the
client's ask. A request arriving **without** prior `initialize`
(2026-07-28 stateless style) is served, taking version and identity
from `_meta` when present; `server/discover` is implemented on all
four servers as the capability/version probe (it is also the spec's
stdio backwards-compat probe). Rationale: echo-if-known keeps strict
clients alive (the agy lesson); downgrade-to-floor for unknowns is the
only honest answer a server can give about semantics it has not
implemented — the desktop bridge's blind echo is the forbidden
anti-pattern, because *claiming* a revision promises request-side
semantics (e.g. MRTR `inputResponses`) the server would then silently
mishandle.

**D3 — additive-first adoption.** Response-side fields introduced by
newer revisions that old clients ignore by JSON tolerance —
`resultType: "complete"`, `ttlMs`/`cacheScope` on lists,
`structuredContent` alongside the text block — are stamped
**unconditionally**, on every negotiated version. Request-side or
behavioral semantics — honoring `inputResponses`, emitting
`input_required`, header-based routing requirements — are implemented
**only** behind the negotiated (or `_meta`-declared) version. This
keeps one code path for responses and confines version branching to
the places where meaning actually differs.

**D4 — deprecation discipline.** Roots, sampling, logging, and the
legacy HTTP+SSE transport are never adopted (we never did; the hub
deliberately 204s roots notifications — `mcp.go:120-139`). New
capabilities land as spec **extensions** where one exists
(`io.modelcontextprotocol/tasks` for host jobs per
[ADR-058](058-host-job-surface.md); MCP Apps for tool-shipped UI)
rather than as private methods.

**D5 — tool metadata is dual-published; wire names are frozen.** The
registry's truth (`ToolSpec.ReadOnly`, `side_effecting`,
`concurrency_safe`, `tier`) is published **both** as spec annotations
(`readOnlyHint`, `title`; `destructiveHint` only where the registry
key genuinely means destructive) and as the existing custom keys —
spec-aware clients get standard hints (which drive their auto-approve
UX), ours keep the richer vocabulary. Wire-visible tool names never
change for spec reasons: the gateway's dot-named tools stay
(`termipod-host` serves claude M4 only, where dots are safe), the
hub's dot-filter stays (agy 1.0.1 protection, ADR-033) — renaming
either direction is a wire break for zero semantic gain.

## 3. Consequences

Easier: version bumps (one line + fixture per language); reasoning
about mixed-fleet engine upgrades (each engine negotiates
independently, all outcomes are defined); response-shape evolution
(additive fields need no version branch); future extensions (a
declared framework slot instead of ad-hoc methods).

Harder / forbidden: the desktop bridge may no longer echo unknown
versions (a 2026-07-28-strict client is refused honestly at the floor
until we implement the semantics — visible, debuggable, and fixable by
a version-set bump, unlike silent mishandling); contributors must
route any new version string through the shared set (the parity test
fails otherwise); no private capability methods where an extension
slot exists.

## References

- Code: `hub/internal/server/mcp.go`, `hub/internal/hubmcpserver/run.go`,
  `hub/internal/hostrunner/mcp_gateway.go`,
  `desktop/electron/src/browserbridge.ts`,
  `hub/internal/server/mcp_protocol_negotiation_test.go`
- Related: [ADR-033](033-tool-catalog-naming-and-registration.md),
  [ADR-058](058-host-job-surface.md),
  [ADR-059](059-agent-browser-bridge.md)
- Fed by: [mcp-2026-07-28-adoption.md](../discussions/mcp-2026-07-28-adoption.md);
  executed by [mcp-2026-07-28-compat.md](../plans/mcp-2026-07-28-compat.md)
