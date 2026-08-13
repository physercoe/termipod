# deepseek-harness borrows — what DeepSeek's plugin harness teaches termipod

> **Type:** discussion
> **Status:** Resolved (2026-08-13) — full code-read of
> [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)
> @ `47f9438` (2026-08-13, TypeScript, MIT, `0.1.0-rc.5` "developer
> preview") plus the official announcement
> ([deepseek.com/harness](https://www.deepseek.com/harness/en/)); borrow
> catalogue ranked — B1's landing zone is
> [ADR-058 host-job-surface](../decisions/058-host-job-surface.md)'s
> executor lane; the drive-dsh-as-engine question answered (watch,
> don't ship yet — §3).
> **Audience:** principal · contributors
> **Last verified vs code:** main `9d36eb42` (2026-08-13)
> **Freshness:** snapshot (refresh when dsh ships 0.2/GA, stabilizes its
> SDK protocol, or adds cancel/resume to a stdio surface — any of which
> flips the §3 verdict)

**TL;DR.** DeepSeek Harness (`dsh`) is an agent *engine* — the layer
termipod drives, not the layer termipod is — built as ~50 Cordis plugin
packages where the model adapter, tool registry, session log, and the
agent loop itself are all replaceable from configuration. It is the
most rigorously *specified* open harness we have read: every durable
fact is an event in one append-only session log with a runtime-asserted
"model-visible ⟺ logged" invariant, every capability is a three-role
seam (definition / provider / consumer), and every package documents
its token and KV-cache effect. Reading it mostly **validates termipod's
architecture** — their projection-cache ladder is our digest
`fold_state_json` design independently re-derived, their approval audit
pair is our ADR-037 cards, their "hub of one process" has no answer to
our multi-host hub/teleport/attention layer, and they drive claude-code
and codex as subagents exactly the way our drivers do (so termipod's
layer is one they *don't* occupy). What they have that we lack
concentrates in five places: a **fail-closed sandbox exec shim**
(`landlock-run` + a `confine(argv, policy)` seam — the missing piece of
ADR-058's executor), a **surface-replacement protocol** that makes an
append-only log compaction-capable without deleting rows, a
**wake-budget** for self-exciting background-job notifications,
**typed message provenance** rendered all the way to a transcript
"Source" tab, and **crash repair that closes turns instead of
truncating them**. As an engine to drive, dsh is real but premature:
its best stdio surface (JSON-RPC, multi-turn, full event firehose) has
**no cancel, no resume, and no approval callback**, and the vendor
promises breaking changes — a family entry would be maintenance we
can't cache-pin yet. Borrow the designs now; add the family when the
wire settles.

---

## 1. What dsh is, and how we read it

One monorepo, three drivable apps (web UI on `127.0.0.1:3080`, one-shot
headless runner, stdio JSON-RPC runtime for the Python/TS SDKs), all
composed from the same plugin tree by ordered patch layers
(`bundle → profile patch → home patch → --patch`). The kernel is
Cordis (vendored, rescoped to `@deepseek-ai/*`): plugins contribute
services (`ctx.tools`, `ctx.llm`, `ctx.sessions`…), typed events with
declared dispatch modes (`emit`/`waterfall`/`parallel`/`serial`), and
reversible effects that unwind on unload. Models are bring-your-own-key
and not DeepSeek-only — Anthropic and OpenAI routes are first-class via
a generic adapter, plus any OpenAI-compatible gateway.

Method: four subsystem sweeps read mechanism-by-mechanism at `47f9438`
(client wire surfaces; sandbox/approval; session log/persistence;
orchestration/UI), each anchored to file:line, plus the architecture
docs, the generated event/persistence/tool catalogs, and their Agent
Notes (ADR-equivalents) and postmortems. Third-party commentary was
checked and discarded — the one circulating "deep-dive" describes a
Tauri/TUI/vector-memory product that does not match the shipped repo.

Terminology mapping, so the rest of this doc reads cleanly:

| dsh | termipod |
|---|---|
| `SessionEvent` log (per-session, seq from 0) | `agent_events` (ADR-038) |
| turn / step | turn / step (same split) |
| `agent/inbox` + `followup`/`steer`/`inject` | agent-input verbs (M2 wire) |
| session projection units + cache | transcript digests (`fold_state_json`) |
| `ctx.approval` + `approval/asked|decided` | attention queue + approval cards (ADR-037) |
| subagent providers (claude-code, codex, ACP) | engine drivers + `agent_families.yaml` |
| `permission-presets` → `sandbox/mode` + `approval/policy` | env profiles / tool posture (ADR-056, L3a) |
| JSONL/SQLite session persistence | hub Postgres-side durable feed |

## 2. Where the comparison validates termipod (no borrow)

- **The multiplexer layer is vacant.** dsh *drives* claude-code, codex,
  and ACP agents as subagent providers inside one process; it has no
  multi-host runner, no durable cross-agent attention queue, no
  teleport, no fleet surface, and its web server ships with **no
  authentication at all** (loopback-pinned by decree; `--host 0.0.0.0`
  is hard-rejected "for safety"). Our hub/host-runner/attention stack
  has no counterpart here, same conclusion as the herdr read.
- **Digest design.** Their projection-cache ladder — pure fold units
  `(init, apply, view, stateVersion)`, durable `(session, key, ver,
  seq, val)` rows, mandatory checkpoints at `turn/end` and
  live-to-cold, cold reads = cached row + tail replay — is our digest
  v7 (`fold_state_json`, `ensureShardColumns`) independently
  re-derived, down to "listings never load full logs." Two refinements
  are worth folding back (§4 B2).
- **Approval decidability.** Their approval outcomes (`allowed-once`,
  `rejected`, `cancelled`, `unavailable`) map to distinct model-facing
  deny reasons "so the model can tell a human 'no' from an absent
  approval channel" — the same team-wide decidability argument as
  ADR-037. Notably they ship **no** always-allow persistence at all:
  one-shot grants only, a deliberate omission we already went past.
- **Event-vocabulary posture.** Kind vocabulary open (merge-extensible
  `SessionEventMap`), producer semantics closed — the same
  open-kind/closed-producer call we made for hub event ingest (#536).
- **Terminal state detection.** Their PTY readiness detection (private
  prompt marker + foreground-group verification + silence fallback) is
  a bash-only, in-process cousin of the pane-state manifests we
  vendored from herdr; nothing there beats the manifest engine, and
  they have no cross-engine equivalent. Their `tmux-context` plugin
  reads pane *location*, never sibling pane content — no overlap with
  our M1 capture.
- **Blob posture.** Attachments are committed host-side before session
  events and resolved per-provider into native content — ADR-061's
  commit-before-reference discipline, independently chosen.

## 3. Can termipod drive dsh as an engine family? Not yet — here's the wire when we want it

The right surface is their SDK runtime: `dsh-jsonrpc-agent
<cordis.yml>` (or `DSH_CORDIS_CONFIG=…`), NDJSON-RPC 2.0 over stdio,
multi-turn on held-open stdin, and a **full session-event firehose**
(`session.event` notifications carry the same `SessionEvent` objects
the log stores; `session.status idle` is the turn boundary). It is the
structural analogue of claude's `--print --input-format stream-json`
and would slot into an M2-style driver with an L2 frame profile.
One-shot headless prints plain text only (their own docs call the JSONL
stream in its tests "test infrastructure, not a supported CLI output
format"); the ACP surface streams no deltas; the web API is
browser-shaped and unauthenticated.

Why not now — four hard gaps plus one soft one, all verified in code
and their own "Known Limitations" sections:

1. **No cancel** on the SDK surface. Abandoning a turn means killing
   the runtime process. Our driver contract treats cancel as a
   first-class verb (kimi 0.31 wire; claude tool_result deny); a
   kill-to-interrupt family would regress the Companion's stop
   affordance into process recycling.
2. **No resume** on any stdio surface — `session/prompt` with an
   unknown id silently creates a *fresh* session; persistence is
   written, never reloaded. N1 resume recipes would have nothing to
   call.
3. **No approval round-trip** — permission policy is baked into the
   `cordis.yml` at composition time. We could only offer
   converse/read-only/unrestricted-style static postures, not ADR-037
   cards.
4. **No protocol versioning** — the handshake carries an unvalidated
   `"0.0.1"`, and the README warns of compatibility breaks in caps.
   Our frame-profile fixtures would be chasing a moving target with no
   version to pin against.
5. Soft: launching requires *us* to author and ship a `cordis.yml`
   composition (there is no default), and stdout purity is fragile —
   one stray logger row in the composition corrupts the channel.

**Verdict:** record the family as *watched, not shipped*. The trigger
to build it: any stdio surface gaining cancel + a version handshake.
When that lands, the driver is cheap — spawn `dsh-jsonrpc-agent` with a
vendored minimal composition (bash + editor, JSONL persistence,
approval `never`, workspace-write sandbox — *their* sandbox then
confines *their* tools, which composes nicely with our posture model),
frame profile over `session.event`, `session.status` for lifecycle.
ACP is the other lane: dsh is an ACP server (cancel ✓, permissions ✓,
but committed-text-only, no streaming) — if the L4 codex work grows a
generic ACP driver per the agent-protocol-roles discussion, dsh comes
along nearly free, at the cost of losing token streaming and tool
visibility. Neither lane blocks the other.

Also note the inversion: dsh drives **claude-code and codex as
subagents** (via the official Claude Agent SDK and `codex app-server
--stdio`). Engines are becoming each other's clients; the
protocol-roles landscape should absorb this data point.

## 4. Borrow catalogue, ranked

### B1 — Sandbox exec shim + `confine()` seam for host-runner (ADR-058 lane) · **highest value**

The gap ADR-058 left open: host-runner executes allowlisted
`host_commands` kinds with no kernel confinement. dsh's answer is two
separable pieces, both MIT and both designed for exactly our topology:

- **`landlock-run`** (`native/landlock-run/`, ~300 lines of C11,
  static-musl, published as prebuilt npm binaries): installs a Landlock
  ruleset **on itself** then `execvp`s the wrapped command — the
  ruleset inherits across exec, so the daemon stays unconfined and
  only the one-shot shim + descendants are fenced. `--ro <path>` /
  `--rw <path>` grants, everything else denied. **Fail-closed
  absolutely**: a kernel without Landlock exits 125 with a `landlock-
  run: ` stderr line rather than exec'ing unconfined; partial-ABI
  enforcement proceeds but is reported; `--probe` is a *functional*
  probe (actually enforces in a scratch process) returning
  full/partial/unusable, and a missing binary deliberately probes
  `unusable` — one degradation path, not two. Trivially reimplementable
  in Go (`unix.Syscall` for 444/445/446 + `PR_SET_NO_NEW_PRIVS`) or
  vendorable as-is under NOTICE discipline.
- **The seam**: `confine(argv, policy) → {argv, enforcement,
  denialSignatures, runnerFailureRules}`. Policy rides the call (two
  concurrent jobs can run under different modes with no provider
  state); each backend returns its **own** stderr denial dialect
  (bwrap's "read-only file system" vs Landlock's "permission denied" —
  deliberately never a cross-backend union); and `RunnerFailureRule
  {allowedExitCodes, fatalSignatures, informationalLines}` separates
  "the sandbox refused to start" from "the sandbox worked and blocked
  the command," checked in that order because exit status alone never
  proves runner failure (their postmortem 0004 is exactly this bug).
  Platform chains: bwrap→landlock on Linux, Seatbelt on macOS
  (write-deny only — reads/network open, a real asymmetry they don't
  document), ACL-restricted tokens on Windows (honestly marked
  `partial`, with a documented list of what stays writable).

Concrete wedge: a `hostrunner/confine` package (Go port of the shim +
the seam struct), wired under ADR-058's detached executor with mode
derived from the job kind's declared write surface; `writableRoots`
single-sourced between the shim profile and any in-process path fence,
canonicalized with native realpath. Their escalation ladder
(`WIDER_MODES`, strict-widening checked at execution time, non-widening
never prompts a human) maps onto our approval cards for a later
"escalate this job" affordance.

### B2 — Surface replacement + log hardening for `agent_events`

Their log is append-only *and* rewritable-for-the-model, via one
primitive: a surface event may carry `surfaceOp: {op:'replace', start,
end}` and **must** cite every shadowed node in `sourceEventSeqs`.
Compaction, tool-result pruning, and any future rewriter are all just
producers of that one op; raw rows never leave the log (still
searchable, classified `current|shadowed|log-only`); the human
transcript deliberately renders append-origin events (what the user
saw), while model history renders the surface. Four sub-borrows,
independent and cheap-to-adopt in ADR-038's vocabulary before we ever
need hub-side compaction:

1. **`ignorable` defaults to required.** An unknown event kind without
   an explicit skippable marker makes a reader *refuse to reconstruct*
   rather than silently drop — a forgotten marker over-refuses instead
   of silently gutting a session. Our readers currently skip unknown
   kinds; one column/flag closes a future silent-wrong-read class.
2. **Writer-driven version-bump rule**, verbatim-worthy: bump exactly
   when an *older runtime* could no longer read a *new log* with full
   semantic correctness — "parses without error" is not correctness;
   vocabulary growth is covered by `ignorable`, never by a version
   bump; when in doubt, bump.
3. **`session/end-seed`** — a durable "this lifecycle's writes start
   here" marker appended on every fork/resume seed, so an unmatched
   open bracket (their `compaction/start`, our future equivalents)
   before the *last* marker is provably a dead lifecycle's orphan, not
   live work. Sharp edges pre-solved: don't re-mark an untouched
   session; readers find the LAST marker; it is explicitly not a
   liveness signal about concurrent writers.
4. **Digest-ladder refinements** for our fold-state rows:
   `restoreFloor` reads the tail from **one event below** the lowest
   watermark so a log that *shrank* (crash repair) is detectable
   instead of serving a stale row as current; and an explicit
   `stateVersion` per fold so semantics changes discard old rows
   rather than forward-applying into garbage. Also the **shadow-price
   adjacency protocol**: any surface rewrite is priced by a metering
   event appended immediately before it, so a pure fold can subtract
   shadowed token cost without per-node state.

### B3 — Approval + permission-state hardening

- **Pending approvals survive client disconnect.** The server owns the
  pending entry keyed by a stable request id; reconnect (their
  mux-open) replays every still-pending request with the *same* id;
  the client clears its stale view on disconnect; answers are
  re-validated against the entry the id routes to ("a mismatched
  answer is malformed, not merely late"); disposal settles everything
  `cancelled` so no ask dangles. Desktop's dock and mobile both reload
  mid-approval today — our attention list re-fetch covers the durable
  queue, but the L3a local service and any future renderer-held asks
  should adopt exactly this replay contract.
- **Log-folded permission state.** `sandbox/mode` and
  `approval/policy` are session events; effective state =
  `fold(events) ?? deployment default`. No config store, survives
  restart by replay, per-session isolation for free, and a preset
  switch writes one `permission/preset` intent event through to both
  knob events. Our L3a tool posture is create-time-only; when it
  becomes mutable, this is the shape (and it matches how we already
  fold plan/mode-like state in digests).
- **Audit pair inside the turn.** `approval/asked`/`approval/decided`
  are mandatory paired appends, refused outside an open turn, with an
  invariant checker for unmatched pairs. Our hub decide route persists
  the decision; the *ask* is currently only implied by the attention
  row — a paired ask event would make transcripts self-auditing.

### B4 — Delivery lanes + wake budget (attention P-lane, Companion, jobs)

Their inbox vocabulary is one primitive — `send(message, target:
next-turn|next-step, wakeup: bool)` — with `followup`/`steer`/`inject`
as named presets, and **every** subsystem (job notices, skill catalog
updates, subagent reports, scheduled reminders, hook context) must pick
a lane and document it. Two mechanisms on top:

- **Busy→inject, idle→wake** for background-work completion notices:
  a busy agent gets the notice folded into its next step (N settlements
  cost one step, and a turn cannot close while an unclaimed notice
  holds it open); an idle agent gets woken with a follow-up turn
  "because a pending notice nothing claims is a completion the model
  never learns about."
- **`maxConsecutiveWakes` (default 3)**: completion-notice wakes are
  budgeted per owner because the chain is self-exciting (a woken turn
  can start the job whose completion wakes it again); past budget,
  notices degrade to injection; only *user-authored* input refills the
  budget.

Termipod's ADR-058 job completions, P3 attention raises, and the L3b
local service all sit on this exact problem. The wake budget is the
piece we have nowhere: nothing today stops an agent-notification loop
from ping-ponging a steward. Small, self-contained, worth a wedge in
the host-runner/hub notice path.

### B5 — Crash repair that closes instead of truncating

On resuming a log that ends mid-turn, they synthesize (never delete):
one errored `tool/result` per unmatched call — with **differentiated
model-facing advice**: "outcome unknown; retry only if read-only or
idempotent, else verify external state first" for a call that started,
vs "never started; retry if still needed" — then `step/end`, then
`turn/end {reason: interrupted}` where `interrupted` is a reason **no
live writer can ever emit**, making crash-orphaned turns a queryable
first-class fact. Timestamps reuse the last real event (never invent
time); synthetic results cite the original call. Our hub marks agents
crashed at the lifecycle level, but transcripts of crashed sessions
currently end with dangling tool calls that a resumed engine (N1
recipes) re-reads without guidance. This is a small, high-leverage
driver/hub wedge.

### B6 — Subprocess hygiene (host-runner + Electron)

- **`SENSITIVE_ENV_PATTERN = /KEY|PASSWORD|SECRET|TOKEN/i`**: every
  spawned child gets a scrubbed env (plus all vendor-prefixed vars),
  exported as one shared function so every spawner uses the same scrub;
  explicit `env` merges *after* the scrub so deliberate forwarding
  still works — and the scrub is applied across the remote boundary
  too. We fixed env-shadowing bug-by-bug (E3 lane); the pattern-scrub
  is the class fix for the *leak* direction, directly applicable to
  host-runner spawns and L3a's engine children.
- **Offset-addressed, non-consuming output readers**
  (`readFrom(fromByte)`): N attached UIs can each read a child's
  output without stealing each other's deltas — exactly the multi-
  client case our desktop+mobile attach creates.
- Temp/spill files: private 0700 dir, random names, `'wx'`+0600
  exclusive opens — symlink-race hygiene our SFTP/host-artifact paths
  should already match (worth a sweep).

### B7 — Chunk-run packing for event storage

Streaming deltas make logs mostly JSON envelope (~56× overhead measured
on a real session). Their fix: a **storage-only** row vocabulary
(`text-chunks {seq0, time0, dt[], texts[]}`) that packs runs of ≥3
same-block delta events; whitelist-or-verbatim encoding (unknown shapes
store unpacked — loses compression, never data); reads are layout-
blind; token boundaries preserved (`texts` never joined). ~60% smaller
logs. Our `agent_events` stores M2 deltas row-per-event today; if
transcript storage growth ever bites, this is the shape — a storage
concern invisible to every reader, which is what makes it safe.

### B8 — Session-log-as-replay-fixture (J8 datasets extension)

Because chunks are logged losslessly, grouping `assistant/chunk` by
`(turn, step)` reconstructs each provider stream exactly — so **the
recorded session IS the test fixture** ("run the real agent once,
harvest the JSONL"), with a small sidecar only for the two things a
chunk log can't express (pure pre-stream throws, cancel timing), regex
placeholders for run-minted ids, and `assertConsumed()` so a scenario
that drove fewer calls than recorded fails loud. Their own survey of
nine OSS agent products found none replaying a recorded event log
through the real backend for UI tests. J8's datasets already replay
*termipod's* wire; the borrow is the discipline extensions:
fixture-consumption assertions, sidecar overrides instead of fixture
edits, and nested-session binding by first-call order.

### B9 — Provenance as a typed union, rendered to the user

Every message carries a `MessageSource` discriminated union (user,
plugin+name, goal round, subagent-report `form:'relay'`,
subagent-settled `form:'notice'`, …), and the runtime's *account* of an
agent ("child ended: completed") is a **different kind** from content
the agent *wrote* — a transcript can never credit a child with words it
never said. Their Trajectory view renders a per-row **Source tab**
(label + raw JSON) and even indexes `messageSource` for search. Our
fold engine already tags producers (ADR-038), and D2's annotation
interplay taught us injected-content ambiguity is real; the borrows
are (a) the relay/notice form split for steward-injected content, and
(b) a Source affordance in the desktop transcript's Inspect pane —
cheap, because the wire already carries producer.

### B10 — `tmux-context`'s inherited-`$TMUX_PANE` check (M1 hardening)

A process launched *from* a tmux shell (VS Code terminal, desktop
launcher) **inherits** `$TMUX`/`$TMUX_PANE` while living in no pane;
their plugin only trusts the env var after comparing the pane's
`#{pane_tty}` against the process's own controlling tty. Any place our
tooling or docs infer "in tmux" from env (host-runner diagnostics,
future pane_explain self-location) should adopt the tty cross-check —
it's three lines of shell and closes a real false-attribution.

### B11 — Process borrows (docs/CI discipline)

Cheap to adopt piecemeal; listed for the director's discretion rather
than as wedges:

- **Generated, CI-verified catalogs**: their event
  producer/consumer matrix, persistence catalog (every durable event
  kind, payload, declaration site), tool catalog, and config catalog
  are generated from source and checked fresh in CI. Our api/schema
  delta indexes (docs audit) are hand-curated; generating the event
  catalog from Go source + checking in CI would end drift by
  construction.
- **A `defensive-patterns.md`**: hard-won bug-class rules as a
  first-class doc ("report orthogonal outcomes independently", "dispose
  must reach quiescence", "async state is not synchronous state") —
  the repo-visible version of what we keep in reviewer memory; several
  entries are classes we've independently hit (cancelled≠failure,
  lifecycle-flip writer sweeps).
- **Postmortems with a bar** (subtle + systemic + costly-to-
  rediscover, executive summary first) distinct from decision records;
  and their Agent-Notes lifecycle (proposed/implemented/rejected/
  archived as *path*, "rejected kept only while it prevents a tempting
  mistake") — a tighter regime than our flat ADR statuses.
- **Invariant registry**: package-owned runtime invariants
  (`ctx.invariants`) with allow/blocklists so expensive checks (their
  dispatch-time "re-derive the request from the log and byte-compare")
  run in dev/staging only — the mechanism that makes invariant 8 of B2
  affordable. Every package must ship an invariant or an explicit
  "No runtime invariant:" justification, mechanically verified.
- **Per-package "Model Experience / Token effect / KV-cache effect /
  Known Limitations"** README sections — the Known-Limitations habit
  alone made this read twice as fast; our plans do this, our packages
  don't.

## 5. Considered and not borrowed

- **Cordis / everything-is-a-plugin.** The composability is real but
  it's a whole-product bet (their own postmortems 0001/0002 are
  plugin-composition footguns: a default-export dropping `inject`, a
  literal `!!js` string disabling filesystem tools). termipod's
  four-tier spine with generated artifacts is the opposite,
  deliberate call (system-tiers axiom); no regrets after this read.
- **Their workflow engine** (model-written scripts fanning out over
  `ctx.subagents`, phase/log events, worker-thread isolation) — it's
  Claude-Code-workflow-shaped ("the field vocabulary matches the
  Claude Code dynamic-workflows meta block") and lives inside one
  engine process; our fleet/steward layer covers this at the product
  tier where it belongs. Their fatal-vs-item-failure discipline
  (`WorkflowError.fatal` re-thrown, `null` reserved for real child
  failures) is a good rule if we ever script fan-out.
- **Schedule** (deliberately-not-cron: `after/at/every≥300s`,
  session-local delivery, at-least-once, maintenance-phase dispatch
  that never steers) — well-designed but hub cron + steward spawn
  already cover the product need; the "dispatch waits for true idle
  and claims a maintenance phase" idea folds into B4 thinking instead.
- **Spill** (oversized tool text → disk + model-facing locator +
  retrieval hint, no runtime resolve API) — elegant, but engine-side;
  our media/blob posture (ADR-060/061) covers the hub side and
  engines' own spill behavior reaches us as ordinary transcript text.
- **Session query FTS** (live-overlay-shadows-durable SQLite FTS5,
  literal-only queries, 17-code error taxonomy) — desktop search will
  want this *shape* eventually (their live-preferred corpus rule
  matches our writeDB/readDB discipline), but it's not a current gap.
- **Driving dsh via its web API** — unauthenticated by design,
  browser-trust-fenced only, explicitly not a stable contract.

## 6. Follow-ups this read trips

- **competitive-landscape-2026.md refresh** — a major-lab open harness
  in developer preview is exactly the standing refresh trigger; this
  doc is source material, the refresh itself remains queued.
- **agent-protocol-roles / aip-acps discussions** — new data points:
  dsh as ACP server *and* client, engines driving engines (claude-code
  + codex as dsh subagents), and a JSON-RPC-over-stdio SDK wire as the
  emerging lingua franca. Worth a short addendum when either doc is
  next touched.
- **Watch triggers for the dsh family** (§3): cancel verb or version
  handshake on a stdio surface; ACP driver landing via the L4 lane.

## Sources

- Repo: <https://github.com/deepseek-ai/deepseek-harness> @ `47f9438`
  (2026-08-13, MIT, `0.1.0-rc.5`)
- Announcement: <https://www.deepseek.com/harness/en/> ·
  [launch post on X](https://x.com/deepseek_ai/status/2087887408440164663)
- In-repo primary docs: `docs/architecture.md`, `docs/cordis-primer.md`,
  `docs/capability-seams.md`, `docs/event-producer-consumer.md`,
  `docs/persistence-catalog.md`, `docs/subsystems/{session,subagent,
  jobs,skills,workflow,terminal,schedule,goal,plan}.md`,
  `docs/defensive-patterns.md`, `docs/postmortem/0001–0004`,
  `native/landlock-run/`, `packages/sdk/*`, `packages/acp/acp`,
  `.agents/notes/` (their ADR tree)
