# Pane-state manifests — declarative screen detection for every engine

> **Type:** plan
> **Status:** Proposed (2026-08-08) — for review. Derived from
> [`discussions/herdr-runtime-borrows.md`](../discussions/herdr-runtime-borrows.md)
> (B1 flagship + B2/B3/B4/B5 independents). The vendor-vs-fork call is
> made: the director chose **vendor-plus-overlay** (2026-08-08); D-1
> records it. Lane P is the core; lanes R/W/I are independent and can
> interleave with other work.
> **Audience:** principal · contributors
> **Last verified vs code:** main `e498416d` (2026-08-08;
> `hub/internal/hostrunner/idle.go`, `driver_pane.go`,
> `hub/internal/agentfamilies/`, `docs/reference/attention-kinds.md`)

**TL;DR.** Host-runner can only say "this agent needs you" for the
three engines with structured M4 adapters; everything else gets
`idle.go`'s one-regex, 90-second stall heuristic that misses most
modern TUI approval panels. This plan ports herdr's screen-manifest
detection as a new fallback state authority: **P1** a pure Go
evaluator for herdr's manifest schema + the 19 vendored TOMLs
(byte-exact, Apache-2.0, `shapes/`-style provenance) + an overlay
layer where all termipod-specific content lives; **P2** wiring into
the existing 2 s pane-capture tick with the ported hysteresis
semantics, posting state as agent events; **P3** blocked→attention
with rule-id evidence, retiring `IdleDetector` for covered agents,
and capture-cost gating; **P4** an explain verb + Inspect surface;
**P5** hub-distributed manifest updates with herdr's tamper-evidence
rule. Independent lanes: **R1** the 16-engine native-resume recipe
table (feeds teleport respawn + vision-parity L3/L4), **W1/W2**
prompt-effect two-phase waits and the done-until-seen bit, **I1**
pane-input hardening (`paste-buffer -p`, generic multi-line path).

## 1. Context and grounding

- **What exists.** `PaneDriver` ticks `capture-pane -p -J` every 2 s
  (`driver_pane.go:30`) for pane-anchored agents; `IdleDetector`
  (`idle.go`) is the only state heuristic for engines without a
  structured driver — one global prompt regex + 90 s content-hash
  stall, gated off registered families by `hasStructuredDriver`, one
  attention raise per idle streak. The `idle` attention kind exists
  ([attention-kinds.md](../reference/attention-kinds.md)). Structured
  state for claude-code / kimi-code-ts / antigravity comes from M4
  LocalLogTail adapters + parked hooks (ADR-027).
- **What herdr proved** (full code-read in the
  [borrows discussion](../discussions/herdr-runtime-borrows.md) §3):
  19 per-agent TOML manifests classify any TUI agent from a
  bottom-anchored screen snapshot; strict-blocked semantics keep
  attention trustworthy; an OSC-title channel outranks the screen;
  remote rule updates need no binary release; `explain` makes screen
  rules debuggable. Schema: rules with `state` / `priority` /
  `region` / `contains` / `regex` / `line_regex` / nested
  `all`/`any`/`not` gates; priority argmax, ties to file order;
  validation caps (≤128 rules, gate depth ≤8, ≤1024 matchers).
- **The concrete failure this closes.** codex blocked on "Allow
  command?" raises nothing today (the regex doesn't match, and the
  90 s threshold would apply even if it did). And the class is proven
  in-house: `preTrustWorkspaceClaudeCode` exists precisely because
  claude's trust dialog blocks invisibly to every structured signal —
  B1's `visible_blocker` is the general detector for that class.

## 2. Design decisions

- **D-1 — vendor-plus-overlay (director, 2026-08-08).** herdr's
  manifest schema is adopted as the format. `manifests/vendor/` holds
  the 19 TOMLs **byte-exact** at a pinned upstream commit
  (`6f311498`, Apache-2.0) — never hand-edited, diffable against
  upstream, provenance pinned by blob-SHA test (the `shapes/`
  discipline; NOTICE gains a paragraph). Everything termipod-specific
  lives in `manifests/overlay/`: our own manifests for engines herdr
  lacks, and per-agent override files that shadow a vendored one
  entirely (herdr's own local-override precedence, repurposed as our
  extension point). Re-vendoring = diff + bump + fixture run. Fork
  triggers (recorded, not planned): upstream schema evolution we
  refuse to follow, a relicense, or the overlay growing into a fork.
- **D-2 — authority order.** Structured driver (M4 tail + hooks) >
  screen manifest > nothing. `hasStructuredDriver` generalizes to
  "has a state authority": the manifest evaluator never runs against
  an agent whose adapter authors state — with one ported exception:
  a rule carrying `visible_blocker` that matches on a
  structured-authority pane MAY raise attention (screen shows a live
  permission dialog the hooks never reported). It adjusts attention
  only, never the session's driver-authored state.
- **D-3 — engine-kind mapping lives in the overlay, not in vendored
  files.** herdr ids (`claude`, `kimi`, `gemini`) differ from our
  family names (`claude-code`, `kimi-code-ts`, `gemini-cli`). A
  single mapping table in the overlay config binds spawn-spec kind →
  manifest id. Unmapped kinds get no evaluation (never a guess);
  unknown manifest ids in the mapping fail validation loudly.
- **D-4 — capture geometry is a contract.** The evaluator's input is
  the **bottom-anchored last 24 rows** of the live screen (herdr's
  `DEFAULT_DETECTION_ROWS`), because the vendored rules were written
  against exactly that shape. `capture-pane -p -J` already returns
  the live screen (alt-screen included); P2 trims to the contract.
  The OSC-title region is fed from tmux `#{pane_title}` (one
  `list-panes -F` round-trip covers all panes); the `osc_progress`
  region is always empty under tmux — rules referencing it never
  match, which the schema tolerates by design (documented, not
  worked around).
- **D-5 — ported semantics are verbatim, and pinned.** Strict
  blocked; no-match on a known agent = idle labeled
  `default_known_agent_idle_fallback`; `skip_state_update` freezes
  (transcript viewers / model pickers); asymmetric hysteresis
  (working→bare-idle held 3×100 ms capped 700 ms — bypassed by
  visible idle chrome; blocked/working publish instantly); 3 s
  startup grace after agent identification (braille-splash trap).
  Each constant and rule lands with a fixture that fails if it
  drifts. herdr's 800 ms visible-blocker re-publish does NOT port —
  the hub's attention model already owns re-delivery; we raise once
  per blocked streak (IdleDetector's existing contract).
- **D-6 — output rides existing rails.** Classification posts as
  agent events (producer `panestate`) feeding session status; a
  `blocked` classification raises the existing attention flow with
  the matched rule id + a bounded region excerpt as evidence.
  No new tables; no new attention kind unless P3 review finds `idle`
  semantically wrong for "blocked on approval" (decide in-wedge
  against [attention-kinds.md](../reference/attention-kinds.md)).
- **D-7 — distribution starts embedded, hub later.** P1 embeds
  vendor + overlay via `go:embed`; binary upgrades ship rule fixes.
  P5 adds hub-distributed updates with herdr's exact hardening:
  versioned per-agent files, schema-validated + complexity-capped
  before commit, monotonic version, **same version + different bytes
  = refuse** (tamper evidence), stale cache loses to newer bundled,
  local file override wins. The hub is the distribution channel — no
  vendor-domain fetches from hosts.
- **D-8 — teeth before wiring.** The #526 recipe: P1 lands with a
  fixture corpus (herdr's own test screens seeded + our captures),
  and every reviewer-facing claim ("the grok splash does not read as
  working") is a fixture, mutation-checked, before P2 wires anything
  user-visible.

## 3. Lane P — pane-state detection (the core)

### P1 — evaluator + vendored manifests + overlay (pure library)

New package `hub/internal/panestate`: TOML schema parse + validation
(caps, positive-matcher rule, region vocabulary incl. prompt-box and
horizontal-rule scanners), gate compilation (`contains` lowercased at
compile; `regex`/`line_regex` case-sensitive), evaluation (every rule
every pass — explain needs the evidence — priority argmax, ties to
file order), and the loader with overlay > vendor precedence and the
D-3 kind mapping. `manifests/vendor/` at pinned `6f311498` with a
provenance test (blob SHAs vs upstream, exactly like #528's
byte-exactness check); NOTICE paragraph. Fixture corpus: screen
snapshot in → `{state, matched_rule, fallback_reason}` out, seeded
from herdr's manifest tests plus captures of the engines we run.
**Acceptance:** provenance test green; fixtures cover every vendored
agent's blocked + idle + working forms and the freeze rules;
mutation-check documented in the PR (a deleted `not` gate or a
flipped tie-break fails a named fixture); no wiring.

### P2 — capture plumbing + state posting

Feed the evaluator from `PaneDriver`'s tick: trim capture to the D-4
geometry, add the `#{pane_title}` reader, port the D-5 debounce /
hysteresis / startup-grace state machine, and post state transitions
as agent events (producer `panestate`). Gated to agents that pass
D-2's authority check and D-3's mapping.
**Acceptance:** unit tests drive the state machine through the
hysteresis and grace windows with fixture screens; an integration
test proves a structured-driver agent is never evaluated; events
carry `{state, rule_id, manifest_version}`.

### P3 — attention + IdleDetector retirement + capture gating

`blocked` (with `visible_blocker`) raises attention per D-6, once per
streak; resolves when the classification leaves blocked.
`IdleDetector` retires for any agent the evaluator covers (its guard
becomes "has any state authority"); it remains for unmapped panes.
The D-2 exception (visible blocker over structured authority) lands
here, attention-only. Capture-cost gating rides along: skip capture
for idle panes whose `#{window_activity}` timestamp hasn't moved
(B5) — one `list-panes -F` for all panes replaces per-pane capture
when nothing changed.
**Acceptance:** a fixture-driven end-to-end test (fake capture func)
takes a codex approval screen → attention item with rule id; the
idle-shell false-positive class (bare `$` prompt) provably cannot
raise; sweep test: no remaining `IdleDetector` path for mapped kinds.

### P4 — explain verb + Inspect surface

`host.pane_explain` host verb returns the herdr-style evaluation
record: final state, matched rule, per-rule evidence for evaluated
rules (bounded region previews), fallback reason, manifest source
(vendor/overlay) + version, kind mapping used. Desktop Inspect gets a
pane-state card rendering it (follows the schema-pane pattern from
the Inspect tab work).
**Acceptance:** verb answers for a live pane and for a supplied
screen text (herdr's `--file` mode — CI-testable); Inspect renders
matched + non-matched rules distinguishably; docs page for the verb.

### P5 — hub-distributed manifest updates (deferrable)

Hub stores per-agent manifest files (versioned rows, no new
infrastructure beyond an existing blob/data channel — decide
in-wedge); host-runner fetches on a slow cadence + on a `host.verb`
nudge, applies D-7's hardening verbatim, reloads without restart, and
`host.pane_explain` reports cached-remote vs bundled provenance.
**Acceptance:** tamper fixture (same version, changed bytes →
refused, logged); downgrade fixture (older remote loses to bundled);
reload-without-restart proven in an integration test.

## 4. Independent lanes

### R1 — native-resume recipe table (B2)

A per-engine resume-recipe table (engine kind → argv template +
session-ref validation: ids ≤512 bytes no control chars, paths
absolute) seeded with herdr's 16 field-verified recipes, replacing
the ad-hoc knowledge in `driver_exec_resume.go` / teleport respawn,
plus the dedupe rule: one session ref resumes once per restore pass.
Scope note: recipes for engines we cannot yet spawn are data + tests
only (they wait for their family registration); the table is exactly
the resume-command column vision-parity L3/L4 needs.
**Acceptance:** claude/kimi paths keep their existing behavior
(regression-pinned); shell-quoting test with a hostile session id;
recipe table pinned against
[session-state doc](../discussions/herdr-runtime-borrows.md) §4.

### W1 — prompt-effect two-phase wait (B3)

The steward/A2A "message an agent, await outcome" path gains herdr's
two-phase shape: baseline = latest agent-event id before submit;
phase 1 requires any observed state change within a prompt-effect
deadline (default 5 s) else fails fast with a distinct
`prompt_stalled` error; phase 2 waits for a settled state with the
remaining budget. Hub-side only — our event feed already provides the
monotonic sequence. (This is the principled fix for the T1d
timeout-class scars.)
**Acceptance:** already-idle vs became-idle distinguished in tests
via the baseline; stalled prompt fails in ~5 s with the distinct
error; long-running phase 2 unaffected by client HTTP timeouts
(poll-based, per attention-kinds §connection-pinned-waits guidance).

### W2 — done-until-seen (B3 tail)

One bit: a session whose agent went idle after its last user-visible
focus shows as **done** (finished, unseen) in fleet board / desktop
sidebar / mobile cards until a client focuses it. Hub records
last-focus per session (or reuses an existing read-marker if review
finds one); rollups sort done > blocked > working > idle.
**Acceptance:** state derivation is a pure hub-side function with
table-driven tests; desktop marks seen on session focus only (reads
via CLI/API do not).

### I1 — pane-input hardening (B4)

Generic `PaneDriver.Input` gains the adapters' named-buffer paste
path for multi-line/long bodies; all paste paths gain `-p` (tmux
brackets the paste iff the app requested DECSET 2004); the generic
path gains a short paste→Enter delay (herdr uses 300 ms) so TUIs
ingest text before submit.
**Acceptance:** multi-line body through the generic path arrives as
one block (fake runner asserts the tmux call sequence); adapters'
existing single-line fast path unchanged.

## 5. Ordering and dependencies

P1 → P2 → P3 → P4; P5 after P3 (any time). R1, W1, W2, I1 are
independent of lane P and each other; I1 is the smallest and can land
first. Suggested first wave: **P1 + I1 + R1** (pure library + two
small independents), then P2+P3 as the user-visible wave, then
P4/P5/W1/W2.

Device-verify debt this plan creates (recorded now): live-TUI
verification of P2/P3 against real codex/cursor/gemini panes on a
display-capable host — fixtures carry the semantics, but at least one
real blocked-dialog → phone-attention run per engine family belongs
in the standing device-verify queue.

## 6. Risks

- **Upstream drift.** Vendored rules go stale as engine TUIs change.
  Mitigation: P5's update channel + the diffability D-1 buys; the
  fixture corpus tells us *what* broke on re-vendor.
- **Geometry mismatch.** Rules assume herdr's snapshot shape; D-4
  makes it a contract, but panes narrower than the chrome the rules
  expect (maki's narrow-pane trap) will misfire in ways only live
  verification catches — hence the device-verify debt.
- **Engine-version coupling.** Vendored manifests declaring a herdr
  engine version we haven't ported fail validation loudly (never
  silently degrade); re-vendoring may therefore require evaluator
  work first. Acceptable: that is the fork-decision signal D-1
  records.
- **False-attention budget.** Strict-blocked + fallback-to-idle is
  herdr's answer and ours; the residual risk is a *missed* blocked
  state (new dialog shape), which degrades to today's behavior — not
  worse, just not better yet.

## 7. References

- [`discussions/herdr-runtime-borrows.md`](../discussions/herdr-runtime-borrows.md)
  — the investigation this plan executes; §3.2 carries the semantics
  P1/P2 port, §7b the substrate verdict.
- [ADR-010](../decisions/010-frame-profiles-as-data.md) — rules-as-
  data precedent; [ADR-027](../decisions/027-local-log-tail-driver.md)
  — the authority lane P slots beneath.
- [attention-kinds.md](../reference/attention-kinds.md) — D-6's
  target rails.
- [`desktop-companion-vision-parity.md`](desktop-companion-vision-parity.md)
  — R1's consumer (L3/L4 resume commands).
