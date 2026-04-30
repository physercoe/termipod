# Transcript / approvals / quick-actions UX — competitive scan

> **Type:** discussion
> **Status:** Resolved (informed `../plans/steward-ux-fixes.md` Issue 4 W-UI-1..4 + the v1.0.299 polish slice)
> **Audience:** contributors
> **Last verified vs code:** v1.0.312

**TL;DR.** Competitive scan of Happy / claudecode-remote / Cursor
mobile etc. (2026-04-26) for transcript / approvals / quick-actions
UX patterns. Findings fed the W-UI sub-wedges in
`../plans/steward-ux-fixes.md` Issue 4, which shipped across
v1.0.281–v1.0.300. Kept as the audit trail.

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
- **Session list with history** is a primitive, not a screen feature. This aligns with the `sessions.md` ontology — sessions as first-class navigable entities.

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
- **Session list as navigation primitive.** Happy + CloudCLI both. We're partway there with the per-project agent list but it's not "sessions" per `sessions.md`.
- **Push notifications when agent waits for input.** Missing entirely; we have notifications but not gated on "decision needed".

---

## 4. Part 1 wedge specification (proposed)

This is the wedge we paused on. Now informed by the scan above, three
sub-wedges, in order of leverage:

### W1.A — Inline tool-call approval (tier-aware)
**The single most important catch-up item.** Without this, we're
strictly behind Happy on the single-engine slice.

> **Important:** unlike Happy, we should NOT prompt on every tool
> call. The decision-tiers framework in `sessions.md` §6.5
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
  sessions.md ontology shift. Not this wedge.
- **Splitting steward UI from the hub-meta channel** — also a
  sessions.md item (§8.5). The current Me "Direct" FAB
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

## 7. Screen-walk findings (2026-04-26)

Did the walk against the App Store screenshots for Happy and the
README screenshots in `siteboon/claudecodeui` (CloudCLI / "CCUI").
Image sources at the bottom of this section.

| Dimension | Happy | CloudCLI / CCUI |
|---|---|---|
| **Tool-call default expansion** | Collapsed to a single-line **file card** with file icon + path. Tap to expand inline; the diff renders right in the transcript with red/green-tinted lines, no line numbers. | Collapsed to a `Using Read > View input parameters > Tool Result` disclosure breadcrumb. Tap to expand, but renders the parameter blob, not a diff-style change view. |
| **Diff rendering** | Inline minimal diff inside the file card (red removed line, green added line, light context). Second-tap opens a **full-screen code viewer** with proper line numbers + monospace styling. | Code is shown as a syntax-highlighted block inside the tool-call card. Less diff-aware; closer to "here is the file content" than "here is what changed". |
| **Approval card** | Per textual description: contextual Allow/Deny prompts intercept tool calls + file edits at runtime, "exact operation details + waits for explicit user approval". I didn't see one inline in the four screenshots — likely a popover that fires on demand. | **Pre-approval via Tools Settings modal**: per-tool allowlist (`Bash(git log:*)`, `Write`, `Read`, `Edit`, `Glob`, …) with quick-add chips for common patterns. Plus a global "Skip permission prompts" checkbox (=`--dangerously-skip-permissions`). Runtime prompts only for things outside the allowlist. **Two different philosophies for the same problem.** |
| **Verbose / debug toggle** | Not visible in any screenshot. Likely either absent or deep in settings; user-facing transcript is already collapsed-by-default. | Not visible. Tools Settings modal serves a different need (permissions); no clear "show raw events" toggle. |
| **Quick-action chips** | Input row has a left-side cog (settings), a `+` (likely the menu trigger for @/slash commands), and a prominent voice button. No always-on chip strip; affordance is a single discoverable button. | No chip strip. Placeholder text teaches the syntax: `Type / for commands, @ for files, or ask Claude anything…`. Plus a "Default Mode" pill and a discreet token-% indicator. |
| **Multi-engine UI** | Not surfaced in the screenshots; per the README, support for Claude Code + Codex is by which CLI you've installed locally. No in-app engine picker. | **Explicit engine picker at New Session**: 4 cards — Claude Code / Cursor / Codex / Gemini — with a model dropdown ("Opus") underneath. Engine is a per-session choice; first-class. |
| **Session list navigation** | **Home screen IS the session list**, in two sections: **Active Sessions** (online, with the host-runner connected) and **Previous Sessions** (`last seen 1 day ago`). Each row: project-art avatar, auto-derived title, project path, status. Titles read like working topics ("Voice Assistant Receives Richer Agent Messa…"), not user-named. | **Left sidebar on desktop, bottom-nav on mobile.** Sidebar lists sessions per project, ordered by recency, with turn count + relative timestamp. **`+ New Session` is a prominent button**, not buried. Mobile uses bottom tabs Chat / Shell / Files / Git / Tasks / More. |
| **Voice input affordance** | **First-class, in-input.** Big mic button next to the text field, with a feature card "Realtime Voice with multiple sessions". This is a positioning bet for them. | Not present in the screenshots. |
| **Push notification triggers** | Per docs: approval requests + task completion. | Per docs: similar. No screenshots show the notification UI itself. |
| **Session metadata strip** *(new dimension)* | Below the message list / above input: `master · 14 files · +18 -18 · 54% left`. Branch + diff stats + token budget — the running state of the agent's work, glanceable. | "Default Mode" pill + a token-% indicator next to the input. Lighter than Happy's strip; less work-state, more session-config. |
| **Project / scope display** *(new dimension)* | Each session header shows project path (`~/Develop/slopus/happy`); session list shows path under each title. Scope is always visible. | Sidebar tree groups sessions by project; less explicit per-session, but the sidebar acts as the scope display. |
| **Approval philosophy summary** *(synthesized)* | Reactive: ask at runtime, with "exact operation details". One Allow/Deny per call. **Maps to "ask everything outside Trivial".** | Proactive: define an allowlist ahead of time, ask only on misses. Patterns like `Bash(git log:*)` are essentially the user's own tier definition. **Maps closer to our four-tier model — they let the user define the line.** |

### Verdict per W1.A/B/C dimension

| Wedge | What to match | What to differ on / improve |
|---|---|---|
| **W1.A approval** | Inline Allow/Deny card with operation details (Happy). Match the placement (between turns, where the pending tool_call sits). | Drive by **tier** (`sessions.md` §6.5), not by every-tool. Add a Tools-Settings-style allowlist UI (CCUI's idea) so users can promote a Routine pattern to "auto-allow" — that's what Routine pre-approval looks like in practice. **Combination of the two philosophies.** Also: approvals are **richer than Y/N** — see §7.5. |
| **W1.B transcript** | Default-collapsed tool calls as one-line file cards (Happy) with disclosure for inline diff. Token budget below input (Happy). | The Happy-style **branch/diff strip** below input belongs on **worker UI** (where code work happens), not steward UI (where decisions happen). Steward sessions usually don't have a worktree of their own. See §7.4. |
| **W1.C compose box** | `Type / for commands, @ for files` placeholder hint (CCUI) teaches syntax. | **Reuse the existing TmuxBackend compose box.** It already has history + snippets + quick-actionbar + key-palette infrastructure; voice is just one input source within it. We don't need to invent a separate "voice button" pattern à la Happy — our compose box already accommodates it. See §7.6. |

### Cross-cutting observations

1. **Sessions are first-class in both apps already.** Happy's home screen is *only* sessions; CCUI's sidebar is sessions. Our current "one persistent steward chat" already feels behind. The `sessions.md` ontology shift moves us toward where they already are.

2. **Engine picker is the multi-engine differentiator visible to users.** CCUI does it as a New-Session step. We have agent-families plumbing; surfacing it as "pick engine + model when you start a session" is the obvious mobile pattern.

3. **Pre-approval allowlist is the missing piece** between "ask everything" (Happy) and our tier model. Adding a `Tools Settings → Auto-allow patterns` surface lets users define their own Routine tier per project. This is the right blend.

4. **Steward UI ≠ worker UI.** Happy and CCUI conflate them because they're single-engine clients with one chat surface. Our positioning has them split (the agent harness has roles); our UI should follow. The Happy-style **branch/diff/file-count strip** is *worker* metadata — it makes sense when you're watching a worker write code in a worktree. A steward session, in our ontology (`sessions.md` §2), does coordination, planning, decisions — there is usually no worktree, no diff, no branch. Putting that strip on the steward chat would be a category error. **Worker UI gets the strip; steward UI gets a session-context strip instead** (loaded artifacts, scope label, decisions made so far). This is one of the clearest places where our role-aware harness can render differently than a single-engine client.

5. **Voice is real but doesn't need a separate pattern from us.** Happy makes it a first-class button because their compose box is minimal. Our TmuxBackend compose box already has history + snippets + quick-actionbar + key-palette + compose-mode toggle — voice is just another input source plugged into that existing compose-box pattern. See §7.6 for the unified compose-box plan.

## 7.4 Steward UI vs worker UI (split, not shared)

The screen-walk made this concrete. Two surfaces, three differences:

| | Steward chat | Worker chat |
|---|---|---|
| **Conversation content** | Questions, decisions, planning, approvals, briefings | Code edits, tool calls, file changes, test runs, branch operations |
| **Worktree presence** | Usually none — coordination happens against artifact graph, not a tree | One per spawn (per `agent-lifecycle.md` worktree spec) |
| **State strip** | Loaded artifacts ("plan + 3 briefings + 8 decisions") + scope ("project X / decision review") + token budget | Branch + file count + +N/-M + token budget (Happy-style) |
| **Tool-call rendering** | Rare; mostly governance tools (audit, decision, attention, template propose) — render as decision cards, not file cards | Frequent; mostly code tools (read/write/edit/run) — render as Happy-style file cards with inline diff |
| **Approval card content** | Decisions ("approve this template change?", "ratify this policy?") with policy/scope context | Code-related ("approve this commit?", "approve this push?") with diff + test results |
| **Distillation outcome** | Decision / Brief / Plan-update artifact (per `sessions.md` §6) | Code-change artifact (per the deferred `code-as-artifact.md`) + worker's task-summary brief |

These are clearly two surfaces, not one. They share the **compose box**
(see §7.6) and the **transcript styling primitives** (collapsed-card,
expand-on-tap, debug toggle), but the *payloads* differ.

**Implication for W1.B**: ship the transcript styling pass for steward
chat first (it's the focus of this workband). Worker UI styling is a
sibling wedge that ships after the steward sessions ontology lands —
or in parallel, since they share primitives.

## 7.5 Approval is richer than Allow/Deny

The Happy reference shows a binary card. Real approvals span several
shapes; the card system should be a small framework, not a fixed
widget. At least four classes:

### 7.5.1 Binary (yes/no)
The default for Significant tier:
- **Approve** (default action, Enter-confirms unless Strategic)
- **Deny**
- **Approve with note** (free-text reason, audit-recorded)
- **Deny with feedback** (note returned to the agent so it can adapt
  rather than die — addresses §6.5.6 open question 1)

### 7.5.2 Always / once toggle
Promotes a Routine pattern to auto-allow (CCUI's idea, applied to
our tier model):
- **Approve once** (default for Significant)
- **Approve always for this tool** (write a Routine rule)
- **Approve always for this *pattern*** (write `Bash(git log:*)`-style rule)
- Lives in the same card as Binary; expand reveals.

### 7.5.3 Multi-choice (agent presents options)
For "which way should I go?" decisions:
- Card shows N options with rich description per option.
- Pick one (radio), optional note.
- This needs a new tool shape: `mcp__termipod__decision_request`
  with `options: [{id, label, description, side_effects}]` —
  parallel to `permission_prompt` but for branching decisions.

### 7.5.4 Modify-and-approve (edit before yes)
For "do X with these parameters" where the user wants to tweak X:
- Card shows the proposed call with editable parameter fields.
- Approve sends the modified version back to the agent.
- Riskier — the agent must accept that the executed call may differ
  from the requested call. Probably gated to certain tool classes.

### 7.5.5 Lifecycle outcomes (the rest of the menu)
A complete card menu also has:
- **Defer** ("ask me later", with optional reminder timestamp)
- **Delegate** (route to another team member or a different role)
- **Cancel task** (stop the whole spawn / session, not just this call)

### 7.5.6 Scope summary
Each card needs three things visible regardless of class:
- **Tier** (Significant / Strategic) — visual weight matches.
- **Operation summary** (Happy: "exact operation details").
- **Audit trail** (who proposed, when, in what session).

So W1.A should ship as a small framework — a shared card chrome plus
class-specific bodies — not a single Allow/Deny widget that we'd
later need to expand. Build the framework once; bodies plug in.

## 7.6 The compose box, unified

The TmuxBackend already has the right compose box (`terminal_screen.dart`
+ `action_bar_provider.dart`). It carries:

- Command **history** (recent inputs, scrollable).
- **Snippet** ActionChips (the bolt-icon presets, per-profile).
- **Quick action bar** (custom buttons configured per panel).
- **Key palette** / custom keyboard (special keys, modifier toggles).
- **Compose mode** toggle (multiline draft).
- **Send** button.

That's already a richer compose box than Happy's mic+text+plus-button
or CCUI's text+placeholder-hint. We don't need a new pattern — we
need to **reuse the same compose box across three contexts**:

1. **Tmux raw shell** (today's only consumer).
2. **Steward chat** (W1.C target — chat input is the same compose box,
   with steward-relevant snippets and quick actions).
3. **Worker chat** (later — worker-relevant snippets, e.g. `commit`,
   `run tests`, `revert`).

Voice input plugs in as a new input *source* within the compose box,
not as a separate button outside it. Tap mic → speech-to-text feeds
the compose box like keyboard typing would. This is the right
abstraction: the compose box is a *what to send* surface, and it
shouldn't care whether bytes came from keyboard, voice, snippet, or
quick-action.

**Implication for W1.C**: don't build a "chat input field" widget for
the steward. Lift the existing TmuxBackend compose box into a shared
widget (`lib/widgets/compose_box/`) and instantiate it in the steward
chat with its own snippet/action profile. Voice and @-file mention
land later as plugged-in input sources, no UI churn.

### Image sources

Happy (App Store screenshots, https://apps.apple.com/us/app/happy-claude-code-client/id6748571505):
- ios_screenshot_1: code-everything (transcript with file cards + inline diff)
- ios_screenshot_2: realtime voice (input row + voice button + branch/diff strip + keyboard)
- ios_screenshot_3: review changes on the go (full-screen diff viewer)
- ios_screenshot_4: fully encrypted (session list home: Active + Previous)

CloudCLI (siteboon/claudecodeui README, /public/screenshots/):
- desktop-main.png (sidebar + chat + tool-call disclosures)
- mobile-chat.png (per-session mobile view + bottom nav)
- cli-selection.png (engine picker at New Session)
- tools-modal.png (per-tool allowlist with quick-add chips)

---

## 8. Where this fits on the roadmap

**North star (2026-04-26):** the steward workband is the focus. The
goal is a TermiPod that a Happy / CloudCLI user can switch to without
losing daily fluency on the (1 host × 1 session × 1 engine) slice,
while gaining multi-host fleet, team governance, and tier-aware
decisions on top.

Everything outside the steward workband is deferred until that pivot
lands. Specifically: code-as-artifact (`docs/code-as-artifact.md`)
is parked until we can do a real-app screen-walk against
GitHub Mobile / Cursor mobile / Codex superapp diff surfaces.

### Steward workband, in order

| # | Wedge | Type | Blocks on |
|---|---|---|---|
| 1 | Screen-walk Happy + CloudCLI (this memo §7) | Research | nothing |
| 2 | Lock W1.A/B/C spec from screen-walk findings | Design | (1) |
| 3 | **W1.A** — Inline tool-call approval, **tier-aware** (`sessions.md` §6.5) | Build | (2) + tier metadata on tool definitions |
| 4 | **W1.B** — Transcript styling pass | Build | (2) |
| 5 | **W1.C** — Quick-action chips | Build | (2); reuse snippet provider from terminal_screen |
| 6 | Sessions ontology — schema + open/close + distillation | Architectural | learnings from (3)–(5); §11 of `sessions.md` |
| 7 | **Steward UI ↔ hub-meta split** (`sessions.md` §8.5) — replace Me "Direct" FAB with "Start session" | Build | (6) |
| 8 | Decommission persistent-chat metaphor | Cleanup | (7) |

Roughly: (1)–(5) is one-to-two weeks of UX work. (6)–(8) is the
multi-week architectural shift that turns the app from "another
chat client" into the directorial harness this codebase was always
meant to be.

### Out of band (explicitly deferred until after the pivot)

- Code-as-artifact wedge (`docs/code-as-artifact.md`) — needs a
  diff-UX reference scan first.
- @file mentions / fuzzy file picker (W1.D) — useful but not
  blocking for the Happy-replacement positioning.
- Voice input (W1.E) — backlog; needs platform plumbing.
- Per-member stewards (deputy model, ia-redesign §11 F-1) — second
  user.
- Cross-team session sharing — not needed at MVP scale.

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
