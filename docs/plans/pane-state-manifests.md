# Pane-state manifests — declarative screen detection for every engine

> **Type:** plan
> **Status:** Proposed (2026-08-08) — for review. Derived from
> [`discussions/herdr-runtime-borrows.md`](../discussions/herdr-runtime-borrows.md)
> (B1 flagship + B2/B3/B4/B5 independents). The vendor-vs-fork call is
> made: the director chose **vendor-plus-overlay** (2026-08-08); D-1
> records it. Lane P is the core; lanes N/S/Q are independent and can
> interleave with other work.
> *Review pass 2026-08-08 renamed the independent lanes* — `R1`→**N1**,
> `W1`/`W2`→**S1**/**S2**, `I1`→**Q1**. `W*` is reserved repo-wide for
> *waves*, and this plan's §5 uses "wave" in its own text; `R1` and `I1`
> already name other wedges in the two live desktop plans
> ([vision-parity](desktop-companion-vision-parity.md) R1 shipped as the
> approval cards, [coworking](agent-desktop-coworking.md) I1 is the Read
> push channel). P/N/S/Q are unused elsewhere: **P** reads the pane,
> **Q** writes to it, **N** is native resume, **S** is settle semantics.
> **Audience:** principal · contributors
> **Last verified vs code:** main `9e06d8fa` (2026-08-09; P1/Q1/P2
> shipped and P3 built against it — `hub/internal/panestate/`,
> `hub/internal/hostrunner/panestate_watch.go`, `idle.go`, `runner.go`,
> `hub/internal/server/handlers_attention.go`,
> `docs/reference/attention-kinds.md`; herdr re-read at `6f311498`)

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
rule. Independent lanes: **N1** the 16-engine native-resume recipe
table (feeds teleport respawn + vision-parity L3/L4), **S1/S2**
prompt-effect two-phase waits and the done-until-seen bit, **Q1**
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
  - **Corrected at P3 (2026-08-09): the exception is not a port, and
    it is deferred.** Upstream has no such case — `pane.rs:809` is
    `if lifecycle_authority_active && !process_exited { pending_idle
    .clear(); continue; }`, an unconditional short-circuit *before*
    the screen is read, and nothing downstream consults
    `visible_blocker` for a pane it skipped. This is a termipod
    invention wearing a port's clothes, so it has to earn its place on
    its own evidence. It needs two things first, neither available
    today:
    1. **Proof it complements rather than duplicates.** The target
       case is claude's trust dialog, which is hook-blind. But
       `claude.toml` also ships `bash_permission_prompt` and
       `generic_permission_prompt` (both `visible_blocker`), and our
       claude agents already raise `permission_prompt` rows from the
       `canUseTool` hook for those events — with Approve/Deny that
       work. Whether the TUI *draws* a dialog the hook has already
       parked decides whether this exception adds a signal or a
       second, un-actionable row beside the right one. Nobody has
       watched a real claude pane (this lane's standing device-verify
       debt), and static reading cannot settle it.
    2. **A suppression rule if it does duplicate** — "no row while one
       is open for this agent" needs a hub query host-runner does not
       have (`handleListAttention` filters on status and scope_kind
       only, never actor).
    One capture of a claude pane mid-permission-prompt settles both.
    Until then P3 ships the safe half: panes with no state authority,
    where there is no other row to collide with.
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
  - **Corrected at P2 (2026-08-09), against the source rather than
    this paragraph.** The geometry is *the visible viewport*, not
    24 rows. Upstream's `ghostty_detection_text` reads
    `terminal.rows()` and falls back to `DEFAULT_DETECTION_ROWS = 24`
    only when the row count is unavailable
    (`src/pane/terminal.rs:2468-2475`). 24 is a fallback, not the
    contract — so trimming to it would CUT rows the rules were
    written to see on any pane taller than 24, and the `top_*`
    region rules are exactly the ones that would go quiet. There is
    no trim step: `capture-pane -p -J` already returns the visible
    screen and that IS the contract. (One residual difference is
    recorded rather than fixed: `-J` joins wrapped lines, so a
    wrapped line reaches the rules as one long line where upstream
    sees the wrapped rows. It affects `$`-anchored `line_regex` only,
    and `-J` is the better input for `contains`.)
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
  *Open at P2 review:* `panestate` is a **new agent-event producer**
  landing while [vision-parity](desktop-companion-vision-parity.md)
  lane E is normalizing that same feed, and that plan binds new
  producers to emit the corrected shapes natively rather than be
  fixed up later. Its payloads are state classifications, not
  tool/turn events, so the overlap is expected to be the envelope and
  not the content — but nobody has checked, and P2 is the moment to.
  - **Checked at P2 (2026-08-09). Three answers, one of which
    invalidates this decision's wording.**
    1. **`panestate` cannot be a producer.** The event-ingest
       endpoint 400s anything outside `agent|user|system`
       (`hub/internal/server/handlers_agent_events.go:95`); the
       agent-*input* endpoint has its own closed vocabulary,
       `user|a2a` (`handlers_agent_input.go:438-445`). Neither has
       a per-subsystem extension point. The axis answers *whose
       bytes are these*, and these are host-runner's — so the
       classification ships as **kind `pane_state`, producer
       `system`**, the same stamping PaneDriver already uses for its
       synthesized lifecycle events. This binds lane L3/L4's local
       drivers too: a new *producer* is not an available extension
       point on this feed, only a new kind is.
    2. **Busy inference is safe.** Both clients invert to an
       allowlist of turn-active kinds (mobile v1.0.721,
       `kAgentTurnActiveKinds`), so an unlisted kind is no-signal.
       A `pane_state` row cannot stick the busy pill on.
    3. **Feed rendering is NOT an allowlist, and that one bites.**
       An unknown kind renders as a raw card in both modes on both
       clients. `pane_state` therefore joins the verbose-only tier
       in both (`desktop/src/ui/feedLens.ts`,
       `lib/widgets/transcript/feed_reducer.dart`) — same tier as
       `lifecycle`, since it is an agent state transition rather
       than turn telemetry, and on a raw pane it is often the only
       structured signal a reader has.
  - **Answered at P3 (2026-08-09): the attention kind stays `idle`,
    and for a reason that is not "no new kind was needed".** D-6 left
    the door open to minting one if `idle` read wrong for "blocked on
    approval". It does read wrong — this lane spent P1 making `idle`
    and `blocked` contrasting states — but the kind on this surface
    selects an *affordance*, and `idle` is the only value both clients
    already route correctly for a row a human can acknowledge but not
    answer: mobile buckets it under Agents with a single Dismiss
    (`me_screen.dart` `_filterForAttention`, `inline_actions.dart`
    `_isInformational`), and the hub keeps it out of
    `attentionAwaitsAgentReply`, which is what makes `/resolve` — the
    retract leg — legal at all. A newly minted kind inherits the
    unknown-kind default instead, and on mobile that default is
    **Approve / Reject** for any row carrying a `pending_payload`:
    two buttons on a state report nothing can approve. Same hazard P2
    found in the event feed, second registry, opposite direction. The
    collision is contained to the wire name; summary, payload, and the
    `pane_state` event all say blocked.
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

**As built (2026-08-08).** `hub/internal/panestate` — schema + validation
+ region vocabulary + evaluator + loader, 19 vendored TOMLs pinned by git
blob SHA, overlay config carrying the family mapping. Four deviations:

- **Rust-regex and RE2 are not the same dialect.** 9 of the 58 vendored
  patterns do not compile in Go: 5 use `\uXXXX` / `\u{XXXX}` escapes and
  4 use `\p{Alphabetic}`, a Unicode *binary property* RE2 does not
  support. Rather than edit the vendored files (D-1 forbids it) or drop
  the rules, `regex_translate.go` rewrites them at compile time and
  **records each translation**, flagging the inexact one:
  `\p{Alphabetic}` becomes `[\p{L}\p{Nl}]`, which drops
  Other_Alphabetic. Every vendored use is `\p{Alphabetic}+\w*ing\b`
  after a spinner glyph, so the dropped set is unreachable there — but
  that is a claim about the manifests as they are today, so it is
  labelled rather than assumed.
- **An unimplemented region is refused, not emptied.** Upstream resolves
  an unknown region to `""`, which turns a typo or a newer-schema region
  into a rule that silently never fires. Only the 8 region kinds the
  vendored manifests use are implemented (plus `bottom_lines(N)`); the
  rest fail validation by name. That is
  risk 3 in §6 working as intended.
- **The corpus is upstream's own, and narrower than this line implies.**
  28 cases (claude, codex, devin) lifted from herdr's tests — screens AND
  expected answers — so it is a cross-implementation parity check rather
  than a self-consistency one. (The first cut carried 14; the review pass
  added the omitted multi-signal cases — blocker-outranks-working,
  transcript-viewer freeze, OSC-vs-screen preference — which are exactly
  where a port divergence would matter most. All pass.) The other 16
  manifests get structural coverage (parse + validate + compile +
  empty-screen fallback) only.
  Per-agent blocked/working screens for them need real captures and are
  device-verify debt, not something to invent from the rules under test.
- **A TOML parser was added** (`github.com/BurntSushi/toml` v1.4.0,
  BSD-2, zero deps, `go 1.18` so it clears the repo's 1.23 pin). D-1's
  byte-exact vendoring requires parsing upstream's format.

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

**As built (2026-08-09).** `hub/internal/hostrunner/panestate_watch.go`
— eligibility, capture plumbing, the D-5 state machine, and the event.
All three acceptance clauses met (18 tests; 7 mutations of the new
guards were introduced and all 7 were caught). Five deviations, each
because a load-bearing claim in this plan turned out to be secondhand:

- **It is wired into the runner's pane tick, not `PaneDriver`'s.**
  Three of this plan's own decisions cannot be satisfied from inside a
  driver: D-3 needs the agent's family, which PaneDriver has no access
  to (it knows an agent id and a pane id); D-4 wants ONE `list-panes`
  round-trip covering all panes, which per-driver becomes one per
  agent; and D-2's ported exception is about panes PaneDriver does not
  own. The runner tick already enumerates every running pane for the
  legacy detector, so this adds no new enumeration. P3's IdleDetector
  retirement and capture-cost gating both live there too.
- **D-2's authority check asks the driver, not the kind.**
  `hasStructuredDriver(kind)` — the existing gate — is a proxy for
  "its adapter reports state", and the proxy is wrong in the case that
  matters most: a codex spawn whose M2 launch failed walks the mode
  ladder down to a raw `PaneDriver`, keeps `kind = codex`, and has
  nothing reporting state at all. The new gate reads the live driver
  map and treats a bare `*PaneDriver` — and an absent driver, i.e. a
  pane that outlived a host-runner restart — as no authority. Upstream
  has the same gate one layer up (`lifecycle_authority_active` short-
  circuits its loop before the screen is read, `src/pane.rs:807`).
- **The producer/kind correction** — see D-6 above. `panestate` is not
  a legal producer value; the event is kind `pane_state`, producer
  `system`, and both clients' verbose tiers were swept.
- **The geometry correction** — see D-4 above. No trim.
- **D-5 was ported from the source, and the prose was one step off.**
  All four constants check out verbatim
  (`AGENT_PENDING_IDLE_RECHECK` 100 ms,
  `AGENT_PENDING_IDLE_CONFIRMATIONS` 3, `AGENT_PENDING_IDLE_CAP`
  700 ms, `AGENT_STARTUP_GRACE_WINDOW` 3 s) but the *shape* differs
  from "held 3×100 ms capped 700 ms": the first plain-idle observation
  arms the hold with **zero** confirmations, so the release lands on
  the fourth observation, and the cap **releases** the hold rather
  than bounding it. Which end fires depends entirely on the caller's
  cadence — upstream polls at 100 ms so the confirmations win; we poll
  at 3 s so the cap always wins and the hold costs exactly one tick.
  Both are ported so a future faster tick does not silently change the
  semantics. Two further findings: upstream publishes on a change to
  the state **or any of the three `visible_*` hints**, not on the
  state alone ("blocked, dialog on screen" is a different claim from
  "blocked, inferred"); and at identification it stores
  `last_visible_idle = true` while publishing `visible_idle: false`,
  which we do not copy — storing a hint we never published makes the
  first real classification emit a redundant idle for a field nobody
  was told about.

Also renamed: `hostrunner.paneState` → `idleProbeState` (and
`Runner.panes` → `idleProbes`). The legacy detector's per-pane hash
bookkeeping was holding the exact term this lane's package owns.

**Not ported, deliberately:** upstream's 800 ms visible-blocker
re-publish (D-5 already excludes it), its `process_exited` short-
circuit and `agent_changed` bypass (both structurally false here — a
respawn mints a new agent id, and process exit is `tickReconcile`'s
job; they stay in the Go signature so a re-vendor can diff against
upstream), and the extra tick upstream burns when the grace expires
(it exists to reset a scan-skip sequence we do not have until P3).

**Still owed:** no engine has been observed live. Every screen in
these tests is upstream's own; the device-verify debt this lane books
is unchanged and now also covers "does a real codex pane, captured
through `capture-pane -p -J`, classify the way the fixture does".

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

**As built (2026-08-09).** Attention raise + retract, the capture
gate, and the guard rewrite landed; the D-2 exception did not (see
D-2's own correction above — it is not a port, and settling it needs
one real claude pane). All three acceptance clauses are met, by
`TestBlockedScreenRaisesAttentionWithRuleID`,
`TestBareShellPromptCannotRaiseAttention` and
`TestIdleDetectorSkipsEveryMappedFamily`. Four things worth carrying
forward:

- **The retirement was already done, and the guard this line
  proposes would have UNDONE part of it.** Every mapped family is a
  registered agent family, and the old guard skipped every registered
  family — so `IdleDetector` already never touched a mapped pane.
  Replacing it with "has any state authority" *literally* would have
  handed the legacy regex the registered-but-unmapped families,
  `kimi-code-ts` above all: deliberately unmapped, and an instance
  whose M4 launch fell back to a raw pane has no authority of either
  sort. That is the W11 TUI-prompt false positive, re-opened by a
  wedge whose job was to close things. `hasAnyStateAuthority` keeps
  the registered-family clause as its third limb, so the legacy set
  only ever contracts. The real change here is precision, not
  coverage — plus the sweep test that turns "already disjoint" from a
  coincidence into an assertion.
- **The `covers()` clause of that guard is dead today, and says so.**
  A mutation deleting it survives the entire suite, because clause 3
  subsumes it. It is kept as the clause that names the actual reason,
  with the subsumption written down and pinned to the test that would
  fail if the overlay ever mapped an unregistered family. Recording a
  shadowed guard is better than pretending a test covers it.
- **`#{window_activity}` is sound, with one sharp edge.** tmux calls
  `window_update_activity()` from `input_parse_buffer()`
  (tmux 3.4 `input.c:975`) on every non-empty chunk of pane output,
  independent of `monitor-activity` — that option only gates the
  alert. But it is per-WINDOW (tmux 3.4 has no `pane_activity`
  format) and one-second resolution, so output landing later in the
  same second as the stamp we read is invisible to an equality test —
  and for an idle pane that skip would repeat forever. The gate
  therefore arms only on a stamp whose second had already elapsed when
  we captured (`now.Unix() > activity`), which makes equality sound
  rather than probabilistic. Skipping is also confined to panes whose
  published state is idle, exactly as upstream confines it
  (`should_skip_idle_screen_scan`, agent_detection.rs:91) — a stale
  stamp can never freeze a blocked pane.
- **Attention is decided on every classified tick, not on the
  transition.** Deciding it inside the publish branch makes a failed
  raise permanent: the streak's transition has already happened, so
  the retry tick has nothing to publish and never looks again.

Also landed: `listTmuxPaneTitles` became `listTmuxPaneMeta` (title +
activity in the one round-trip P2's note reserved for it), and
`internal/panestate/region.go`'s `Input.Screen` comment lost the last
copy of the retracted 24-row claim.

19 new/changed tests; 8 mutations introduced, 7 caught, 1 documented
above as shadowed.

**Still owed by this wedge:** the D-2 exception, and the device-verify
line it is blocked on.

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

**As built (2026-08-10).** All three acceptance clauses met.
`host.pane_explain` (host-runner) + `POST /v1/teams/{team}/pane_explain`
(hub, both modes) + an Inspect `panestate` tab + the how-to
[`debug-pane-state.md`](../how-to/debug-pane-state.md). Notes:

- **P1 had already built the evidence side.** `Explain` has carried
  `EvaluatedRules` with bounded per-rule previews since the first wedge
  ("every rule every pass — explain needs the evidence"), so P4 is
  transport and surface, not evaluation. The tick discards that detail
  every 3 s; only this verb ships it.
- **The record is one type, in one place.** `panestate.Explain` gained
  JSON tags rather than growing a parallel wire struct, and
  `ExplainResult` wraps it with what the evaluator cannot know (agent,
  pane, host, family→manifest, screen size, OSC title). The price of one
  type is that a field added for evaluation would ship by default, so
  `TestExplainWireKeysAreDeliberate` pins the exact key set: adding a
  field is fine, adding it silently is not.
- ★ **Agent-kind tokens are refused (403).** The record carries bounded
  pane text, and `teamGate` checks only the team, so any team-scoped
  token reaches the route — including a spawned agent's, because the
  egress proxy is a plain reverse proxy with **no path allowlist**
  (`egress_proxy.go`: it forwards everything). Without the refusal one
  agent could read another agent's terminal through a debugging tool.
  This is the narrow, asked-for exception to P3's rule that automatic
  output carries a rule id and never pane text: the tick publishes to a
  feed, the verb answers one person.
- **A coverage endpoint came along** (`GET .../pane_explain`): D-3's
  family→manifest table, which was otherwise readable only by opening a
  YAML compiled into the binary. It feeds the supplied-mode picker and
  answers "why is this agent never classified" directly. Unmapped
  families are ABSENT rather than listed as false — a row would imply a
  manifest exists.
- **Live mode ignores the capture gate and the startup grace.** Both
  exist to skip work nobody asked for, and this call is the asking; a
  debugger that answered from a cached classification would describe a
  pane it did not read.
- `datasetVerbJSON`/`datasetVerbError` became `verbJSON`/`verbError` —
  they were never dataset-specific, and P4 is the second caller.

**A gap this wedge found and did not close:** the state-authority order
(structured driver > screen manifest > nothing) exists only here, as
D-2. `spine/agent-lifecycle.md` is an axiom doc that predates screen
rules and does not mention them, so the fleet's actual answer to "how is
an agent's state decided" is not in the spine. Recorded rather than
patched in a feature wedge — an axiom edit deserves its own pass.

**Still not verified:** the Inspect card has never been rendered. No
display on the authoring machine; the parsing layer
(`state/paneExplain.ts`) is pure and unit-tested, the CSS classes are
checked against the stylesheet, and everything past that is unproven.
Joins the same device-verify line as the rest of this lane.

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

### N1 — native-resume recipe table (B2)

A per-engine resume-recipe table (engine kind → argv template +
session-ref validation: ids ≤512 bytes no control chars, paths
absolute) seeded with herdr's 16 field-verified recipes, replacing
the ad-hoc knowledge in `driver_exec_resume.go` / teleport respawn,
plus the dedupe rule: one session ref resumes once per restore pass.
Scope note: recipes for engines we cannot yet spawn are data + tests
only (they wait for their family registration); the table is exactly
the resume-command column vision-parity L3/L4 needs.

**The consumer is in another language — so the table ships as data,
not as Go.** L3's local agent service lives in Electron main
(TypeScript) and rebinds across app restarts by engine-native resume;
it cannot call a Go table, only re-derive one. A Go-only N1 therefore
*guarantees* the second copy rather than preventing it — the failure
[vision-parity L1](desktop-companion-vision-parity.md) already names
in the neighbouring case ("inventing a loader now would ship a second
copy of `agent_families.yaml` with nothing reading it"). Follow the
**L2 recipe** instead, which shipped this exact shape in #526: the
recipes live in a checked-in data file, the Go table and any TS reader
both load it, and one shared fixture corpus pins both — parity by
construction, not by discipline. Whether the file is a new one or a
`resume:` block on the existing families YAML is an in-wedge call;
shipping the knowledge as Go literals is not.
**Acceptance:** claude/kimi paths keep their existing behavior
(regression-pinned); shell-quoting test with a hostile session id;
recipe table pinned against
[session-state doc](../discussions/herdr-runtime-borrows.md) §4;
recipes are data with a language-neutral fixture corpus, and the Go
loader is tested against that corpus (so an L3/L4 TS reader can be
pinned to the same file without re-deriving anything).

**As built (2026-08-08).** `hub/internal/resumerecipes` — `recipes.yaml`
(17 engines: herdr's 16 at `6f311498` plus `gemini`, which is ours) +
loader + validation envelope + shell quoting, with
`testdata/resume_recipes_fixture.json` generated from the table and
compared on every run. Reference:
[engine-resume-recipes.md](../reference/engine-resume-recipes.md).
Four deviations worth recording:

- **The ad-hoc knowledge was not where this plan said.** It named
  `driver_exec_resume.go` / teleport respawn. `driver_exec_resume.go` is
  gemini's *per-turn* argv and is correctly driver-internal — untouched.
  The real site was `server/resume_splice.go` plus **two hand-copied
  family switches** (`handlers_sessions.go`, which teleport respawn also
  goes through, and `respawn_with_spec_mutation.go`). Both now call one
  `spliceResume`.
- **The two switches had already diverged**: the spec-mutation copy never
  grew antigravity's arm. Latent, not live — that path returns
  `errUnknownFamilyField` for any family absent from `flagForField`, and
  antigravity is absent — but adding antigravity there would have turned
  it into a silent cold-start on every mode/model flip.
- **The table refuses two mappings on purpose.** `kimi-code-ts` is NOT
  wired to herdr's `kimi --session` (unverified that it is the same
  binary; a wrong recipe cold-starts silently), and `gemini-cli` is
  `acp_session_load` rather than `argv` because the *hub* injects the ACP
  field — the argv recipe describes what the M2 driver does per turn.
  Treating it as a spawn-time splice would rewrite a cmd the hub has
  never rewritten.
- **Not done: the dedupe rule is a primitive, not a policy.**
  `DedupeKey` exists and is tested; nothing consumes it, because there is
  no single restore-pass owner in our architecture to hang "one ref
  resumes once" on. Wiring it needs that owner identified first — a
  separate wedge, not a line in this one.

### S1 — prompt-effect two-phase wait (B3)

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

### S2 — done-until-seen (B3 tail)

One bit: a session whose agent went idle after its last user-visible
focus shows as **done** (finished, unseen) in fleet board / desktop
sidebar / mobile cards until a client focuses it. Hub records
last-focus per session (or reuses an existing read-marker if review
finds one); rollups sort done > blocked > working > idle.
**Acceptance:** state derivation is a pure hub-side function with
table-driven tests; desktop marks seen on session focus only (reads
via CLI/API do not).

### Q1 — pane-input hardening (B4)

Generic `PaneDriver.Input` gains the adapters' named-buffer paste
path for multi-line/long bodies; all paste paths gain `-p` (tmux
brackets the paste iff the app requested DECSET 2004); the generic
path gains a short paste→Enter delay (herdr uses 300 ms) so TUIs
ingest text before submit.
**Acceptance:** multi-line body through the generic path arrives as
one block (fake runner asserts the tmux call sequence); adapters'
existing single-line fast path unchanged.

## 5. Ordering and dependencies

P1 → P2 → P3 → P4; P5 after P3 (any time). N1, S1, S2, Q1 are
independent of lane P and each other; Q1 is the smallest and can land
first. Suggested first wave: **P1 + Q1 + N1** (pure library + two
small independents), then P2+P3 as the user-visible wave, then
P4/P5/S1/S2.

**Against the two live desktop plans.** This is a third parallel
track, not a competitor for their files: lane P is host-runner Go
aimed at engines *without* a structured driver, while coworking W3
(I1–I4, J3, K, D2, E1–E3, H2 tail) is client-side and vision-parity W3
(L3, E3, E4, R4) is Electron plus hub event vocabulary. One edge
crosses: **N1 feeds vision-parity L3 (W3) and L4 (W4)**. It does not
block them — vision-parity's stated gates are L1 before L3/L4 and L2
before L3, both shipped — but L3's hardest hidden requirement is
rebinding across app restarts by engine-native resume, which is
exactly what N1 tabulates. Landing N1 first turns that sub-problem
into a lookup, and N1 is independent of lane P, so it can be pulled
forward without dragging the manifest work along.

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
  — N1's consumer (L3/L4 resume commands), and lane E's event feed
  that D-6's `panestate` producer joins.
- [`agent-desktop-coworking.md`](agent-desktop-coworking.md) — the
  other live plan this one runs beside; no shared surface, but its
  `I1` and vision-parity's `R1` are why this plan's independents are
  lettered N/S/Q (see the status block).
