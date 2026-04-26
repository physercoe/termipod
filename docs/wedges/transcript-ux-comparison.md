# Wedge memo: Transcript / approvals / quick-actions UX

> **Status: DRAFT**, derived from a competitive scan run 2026-04-26.
> Part of the Part-1 follow-up after `steward-sessions.md` deferred
> implementation. Do not code from this yet — it's the input to a
> design discussion, not the spec itself.

## Why this memo exists

When we discussed the steward UX gaps (transcript reads like a debug
log; "ask the agent to pop up a decision" doesn't work; no quick
actions), we paused before designing because:

1. The reference apps in this space have moved fast in 2026.
2. TermiPod's positioning (multi-host, multi-engine, team-scale) means
   we should *match* what the leading single-engine clients do for the
   (1 host × 1 session × 1 engine) subset — not invent.
3. We needed to know which patterns are "table stakes" before we
   commit to building.

This memo captures the landscape, what's table stakes, where we're
genuinely ahead, and the open design questions for the wedge itself.

---

## 1. Competitive landscape (April 2026)

| App | Engines | Hosts | Auth model | What stands out |
|---|---|---|---|---|
| **Anthropic Remote Control** (official, Feb 2026, Research Preview) | Claude Code only | Single, paired via QR | Built-in, encrypted bridge | Push notifications when "Claude decides"; verbose-transcript toggle (Ctrl+O); read-only mirror by default with control |
| **Happy Coder** (slopus/happy, open source) | Claude Code + Codex | Single, encrypted relay | E2EE, free | **Real-time inline permission prompts (Allow/Deny per tool call / file edit)**; voice; bidirectional sync; @file mentions; slash commands |
| **CloudCLI / claudecodeui** (siteboon, open source) | Claude Code + Cursor CLI + Codex + Gemini CLI | Single | Web + mobile responsive | Multi-engine pioneer; integrated shell; file explorer; Git UI; session list with history |
| **Claude Remote** (3rd-party) | Claude Code | Single | Cloud relay | Chat-style UI; minimal |
| **GitHub Mobile (Agents view)** | Claude + Codex | Cloud-hosted only | GitHub auth | Real-time agent progress; session list; cloud-native, no local SSH |
| **Codex superapp** (April 2026) | OpenAI Codex | Cloud SSH devboxes | OpenAI auth | Parallel agents (5 at once), in-app browser, image gen |
| **TermiPod** | Claude + extensible | **Multi**, fleet model | SSH + hub token | Team/governance (audit, decisions, attention items, policies); host-runner protocol |

Sources are listed at the end of this memo.

## 2. What we learn from each

### Anthropic Remote Control
- **Verbose toggle is acknowledged as needed.** Even Anthropic's first-party client added Ctrl+O to switch between clean and debug views. This validates the Part 1 instinct: default to clean, gate raw events behind a toggle.
- **Push when the agent needs a decision** is a first-class feature, not an afterthought. They built `pushNotification` as an MCP tool the model calls explicitly.
- **Read-only mirror covers ~80%** per the public commentary; the rest is approval + redirect. This biases the mobile chat surface toward observation + decision, not authoring.

### Happy Coder
- **Inline approval is the headline feature.** Quote: "intercepts MCP tool calls and file edit operations… presents Allow/Deny prompts… waits for explicit user approval before proceeding." Single most important UX pattern to match.
- **@file and slash command parity** with desktop Claude Code. Mobile users expect to type `@some/path/file.ts` and get fuzzy completion. We have snippets; we don't have file-aware mentions yet.
- **Voice as a first-class input modality**, not a setting. "Tap mic, walk around, brain-dump, tap stop." Worth a backlog item; not blocking.
- **Fully synced bidirectional**: terminal and phone see the same session in real time. We have most of this; biggest gap is "approve actions" which is Happy's headline.

### CloudCLI (claudecodeui)
- **Multi-engine done right looks like:** one app, four agents (Claude Code, Cursor CLI, Codex, Gemini CLI), shared session/projects/git surface. They engineer-the-CLI-into-a-pluggable-protocol; we *also* aim for vendor-agnostic, so this is direct comparison territory.
- **Integrated shell + file explorer + Git UI** as siblings to the chat. We have terminal_screen, but it's not co-resident with the steward chat. Worth thinking about whether a unified "session workspace" is the right pattern.
- **Session list with history** is a primitive, not a screen feature. This aligns with the `steward-sessions.md` ontology — sessions as first-class navigable entities.

### Codex superapp
- **Parallel agents (5 simultaneous in cloud sandboxes).** Their model: many agents, one user, cloud-managed. Ours: many agents, many hosts, user-controlled compute. Their "many parallel" is a distinct lane.
- **In-app browser + image gen + agent memory** — feature-creep direction we should *not* chase; we'd lose the harness focus. Note as bounded.

---

## 3. Where TermiPod is ahead, at parity, and behind

### Genuinely ahead (today)
- **Multi-host fleet model.** None of the peer apps think in terms of "many machines, each with its own runner". Even Happy/CloudCLI assume one paired desktop. This is our biggest differentiator.
- **Team-scale governance** (audit, attention items, decision tools, role-bound capabilities, policy templates). No peer app has a steward/governance concept; they're all 1-user-N-projects. This is differentiator #2.
- **A2A relay for NAT'd hosts.** Worker-on-GPU + steward-on-VPS via the hub — peer apps don't reach this layer because they don't conceive of "another agent" as an actor.

### At parity (today)
- **SSH-attached host model.** Comparable to CloudCLI's installable backend. Slightly more coherent because we have the hub layer.
- **Snippets / quick commands.** Comparable to slash commands but less integrated.
- **Multi-engine plumbing.** Agent-families catalog exists. Whether a *user* experiences this as multi-engine depends on UI surfacing — currently weak.

### Behind (today)
- **Inline tool-call approval.** Happy's headline feature. Server side we have permission_prompt MCP tool + attention items; client side the loop isn't closed.
- **Transcript styling.** Reads like a debug stream. Anthropic, Happy, CloudCLI all collapse and style.
- **@file mentions.** Standard in Happy and CloudCLI. Missing here.
- **Voice input.** Standard in Happy. Missing here.
- **Session list as navigation primitive.** Happy + CloudCLI both. We're partway there with the per-project agent list but it's not "sessions" per `steward-sessions.md`.
- **Push notifications when agent waits for input.** Missing entirely; we have notifications but not gated on "decision needed".

---

## 4. Part 1 wedge specification (proposed)

This is the wedge we paused on. Now informed by the scan above, three
sub-wedges, in order of leverage:

### W1.A — Inline tool-call approval (tier-aware)
**The single most important catch-up item.** Without this, we're
strictly behind Happy on the single-engine slice.

> **Important:** unlike Happy, we should NOT prompt on every tool
> call. The decision-tiers framework in `steward-sessions.md` §6.5
> says only Significant + Strategic tier calls reach the user;
> Trivial + Routine pre-approve via the session's capability scope.
> Match Happy on the *card design* (inline placement, expandable
> details, allow/deny/note); diverge on *what triggers it* (tier
> filter, not all calls). This is a positioning win — we look more
> directorial, less assistantial.

What it does: when the steward (or any agent) calls
`mcp__termipod__permission_prompt` for a Significant or Strategic
tier action, the request lands as an attention item with
`scope_kind=agent, scope_id=<id>`. Mobile must:

1. Subscribe to attention items keyed to the active agent's id.
2. Render an inline approval card in the transcript at the position
   where the pending `tool_call` sits.
3. Show the tier (Significant / Strategic) on the card.
4. Buttons: **Approve** / **Deny** / **Approve with note**.
5. Strategic tier additionally requires a typed reason and is
   non-default-yes (must explicitly tap; no Enter-to-confirm).
6. On tap, send the decision via the existing `Inputter.approval`
   path. Card collapses to "✓ approved (tier)" / "✗ denied" with
   the decision audited.
7. Push notification when the prompt arrives if the app is backgrounded.

Server side: already exists for the prompt routing. Tier metadata
needs to be attached at MCP-tool-definition time (separate small
infra wedge — could ship as a default-Routine-everywhere bootstrap,
then explicitly bump tier-bumps later).

Client side: ~2 days for the card + tier filter; another ~1 day
for the Strategic-tier reason field + biometric gate.

Leverage: huge.

### W1.B — Transcript styling pass
What today's debug stream becomes:

| Event kind | Today | Proposed default | Verbose toggle |
|---|---|---|---|
| `lifecycle.started/stopped` | row | "● running" pill at top | row |
| `system` (init) | row | folded into a "Loaded N tools, M MCP servers" pill | full |
| `text` (agent) | row | chat bubble | unchanged |
| `tool_call` | row with full payload | collapsed card "🔧 Read(path)" — tap to expand | full |
| `tool_result` | row | inline inside the tool_call card | full |
| `usage` | row | quiet token-count strip per turn | full |
| `completion` (deprecated) | row | suppress | full |
| `input.text` (echo) | row | suppress (already shown as user msg) | full |
| `raw` (thinking, unknown) | row | hide | full |
| `error` | row | red banner | full |

A "Show debug" toggle (matching Anthropic's Ctrl+O) reveals the raw
event stream. Default is clean.

Open questions:
- Does the toggle persist per-session or globally?
- Do we keep `completion` for one more release for back-compat (per
  the comment in driver_stdio.go) or kill it now?

### W1.C — Quick actions strip
Above the input field, a row of action chips. Two layers:

1. **Built-in slash-style chips**: `/cancel`, `/status`, `/help`, plus
   "summarize", "what's blocking?", "show recent decisions".
2. **Snippet chips**: pull from existing snippet provider (the
   bolt-icon presets); user-customizable via the snippets sheet.

Future expansions (not in this wedge):
- @file mentions (W1.D).
- Voice input (W1.E, separate wedge).

Open questions:
- Do the built-in chips live in the user's snippets list (so they're
  editable) or are they hardcoded "system snippets"?
- Should the snippet bolt UI from terminal_screen.dart be shared
  with the steward chat? Probably yes — same widget, same store.

---

## 5. What we *don't* do in this wedge

Listing explicitly so the wedge doesn't grow:

- **@file mentions / fuzzy file picker** — separate wedge (W1.D).
  Requires a project file index; bigger than it looks.
- **Voice input** — separate wedge (W1.E). Needs platform plumbing
  (Android speech, iOS Speech) + privacy review.
- **Session list as the steward navigation surface** — that's the
  steward-sessions.md ontology shift. Not this wedge.
- **Splitting steward UI from the hub-meta channel** — also a
  steward-sessions.md item (§8.5). The current Me "Direct" FAB
  conflates director↔steward 1:1 with the team channel; fixing
  that is a sessions-wedge concern, not a transcript-styling one.
- **Code-change review surface** — separate wedge, see
  `docs/code-as-artifact.md`. Once W1.A inline approval ships,
  CodeChange artifacts plug in as a richer payload inside the same
  card pattern. Don't try to land both in one wedge.
- **Approval flow for *batches* of tool calls** — Happy does
  per-call. We do per-call too for now. Batching for code-change
  approvals is in scope for the code-as-artifact wedge, not here.
- **Push notifications gated on "agent waits for input"** — we'd
  need the agent to emit a clear "I'm blocked on you" signal before
  we wire push. Belongs in the same wedge as inline approval (W1.A)
  if cheap, otherwise its own item.

---

## 6. Open questions before we commit to this spec

1. **Is per-call approval the right granularity?** Happy chose per-
   call. With 50+ tool calls per long task, a phone user may not
   want to tap 50 times. Some agents (Claude Code) batch reads
   already. Tentative: per-call MVP, group later if user feedback
   demands.

2. **What about hosts where the user has already pre-approved a
   capability scope (e.g. `--dangerously-skip-permissions`)?**
   Approval card never fires for those tool calls. Today's path is
   fine; but we should signal "this agent is in skip-permissions
   mode" prominently so the user isn't surprised by autonomous
   actions.

3. **Multi-engine implications for transcript styling.** Codex
   stream-json shape differs from Claude's. The agent_event kinds
   we emit are normalized (driver_stdio's translate()), so the
   UI side mostly doesn't care — but tool_call rendering will
   still need engine-aware affordances if Codex's tool semantics
   differ. Verify before W1.B lands.

4. **Should approve/deny show the *file diff* for edit operations?**
   Happy does. We don't have a diff renderer in the chat surface
   today. Probably worth doing (small win for trust) but it's a
   stretch goal for W1.A.

5. **What does "show debug" gate mean for performance?** When
   collapsed, do we still render hidden events to the layout tree?
   Probably no — should be lazy. Verify in the spec.

---

## 7. Suggested screen-walk before locking the spec

Two apps, ~30 min each, one note per dimension:

| Dimension | Watch-for in Happy | Watch-for in CloudCLI |
|---|---|---|
| Approval card position | inline vs. modal vs. tray | same |
| Approval card content | which fields shown by default; what's expandable | same |
| Tool-call default expansion | collapsed vs. expanded | same |
| Verbose / debug toggle location | per-session toggle, app-level setting? | same |
| Quick-action chips | what built-ins; how snippets surface | same |
| Multi-engine UI | how engine choice is presented; per-session? per-message? | direct comparison — they have 4 |
| Session list navigation | how scoped; where it lives | same |
| Voice input affordance | mic location; modal during recording | same |
| Push notification triggers | what events generate push | same |

Output of the walk: 1-page screenshot diff against the spec proposed
in §4, with a "match / differ / new pattern" verdict per dimension.
That's the input to a final spec doc that an implementing wedge can
build from.

---

## 8. Where this fits on the roadmap

This wedge slots **before** the steward-sessions.md ontology work in
implementation order, because:

- The Part 1 wedge is non-architectural (UI work on existing event
  streams) and unblocks user value within ~1 sprint.
- The sessions ontology is a multi-week architectural shift that
  benefits from the Part 1 wedge's UX learnings (we'll know what
  approval / quick-action patterns work before we decide how
  sessions package them).

So order is:
1. Walk the apps (this memo, §7).
2. Lock the W1.A/B/C spec.
3. Ship W1.A (inline approval) — biggest leverage.
4. Ship W1.B (transcript styling) — biggest visible improvement.
5. Ship W1.C (quick actions) — fastest follow-up.
6. *Then* sit with the steward-sessions doc and decide whether the
   ontology shift still feels right after using the improved UX.

---

## Sources (2026-04-26 scan)

Apps:
- [Happy — Claude Code Mobile Client](https://happy.engineering/)
- [Happy features](https://happy.engineering/docs/features/)
- [Happy real-time sync](https://happy.engineering/docs/features/real-time-sync/)
- [slopus/happy on GitHub](https://github.com/slopus/happy)
- [siteboon/claudecodeui on GitHub](https://github.com/siteboon/claudecodeui)
- [Anthropic Remote Control docs](https://code.claude.com/docs/en/remote-control)
- [Claude Remote (3rd-party)](https://clauderemotecontrol.com/)

Comparison roundups:
- [Best Mobile Apps for Claude Code 2026 — Nimbalyst](https://nimbalyst.com/blog/best-mobile-apps-for-claude-code-2026/)
- [Claude Code Mobile: iPhone, Android & SSH (2026) — Sealos](https://sealos.io/blog/claude-code-on-phone/)
- [Claude Code Q1 2026 Update Roundup — MindStudio](https://www.mindstudio.ai/blog/claude-code-q1-2026-update-roundup)
- [Anthropic Remote Control launch — VentureBeat](https://venturebeat.com/orchestration/anthropic-just-released-a-mobile-version-of-claude-code-called-remote)
- [Claude Code Remote Control: Run Your Terminal from Your Phone — NxCode](https://www.nxcode.io/resources/news/claude-code-remote-control-mobile-terminal-handoff-guide-2026)

Feature requests / context:
- [Voice input for Remote Control — anthropics/claude-code#29399](https://github.com/anthropics/claude-code/issues/29399)
- [Resuming sessions across devices — anthropics/claude-code#47926](https://github.com/anthropics/claude-code/issues/47926)

— draft 1, 2026-04-26
