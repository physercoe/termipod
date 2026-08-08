# herdr runtime borrows — what a 25k★ agent multiplexer teaches host-runner

> **Type:** discussion
> **Status:** Open (2026-08-08) — full code-read of
> [herdrdev/herdr](https://github.com/herdrdev/herdr) @ `6f311498`
> (2026-08-08, Rust, Apache-2.0 since 0.8.0, ~25.6k★); borrow catalogue
> ranked, hostrunner-focused per the director's ask. No plan authored
> yet — the flagship borrow (B1) deserves a director call on scope
> before wedges are cut.
> **Audience:** principal · contributors
> **Last verified vs code:** main `7088a95e` (2026-08-07)
> **Freshness:** snapshot (refresh when herdr ships a major detection
> or protocol change, or when B1 lands and pins its own fixtures)

**TL;DR.** herdr is "the runtime your coding agents live on": a Rust
background server that owns PTYs, marks every pane `working` /
`blocked` / `idle` / `done`, exposes one socket API that agents drive
(`agent start|prompt|wait`), and resumes agents natively after a
restart. It is the strongest open implementation yet of the layer
termipod's host-runner already occupies — and the comparison mostly
**validates our architecture** (tmux ownership makes herdr's entire
SCM_RIGHTS live-handoff machinery unnecessary for us; our hub-durable
event feed, teleport, and sealed-secret model have no herdr
counterpart; its socket has *no* auth beyond file mode 0600). What
herdr has that we lack is concentrated in one place: **declarative,
per-agent screen-state detection** — 19 battle-tested TOML manifests
that classify any TUI agent from a bottom-anchored screen snapshot,
remotely updatable without a binary release, with an `explain` surface
and a carefully-reasoned authority model. That is the same design
family as our ADR-010 frame profiles (rules as data + interpreter +
fixtures), pointed at the one signal source we currently read with a
single dumb regex (`idle.go`'s `IdleDetector`). Borrow B1 gives every
non-M4 engine — codex, cursor, gemini, copilot, droid, grok, and
anything else with a TUI — working/blocked/idle state and hub
attention **without writing a Go adapter per engine**. Four smaller
borrows (native-resume recipe table, prompt-effect wait semantics,
pane-input hardening, capture-cost gating) are cheap and independent.

---

## 1. What herdr is, and why it matters now

One Rust binary, three roles: a detached session server owning PTYs; a
terminal UI client (tmux-style prefix keys *and* mouse); and a CLI +
unix-socket JSON API that is explicitly "the same surface agents
drive." Agents run unwrapped — herdr owns their terminals, not their
protocols. Around it, a fast-growing ecosystem (review sidebars, file
viewers, a Chromium-pane plugin, phone bridges, a PWA remote) is
rebuilding, piece by piece, what termipod's desktop/mobile already
ship integrated.

Why read it carefully: it is the highest-traction independent answer
to host-runner's exact question — *how do you supervise N interactive
coding agents you don't control?* — and it relicensed from AGPL to
Apache-2.0 at 0.8.0 (2026-08-03), which makes its detection data
vendorable under the same NOTICE discipline we used for
next-ai-draw-io's shape libraries. It also trips
[competitive-landscape-2026.md](../reference/competitive-landscape-2026.md)'s
refresh rule ("any new framework reaching ≥10k stars in <2 months") —
that refresh is a follow-up, not this doc.

Method: three subsystems read mechanism-by-mechanism at `6f311498`
(detection engine + manifests; socket API + wait primitives;
persistence/resume/handoff), each verified against source with
file:line anchors, not README-trusted.

## 2. Where the comparison validates termipod (no borrow)

- **Process survival.** herdr owns PTY masters, so surviving a server
  update required a 400-line SCM_RIGHTS fd-passing protocol
  (`server/handoff.rs`: token handshake, reader quiescing, 64-pane
  cap, strict all-or-nothing import, rollback). Host-runner delegates
  pane ownership to tmux and gets the same property for free — agents
  keep running across host-runner restarts because host-runner never
  held the PTY. Our
  [graceful-host-update discussion](graceful-host-update-and-session-resume.md)
  stays closed.
- **Event durability.** herdr's event hub is a 512-entry ring, poll-
  based (~100 ms), silently lossy, no cursor on the wire, no replay.
  The hub's DB-backed agent-event feed is categorically stronger.
- **Cross-host.** herdr's "remote" is an ssh stdio bridge to a remote
  server's client socket. There is no session movement between hosts —
  nothing like teleport (ADR-057) exists.
- **Security.** The socket API has *no* authentication: any same-user
  process holds full control (read any pane, type into any pane, stop
  the server). Trust boundary is file mode 0600 plus an `HERDR_ENV=1`
  honor check in the agent skill. Our MCP-token + hub-auth + sealed
  secret-envelope model (ADR-056) must not regress toward this.
- **Signal hierarchy.** herdr's own authority model ranks complete
  lifecycle hooks above screen scraping and refuses to run both at
  once ("avoids two competing sources of truth"). That is exactly
  M4's position — structured JSONL/wire tails + parked hooks author
  state. herdr's screen layer is its *fallback* for engines without
  hooks; the borrow below adds the fallback rung we lack, not a
  replacement for M4.

## 3. B1 — screen-manifest state detection (the flagship)

### 3.1 What we have today

For engines with a structured driver, M4 authors state from the
engine's own log/wire plus hooks. For everything else, `idle.go`'s
`IdleDetector` is — its own comment — "intentionally dumb:
capture-pane + regex + hash": one global prompt regex, a 90-second
stall threshold, one attention kind. It cannot say *working*, cannot
distinguish a permission dialog from a finished run, and false-negs on
any TUI whose prompt doesn't look like `[y/N]`. `hasStructuredDriver`
exists precisely to keep this crude detector away from the engines
that deserve better — which leaves the uncovered engines with almost
nothing.

### 3.2 What herdr built

Per-agent TOML manifests (19 agents: claude, codex, cursor, gemini,
copilot, devin, droid, grok, kimi, opencode, kilo, hermes, qodercli,
amp, antigravity, cline, kiro, maki, pi) evaluated every ~300 ms
against a **bottom-anchored** snapshot of the last N screen rows —
deliberately not the user-visible viewport, so scrollback position
never affects detection. Rules carry `state` + `priority` + a `region`
selector + `contains`/`regex`/`line_regex` matchers + nested
`all`/`any`/`not` gates; highest priority wins, ties break to file
order. The engine evaluates every rule every tick, which is what makes
`herdr agent explain` able to show the matched rule, per-rule
evidence, and the fallback reason.

The semantics that took herdr real field time to learn — the part
worth porting verbatim rather than rediscovering:

- **Strict blocked.** `blocked` fires only on *known, visible*
  approval/question chrome ("esc to cancel" + a confirm/select
  affordance; "do you want to proceed?" + a numbered yes/no list).
  No match on a known agent falls back to `idle`, labeled
  `default_known_agent_idle_fallback` — unknown prompts read as idle,
  never as a false alarm. Attention stays trustworthy.
- **`visible_*` liveness flags.** A rule can match state without
  claiming the chrome is *live* (scrollback residue vs. an on-screen
  dialog). Only a live `visible_blocker` may override a hook-reported
  state — the one asymmetry where screen beats hooks, because "the
  hook says working but a permission dialog is on screen" resolves to
  blocked.
- **Freeze, don't guess.** Transcript viewers and model pickers get
  `skip_state_update` rules: classification is suppressed entirely and
  the last state persists, because any answer would be a lie.
- **Region selectors as liveness proofs.** `after_last_horizontal_rule`,
  `prompt_box_body`, `bottom_non_empty_lines(N)` exist to make rules
  *unable* to see scrollback.
- **The OSC title channel outranks the screen.** Agents put spinner
  glyphs, `✳`, or "Action Required" in the terminal title (OSC 0/2) —
  a channel immune to pane width, redraw timing, and scrollback. Every
  manifest's highest-priority rules read it.
- **Asymmetric hysteresis.** Only `working → bare idle` is debounced
  (3 confirmations × 100 ms, capped 700 ms) — agents blank their
  footer between tool calls. Blocked and working publish instantly; an
  idle with *visible* prompt chrome bypasses the hold.
- **Startup grace.** 3 s after an agent is identified, state is pinned
  idle — splash screens draw logos in braille glyphs that would
  otherwise read as spinners (grok's manifest documents this trap).
- **Manifests update without a release.** Bundled → cached-remote →
  local-override precedence; remote updates are versioned, schema-
  validated, complexity-capped, and **tamper-evident** (same version
  with different bytes is an error, not a refresh). A stale cache
  loses to a newer bundled manifest, so binary upgrades self-heal.

### 3.3 The borrow, concretely

A new hostrunner package (`panestate` or similar) + vendored manifests:

1. **Vendor the 19 TOMLs** at a pinned herdr commit, byte-exact, under
   the shapes/-style discipline: NOTICE paragraph, generator-free (they
   are already data), staleness diffable against upstream. Apache-2.0.
2. **Port the evaluator to Go** (~500 lines by herdr's shape: gate
   matching, priority argmax, region slicing incl. the horizontal-rule
   and prompt-box scanners). Pin behavior with fixtures the way #526
   pinned frame profiles — screen snapshots in, expected state out;
   herdr's own test corpus seeds ours.
3. **Feed it from what we already run**: `PaneDriver`'s existing 2 s
   capture tick (`capture-pane -p -J` is already bottom-anchored to
   the live screen, alt-screen included). Add the OSC-title channel
   via tmux's `#{pane_title}` (one `list-panes -F` round-trip covers
   every pane). tmux exposes no OSC 9 progress — accept the loss;
   manifests degrade to screen rules when title/progress evidence is
   absent, by design.
4. **Authority order**: structured driver (M4 tail + hooks) →
   screen manifest → nothing. The `IdleDetector` retires for any agent
   a manifest covers (its `hasStructuredDriver` guard generalizes to
   `hasStateAuthority`). Port the one exception: a live
   `visible_blocker` match may raise attention even when the
   structured feed says working — claude's trust dialogs and plan-mode
   prompts have no hook today.
5. **Output**: post the classification as agent state/attention the
   hub already models — `blocked` maps to an attention item with the
   matched rule id as evidence; `working`/`idle` feed session status
   and the desktop sidebar. The `done`-until-seen refinement is B3's
   tail.
6. **Explainability**: a `host.pane_explain` verb returning the herdr-
   style evaluation record (matched rule, per-rule evidence, fallback
   reason, manifest source+version) — Inspect tab renders it. Debugging
   screen rules without this is guesswork; herdr ships it for a reason.
7. **Distribution**: manifests are hub-distributed data (the hub
   already moves datasets and profiles), *not* fetched from a vendor
   domain. Keep herdr's tamper rule: same version + different bytes =
   refuse. Local override wins, bundled beats stale cache.

Why this is the flagship: it is ADR-010's exact thesis ("adding an
engine is content, not Go code") applied to the *screen* signal, it
reuses infrastructure we already have (capture tick, attention kinds,
Inspect), and its unit economics are herdr's — one TOML per new agent,
maintained upstream by a 25k★ community we can diff against.

Honest costs: screen scraping is inherently fragile (herdr mitigates
with remote updates — we inherit the mitigation); the manifests encode
*herdr's* pane geometry assumptions (24-row default snapshot — ours
must match the vendored rules' expectations); upstream drift means
periodic re-vendoring; and two of herdr's agents (omp, mastracode) are
hook-only with no manifest to vendor.

## 4. B2 — the native-resume recipe table

herdr resumes agents after a restart by typing the engine's own resume
command into a fresh login shell: `claude --resume <id>`,
`codex resume <id>`, `kimi --session <id>`, `cursor-agent --resume
<id>`, `agy --conversation <id>`, `copilot --resume=<id>` … 16 engines,
each recipe hardcoded and version-gated, with validation (ids ≤512
bytes no control chars; paths absolute), shell-quoting hardened
(session ids are data, not shell text — their test literally uses
`"abc; rm -rf /"`), **restore-time dedupe** (two panes claiming one
session ref → only the first resumes, the duplicate becomes a plain
shell), and the rule that *native resume owns history* (no screen
replay into a resumed pane).

Host-runner already does engine-native resume for claude-code and
kimi (teleport respawn, `driver_exec_resume.go`). The borrow is the
**table itself** — 14 more engines' exact flags, pre-verified by
herdr's field population — plus three details worth copying: the
dedupe-on-restore rule (teleport + respawn can race the same session
id today), the id-validation envelope, and typing-into-a-shell rather
than exec (the user's rc/PATH/nvm apply, and exit leaves a live pane —
our tmux spawns already behave this way; keep it deliberate). Feeds
vision-parity L3/L4 directly: the resume-command column is exactly
what the Companion's local engine catalog needs per engine.

## 5. B3 — prompt-effect waits and the seen-bit

herdr's `agent prompt --wait` is two-phase and anti-staleness by
construction: it snapshots a **monotonic per-agent `state_change_seq`**
before submitting, then (phase 1) requires *any* observed state change
within 5 s — else it fails `agent_prompt_stalled` instead of waiting
forever — then (phase 2) waits for a settled state (`idle|done|blocked`
by default) with the remaining budget. The seq baseline is what
separates "was already idle" from "became idle because of my prompt";
occupant identity (pane + agent kind + name) is re-verified on every
probe, so a replaced process can never satisfy the wait.

Borrow into the steward/A2A "message an agent and await outcome" path
and the desktop Companion: our agent-event feed's row ids already
provide the monotonic seq; what's missing is the *two-phase shape* —
an explicit prompt-effect deadline distinct from the completion
deadline. We have prior scar tissue here (T1d: 30 s client timeout vs
15-minute commands, cancel-strands-agent); herdr's split is the
principled fix — fail fast when the prompt visibly did nothing, wait
patiently once it visibly did.

The tail of this borrow: herdr's `done` vs `idle` is one bit —
*finished and not yet seen by a human*. `done` rolls up through
pane → tab → workspace and is what makes "which of my ten agents needs
me" answerable at a glance. The hub's attention model has kinds but no
seen-bit on session completion; the desktop sidebar could carry
exactly this (mark seen on session focus).

## 6. B4/B5 — small, independent hardenings

- **B4a — bracketed paste.** No termipod send path uses tmux's
  `paste-buffer -p`, which wraps the paste in bracket codes *only if
  the application requested them* — precisely the live-mode-aware
  behavior herdr implements by reading the pane's DECSET 2004 state.
  One flag on the adapters' existing paste path; multi-line prompts
  stop being fragile against TUI line-editing.
- **B4b — generic pane input.** `PaneDriver.Input` (exactly the
  engines B1 serves) still does `send-keys -l <body>; send-keys Enter`
  — a multi-line body submits at its first newline. Give it the
  adapters' paste-buffer path. herdr also waits 300 ms between paste
  and Enter so the TUI ingests the text; our adapters send Enter
  immediately — worth adopting the delay on the generic path at least.
- **B5 — capture-cost gating.** herdr skips even *reading* the screen
  while an agent is idle and no PTY bytes have arrived (a monotonic
  byte counter, not a digest). Our per-pane `capture-pane` subprocess
  every 2 s is fine at 5 panes and wasteful at 50. tmux's per-pane
  activity timestamps (`#{window_activity}` via one `list-panes -F`)
  give the same skip signal in a single subprocess for all panes.
  Worth folding into B1's tick rather than shipping alone.

## 7. Rejected borrows

- **Live handoff (SCM_RIGHTS PTY transfer).** Solved by tmux
  ownership; complexity without benefit here.
- **Session/scrollback JSON persistence + replay.** tmux scrollback
  survives host-runner restarts already; and herdr itself treats
  replay as the weakest path (opt-in, secret-laden, superseded by
  native resume). Its one good hygiene point — segregating pane
  content from layout state because content holds secrets — is moot
  for us; host-runner persists no pane content.
- **The socket-API surface as a whole.** Ours is the hub + MCP with
  real auth; adopting an unauthenticated local socket would be a
  regression. The *semantics* of two calls (B3) are the borrow, not
  the transport.
- **Workspace/tab/pane object model.** Hub sessions + tmux panes
  already carry our equivalents; herdr's four id-spaces exist to
  solve restore problems tmux solves for us.
- **`HERDR_AGENT` wrapper-hint env var.** Spawn specs already declare
  the engine kind; we never guess from process trees.

## 8. Suggested build order (for director review — not a plan yet)

1. **H1 — evaluator + vendored manifests + fixtures** (B1.1–B1.2, pure
   library, no wiring): the #526 recipe — port, pin against fixtures,
   mutation-check the teeth.
2. **H2 — wire into PaneDriver + attention** (B1.3–B1.5, retire
   IdleDetector where covered; B5's capture gating rides along).
3. **H3 — explain verb + Inspect surface + hub-distributed manifest
   updates** (B1.6–B1.7) — the debuggability and ops half.
4. **H4 — independents, any order**: B2 resume table (pairs with
   vision-parity L3), B3 prompt-effect waits + seen-bit (pairs with
   steward/Companion work), B4 input hardening (one small PR).

H1/H2 are the wedge that changes what the product can see; H3 is what
keeps it debuggable; H4 items are opportunistic. A plan doc should cut
these into PR-sized wedges once the director confirms scope — in
particular whether B1 vendors manifests verbatim (fast, drift-coupled)
or forks the schema (slower, sovereign).

## 9. References

- [herdrdev/herdr](https://github.com/herdrdev/herdr) @ `6f311498` —
  subsystem maps on file in this session's investigation notes:
  `src/detect/` (manifest schema, evaluator, remote updates),
  `src/api/` (wait/prompt semantics), `src/persist/` +
  `src/agent_resume.rs` (restore + resume recipes).
- [ADR-010](../decisions/010-frame-profiles-as-data.md) — the in-house
  precedent for rules-as-data + interpreter + fixtures.
- [ADR-027](../decisions/027-local-log-tail-driver.md) /
  [local-log-tail-m4-replacement.md](local-log-tail-m4-replacement.md)
  — the structured-authority rung B1 slots beneath.
- [competitive-landscape-2026.md](../reference/competitive-landscape-2026.md)
  — refresh trigger fired; herdr belongs in its next quarterly pass.
