# 059. Agent browser bridge — CDP over guest partitions, bearer-scoped

> **Type:** decision
> **Status:** Accepted (2026-07-30, director) — records the security architecture that
> shipped as W1 (#471) + W2 (#472) of
> [`plans/desktop-agent-browser-bridge.md`](../plans/desktop-agent-browser-bridge.md),
> extracted to decision tier **before W3** (hub relay + per-call approval
> cards) so the invariants below are citable and CI-defended after the plan
> flips to Done/snapshot. Nothing here changes shipped behaviour.
> **Audience:** principal · contributors · maintainers
> **Last verified vs code:** origin/main `589a01b0`
> (`desktop/electron/src/browserbridge.ts` / `browserbridge_host.ts`,
> `desktop/electron/src/webtab_policy.ts:35-76`,
> `hub/internal/hostrunner/launch_m2.go:326,637-770`)

**TL;DR.** Agents get a browser by driving the browser the desktop already
has: an **MCP server in the Electron main process** exposes `browser_*`
tools over **CDP (`webContents.debugger`) against `<webview>` guest
partitions only** — never a separate browser, never the user's OS browser,
never arbitrary `webContents`. Delivery is an **additive `termipod-browser`
MCP entry** the host-runner injects at spawn from a `0o600` discovery file;
tokens are **per-app-run** and die at quit. The trust model is four stacked
gates, each fail-closed: (1) a tab exists to the bridge only if its
partition's policy row opts in (`bridge: full | read | none`); (2) the
caller's **scope is derived from the bearer alone** — the read token never
unlocks action tools, and read sessions never even *list* them; (3)
partitions that host another agent's UI (`kimiweb`, `rerunweb`) are pinned
**read-only** regardless of caller scope; (4) **URL fragments are secrets**
and are never emitted anywhere. Every action call writes one redacted audit
entry. What audit attribution and per-call consent still lack is W3's
problem — and W3 must build on these gates, not around them.

## Context

Kimi's WebBridge model (agent drives a real browser over an extension)
was rejected in the plan's §1.1: TermiPod already embeds the surfaces
agents need as `<webview>` guests with per-partition policy
(`webtab_policy.ts`, shipped with read-web-tabs), so a bridge over those
guests inherits an allowlist that already exists instead of inventing a
second browser-shaped attack surface. Stock computer-use screenshot
tools were rejected (§1.4) as both weaker (no DOM) and wider (whole
screen). The MCP delivery channel also already existed:
`writeMCPConfigForFamily` (`launch_m2.go`) writes per-family MCP config
at spawn for all four engine families plus codex's TOML.

Two facts shape the trust model:

- The kimiweb embed URL carries its bearer **in the URL hash**
  (`#token=…`, non-persistent partition precisely to keep it off disk).
  Any bridge that echoes raw URLs hands that token to every
  bridge-enabled agent.
- A bridge-enabled agent typing into the kimiweb guest is **one agent
  prompting another agent's chat UI with the user's authority**.
  Navigation policy (`allowTopFrame`) says nothing about input
  injection; it had to be a separate gate.

## Decision

### D-1 — The bridge is CDP over allowlisted guest partitions, in main

One MCP server in the Electron main process (`browserbridge.ts` pure
logic, `browserbridge_host.ts` Electron wiring), speaking CDP through
`webContents.debugger` to `<webview>` guests. The tab registry admits a
guest **only** if its partition's `webtab_policy.ts` row declares
`bridge: 'full' | 'read'`; a partition with `'none'` — or with no row —
never enters the registry. Every CDP command re-resolves its target
through the registry (`resolveGuest`), so a guessed or stale id never
reaches a debugger session. There is no generic CDP passthrough: the
tool surface is curated (read: `browser_list_tabs`, `browser_find_tab`,
`browser_snapshot`, `browser_read_text`, `browser_screenshot`; action:
`browser_navigate`, `browser_click`, `browser_type`,
`browser_send_keys`, `browser_scroll`, `browser_eval`,
`browser_upload_file`), and `Runtime.evaluate` interpolates only
validated values.

### D-2 — Delivery is an additive MCP entry from a per-run discovery file

When the user enables the bridge, the desktop mints tokens
(`crypto.randomBytes(24)`, base64url) and writes
`~/.termipod/browser-bridge.json` (`0o600` in a `0o700` dir): loopback
`url`, `token` (read), `action_token` (W2), pid/version metadata. The
host-runner injects an additive `termipod-browser` MCP entry into every
spawn's config — all four families + codex TOML — and **discards the
tokens at app quit**; a fresh run mints fresh tokens, so a leaked one
ages out with the process. The hub is not in the W1/W2 path at all.

The host-runner's gate on the discovery URL is **parsed, not
pattern-matched**: `url.Parse`, scheme `http`, no userinfo, hostname ∈
{`127.0.0.1`, `localhost`, `::1`}. (The first cut was a string-prefix
check; `http://127.0.0.1:8080@evil.com/mcp` passes a prefix check and
POSTs the bearer to `evil.com`. Fixed in W1 review, pinned by tests —
the recurring lesson that URL authority checks must go through a
parser.)

### D-3 — Scope is derived from the bearer alone; read is the default

Two bearers, one scope decision, made at MCP-session construction from
which token authenticated — never from a header, an env var, or a tool
argument. The **read token** rides every spawn while the bridge is
live. The **action token** is injected only when the spawn's spec opts
in (`browser_bridge: true` in `spawn_spec_yaml` →
`spec.BrowserBridge`, threaded `launch_m2.go:326`). Read-scoped
sessions do not *list* action tools; a direct call anyway gets a typed
`SCOPE_READ_ONLY` error naming the respawn fix. `TP_BROWSER_SCOPE` in
the agent's env is informational only. A W1-era discovery file (no
`action_token`) degrades an opted-in spawn to read, with a log note —
fail-closed across versions.

### D-4 — Embedded agent UIs are read-only to every caller

`kimiweb` and `rerunweb` are pinned `bridge: 'read'` in the policy
table, and action tools refuse their tabs with `PARTITION_READ_ONLY`
(`requireActionTarget`) **regardless of the caller's scope** — the
action token widens *what the caller may do*, never *what a partition
admits*. `rerunweb` is deliberately identical to `kimiweb` rather than
looser, so "another web UI is one registry row" cannot quietly widen
policy. `browser_navigate` additionally re-applies the partition's
`allowTopFrame` predicate fail-closed — loopback-pinned partitions
refuse non-loopback URLs even though they're read-only anyway.
Revisiting action access on these partitions requires W3's per-call
approval cards, not a policy-row edit.

### D-5 — URL fragments are secrets; the bridge never emits one

Every URL the bridge emits — tool results, tab listings, audit entries,
hub mirrors — passes `stripFragment` first (a plain string cut, not URL
parsing, so it cannot mis-parse its way into leaking). Pinned by a
regression test with a realistic `#token=` kimiweb URL. Rationale: any
embedded surface may ride credentials in the hash (kimiweb does today);
a curated tool surface that echoes URLs is otherwise a token oracle.

### D-6 — Every action call is audited, redacted, locally and to the hub

One audit entry per action call: typed text becomes `<redacted N
chars>`, single-character keys likewise, `browser_eval` code capped at
200 chars. A last-50 in-memory ring backs the desktop UI; a best-effort
mirror posts `browser_bridge`-kind agent events to the hub, reading the
hub bearer **in-process** (`keychainGetLocal`) so it never crosses IPC.
Audit must never contain what D-5 forbids — redaction happens before
both sinks.

### D-7 — Attribution is advisory until W3; W3 extends, never bypasses

`x-tp-agent-id` is caller-asserted, so audit attribution is advisory —
recorded honestly as such. W3 (hub relay for remote agents, per-call
approval cards) is the layer that adds verified identity and consent.
The constraint this ADR exists to state: **W3's relay must terminate in
this same bridge server behind gates D-1–D-6** — a relay that speaks
CDP to guests directly, or that mints its own scope decisions, recreates
the surface this design closed.

## Consequences

- Agents can read any `webtab` page the user could open, and act on
  `webtab` pages when the spawn was opted in — with the user's cookies
  in that partition. That is the accepted risk the audit trail (D-6)
  and W3 approvals exist to govern; the non-persistent partitions and
  per-run tokens bound its blast radius.
- A new embedded surface gets bridge access by **choosing** a `bridge:`
  value in its policy row — review happens at that one line.
- The kimiweb/rerunweb read-only pin means some legitimate automation
  ("ask the assistant in my kimi tab") is impossible until W3. Accepted:
  cross-agent prompting with user authority is the worst primitive in
  this surface's threat model.
- Fragment redaction is load-bearing for kimiweb's token scheme; if an
  embed ever moves its token out of the hash, D-5 still holds (defence
  stays, dependency goes).
- Tokens die with the app run; there is nothing to rotate, revoke, or
  sync. The discovery file is the only persistence and it is mode-gated.

## Alternatives considered

- **Real browser via extension (WebBridge proper).** Rejected (plan
  §1.1): a second browser surface with its own profile, updater, and
  extension trust chain, to reach pages the app already embeds.
- **Stock computer-use / screenshot tools.** Rejected (plan §1.4):
  screen-wide (wider than the partition allowlist) and DOM-blind
  (weaker than CDP), the wrong shape on both axes.
- **Generic CDP passthrough tool.** Rejected: CDP is a debugger
  protocol; passthrough is "run anything in the page" plus "read
  anything in the process". The curated tool list is the surface.
- **Scope from request metadata (header/env/argument).** Rejected:
  caller-controlled. The bearer is the only input the server trusts.
- **Persistent bearer tokens.** Rejected: nothing needs them (spawns
  are re-configured per run by the host-runner), and per-run tokens
  make leak recovery "restart the app".

## References

- Code: `desktop/electron/src/browserbridge.ts` (registry, scopes,
  `stripFragment`, audit ring) · `desktop/electron/src/browserbridge_host.ts`
  (CDP wiring, snapshot-ref pruning on guest destroy) ·
  `desktop/electron/src/webtab_policy.ts` (per-partition `bridge:` rows) ·
  `hub/internal/hostrunner/launch_m2.go` (discovery gate, MCP injection,
  `spec.BrowserBridge`)
- Plan: [`plans/desktop-agent-browser-bridge.md`](../plans/desktop-agent-browser-bridge.md)
  (§3.5 security model this ADR extracts; §4 wedges; W3 open questions)
- Related ADRs: [021](021-acp-capability-surface.md) (capability-surface
  precedent) · [030](030-governed-actions-and-propose-verb.md) (the
  approval-ladder shape W3 should reuse) ·
  [052](052-breakglass-ssh-and-key-vault.md) (webview + policy substrate)
