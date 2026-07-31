# Companion vision parity — kimi-web's bar, engine-agnostic

> **Type:** discussion
> **Status:** Resolved (2026-07-31) — feeds
> [desktop-companion-vision-parity.md](../plans/desktop-companion-vision-parity.md)
> **Audience:** principal · contributors weighing the Companion's next moves
> **Last verified vs code:** 2026.730.1231-alpha (`cea267fa`); kimi-web
> claims verified against a 2026-07-31 clone of MoonshotAI/kimi-code (MIT)
> **Freshness:** snapshot

**TL;DR.** The principal observed that kimi-web is the de-facto
vision-first companion (paste a screenshot, see everything render) while
the terminal companion is inconvenient for vision context — and asked
whether a kimiweb-like surface is possible for claude-code, codex, and
future engines. Investigation answer, in three findings: **(1)** the
literal kimiweb approach cannot generalize — kimi-web is the *vendor's
own SPA* spawned locally, and no other engine ships one — but **(2)** an
engine-agnostic vision path already exists end-to-end in this repo (the
Companion dock + per-driver image lowering + MCP vision tools), gated
only by six fixable asymmetries; and **(3)** kimi-web's professional
look is client-side craft on top of a strict view-model boundary — the
protocol beneath it is *less* rich than what our hub's typed
`agent_events` already carry for claude and kimi. ACP v2 can drive the
conversation core of such a UI but not the orchestration layer; our data
scheme is a superset on the axes that matter. Conclusion: match the bar
by investing in the Companion's renderer and composer against our own
event vocabulary — not by swapping protocols or embedding web apps.
The work is scoped in the companion plan linked above.

---

## 1. The question

kimi-web lets the user paste/drag an image, point at UI, and watch a
rich transcript render — vision-first. The terminal companion (tmux
panes) can't accept a pasted image and renders tool traffic as text.
kimi-web is engine-specific. Can we have the same class of surface for
claude-code, codex, and whatever ships next — ideally one surface, not
one per engine?

Two sub-questions fell out during investigation:

- What *exactly* makes kimi-web engine-specific, and what makes it look
  professional? (§2, §5)
- Which protocol should feed such a surface — the engines' own wires,
  ACP, or our hub events? (§6–§8)

## 2. What kimi-web actually is

Not kimi.com. The desktop spawns the user's local engine binary as a
web server — `kimi web --no-open --port N` (`desktop/electron/src/kimiweb.ts`)
— scrapes the `http://127.0.0.1:<port>/#token=…` embed URL from its
stdout banner, and hosts the vendor's SPA in a `<webview>` guest with a
non-persistent, loopback-pinned partition (`webtab_policy.ts`; the
bearer token rides the URL hash, so the partition must never persist).
The partition is deliberately `bridge: 'read'` — action-driving an agent
chat UI would let one bridge-enabled agent submit prompts into another
agent's session with user authority.

Vision transfer into it is CDP puppeteering on the user's gesture
(`annotation.ts`): `DOM.setFileInputFiles` + synthetic `input`/`change`
events for the crop, `DOM.focus` + `Input.insertText` for the pointer
note. The selectors are generic chat-composer heuristics
(`textarea`, `[contenteditable="true"]`, `input[type="file"]`) — only
the constant *names* are kimi-flavoured.

So the engine-specific residue is exactly: the spawn command + banner
parse, the `~/.kimi-code` data root, the partition row — and, decisively,
**the SPA itself**. Claude Code has no `claude web`; codex's app-server
is a protocol harness with no bundled UI. The kimiweb pattern is
"embed the vendor's own client"; without a vendor client there is
nothing to embed. (Embedding the vendors' *cloud* apps instead was
considered and rejected — those are cloud agents without local exec,
i.e. not the local companion; third-party-SPA selector coupling is
brittle; and wiring our bridge into them with write authority would
reopen the cross-agent prompt-injection hole the `bridge:'read'`
posture exists to close.)

## 3. What already exists engine-agnostically

The surprise of the audit: the vision *pipeline* is already
engine-neutral end-to-end.

- **Companion surface.** The dock's Companion tab
  (`desktop/src/ui/AgentCompanion.tsx`) binds to *any* hub agent of any
  family, streams its events over the hub SDK, and its Composer stages
  image attachments as base64 `WireAttachment`s via
  `postAgentInput` — including the D2 annotation-crop handoff chip.
- **Per-engine lowering.** `hostrunner/image_inputs.go` is the canonical
  attachment shape; `driver_stdio.go` lowers to Anthropic image content
  blocks for claude-code M2 (the *default* claude mode — claude is not
  terminal-driven), `driver_appserver.go` lowers to codex
  `input_image` data-URIs, the ACP driver lowers to ACP image blocks,
  and `annotation_materialize.go` gives every image-less mode the
  file-in-workdir + feed-line fallback.
- **Vision pull.** The desktop bridge's MCP tools
  (`browser_screenshot`, `ui_screenshot`, `ui_get_focus`) return real
  MCP image content blocks over the local stdio relay, and workdir MCP
  seeding (`launch_m2.go`) already writes claude/codex/gemini/kimi
  config formats.

Six asymmetries keep this from feeling like kimi-web:

| # | Asymmetry | Anchor |
|---|---|---|
| 1 | The whole dock — Companion included — is gated on the kimi panel existing and the kimi server having started | `AssistantDock.tsx` (early return on `!shell \|\| panel === undefined \|\| !started`) |
| 2 | Annotation routing hardcodes `kimi: boolean` instead of a target registry | `desktop/src/state/annotationTargets.ts` |
| 3 | Hub-relayed vision tools flatten MCP image blocks to JSON text | `hub/internal/server/mcp_browser_bridge.go` (`mcpResultJSON`) |
| 4 | The transcript renders user-input images only; agent-produced images (screenshots, plots) don't paint | `desktop/src/ui/EventCard.tsx` (`InputImages`, blob refs skipped) |
| 5 | User-level MCP reseed for ad-hoc sessions is kimi-only | `desktop/electron/src/kimimcp.ts` |
| 6 | Desktop Composer ignores the family registry's `prompt_image` flags (mobile checks them) | `lib/widgets/image_attach/composer_image_attach.dart` vs `desktop/src/ui/Composer.tsx` |

## 4. Options considered

- **A. Promote the Companion** to the vision-first surface by fixing the
  six asymmetries and closing the renderer gap. *Chosen — see the plan.*
- **B. ACP as the M1 unification layer.** The family registry already
  declares claude-code M1 as a future YAML-only addition
  (`launch_m1.go` header, `agent_families.yaml`). Externally this
  matured: official-namespace adapters exist for both engines
  (`agentclientprotocol/claude-agent-acp` on the Claude Agent SDK;
  `codex-acp` wrapping the codex app-server, images supported), gemini
  speaks ACP natively, and an adapter registry launched 2026-01. *Chosen
  as transport, explicitly not as the UI contract* (§8).
- **C. Webview clones of vendor/cloud UIs.** Rejected (§2).
- **D. Status quo** (materialized files + feed lines). Works everywhere,
  stays image-blind in the transcript; it is the fallback, not the bar.

## 5. Why kimi-web looks professional — the design study

Studied from a fresh clone of MoonshotAI/kimi-code (`apps/kimi-web`,
~19k lines of SFC; MIT license repo-wide, so everything below is
borrowable). Five pillars:

1. **A token system with a cop.** No component library — 27 custom
   primitives over a 954-line `style.css` where four colour seeds derive
   everything. A bespoke linter (`scripts/check-style.mjs`) *fails CI*
   on hardcoded hex, off-scale radii (4/6/8/12/16/20/999 only),
   off-scale weights, gradient text, glassmorphism, glows, and
   non-registry icons. A 2,390-line in-app design-spec view renders from
   the live tokens, so spec and product cannot drift. This mechanism —
   not taste — is why nothing looks arbitrary.
2. **Restraint as a rendering thesis.** Tool calls are 30px single-line
   mono rows (glyph · name · key-arg · status dot); consecutive calls
   auto-merge into one collapsible group; **only Questions and
   Approvals ever get a full card**, and those blocking prompts live in
   one fixed dock position. Prose is sans; paths/args/timestamps are
   mono — the contrast is a semantic signal.
3. **Motion engineering.** Everything ≤260ms from easing/duration
   tokens; expands animate `grid-template-rows: 0fr↔1fr`; a rAF
   scroll-pinning loop holds the viewport steady while cards expand;
   the moon spinner is reserved by written rule for exactly one state
   (waiting for the first response); global reduced-motion kill switch.
4. **Streaming markdown done seriously.** markstream-vue stable-prefix
   streaming; KaTeX/mermaid in workers; shiki that adaptively downgrades
   on huge outputs but never mid-stream; inline `$` math disabled so
   `$PATH` stays literal; diff fences rendered locally so deletion
   lines survive.
5. **A strict view-model boundary.** WS frames → a kimi-specific
   projector (`agentEventProjector.ts`, 1.6k lines) → neutral
   `AppEvent`s → reducer → turn folding (consecutive-assistant merge,
   streamed-vs-persisted dedup by content signature, dangling-tool
   settling) → components that only see pure view-model types and never
   touch the API. The visual layer is engine-portable by construction;
   only the projector knows kimi.

Sobering counterpoint: kimi-web itself cannot host another engine — it
hard-requires kap-server's 58-method REST surface, and the repo's ACP
package is *outbound only* (kimi-as-ACP-agent for Zed/JetBrains). Its
value to us is as a reference implementation and MIT parts bin — which
the desktop already uses (`toolGroups.ts` cites `ToolGroup.vue` /
`chatTurnRendering.ts` as its visual spec).

## 6. The wire contract beneath the bar

kimi-web consumes REST + WS with a snapshot/cursor resync protocol and
~40 raw engine event types. The features that read as "rich" map to
specific structured events; the load-bearing ones beyond plain chat:

| kimi-web feature | Powered by | ACP v2? |
|---|---|---|
| Streaming text + thinking blocks | `assistant.delta`, `thinking.delta` | ✅ message/thought chunks |
| Tool rows/groups, status dots | `tool.call.started` / `tool.result` | ✅ `tool_call(_update)` |
| Live tool output (running bash) | `tool.progress`, `shell.output` | ✅ `tool_call_content_chunk`, terminal upserts |
| Edit diff cards, ±line chips | tool args + `display.kind='diff'` | ✅ diff content (git patch) |
| Approval card, 10 typed bodies | `tool_input_display` 13-kind union | ⚠️ `session/request_permission` — options yes, typed bodies no |
| Question stepper | `event.question.*` | ✅≈ elicitation forms (JSON Schema) |
| Todo/plan card + dock pill | `TodoList` latest-wins | ✅ `plan_update` |
| Context ring, `/compact` chip, cost | `usage_updated{context_tokens, limit, total_cost_usd}` | ✅ `usage_update` (tokens+cost) |
| Model pill + thinking-effort segments | `/models` catalog, `support_efforts` | ✅ session config options (`model`/`thought_level` categories) |
| Permission-mode pill, plan/swarm modes | status + prompt fields | ✅≈ config options / session modes |
| Slash palette + skills | skills API + local table | ✅ `available_commands_update` |
| Subagent detail panel, swarm phase card | `subagent.*{phase, swarmIndex}` | ✗ |
| Background-task dock (bash chips) | `task.started/terminated{pid,…}` | ✗ |
| Compaction divider with token deltas | `compaction.completed{before,after}` | ✗ |
| Goal strip (budgets, pause/resume) | `goal.updated{budget…}` | ✗ |
| Cron-fire notice | `cron.fired{origin}` | ✗ |
| Inline prompt queue + steer | `prompts` queue + `:steer` | ✗ |
| Undo / fork / export | per-session REST verbs | ✗ |
| Git header, PR pill, sidebar badges | `fs:git_status`, `work_changed` | ✗ |
| Turn duration footer | `turn.ended{durationMs}` | ✗ (client-derivable) |

An important correction discovered en route: judging ACP by kimi's own
adapter *undersells the protocol* — that adapter drops tool progress
(→ `null`), emits an empty slash catalog, and hardcodes plan priority,
all documented in its own comments. The rows above are scored against
the ACP **v2 spec** (message upserts, `tool_call_content_chunk`,
terminal upserts, `usage_update` with cost, elicitation, session config
options with `model`/`mode`/`thought_level` UX categories), which is
markedly richer than the v1-era adapters implement today.

## 7. So — does ACP support such a UI?

Three-part verdict:

- **The design layer: trivially yes.** Tokens, primitives, folding,
  streaming markdown, motion — protocol-independent, MIT, already our
  cited visual spec.
- **The conversation core: yes.** ACP v2 carries everything in the ✅
  rows — including several things people assume it lacks (usage+cost,
  live tool output, model/mode/effort pickers, form questions).
- **The orchestration/product layer: no.** Subagent/swarm structure,
  goals, cron provenance, compaction deltas, server-side queue/steer,
  undo/fork/export, git surface — no ACP channel; the `_`-prefixed
  custom-kind escape hatch means per-engine agreements again.

## 8. Implication for TermiPod — the data scheme is the moat

TermiPod already implements kimi-web's architecture one level up: the
per-family frame profiles (ADR-010) are our `agentEventProjector`
equivalents, and the hub's typed `agent_events` already carry
orchestration data ACP cannot express — usage `by_model` on
`turn.result`, subagent lineage, compaction system events, attention
items with typed payloads — for kimi (wire tail) and claude (log tail /
stream-json) alike. Three consequences:

1. **The UI contract is the hub event vocabulary, not ACP.** ACP is a
   *transport* (the M1 rung; adapters now exist for claude-code and
   codex), and family frame profiles enrich beyond it exactly as they
   do today. Capping the Companion at ACP's vocabulary would forfeit
   the orchestration layer — the third of the feature table ACP can't
   carry is the third our scheme already has or can add.
2. **The Companion's gap versus kimi-web is renderer + composer
   investment, not data.** The transcript work (tool groups, plan
   fold, digest, streaming-markdown discipline) already landed the fold
   rules; what's missing is the design-system depth, the two full-card
   surfaces, the work-bar dock polish, and the composer pills.
3. **The enforcement mechanism is the highest-leverage borrow.** We
   already lint desktop tokens (`scripts/lint-desktop-tokens.sh`);
   kimi-web shows the end state — anti-pattern rules that make
   off-system UI *unmergeable*. See also
   [design-system-enforcement.md](design-system-enforcement.md) (the
   mobile-side version of this argument, resolved in ADR-047).

## 9. Addendum (2026-07-31) — how kimi-web drives, and the hub-optional consequence

Follow-up questions from the principal, answered here for the record.

**How does kimi-web drive?** Through none of our M-rungs — it is a
fourth topology: **engine-as-server, UI-as-thin-client**. `kimi web`
starts the engine's own daemon (kap-server), which owns the agent loop
and all session state; the SPA is a pure client of 58 REST verbs
(prompts, approvals, `:fork`/`:undo`/`:compact`/`:steer`, `fs:*`) plus
a WS stream with a real sync protocol — snapshot + per-session cursor
`{seq, epoch}`, `resync_required`, and an `in_flight_turn` block so a
client can attach mid-turn and recover a half-streamed reply. No hub
is involved anywhere in that loop; TermiPod only spawns the daemon,
embeds the SPA, and injects images on the user's gesture.

**M2 vs kimi's wire, compressed:**

| Axis | Our M2 (claude stream-json / codex app-server) | kimi's wire (kap-server WS; same agent-core events as `wire.jsonl`) |
|---|---|---|
| Topology | Hub spawns/owns the engine child; stdio pipe; one client | Daemon owns sessions; multi-client (TUI + SPA + our M4 tail concurrently); server outlives clients |
| Recovery | Process dies → turn gone | Snapshot/cursor/epoch resync + mid-turn `in_flight_turn` |
| Vocabulary | codex: richest telemetry (streaming text, `context_window`, `last_*`, real cancel); claude: richest cost (`cost_usd`, `by_model`) | Richest orchestration (`turn.step`, subagent phases, goals, cron, compaction deltas, queue/steer) |
| Verbs | Prompt / cancel / approvals | Product verbs: fork, undo, compact, export, BTW, file ops |

Within M2: claude stream-json is a per-session child speaking
line-JSON — a conversation pipe, not a server. Codex `app-server` is a
JSON-RPC server that happens to listen on stdio (and, since 2026-03,
WebSocket) — architecturally **kap-server's twin**. Claude is the odd
one out: no daemon exists; its native shape is spawn-per-session.

**The hub-optional consequence.** "Local" for the Companion means it
must work with no hub at all — the hub is one *source*, chosen for
remote hosts, fleet, and durable state, never a prerequisite. The
pieces above compose into the answer: the desktop plays the client
role the hub's M2 drivers play today, speaking the same native wires
(the D-6 mode decision is about *protocols* and survives intact —
what moves is who hosts the driver), and the **typed `agent_events`
vocabulary — not the hub process — is the UI contract**. Because
frame-profile translation is declarative YAML (ADR-010), a desktop-side
interpreter of the *same* `agent_families.yaml` rules, validated
against the *same* corpus fixtures as the Go interpreter, keeps the
two producers unable to drift. This lands as lane L + decision D-7 in
the plan.

**Direction from the principal (2026-07-31):** prefer the
WebSocket/service topology, and fold kimi-web *into* the Companion
rather than beside it — per engine, use the vendor's service and UI
where the vendor ships them (kimi: both; codex: service only), and
TermiPod builds the service where the vendor ships neither (claude).
Flexibility is the invariant: a vendor shipping a new service or UI
changes one row in the resolution, not the architecture. Recorded as
plan decisions D-1 (surface resolution) and D-8 (service-first) with
the lane-L reshape.

## 10. Resolution

Proceed per
[desktop-companion-vision-parity.md](../plans/desktop-companion-vision-parity.md):
fix the six asymmetries (§3), close the renderer/composer gaps against
the feature table (§6) using typed `agent_events`, borrow the kimi-web
design mechanisms under MIT, and add ACP M1 registry rows for
claude-code and codex as a transport lane. Related reading:
[desktop-design-review.md](desktop-design-review.md),
[codex-m2-app-server-surface-audit.md](codex-m2-app-server-surface-audit.md),
[../plans/desktop-ui-context-and-pointing.md](../plans/desktop-ui-context-and-pointing.md),
[../plans/agent-transcript-redesign.md](../plans/agent-transcript-redesign.md).
