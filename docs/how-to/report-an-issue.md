# Report an issue

> **Type:** how-to
> **Status:** Current (2026-04-30)
> **Audience:** testers, end-users
> **Last verified vs code:** v1.0.349

**TL;DR.** A guide for testers and normal users who want to report a
bug or surprise behaviour accurately. The single most useful thing a
report can do is name the right UI elements, screens, and actions —
"the BottomSheet for the agent didn't show the Resume button" is a
1-minute fix; "the thing didn't work" is a 30-minute back-and-forth.
This doc walks through every major screen with labelled diagrams, the
vocabulary the project uses for each UI piece, and a bug-report
template that asks for the right pieces in the right order.

If you're a developer hunting a term collision (*hub session* vs
*engine session*, *fork* vs *resume*), you want
[`reference/glossary.md`](../reference/glossary.md) instead — that's
the engineering glossary, this one is the UI glossary.

---

## 1. The bug-report template

Copy this template into your report. Fill every section.

```
**Screen:** <name from §3 below — e.g. "Project Detail" or "Session
Transcript">
**Steps:**
1. <what you tapped / typed>
2. <next thing>
3. ...
**Expected:** <what should have happened>
**Actually:** <what did happen>
**Device:** <Pixel 8 / iPhone 15 / etc.>
**OS version:** <Android 14 / iOS 17 / etc.>
**App version:** <see §6 — Settings → About>
**Server / engine:** <claude / codex / gemini, if relevant>
**Screenshot:** <attach if possible>
**Time:** <local time + timezone — narrows server-side log search>
```

Two minutes to fill in. Saves an hour of guessing. The "Screen" and
"Steps" lines do most of the work.

### A worked example

**Bad report:**
> "I tapped resume but nothing happened. The agent is still dead."

**Good report:**
> **Screen:** Sessions screen → Session Detail (transcript view), session "Demo run 2"
> **Steps:**
> 1. Opened the session from the Sessions list (it had the orange Paused chip)
> 2. Tapped the Resume button in the BottomSheet
> 3. The BottomSheet closed; transcript scrolled to bottom
> **Expected:** A new agent message after a few seconds, like the prior turns
> **Actually:** Nothing for 30s. The session chip flipped from Paused to Active, but no new turns appeared. The new agent's first message arrived 90s later instead of immediately, and didn't reference any prior conversation.
> **Device:** Pixel 8
> **App version:** 1.0.348-alpha+10348
> **Engine:** claude-code via host-runner on `vps-01`
> **Time:** 2026-04-30 14:03 JST

The good report names the screen (Session Detail), the affordance
(Resume button in BottomSheet), the chip colour (orange), and the
quantitative expectation (90s vs immediate). Each of those narrows the
investigation to a code path.

---

## 2. App layout — the big picture

Termipod's mobile app has one persistent **BottomNav** at the
bottom of every top-level screen. Four tabs:

```
+----------------------------------------+
|              [AppBar]                  |  <- top bar (title + actions)
|                                        |
|                                        |
|                                        |
|       [content for active tab]         |
|                                        |
|                                        |
|                                        |
|                                        |
+----------------------------------------+
| [Projects]  [Activity]  [Hosts]  [Me]  |  <- BottomNav
+----------------------------------------+
```

Tap a BottomNav tab to switch between Projects, Activity, Hosts, Me.
Tap an item inside a tab to drill into its detail screen, which adds
a back arrow to the AppBar.

Most screens are one of these shapes:

```
SCREEN                  DETAIL                  SHEET
+--------------+        +--------------+        +--------------+
| AppBar       |        | < AppBar     |        |              |
|              |        |              |        |              |
| List of      |        | Header info  |        | (underlying  |
| Cards        |        |              |        |  screen      |
|              |        | Tab bar      |        |  dimmed)     |
|              |        | ┌──┬──┬──┐   |        |              |
|              |        | │  │  │  │   |        +==============+
| ─────────    |        | └──┴──┴──┘   |        | BottomSheet  |
| (BottomNav)  |        | Tab content  |        | actions      |
+--------------+        +--------------+        +--------------+
```

---

## 3. Every screen, labelled

### 3.1 Projects (BottomNav tab 1)

```
+------------------------------------------+
|  Projects                       [⋮]      |  <- AppBar (title + menu)
+------------------------------------------+
|                                          |
|  +------------------------------------+  |
|  |  ▣  My research repo               |  |  <- Card (project)
|  |     project · 3 active sessions    |  |
|  |     [active] [paused]              |  |  <- status Chips
|  +------------------------------------+  |
|                                          |
|  +------------------------------------+  |
|  |  ▣  Ops checklist                  |  |  <- Card
|  |     project · 1 active             |  |
|  |     [active]                       |  |
|  +------------------------------------+  |
|                                          |
|                              [+]         |  <- FAB (new project)
|------------------------------------------|
| [Projects]  [Activity]  [Hosts]  [Me]    |
+------------------------------------------+
```

Vocabulary:
- **AppBar** — the top bar showing the screen title and any actions.
- **Card** — the rectangular tappable block representing one project.
- **Chip** — the small pill labels (`active`, `paused`).
- **FAB** — the floating circular button, bottom-right, for "new".
- **BottomNav** — the four-tab strip at the very bottom.

### 3.2 Project detail

Tapping a project Card opens its detail:

```
+------------------------------------------+
|  <  My research repo            [⋮]      |  <- back arrow + menu
+------------------------------------------+
|  ▣ project · 3 active                    |  <- header strip
|                                          |
|  +-------+--------+----------+           |
|  | Tasks | Plans  | Sessions |           |  <- TabBar (within screen)
|  +-------+--------+----------+           |
|                                          |
|  ┌────────────────────────────────────┐  |
|  │ Tasks (5)                          │  |  <- tab content
|  │ ──────────────────────────────────│  |
|  │ [ ] gather data                    │  |  <- ListTile (each row)
|  │ [✓] preprocess                     │  |
|  │ [ ] train model                    │  |
|  └────────────────────────────────────┘  |
|                                          |
|------------------------------------------|
| [Projects]  [Activity]  [Hosts]  [Me]    |
+------------------------------------------+
```

Vocabulary:
- **TabBar** — the horizontal tab strip inside one screen
  (Tasks/Plans/Sessions). Different from BottomNav (which is the
  app-level tabs).
- **ListTile** — one row inside a list, here a task with checkbox.

### 3.3 Sessions list

Sessions are conversation transcripts with agents. Reachable from the
project's Sessions tab, or as a direct deep link.

```
+------------------------------------------+
|  Sessions                                |
+------------------------------------------+
|                                          |
|  ▶ Active                                |  <- section header
|  +------------------------------------+  |
|  |  ◯ Demo run 2                      |  |  <- session Card
|  |    [active] · steward · 2m ago     |  |  <- status chip + role + time
|  +------------------------------------+  |
|                                          |
|  ▼ Previous                              |  <- collapsible header
|  +------------------------------------+  |
|  |  ◯ Demo run 1                      |  |
|  |    [paused] · steward · 1d ago     |  |  <- orange chip
|  +------------------------------------+  |
|  +------------------------------------+  |
|  |  ◯ Initial setup                   |  |
|  |    [archived] · steward · 5d ago   |  |  <- grey chip
|  +------------------------------------+  |
|                                          |
+------------------------------------------+
```

Status chip colours:
- **Green / blue** = active (live, currently chatting)
- **Orange** = paused (the agent died; tap into the session to
  Resume)
- **Grey** = archived (read-only; can Fork to start a new
  conversation that references this one)

### 3.4 Session detail (transcript)

The chat view. This is where most of the user time happens.

```
+------------------------------------------+
|  <  Demo run 2                  [⋮]      |  <- AppBar (back, title, menu)
|     [active] · steward                   |  <- status strip under title
+------------------------------------------+
|                                          |
|  ┌──────────────────────────────────┐    |
|  │ [user] What's the test pass rate?│    |  <- user bubble (right-align)
|  └──────────────────────────────────┘    |
|                                          |
|     ┌──────────────────────────────────┐ |
|     │ [agent] Running pytest now...   │  |  <- agent bubble (left-align)
|     │  ▣ tool: pytest                 │  |  <- tool_call chip inline
|     │  ✓ tool result: 47/50 passed    │  |  <- tool_result chip
|     │  3 tests failing in test_auth.py │  |
|     └──────────────────────────────────┘ |
|                                          |
|  ┌──────────────────────────────────┐    |
|  │ [system] context compacted       │    |  <- operation marker (centered)
|  └──────────────────────────────────┘    |
|                                          |
|     ┌──────────────────────────────────┐ |
|     │ [agent] Let me look at the auth  │ |
|     │ tests in detail...               │ |
|     └──────────────────────────────────┘ |
|                                          |
+------------------------------------------+
|  [⚡] Snippets                            |  <- ActionChip strip
|  ┌────────────────────────────┐ [↗]     |
|  │ Type a message...          │  send    |  <- compose field + send button
|  └────────────────────────────┘          |
+------------------------------------------+
```

Vocabulary:
- **Compose field** — the text field at the bottom where you type.
- **Send button** — the arrow icon to the right of the compose
  field.
- **Snippets bolt** — the lightning-bolt icon that opens a list of
  canned inputs.
- **Action bar** — the entire strip across the bottom: snippets +
  compose + send. Different from AppBar (top).
- **User bubble / agent bubble** — chat messages, right- or
  left-aligned by speaker.
- **Tool call chip** / **tool result chip** — inline markers inside
  an agent bubble showing the agent's tool use.
- **Operation marker** — a centered, styled-differently chip
  showing a hub-side event (context.compacted, lifecycle.paused,
  etc.). These come from the hub, not the engine, and mark moments
  worth flagging in your report.

### 3.5 Session actions (BottomSheet)

Tapping the AppBar `[⋮]` menu on a session opens the actions
BottomSheet:

```
+------------------------------------------+
|  (transcript dimmed)                     |
|                                          |
+==========================================+
|     ▔▔▔                                  |  <- drag handle
|                                          |
|  Session: Demo run 2                     |
|                                          |
|  ▷  Resume                  (only when paused)
|  ▣  Fork                    (only when archived)
|  ⏸  Pause                   (only when active)
|  ⏹  Archive                                |
|  ✏  Rename                                 |
|  🗑  Delete                  (destructive)  |
|                                          |
+------------------------------------------+
```

Vocabulary:
- **BottomSheet** — the modal panel that slides up from the bottom.
  Doesn't cover the whole screen; the underlying screen is dimmed.
- **Drag handle** — the small horizontal bar at the top of a
  BottomSheet; you can drag it to dismiss.

### 3.6 Attention (Activity tab)

The Activity tab surfaces things the agents need you to decide.

```
+------------------------------------------+
|  Activity                                |
+------------------------------------------+
|                                          |
|  ▶ Needs you (3)                         |  <- attention items
|  +------------------------------------+  |
|  |  ⚠ Approve tool call               |  |  <- AttentionItem Card
|  |     steward · run_command          |  |
|  |     [Approve] [Deny]               |  |  <- inline action Buttons
|  +------------------------------------+  |
|  +------------------------------------+  |
|  |  ❓ Pick an option                  |  |
|  |     steward · "Which model?"       |  |
|  |     ◯ Opus  ◯ Sonnet  ◯ Haiku       |  |  <- Radio options
|  +------------------------------------+  |
|                                          |
|  ▼ Recently resolved                     |
|  +------------------------------------+  |
|  |  ✓ Approved · run_command          |  |
|  +------------------------------------+  |
+------------------------------------------+
```

Vocabulary:
- **Attention item** — one row representing something the agent is
  waiting on you for.
- **Severity icon** — `⚠` warn, `❓` info, `🔴` block. Drives
  notification routing.
- **Inline Buttons** — the Approve/Deny pair embedded in the Card.
- **Radio options** — circular selectors for "pick one" attention.

### 3.7 Hosts (BottomNav tab 3)

Lists machines running host-runner.

```
+------------------------------------------+
|  Hosts                          [+]      |  <- + adds a new host
+------------------------------------------+
|  +------------------------------------+  |
|  |  🖥 vps-01                         |  |  <- host Card
|  |     online · 2 agents              |  |
|  |     ●                              |  |  <- status dot (green=online)
|  +------------------------------------+  |
|  +------------------------------------+  |
|  |  🖥 gpu-rig                        |  |
|  |     offline · 0 agents             |  |
|  |     ○                              |  |  <- grey dot
|  +------------------------------------+  |
+------------------------------------------+
```

### 3.8 Me (BottomNav tab 4)

Account, settings, snippets, SSH keys, attention prefs.

```
+------------------------------------------+
|  Me                                      |
+------------------------------------------+
|  ▣ @physercoe                            |  <- principal handle
|                                          |
|  Settings                                |
|    Snippets               >              |  <- ListTile with chevron
|    SSH keys               >              |
|    Notifications          >              |
|    About                  >              |
|                                          |
|  Sign out                                |
+------------------------------------------+
```

Vocabulary:
- **Principal** — the project's word for "the human user." Your
  `@handle` shows here.
- **Chevron** — the `>` at the right of a tappable row indicating
  drill-in.

---

## 4. Verbs of interaction

Use these exact words in bug reports. They map to the project's
gesture handlers.

| Word | What it means | Common confusion |
|---|---|---|
| **Tap** | Single quick touch + release | Don't say "click" on mobile |
| **Double-tap** | Two taps within ~250ms | Different from "two taps" |
| **Long-press** | Hold for ~500ms without release | Different from "press and hold + drag" |
| **Swipe** | Quick directional drag | Always specify direction (left/right/up/down) |
| **Pull-to-refresh** | Drag from top, release to trigger | Specific to scrollable lists |
| **Drag** | Slow directional touch movement | Used for reordering |
| **Scroll** | Drag a long list to move through it | Don't say "swipe" for scrolling |
| **Pinch** | Two-finger zoom in/out | Used in image / file viewers |

A common mistake: "I swiped left and the agent disappeared." Was it
a quick swipe (gesture trigger) or a slow drag (reorder)? Different
code paths. Always say which.

---

## 5. Common confusion points worth naming explicitly

These are points where users have mis-described problems often
enough that it's worth pre-empting:

### 5.1 BottomNav vs TabBar

```
+--------------+         +--------------+
|              |         | < AppBar     |
|              |         |              |
|              |         | ┌──┬──┬──┐   |   <- TabBar (scoped to one screen)
|              |         | │  │  │  │   |
| (content)    |         | └──┴──┴──┘   |
|              |         |              |
|              |         | (tab content)|
|              |         |              |
+──────────────+         +──────────────+
| [P][A][H][M] | <- BottomNav (app-wide)
+--------------+         +--------------+
```

If your report says "I tapped the Tasks tab" and you mean the
in-screen TabBar, say so. "I tapped the Activity tab" usually means
the BottomNav. They're different elements and different code.

### 5.2 BottomSheet vs Dialog

```
+------------------+         +------------------+
| (dimmed screen)  |         | (dimmed screen)  |
|                  |         |   ┌──────────┐   |
|                  |         |   │  Dialog  │   |  <- centered, smaller
|                  |         |   │  [Yes]   │   |
+==================+         |   │  [No]    │   |
|     ▔▔▔          |         |   └──────────┘   |
|  BottomSheet     |  <- bot |                  |
|  options...      |         +------------------+
+------------------+
```

A BottomSheet slides up from the bottom and holds a list of
options. A Dialog is centered, smaller, usually has a single
Yes/No or Confirm/Cancel choice. They look different and behave
differently. If you got a confirmation prompt, say "Dialog"; if
you got a list of actions, say "BottomSheet."

### 5.3 Status chips by colour

Mobile renders status chips with stable colours:

| Chip text | Colour | Meaning |
|---|---|---|
| `active` | green | live and currently chatting |
| `paused` | orange | agent died, transcript preserved, can Resume |
| `archived` | grey | finished, read-only, can Fork |
| `running` | green | (agent status) live process |
| `crashed` | red | process died unexpectedly |
| `terminated` | grey | cleanly stopped |

If a chip's colour doesn't match its text in your bug, that's
itself a reportable issue — say "the chip said `active` but
showed orange."

### 5.4 Resume vs Fork

These look adjacent in the UI but mean opposite things:

- **Resume** — same chat, new agent process. The conversation
  context comes back. Available on **paused** sessions only.
- **Fork** — new chat shell, blank engine. The new agent doesn't
  remember the source's conversation. Available on **archived**
  sessions only. Currently experimental — don't expect the new
  agent to "pick up where you left off."

If your session shows a Resume button, the engine session is
expected to be threaded. If it shows Fork, expect a cold start.

### 5.5 The agent vs the engine

In casual reports, "the agent didn't respond" is fine. In careful
ones, distinguish:

- **Agent** — the project's row that represents the conversation
  partner. Has a handle (`steward`, `worker-1`).
- **Engine** — the actual program running on a server (`claude`,
  `codex`, `gemini`). The engine is what produces text.

"The agent's status flipped to `crashed`" is a hub-side fact; "the
engine returned an error" is engine-side. Both are useful;
naming which layer helps narrow the bug.

---

## 6. Where to find your app version

Settings → Me → About. Copy the full version string, including the
build number, e.g. `1.0.349-alpha+10349`.

---

## 7. What we genuinely can't infer from "it broke"

If your report omits these, expect a follow-up question:

- **Which screen.** Without screen name, we don't know which code
  path to load.
- **Steps in order.** "I tapped Resume" vs "I opened the
  BottomSheet, scrolled, then tapped Resume" can land on different
  bugs.
- **What you expected.** Some surprises are bugs, some are intended.
  We can't tell from outcome alone.
- **Local time + timezone.** Server logs are correlated by UTC; your
  local time + zone lets us search.
- **Engine kind, if relevant.** A bug under claude-code might not
  reproduce under codex or gemini, and vice-versa. The chip in the
  Session detail shows engine kind.

---

## 8. References

- [Engineering glossary](../reference/glossary.md) — for developers
  hunting term collisions.
- [Doc spec](../doc-spec.md) — how all docs are structured (for
  contributors who want to extend this guide).
- Settings → About → "Report an issue" link — opens this doc plus
  a pre-filled bug template.
