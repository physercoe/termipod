# Integrating open-source and computer-use agents

> **Type:** discussion
> **Status:** Open (2026-04-30)
> **Audience:** contributors
> **Last verified vs code:** v1.0.349

**TL;DR.** Termipod's plurality today is commercial engines (claude-code,
codex, gemini-cli). The 2026 open-source landscape splits sharply into
four groups, and our pluggability story is honest only for one of them.
**CLI coding agents** (OpenClaude, OpenCode, Goose, OpenHands, SWE-agent,
Aider) integrate cleanly via [ADR-010](../decisions/010-frame-profiles-as-data.md)
frame profiles + driving modes M1/M2 — OpenClaude in particular is nearly
drop-in because it's a Claude Code fork. **Messaging-gateway personal
agents** (OpenClaw shape) invert the principal/director archetype and
should stay a non-goal. **GUI / computer-use agents** (Cua, OpenCUA,
OpenAdapt, Claude Computer Use, Operator, Manus Desktop) don't fit the
pane primitive at all — they need a new spatial primitive and a new
driving mode (call it **M5 graphical**), roughly the size of the original
A2A wedge. **Hermes Agent** is the interesting middle case: its CLI half
is M2-compatible, its self-improving skill library competes
philosophically with our session/transcript primitive, and its
messaging-gateway half stays out. This doc surveys the landscape, names
the integration surface, and lists the concrete gaps the current design
has — without committing to any of them. The decisions belong in
follow-up ADRs.

---

## 1. Two design questions, not one

The user's question — "does the design support open-source / GUI agents?"
— hides two distinct subquestions:

1. **Engine-pluggability**: can a new engine kind drop in without a Go
   diff? ADR-010 commits to *yes for stream-json dialects*. Open
   question is whether the YAML grammar is wide enough for engines whose
   primary output is markdown, image, or screenshot rather than line-
   oriented JSON.
2. **Paradigm-pluggability**: does termipod's principal/director
   archetype, and its pane spatial primitive, accommodate agents that
   are operated through messaging gateways or that control a graphical
   desktop? This is not solved by a frame profile — it's a primitive-
   level question.

Conflating them produces the wrong recommendation in both directions
("we already support it, just write a profile" overshoots; "we don't
support open-source" undershoots). Sections 4–7 below split by paradigm
first, then by engine within paradigm.

---

## 2. The 2026 landscape (snapshot)

Brief survey of what's actually being adopted in early 2026, sourced from
public materials at time of writing. Not exhaustive; chosen to span the
paradigm space.

**CLI coding agents (Claude-Code-shaped):**

- **OpenClaude** — open-source coding-agent CLI forked from the Claude
  Code codebase; 200+ models via OpenAI-compatible APIs, MCP, slash
  commands, streaming output. The closest existing analogue to claude-
  code itself.
- **OpenCode** — Go terminal UI by SST; 75+ providers including Ollama
  for fully local. ACP-capable.
- **Aider** — terminal chat with diff-based edits; markdown-first output.
- **OpenHands** / **SWE-agent** — research-grounded GitHub-issue solvers;
  YAML-configurable agent-computer interface (ACI).
- **Goose** (Block) — CLI agent with built-in session model and rewind.
- **Cline** — VS Code extension; not a spawnable CLI in a tmux pane.

**Messaging-gateway personal agents:**

- **OpenClaw** — went viral early 2026 (247K stars by March). Lives
  *inside* messaging clients (Signal/Telegram/Discord/WhatsApp). The
  user talks to the agent through the messenger, not through a phone
  app.
- **Hermes Agent** (Nous Research, Feb 2026) — multi-platform messenger
  + CLI; persistent memory; self-improving skill library that writes
  new skill files into the workspace and queries them on the next
  similar task. MCP-native.

**GUI / computer-use agents:**

- **Claude Computer Use**, **OpenAI Operator**, **Manus Desktop** —
  commercial; control a graphical desktop session.
- **Cua** (trycua/cua) — open-source infrastructure for computer-use
  agents; cloud desktops, sandboxes, benchmarks.
- **OpenCUA** (xlang-ai) — open foundations; AgentNet dataset (3 OSes,
  200+ apps); 72B reference model.
- **OpenAdapt** — multimodal process automation.

The key axis is not commercial-vs-open. It's *what does the agent
control?* — a CLI in a pane, a messenger account, or a graphical
desktop. Termipod's spatial primitive (pane) supports the first
unconditionally and the others not at all.

---

## 3. The integration surface termipod actually has

Three commitments make engine-plugability possible today:

**S1. Frame profile** ([ADR-010](../decisions/010-frame-profiles-as-data.md)).
Per-engine YAML rules mapping engine frames → typed hub events. New
engine = YAML, not Go. Expression DSL is JSON-path + coalesce + boolean
literal-equality. **No regex. No image type.** Forward-compatible:
unmatched frames pass through as `kind=raw`.

**S2. Driving modes** ([blueprint §5.3.1](../spine/blueprint.md)). M1
(ACP), M2 (engine-native structured stdio), M4 (tmux pane only). M3 is
not a mode; it's an `llm_call` plan step. Modes are about *how
host-runner wires the engine's stdio*, not about what the engine can do.

**S3. MCP gateway relay** ([blueprint §5.2](../spine/blueprint.md)).
Engines call `hub://*` capabilities through host-runner. An engine that
isn't an MCP client is observable but cannot post back to channels,
attention, projects, etc. — hub mediation becomes one-way.

If a candidate engine satisfies all three (spawnable binary, profileable
stdout dialect, MCP client), integration is hours of YAML. If any of the
three is missing, it's an architectural change.

---

## 4. Group A — CLI coding agents (strong fit)

| Engine | Mode | Frame profile | Effort | Notes |
|---|---|---|---|---|
| **OpenClaude** | M2 | Existing `claude-code` profile, minor edits expected | Hours | Fork of Claude Code; same stream-json by lineage. Almost drop-in. |
| **OpenCode** | M1 (ACP) or M2 | New profile | Days | ADR-010 §Context names ACP-native engines including OpenCode. |
| **Goose** | M2 | New profile | Days | Built-in session/rewind would benefit from an ADR-014-style engine session-id capture rule. |
| **OpenHands**, **SWE-agent** | M2 (ACI commands) | New profile | Days | YAML-configurable already; profile work is matching. Neither speaks MCP outbound — see §8 gap (3). |
| **Aider** | M4, or M2 with text profile | **DSL extension required** | Wedge | Markdown + diff blocks, not JSON frames. ADR-010 expression DSL is JSON-path only. Either extend DSL with regex matchers (`profile_version: 2`) or accept M4 fidelity. |
| **Cline** | — | — | Out of paradigm | VS Code extension, not a spawnable CLI. Termipod's host-runner spawns a process and steers stdio; Cline is steered from inside an editor. |

The OpenClaude case is the validation case for ADR-010: a real second
engine in this family lands without Go. The Aider case is the first
honest stress test of the frame-profile DSL — it tells us whether the
expression subset is wide enough or needs `profile_version: 2`.

---

## 5. Group B — Messaging-gateway personal agents (paradigm mismatch)

OpenClaw is operated through Signal/Telegram/Discord/WhatsApp. Hermes
Agent (in its messenger half) the same. Termipod's principal/director
archetype assumes the human directs *through the termipod app* — that's
the entire UX premise of the
[information architecture](../spine/information-architecture.md).

Two integration paths exist; neither is small:

- **Sidecar pattern** — host-runner spawns the messaging gateway
  alongside the engine. Forces a routing decision: does termipod's
  attention queue mirror into Signal, or does Signal traffic mirror
  into termipod? Both produce a coherent product, neither matches the
  current archetype.
- **CLI-only spawn** — run the engine in a tmux pane, ignore its
  messaging gateway. Defeats the engine's product thesis.

The right answer is most likely *we don't integrate this paradigm.*
This is consistent with the [blueprint §10](../spine/blueprint.md) non-goal
"we are not a coding assistant" — analogously, we're not a personal-
agent platform. A director who wants OpenClaw should run OpenClaw, not
termipod. **Recommendation:** add an explicit non-goal entry in
blueprint §10 to that effect, so this question doesn't re-litigate.

---

## 6. Group C — GUI / computer-use agents (primitive gap)

Cua, OpenCUA, OpenAdapt, Claude Computer Use, Operator, Manus Desktop
control a graphical session (mouse, keyboard, screen). They do not run
in a tmux pane. Three concrete gaps in the current design:

**G1. Spatial primitive.** The pane primitive in
[blueprint §3.2](../spine/blueprint.md) is one-dimensional (text stream).
Computer-use agents need a 2-D primitive: a desktop session backed by a
display server (X11/Wayland/macOS WindowServer) or a remote one
(VNC/RDP). The Enter-pane SSH binding flow (§5.3.3) has no analogue —
we'd need an `Enter screen` flow with a VNC/RDP client and a
`hub_screens` analogue to `hub_host_bindings`.

**G2. Frame profile insufficiency.** Computer-use frames are
`(action, screenshot)` pairs. ADR-010's expression DSL has no image
type, and the AG-UI rendering surface has no screenshot card. The
profile would need (a) a binary-blob payload kind, (b) a hub artifact-
upload step inside the rule, and (c) a new card type in
[lib/widgets/agent_feed/](../../lib/widgets/agent_feed/).

**G3. Audit granularity.** A3 (governance) requires every action be
bounded by an MCP-policy-gateable rule. Mouse clicks aren't gateable at
click granularity — only at *application-launch* granularity ("agent may
launch firefox, may not launch slack"). The
[principal/director authority model](../decisions/005-owner-authority-model.md)
assumes tool-call boundaries; for GUI agents we'd coarsen to launch
boundaries plus screenshot-pause-for-approval at salient moments
(e.g. "about to click 'Send'"). This is a real ADR, not a small edit.

Filling Group C is a coherent piece of work — call it **M5 graphical**
with a `desktop_sessions` primitive, screenshot card type in AG-UI, and
a coarsened approval model — but it is wedge-sized, not free. Roughly
the scope of the original P3 (A2A) work in the
[blueprint roadmap](../spine/blueprint.md).

---

## 7. Group D — Hermes Agent (the middle case)

Hermes ships a messaging gateway *and* a CLI mode, is MCP-native out of
the box, and supports any model via OpenRouter / Nous Portal / local
Ollama. Its CLI half is M2-compatible with a YAML frame profile —
straight Group A. Its messaging-gateway half is Group B and stays out.

What's interesting is what Hermes brings that termipod does not model: a
**self-improving skill library**. The agent writes new skill files into
its workspace when it completes a complex task, and queries them on the
next similar task. This is philosophically adjacent to the
[`documents` primitive](../spine/blueprint.md) but at a tighter feedback
loop — the agent reads its own output back as context.

Two open questions worth recording:

**Q1. Where do Hermes skill files live across forks?** If they're in the
worktree, the [fork-is-cold-start invariant](../decisions/014-claude-code-resume-cursor.md)
wipes the skill library on every fork. The user would expect skills to
persist. We'd want either:
- an opt-in "carry skill files on fork" affordance (fork copies a
  declared skill directory from source worktree),
- or accept it as a known limitation and document it in the fork
  productisation
  [discussion](fork-and-engine-context-mutations.md).

**Q2. Does engine-side state mutation (skill writes, model fine-tunes,
local cache state) count as an *engine session* in the
[blueprint §3.4](../spine/blueprint.md) sense?** Hermes is stateful in a
way claude-code is not (engine-side mutation between runs, not just
within a turn). The fork/resume semantics that produced OQ-1/OQ-2/OQ-4
in [ADR-014](../decisions/014-claude-code-resume-cursor.md) would
surface again here. May warrant an ADR-014 amendment if Hermes is
adopted, or a generalised "engine state surface" treatment in
agent-state-and-identity.

Hermes is the most interesting integration candidate precisely *because*
it stresses the design at exactly the joints we already know are
fragile.

---

## 8. Concrete gaps in current design

Five, in order of size:

**Gap 1 — Frame-profile DSL is JSON-only.** Markdown/text engines
(Aider) and binary frames (computer-use) need richer matchers. **Fix:**
ADR amendment + `profile_version: 2`. Regex matcher for text engines is
small (~50 LoC). Binary-blob payload + artifact-upload step for image
frames is larger (~200 LoC + AG-UI card type) and probably belongs to
M5.

**Gap 2 — No M5 graphical mode.** Computer-use agents need a
`desktop_sessions` primitive parallel to panes, an `Enter screen`
binding flow analogous to Enter-pane, a screenshot card type in AG-UI,
and a coarsened MCP approval model. **Fix:** wedge-sized; warrants its
own ADR (proposed: ADR-015). Roughly the scope of A2A.

**Gap 3 — No engine adapter for non-MCP outbound agents.** SWE-agent
and several research agents don't speak MCP outbound — they emit
ACI-style commands or markdown action blocks. We observe but they
can't post to hub channels / attention / projects. **Fix:** small —
host-runner-side translator that recognises a known subset of their
output shapes and synthesises MCP-equivalent calls. Per-engine cost,
not architectural.

**Gap 4 — Engine session-id capture is per-family Go.**
[ADR-014](../decisions/014-claude-code-resume-cursor.md) ships the
splice for claude-code; OQ-1/OQ-2 cover codex/gemini. If we're adding
5+ engines, this should become a frame-profile rule kind:
`emit: { capture_session_id: $.session_id }`. **Fix:** small — move
the capture from Go into the profile evaluator. Should land before the
third engine's session-id work, not after.

**Gap 5 — No first-class non-goal for messaging-gateway agents.**
Without an explicit non-goal in [blueprint §10](../spine/blueprint.md),
the question re-litigates each time someone notices OpenClaw / Hermes
in the wild. **Fix:** trivial — append a non-goal entry.

---

## 9. Recommendation

A two-track read.

**MVP-track:**
- Add **OpenClaude** as the first non-commercial engine after gemini-cli.
  Likely zero Go cost given ADR-010. Gives termipod a real
  "supports open-source agents" story without architectural change.
- Append the messaging-gateway non-goal to blueprint §10 (Gap 5).
- Promote engine session-id capture into the frame-profile rule
  vocabulary (Gap 4) before the third engine's resume work, not after.

**Post-MVP-track:**
- **M5 graphical** as ADR-015 (Gap 2), pulling Gap 1's binary-blob
  expression-DSL extension along with it.
- **Aider** integration as the forcing function for the text/regex
  expression-DSL extension (Gap 1, text half).
- **Hermes Agent** CLI integration as the forcing function for
  fork-time skill-file carry (Q1) and the engine-state-surface
  question (Q2). Defer until adoption pressure is real; the design
  cost is non-trivial.

Group B (OpenClaw shape) stays out by policy. Group C (computer-use)
stays out for MVP, in for post-MVP via ADR-015.

---

## 10. Open questions for follow-up ADRs

- **OQ-1.** Should `profile_version: 2` add regex matchers, image
  payloads, both, or split into two amendments? (Forced by Gap 1.)
- **OQ-2.** What is the spatial primitive for M5 — `desktop_sessions`
  parallel to panes, or a `surface` supertype with `pane` and `screen`
  as subtypes? (Forced by Gap 2.)
- **OQ-3.** Coarsened approval model for GUI agents — application-
  launch + screenshot-pause-for-approval, or something else? (Forced
  by Gap 2 / G3.)
- **OQ-4.** Engine state surface — does termipod model engine-side
  mutable state (Hermes skill files, fine-tune deltas, local cache) as
  a hub-visible primitive, or does it stay opaque inside the worktree?
  (Forced by Hermes Q1 + Q2.)
- **OQ-5.** Do we want a "BYO engine" contributor path — public
  documentation that walks a third party through adding an engine kind
  via overlay-only changes (no fork required)? (Forced by adoption-
  pressure, not by code.)

---

## 11. References

- [ADR-010 — Frame profiles as data](../decisions/010-frame-profiles-as-data.md)
- [ADR-014 — Claude Code resume cursor](../decisions/014-claude-code-resume-cursor.md)
- [Blueprint §3 (ontology), §5.3 (driving modes), §10 (non-goals)](../spine/blueprint.md)
- [Discussion — fork and engine context mutations](fork-and-engine-context-mutations.md)
- [Discussion — multi-engine frame parsing](multi-engine-frame-parsing.md)
- [Discussion — transcript source of truth](transcript-source-of-truth.md)
- External (snapshot 2026-04-30):
  - OpenClaude — github.com/Gitlawb/openclaude
  - OpenClaw — github.com/openclaw/openclaw
  - Hermes Agent — hermes-agent.nousresearch.com
  - Cua — github.com/trycua/cua
  - OpenCUA — opencua.xlang.ai
  - Awesome Computer Use — github.com/ranpox/awesome-computer-use
