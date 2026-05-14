# 026. Kimi Code CLI is the fourth engine, M1-only

> **Type:** decision
> **Status:** Accepted (2026-05-14)
> **Audience:** contributors
> **Last verified vs code:** v1.0.575

**TL;DR.** Add `kimi-code` (Moonshot AI's "Kimi Code CLI", repo
`MoonshotAI/kimi-cli`, binary `kimi`) as the fourth engine family
alongside claude-code, codex, and gemini-cli. Kimi only ships an ACP
daemon (`kimi acp` subcommand) — there is no stream-json one-shot
mode and no JSON-RPC app-server — so termipod's integration is
**M1-only** through the existing `ACPDriver` with M4 (tmux pane) as
the sole fallback. The cmd template is `kimi --yolo --thinking acp`:
`--yolo` and `--thinking` are kimi-cli top-level flags that precede
the subcommand. `--yolo` auto-approves tool calls at the engine
layer — this intentionally bypasses ACP's `session/request_permission`
gate (gemini-cli's steward template omits `--yolo` for the opposite
reason; the two engines occupy opposite ends of the consent spectrum
in v1). Authentication is out-of-band via `kimi login` run
interactively on the host; the daemon returns `AUTH_REQUIRED` until
login completes and ACPDriver surfaces that as a `kind=error`
agent_event with operator-actionable remediation text. MCP injection
uses kimi's top-level `--mcp-config-file` flag (repeatable, defaults
to `~/.kimi/mcp.json` — a JSON file separate from `~/.kimi/config.toml`,
which we leave entirely untouched).

## Context

Three production-quality CLI agents already integrate cleanly via
their respective driving modes:

| Engine | Primary mode | Driver |
|---|---|---|
| claude-code | M2 (stream-json) | `StdioDriver` |
| codex | M2 (app-server JSON-RPC) | `AppServerDriver` |
| gemini-cli | M1 (Zed ACP) | `ACPDriver` |

The Zed ACP spec — a JSON-RPC 2.0 stdio protocol where the agent is
the server and the host-runner is the client — is the convergence
point. ADR-013 amended gemini-cli to land on M1 once `gemini --acp`
stabilized; ADR-021 generalized the capability surface (auth methods,
prompt image/PDF/audio/video, runtime mode/model switching) over
that ACP path.

Kimi Code CLI (Moonshot AI's "Kimi K2.5" agent driver) is the fourth
engine candidate. Its public surface as of 2026-05-14:

- **`kimi acp`** — long-running stdio ACP daemon (Zed spec, JSON-RPC
  2.0). The same wire shape gemini-cli's `--acp` uses; ACPDriver
  covers it with no Go diff.
- **No stream-json mode.** kimi-cli exposes `--print` for
  non-interactive use but its output format is text-only (one-shot
  pretty-printed). There is no equivalent of claude's
  `stream-json` or gemini's `--output-format stream-json`. M2 is
  therefore not a viable fallback.
- **No JSON-RPC app-server.** kimi-cli has `--wire` (experimental
  JSON-RPC over stdio) but the documentation explicitly marks it as
  "experimental" with no stability commitment. Pinning the engine
  to an experimental flag is the wrong tradeoff; we use ACP.
- **`--yolo` (`-y`)** — top-level flag that auto-approves all tool
  calls at the engine layer. Aliases `--yes`, `--auto-approve`.
- **`--thinking` / `--no-thinking`** — top-level flag that
  enables/disables Kimi K2.5's reasoning mode (no-op on models that
  don't support thinking).
- **`--mcp-config-file <path>`** — top-level, repeatable. Default
  `~/.kimi/mcp.json`. **JSON file**, separate from kimi's main
  `~/.kimi/config.toml` (which carries the operator's Moonshot
  search API key under `[services.moonshot_search]`).
- **`kimi login`** — interactive auth flow (browser OAuth or device
  code). Until login completes on a given host, the ACP daemon
  rejects `session/new` with an `AUTH_REQUIRED` error.
- **SearchWeb** — kimi's built-in web search tool, backed by
  Moonshot's hosted search endpoint (`[services.moonshot_search]`
  in `~/.kimi/config.toml`). Surfaces in ACP as a `tool_call` with
  `name: SearchWeb`. **Native** — kimi's training biases the model
  toward this tool for general queries; teams don't need an MCP
  search server alongside it.

What this means for our integration:

- The ACP path is **bin/template-only** — no Go diff in `ACPDriver`,
  no driver-shape branching, no new wire shape. Adding kimi reuses
  the same handshake / `session/new` / `session/prompt` / event
  translation that gemini-cli's M1 path runs today.
- M1-only is genuine: there is no production-ready M2 fallback flag
  on kimi-cli, so the family `supports: [M1, M4]` with `M4` as the
  resolver's emergency fallback (raw tmux pane, no transcript
  enrichment).
- The interesting differences vs. gemini-cli are all in the
  steward-template layer: cmd flags (`--yolo --thinking`), auth UX
  (out-of-band vs. flag-time), and search-tool ergonomics (native
  vs. MCP).

## Decision

**D1. Family is M1-only.** `supports: [M1, M4]`, no M2. If Moonshot
ships a stable stream-json or app-server mode later, a follow-up ADR
adds the M2 driver branch; until then, hosts whose kimi build can't
speak ACP fall through to M4 (tmux pane) and the operator iterates
on the host's kimi installation.

**D2. cmd template is `kimi --yolo --thinking acp`.** Top-level flags
must precede the `acp` subcommand. The MCP-config writer (W2 of the
implementation plan) splices `--mcp-config-file <path>` between
`kimi` and `--yolo` at materialization time. Final shape:

```
kimi --mcp-config-file <workdir>/.kimi/mcp.json --yolo --thinking acp
```

**D3. `--yolo` is default-on** despite the consent-flow tradeoff.
ACP's in-stream `session/request_permission` gate is bypassed when
`--yolo` is set; the principal sees tool calls in the transcript
but doesn't approve per-call. This is the **opposite** stance from
gemini-cli's steward template, which omits `--yolo` precisely so
the ACP gate fires. The two engines occupy opposite ends of the
consent spectrum in v1 by intent — operators who want per-call
consent on a kimi steward override `cmd` in their team-local
template overlay; operators who want flag-time auto-approve on a
gemini steward do the symmetric override.

We accept the asymmetry rather than papering over it with a
hub-side abstraction because:

- The consent contract is per-engine flag-time anyway; abstracting
  it would require a second normalization layer that adds
  complexity without removing the underlying choice.
- The director can flip the default per-team via the template
  overlay (the same edit-the-yaml-in-the-overlay UX every other
  engine uses).
- The release-testing scenario covers the `--yolo`-on behavior so
  the per-call consent gap doesn't surface as a surprise.

If on-host operation shows the consent gap is materially worse than
the convenience of flag-time auto-approve, a follow-up wedge can
flip the default; the cost is one YAML line.

**D4. `--thinking` is default-on.** Kimi K2.5's reasoning mode
produces better answers on multi-step tasks at the cost of latency
and output verbosity. The steward role is exactly the multi-step-
task profile that benefits. On models that don't support thinking
the flag is a documented no-op, so the default doesn't break older
hosts.

**D5. MCP injection is JSON read-merge-write into a per-spawn
`<workdir>/.kimi/mcp.json`.** Kimi's `--mcp-config-file` is
top-level, repeatable, defaults to `~/.kimi/mcp.json` (JSON, not
TOML). The MCP-config writer (W2) reads the operator's
`~/.kimi/mcp.json` if it exists, deep-merges a `mcpServers.termipod`
entry pointing at `hub-mcp-bridge`, and writes the merged result to
`<workdir>/.kimi/mcp.json` at file mode `0o600`. The cmd splices
`--mcp-config-file <workdir>/.kimi/mcp.json`. Operator-set MCP
servers pass through unchanged. The operator's
`~/.kimi/config.toml` (including their
`[services.moonshot_search].api_key` for kimi's native web search)
is **never read or written** — fully out of our scope, no
secret-copying risk into the per-spawn workdir.

**D6. Authentication is out-of-band.** We do not automate
`kimi login` from the hub. The daemon's `AUTH_REQUIRED` response
on `session/new` (or `session/prompt`) is surfaced verbatim by
ACPDriver as a `kind=error` agent_event; the payload carries
kimi's own error message, which is operator-actionable as-shipped
("authentication required; run `kimi login` to continue" or
similar). If kimi's error string is opaque, W3 adds a one-line
rewrite in `driver_acp.go::translateError`.

**D7. SearchWeb renders as a generic `tool_call`.** Kimi's native
web search emits ACP `session/update`s with a `tool_call` content
block where `name == "SearchWeb"`. We do **not** promote these to
a typed `web_search` agent_event kind in v1 — search results are
voluminous (long passage text, citation lists with snippets), and
a dedicated transcript card would clutter the feed. Operators who
want to inspect raw search output expand the generic `tool_call`
row, same as any other tool. If the generic row turns out to be
materially worse than a dedicated card, a future ADR can revisit;
the cost of waiting is one more transcript-noise complaint.

**D8. Assumed-true ACP capability flags.** W1's family row
declares `runtime_mode_switch.M1: rpc`, `prompt_image.M1: true`,
and `prompt_pdf.M1: true` without on-host verification. The
director's directive (2026-05-14) is to assume kimi's `initialize`
response advertises `session/set_mode`, `session/set_model`,
image content blocks, and PDF content blocks — and to verify
during W4's first end-to-end smoke. Mismatches get patched in W4
itself (same release as the smoke). Mode-switch ladder degrades
safely to "unsupported" if a flag turns out to be wrong, so the
worst case is a non-functional picker, not a crash.

## Consequences

**Becomes possible:**

- A fourth engine ships with the same data-driven substrate as the
  first three — no Go diff in `ACPDriver`, only declarative
  additions to `agent_families.yaml` and a steward template.
- Stewards can be swapped between engines via a template change
  alone, with the asymmetric consent stance documented explicitly
  rather than papered over.
- Operators who want centralized Moonshot search billing override
  the `[services.moonshot_search]` block per-host; the integration
  doesn't fight that pattern.

**Becomes harder:**

- Four engine families to keep in sync. The steward-template tax
  (one YAML + one prompt per engine) is now a four-row tax;
  acceptable because every row is small and copy-edit-friendly.
- Per-engine consent stance is now load-bearing prose, not just
  config — operators need to know that `--yolo`-on (kimi) and
  `--yolo`-off (gemini) are intentional defaults, not oversights.
  The engine-capability matrix entry W5 ships includes a column
  for this.
- The `AUTH_REQUIRED` UX is operator-actionable but requires the
  operator to ssh into the host and run `kimi login` — a step back
  from claude's flag-time auth and codex's env-var auth. We accept
  this rather than building auth-bridging machinery for a single
  engine.

**Becomes forbidden:**

- Spawning kimi-code without `--yolo --thinking` from the built-in
  template. Operators who want the symmetric "ACP-gate-on" stance
  override via team-local template overlay; the built-in default
  stays as decided here for consistency with the release-testing
  scenario.
- Reading or writing `~/.kimi/config.toml`. That file is
  operator-managed; the integration is forbidden from touching it
  to avoid clobbering keys we don't manage (Moonshot search API
  key being the immediate example).

## References

- [ADR-010](010-frame-profiles-as-data.md) — frame profile substrate
  (reserved for kimi-code future polish; not used in v1).
- [ADR-013](013-gemini-exec-per-turn.md) — gemini-cli M1/ACP precedent
  that this ADR builds on.
- [ADR-021](021-acp-capability-surface.md) — ACP capability negotiation
  grammar (auth method, prompt_image, set_mode/set_model RPCs).
  The W1 family row declares per its surface.
- [Plan: Kimi Code CLI engine](../plans/kimi-code-engine.md) —
  wedge-by-wedge implementation tracker.
- Moonshot AI — [`MoonshotAI/kimi-cli`](https://github.com/MoonshotAI/kimi-cli)
- Kimi Code docs — [`kimi acp` subcommand](https://www.kimi.com/code/docs/en/kimi-code-cli/reference/kimi-acp.html),
  [`kimi` command reference](https://moonshotai.github.io/kimi-cli/en/reference/kimi-command.html),
  [config files](https://moonshotai.github.io/kimi-cli/en/configuration/config-files.html)
- DeepWiki — [Command-Line Options Reference](https://deepwiki.com/MoonshotAI/kimi-cli/2.3-command-line-options-reference)
