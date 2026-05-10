# Agent-driven mobile UI — overlay steward + intent channel

> **Type:** discussion
> **Status:** Open
> **Audience:** principal · contributors · reviewers
> **Last verified vs code:** v1.0.466

**TL;DR.** Today the mobile app is operated by tap. The principal
asked: can a [steward](../reference/glossary.md#steward) instead
operate the app — open pages, fill fields, fire actions — while the
user reads the project plan, deliverables, and acceptance criteria
in parallel? The deeper framing landed in §2 below: the steward is
*above* the app, sharing the user's context, so the user-to-system
information loop runs at superfluid efficiency (zero friction,
zero context-rebuild cost). MVP-critical because tap throughput is
the demo's bottleneck and the agent-driven mode must measurably
beat manual mode to justify the wedge. This doc captures the
grounding use case, the three-layer architecture (persistent
overlay, voice via system IME, intent channel with shared state),
the demo script that proves the efficiency claim, and the open
questions that must be locked in an ADR before plan-writing.

---

## 1. The framing — what the user asked

> *"It needs human to tap to change page. Can these ops be done by a
> program or AI agent that is in this app? I hope there is an
> overlay/floating page that is steward — when I direct it on this
> page, it can operate the app for me, such as open some page,
> tap/edit something. To go further, I want to talk to it then it
> operates for me."*

Two requirements buried in the directive:

1. **The overlay must persist across pages** — Project Detail, Me,
   Activity, Hosts, etc. The user wants to *direct while reading*.
   A modal sheet that consumes the screen breaks the use case.
2. **The steward drives the app** — not "navigate to a page that
   shows information," but "open the project plan view in the
   foreground while we talk about it." The overlay is the input
   surface; the rest of the app is the output surface that the
   steward animates.

The principal flagged this as load-bearing for MVP demo efficiency.
A demo where the user taps through ten screens to set up a research
run is a demo of mobile menus, not a demo of agent orchestration.
A demo where the user *directs the steward* and the app responds is
a demo of the actual product thesis.

---

## 2. The grounding use case — phone as multiplexed screen

The mental model that pins down what we're trying to replace.

### 2.1 Desktop today: parallel screens, sequential attention

The principal currently works at a desktop with multiple large
monitors. Each screen holds a different *thread* of work — chatbot
on one, browser on another, IDE on a third. Threads run in
parallel; the user can only focus on one at a time, switching
heads as needed. The architecture is "many concurrent visible
threads, one user-attention pointer."

This works because the **screens themselves are the multiplexer**
— the user's eye picks which thread to read next. Switching cost
is near-zero: a head turn.

### 2.2 Phone today: one screen, one thread, expensive switching

A phone has one screen. Without help, the user becomes the
multiplexer themselves — tapping back, navigating menus, scrolling
to find the thread they want. Switching cost goes from "head turn"
to "tap sequence." The slowdown isn't subtle; it's the difference
between watching three monitors and refreshing one window in
sequence.

The MVP demo target is a foldable phone (~tablet-class screen
unfolded). That partly closes the visual-bandwidth gap, but it
doesn't change the multiplexing problem — even an unfolded device
has one focal context at a time, where a desktop has six.

### 2.3 The translation: phone as time-multiplexed screen

The phone serves as a **multiplexing screen** — sequential output
simulating multiple concurrent threads, like a CPU sequentially
running threads that the kernel context-switches between. The
analogy is exact:

| Desktop | Phone (manual) | Phone (agent-driven) |
|---|---|---|
| Spatial multiplex (N monitors) | User multiplexes by tap | Steward multiplexes for user |
| Switching cost: head turn | Switching cost: tap sequence | Switching cost: utterance |
| User sees N threads at once | User sees 1 thread at a time | User sees the *right* thread at a time |

The agent-driven mode doesn't try to fit N threads on the phone
screen. It promotes the *steward* to the role of the
context-switching kernel — the user expresses intent, the steward
routes the right thread to the foreground. Threads still run in
parallel (multi-host, multi-agent on the backend); only the
foreground thread is rendered.

### 2.4 The steward is "above the app, as the user"

This is the load-bearing claim. The steward is not *in* the app
(another panel that runs alongside the rest of the UI). It is
*above* the app, **co-located with the user's attention pointer**.
The steward sees what the user sees: current route, selected
project, scroll position, focused field, last-read briefing. When
the user types a comment inline, the steward sees that comment as
event in the loop. When the steward navigates, the user sees the
new screen.

There is one shared state machine. The overlay is the input
channel for the user's side; the rest of the app is the output
channel for the steward's side; both sides read the same state.

This is what changes the architecture. The intent channel (§4.3) is
not "steward sends commands to mobile" — it's "steward + mobile
operate on shared state, with both sides allowed to write."

### 2.5 Superfluid information flow as the success metric

> *"The information flow loop b/w user and the app/system/steward
> is at its maximum efficiency, like electric flow in a superfluid
> state circuit."*

A superfluid circuit has zero resistance. Translated to UI: zero
friction, zero context-rebuild cost, zero "wait, where was I?"
The user's intent flows out, the system's response flows in, both
sides stay in step.

The metric falls out of this: **time from intent to acted-on
result**. Manual mode (today): user thinks → user navigates →
user reads → user acts. Each step has friction. Agent-driven mode
(target): user speaks intent → steward routes → user reads → user
edits in place. The middle two steps fuse.

The MVP demo's job is to show that the loop is faster *and* less
frictional than manual mode on a real, sharp use case. Not faster
in seconds (sometimes voice + steward thinking is slower than a
single tap), but faster in *throughput* across the loop — total
useful work per unit attention.

### 2.6 Pre-conditions for the demo

| Precondition | Why |
|---|---|
| User is on a foldable phone (tablet-size unfolded) | The screen real-estate fits the demo; smaller phones force overlay-vs-content competition the demo doesn't need |
| 2 hosts already registered to the hub | Multi-host is the differentiator; one host degenerates to single-engine remote-control |
| App is otherwise empty (no projects yet) | The demo arc starts at "user has an idea, nothing is set up" — first-run flow is part of the test |
| General steward exists on the team | Otherwise the overlay has no agent to talk to; the steward must be live before the demo opens |

The demo's entry point is the user opening an empty app with an
idea in their head. Everything that follows is the loop.

---

## 3. What already exists — pieces we can reuse

| Piece | Where | Use here |
|---|---|---|
| Persistent steward concept | `lib/widgets/home/persistent_steward_card.dart` | The card already surfaces the [general steward](../reference/glossary.md#general-steward) on Me. The overlay is a more aggressive version — same identity, different lifecycle. |
| Steward MCP gateway | `hub/internal/hubmcpserver/` + `mcpbridge/` | Stewards already call hub-side MCP tools (create project, ratify deliverable, archive agent). Adding mobile-side intents is a new tool family on the same gateway. |
| SSE → mobile fanout | `lib/services/hub/hub_sse.dart` | Hub already pushes events to mobile in real time (sessions, attention, audit). A new event kind — `mobile.intent` — fits the existing pump. |
| Deep link scheme | `termipod://` URLs | Already covers `project/<id>` and `session/<id>` external entry. Extending the scheme to cover internal verbs (`activity?filter=stuck`, `attention/<id>/approve`) lets a steward speak deep links rather than custom tool calls. Worth weighing against an MCP-tool-per-verb approach. |
| ACP capability surface (ADR-021) | `decisions/021-acp-capability-surface.md` | The capability negotiation pattern (driver advertises which features the engine supports) is reusable for mobile UI capabilities — the overlay advertises which intents this app version can execute. |

What's new:
- The overlay widget itself (cross-route persistence)
- The intent dispatcher on the mobile side (route a received intent to a navigation/action)
- The intent schema (the contract between steward and mobile)
- The voice path — though see §3 for the cheap version

---

## 4. The three layers

### 4.1 Floating overlay

Flutter supports cross-route overlays via `Navigator`'s `Overlay`
attached at app root, or a `MaterialApp.builder` wrapper that hosts
a `Stack` above every route. The overlay is a draggable
mini-chat-pill that:

- Stays visible across Projects / Me / Activity / Hosts / Settings
- Collapses to a small puck (steward avatar) when not in use; expands
  to a partial-height chat panel on tap
- Owns its own scroll surface so user-side scroll on the page
  beneath isn't intercepted
- Survives push/pop transitions (entry attached to root navigator,
  not per-route)

Open: when the user pushes a fullscreen modal (e.g., terminal pane,
session chat) does the overlay hide, dim, or persist? Probably
*hide* during transcript-bearing surfaces (the overlay chat would
double up with the surface chat) and *persist* everywhere else.

### 4.2 Voice — defer to system IME

The user's insight here saves a wedge of work. Modern Android +
iOS keyboards (Gboard, Samsung Keyboard, iOS dictation) already
provide a microphone button that converts voice to text directly
into the focused input field. The steward chat input is just a text
field — the system handles speech-to-text upstream of us.

This means:
- No `speech_to_text` package, no permissions ceremony, no STT
  vendor lock-in
- The chat box is the only input surface; voice and typing route
  through the same path
- Text-to-speech for read-back is a separate decision (probably
  defer post-MVP — the screen is right there)

The cost: dictation quality is only as good as the user's keyboard.
For demo purposes that's fine; the principal can pick a known-good
keyboard.

### 4.3 Intent channel — shared state, not commands

Per §2.4 the architecture is **shared state**, not steward-commands-mobile.
That means the channel is bidirectional and symmetric:

- **Mobile → steward.** Mobile reports a small state digest on every
  user-driven change: current route, selected entity ids, scroll
  anchor, selected text. The steward's prompt context is reseeded
  with this digest each turn — so when the user says "fix that
  one", the steward already knows what's on screen.
- **Steward → mobile.** Steward emits intents that mutate the same
  state machine: `navigate`, `set_text`, `expand_section`,
  `confirm`, `approve`. Mobile applies them and reports the
  resulting digest back. Loop closes.

There is no "the steward operates the app while the user watches."
There's "the user and the steward both operate one shared state,
and each side sees what the other did."

Two competing shapes for the wire format:

**Option A — whitelisted MCP tools.** The hub exposes a `mobile.*`
namespace: `mobile.navigate(uri, ...)`, `mobile.set_text(...)`,
`mobile.confirm(...)`, `mobile.approve(...)`. Each tool-call
writes an intent record to the database; mobile's SSE listener
picks it up and dispatches.

**Option B — generic UI driver.** The steward can say "tap the
button labeled 'Approve'" and the mobile parses and executes. More
flexible, but the steward-mobile coupling becomes opaque (the
steward's prompt has to know UI labels, which drift).

A is cleaner — it matches the agent-native design principle ("ship
schemas, prefer worked examples"). The whitelisted set is small
enough to enumerate in the ADR; future verbs are explicit additions
to the schema.

The intents themselves split into two tiers:

- **Read-only intents** (navigate, expand, scroll, filter) — auto-execute,
  show a tiny steward-did-this banner so the user sees what changed.
- **Write intents** (approve, archive, delete, send a message,
  rename) — gate behind a confirmation banner. The steward proposes,
  the user one-taps yes/no.

Matches the existing permission scope MVP — auto-allow tool calls
on the data side, gate destructive ops as attention items.

#### Addressing scheme — URIs are the public API

The most consequential sub-decision under Option A is **how
destinations are named** — and the AI-native answer is *URI
strings*, not typed Dart routes.

`navigate` and any verb that addresses an entity (`approve(id)`,
`set_text(field)`, `ratify(deliverable)`) must speak a schema both
sides understand. Two shapes compete:

| Property | Typed routes (Dart enums / GoRouter classes) | URI strings (`termipod://...`) |
|---|---|---|
| LLM training-data fit | rare / none | URLs are everywhere in training corpora |
| Self-describing in logs | needs schema lookup | obvious cold |
| Survives serialization | loses meaning across hub / mobile / audit | portable string |
| Composes with external links | no | yes — same URI works from Slack, email, share sheet |
| Embeddable in content | no | yes — a deliverable can link to its criteria |
| Version-tolerant | hard mismatch on shape change | older app falls back gracefully on unknown path |
| Type safety | inherent | recovered at the parse boundary |

URIs win on every AI-native axis. The router *inside* the app
(currently imperative `Navigator.push…`; could become a GoRouter
or a custom dispatcher post-MVP) is an implementation detail —
what matters is that the **public addressing schema** is a URI
the steward generates first-shot.

This also unifies the audit shape. When the user taps a project,
mobile fires `termipod://project/<id>` internally; when the
steward speaks the intent, it emits the same URI. The audit log
records *one* event shape regardless of initiator. Typed routes
would split that into two code paths fighting to stay in sync.

Termipod already has the pipeline — `DeepLinkService` (`lib/services/deep_link/deep_link_service.dart:30`)
parses `termipod://` URIs from the Android intent filter via a
MethodChannel. The current `DeepLinkData` schema is narrow
(server / session / window / pane — legacy MuxPod tmux
addressing); extending it to cover projects / documents /
sections / deliverables / criteria / attention items is the
chassis the wedge plugs into. A `mobile.navigate(uri=...)`
intent goes through the same parser an external URL click would
hit. One code path, one router, two callers.

Worked URI examples to lock in the ADR:

```
termipod://project/<id>
termipod://project/<id>/documents/<docId>/sections/<sectionId>
termipod://project/<id>/deliverables/<delId>/criteria/<critId>
termipod://activity?filter=stuck
termipod://attention/<id>
termipod://agent/<id>/transcript
termipod://session/<id>
```

---

## 5. Open questions for the ADR

These are the calls that must be locked before plan-writing:

1. **Intent schema scope.** Which verbs ship in v1? Proposed minimum:
   `navigate`, `set_text`, `confirm`, `approve`, `archive`. The
   minimum is "enough for the demo" — write the demo script, derive
   the verb set from it.

2. **Confirmation gate policy.** Read-only auto-execute is obvious.
   On write intents, do we gate them all uniformly, or carve out a
   "low-cost write" tier (set_text into a draft field) that
   auto-executes? Affects perceived latency.

3. **Steward awareness of mobile state.** ~~Open?~~ §2.4 closes this:
   *yes, load-bearing.* The steward must see the user's current
   route, selected entity, scroll anchor, and selected text.
   What's open is the **digest shape**: how much state, how often
   transmitted (every user action vs. every steward turn), what
   compression. Suggest: emit a compact JSON digest on every
   navigation / selection event, included in the next steward turn
   prompt. Bandwidth is small; the cost is prompt-tokens, which is
   real but tractable. Lock the schema in the ADR.

4. **Conflict resolution.** User is reading Project Y; steward
   navigates to Project Z. What happens? Three options: (a)
   navigate-and-take-over, (b) ask first ("Open project Z?"), (c)
   show a deep-link chip the user taps to follow. Affects trust.

5. **Audit trail.** Every agent-driven UI action should land in
   `audit_events` so the user can review what the steward did. Same
   table the existing audit feed uses; new actor kind
   (`agent_ui_driver`?) for distinguishability.

6. **Single steward vs many.** The persistent overlay tracks the
   [general steward](../reference/glossary.md#general-steward) by
   default. When the user is inside a project that has a
   [domain steward](../reference/glossary.md#domain-steward), does
   the overlay swap to the domain steward (project-scoped expertise)
   or stay general (cross-project consistency)? Or do we expose
   both, with a tab? Affects the IA on Project Detail.

7. **Background-app handling.** If the user backgrounds the app and
   the steward fires an intent, does it: (a) buffer until foreground,
   (b) drop, (c) push a notification ("steward wants to navigate")?
   Drops are silent failures; buffers can stack stale intents;
   notifications match existing attention patterns.

8. **Engine cost.** Every voice command is a steward turn. At the
   current per-turn token volume (hundreds to low-thousands depending
   on context), heavy voice usage doubles or triples spend per
   session. Worth it? Cap somehow? At minimum, surface in the
   Insights view.

9. **Multi-device.** A user with phone + tablet, both running the
   app, both subscribed to the same steward — which device receives
   the intent? Probably the *most recently active* one
   (`mobile.intent.target = mostRecentClient(team_id)`), but needs an
   explicit rule.

10. **Discoverability.** First-launch UX. Without buttons, how does a
    new user know what to ask? Probably: a starter-prompt sheet on
    first overlay-tap ("Try: Open today's runs · Show me what's stuck
    · Approve the gemini one"). Same shape as the spawn-steward sheet
    that exists today.

11. **Capability negotiation.** The mobile app of v1.0.463 has fewer
    intent verbs than v1.0.500 will. Mirror ADR-021 — the overlay
    sends a capability list to the hub on connect; the steward
    template includes a system-prompt section that conditions on
    available verbs. Otherwise stewards on old phones invoke
    not-yet-shipped intents and silently fail.

12. **Trust failure mode.** Demo-grade: what does it look like when
    the steward proposes the wrong action? A retract/undo affordance
    on the steward-did-this banner — same lifetime as the post-action
    "you can undo for 5 seconds" pattern email apps use. Pre-emptive
    rather than reactive.

13. **Route addressing scheme.** ~~Open?~~ §4.3 closes this:
    *URIs are the public API.* The grammar locks in the ADR — the
    set of URI shapes the v1 steward can address (project,
    document/section, deliverable/criterion, activity?filter=…,
    attention/<id>, agent/<id>/transcript, session/<id>). The
    in-app router (imperative Navigator today, possibly GoRouter
    later) is an implementation detail. What's open is whether to
    namespace internal-only paths (e.g. `termipod://_internal/...`)
    so future external URLs don't collide.

---

## 6. Why MVP-critical — efficiency over manual mode

The wedge has to *measurably beat* manual mode on the same task.
That's the test. Below is the framing; §7 is the concrete script.

- The MVP demo target (per `docs/spine/blueprint.md` §9 P4) is the
  research-lifecycle loop: directive → decompose → multi-host runs
  → review.
- Every phase of that loop currently requires several screen taps
  (open project, switch to plan, open deliverable, ratify, advance
  phase, switch to runs view, …).
- Tap-throughput on a phone is the demo's actual bottleneck. The
  agents move fast, the network moves fast, the user's thumb is the
  slow path. Even on a foldable's tablet-class screen, the
  multiplexing problem (§2.2) doesn't go away — one focal context
  at a time.
- The competitor framing
  (`memory: project_positioning_vs_competitors.md`) is single-engine
  remote-control apps. They tie the user to one engine but they
  don't try to operate the *whole product* by voice. If we ship
  agent-driven UI, we leapfrog them on the axis they don't compete
  on.

The success criterion is empirical, not theoretical:
**user completes the demo arc faster, with fewer attention
context-switches, in agent-driven mode than in manual mode.**
Concretely:

- Wall-clock time from "user has the idea" to "runs are kicked off
  on both hosts."
- Number of distinct UI surfaces the user must focus on (proxy for
  context-rebuild cost).
- Subjective: did the user have to *remember where they were* at
  any point? (Friction signature.)

Both metrics must move in the right direction or the wedge isn't
shipping.

The MVP scope is *not* "every UI verb is reachable by voice." It's
*the demo path is reachable by voice* — five to ten verbs derived
from §7's script. The whitelisted-MCP-tool approach in §4.3 makes
the scope literal: ship the verbs the script needs, defer the
rest.

---

## 7. The demo script

A real, sharp use case. The principal opens an empty app on a
foldable phone (~tablet-size unfolded); 2 hosts already registered;
general steward live; no projects yet. Has an idea: *test whether
model X handles edge case Y across two engines*.

### Manual-mode baseline (the thing we beat)

Steps the user must take today, every tap an attention switch:

1. Tap *Projects* tab → tap **+** → fill name → save
2. Tap into the new project → tap *Plan* → type plan content → save
3. Tap *Deliverables* → tap **+** → name deliverable → save
4. Tap *Acceptance criteria* → tap **+** × 3 → fill each → save
5. Tap *Spawn agent* sheet on host 1 → pick template → submit
6. Switch to host 2 → spawn second agent → submit
7. Wait, switch to *Activity*, watch attention items
8. Each completion → tap into agent → review output → tap *Approve*
9. Switch back to project → ratify deliverable → advance phase

Estimate: 25–40 taps, 8–12 distinct UI surfaces, 5–8 minutes if the
user knows the app cold. More if not.

### Agent-driven mode (the demo)

User opens the app. The persistent overlay sits at the bottom-right
as a steward avatar puck. User taps it. Voice/text input.

**Turn 1 (text dialog).** *"I want to test if claude-opus-4-7 and
gemini-3 both handle the off-by-one edge case in our pagination
logic. Can you set up a research project to compare?"*

The steward responds **in text first** — the lowest-cost, lowest-
risk surface. Confirms understanding, proposes a plan in
conversation:
*"OK — research project, two hosts, one agent per engine, deliverable
is a comparison brief. Acceptance: both engines produce a fix that
passes the existing test suite plus a new edge-case test. Want me
to draft the project page?"*

**Turn 2 (steward graduates from text to UI).** User says yes. The
steward emits a sequence of intents:

1. `mobile.create_project(name="Pagination edge-case bake-off",
   template="research")` → returns `<new_id>`
2. `mobile.navigate(uri="termipod://project/<new_id>?tab=plan")`

The user's screen flips to the new project page, with the plan
section visible. The steward populates the plan (which the user
sees streaming in) and ratifies the initial draft.

The user reads the plan inline. Notices a missing detail —
selects a paragraph and types directly: *"Add: also compare token
counts."* No re-tapping into the overlay; the inline comment is
*part of the shared state*, the steward sees it on the next
state-digest tick.

**Turn 3 (deliverable + criteria).** Steward acks the comment,
emits intents to add the deliverable + 3 acceptance criteria
(*existing tests pass*, *new edge-case test passes*, *token-count
diff captured*). User watches the criteria appear.

**Turn 4 (multi-host kickoff).** User: *"Run it on both hosts."*
Steward emits two `agent.spawn` intents (one per host) and
navigates to a split view (or the Activity tab, depending on
foldable layout) so the user can watch progress.

**Turn 5 (reviewing as runs complete).** Each agent fires
attention items as it converges. The steward routes the user to
each — *"Host 1 finished, here's the diff"* with `mobile.navigate`
to the relevant agent's transcript. User reads. If happy, says
*"approve"*; if not, comments inline.

**Turn 6 (briefing).** Steward writes the comparison brief into
the deliverable, navigates the user to it. User reads, ratifies
(by saying *"looks good, ratify"* or tapping inline). Phase
advances. Done.

### Why this script is "decisive"

- **Sharp**: every step solves a real friction the manual-mode
  baseline has. No artificial showcasing.
- **Typical**: this is a workflow the principal actually does in
  some form on desktop today — that's where the desktop-multi-screen
  framing comes from.
- **Simple**: 6 turns, ~5 verbs (`create_project`, `navigate`,
  `set_text`, `spawn_agent`, `approve`, `ratify`). The verb set is
  cheap to schema and ship.
- **Measurable**: tap count goes from 25–40 to ~3 (open overlay,
  approve x2). Surface count goes from 8–12 to ~4. Time goes from
  5–8 min to maybe 90 seconds end-to-end (most of it spent on
  agent execution, which would happen in manual mode too).
- **Multi-host is non-trivial**: the split-or-merged view across
  two hosts is the differentiator that single-engine clients can't
  replicate.
- **Foldable-friendly**: the tablet-class screen lets the overlay
  sit alongside the project page rather than over it; degrades
  gracefully on phone-size by collapsing the overlay to a puck.

### What the script is NOT

- Not "user reads briefings already produced overnight" — that's a
  separate demo. This one ends when the runs *kick off*; the
  overnight + briefing arc is the existing P4 demo (see
  `memory: project_research_demo_focus.md`).
- Not "voice-only." The user can talk or type at every step;
  steward responses can be text or UI navigation. Voice is the
  fast path for intent; reading is the fast path for output.
- Not "agent does everything autonomously." The user remains the
  director — every write intent is gated, every plan is reviewable,
  every criterion is editable.

### What the script needs from the platform

Implied verb set (lock in ADR-023 §1):

| Verb | Read/write | Notes |
|---|---|---|
| `create_project(name, template)` | write | gated; confirmation banner; returns new project id |
| `navigate(uri)` | read-only | auto-execute; `uri` is a `termipod://` URI per §4.3 |
| `set_text(uri, value)` | write | low-cost; auto-execute into draft fields; `uri` addresses the field |
| `add_deliverable(project_uri, kind)` | write | gated |
| `add_criterion(deliverable_uri, body)` | write | gated |
| `spawn_agent(host_uri, template)` | write | gated; cost-bearing |
| `approve(attention_uri)` | write | gated; one-tap from banner |
| `ratify(deliverable_uri)` | write | gated |
| `advance_phase(project_uri)` | write | gated |

Every entity reference is a URI. That keeps the audit log
self-describing, makes the steward's tool calls survive
serialization (they show up legibly in transcripts and briefings),
and lets the same string round-trip from external link → mobile
intent → audit event → briefing doc without translation layers.

Plus the bidirectional state digest (§4.3): mobile pings the hub
on each navigate / selection so the steward's next turn has fresh
context.

---

## 8. Tradeoffs and risks

| Risk | Mitigation |
|---|---|
| Voice-to-intent round trip is slow (STT + steward turn + SSE + mobile dispatch ≈ 1–4s) | Cache common verbs as deterministic on-device parses ("open <name>") that bypass the steward; route only ambiguous prompts to the agent. Latency budget = perceived as "thinking" if under 2s, slow if over. |
| Steward navigates aggressively, user feels hijacked | Confirmation policy (§5 Q4); always-visible undo banner; "ask first" mode toggle in Settings. |
| Voice fidelity is keyboard-dependent | Accept; doc the recommended keyboard for demo. Post-MVP we can revisit on-device STT. |
| Discoverability — users don't know what to ask | Starter-prompt sheet (§5 Q10); persistent "Try saying…" hints in the empty overlay state. |
| Engine cost balloons with voice usage | Per-team daily budget cap on `mobile.*` tools; surface usage in Insights. |
| Background-app silent failures | Notification fallback (§5 Q7); intent log so the user can see what was queued. |
| Schema drift between mobile + steward | Capability negotiation (§5 Q11) + intent schema versioning in the same shape as the rest of the API. |

---

## 9. Recommended next steps

1. **Lock the demo script first.** The verb set falls out of the
   script. §7 above is a starting draft — refine before committing.
2. **Draft the ADR** locking §5 Q1–Q12. Suggested number: ADR-023
   (next available).
3. **Cut a plan** with three workbands:
   - W1 — Persistent overlay shell + steward identity binding (no
     intents yet; just the chat surface)
   - W2 — Intent schema + dispatcher + read-only verbs (navigate,
     set_text)
   - W3 — Write verbs + confirmation policy + audit log entry

   Voice is implicitly handled across all three since the system IME
   path needs no in-app code.

4. **Flag for the demo rehearsal**: the first concrete test is
   "user can open three projects + ratify one deliverable + advance a
   phase, with hands free, in under 30 seconds." If that works, the
   wedge ships. If not, iterate before generalizing.

---

## 10. SOTA landscape and design axioms (2026 research)

Compiled from a sweep of 2026 sources (linked in §11) just before
shipping the v1.0.464-alpha prototype, to validate the architecture
choices in §2–§4 against what's actually working (and what's
visibly failing) elsewhere. Treat this as the "what does the
industry know that we should bake in" snapshot — refresh before
ADR-023.

### 10.1 The SOTA pattern table

| Approach | Who | Pattern | Termipod relationship |
|---|---|---|---|
| **OS-level system agent** | Apple Intelligence + App Intents | Apps expose `@IntentParameter` schemas → Siri/AI orchestrates. On-screen content awareness; cross-app context. *"Apps that don't expose intents will feel invisible in an AI-first OS."* | Schema-first declaration. Our URI grammar (§4.3) is the embryo. |
| **OS-level system agent** | Google Gemini Agent + Project Astra (Android) | Replaces Google Assistant 2026. Navigates Android apps directly. **Security caveat:** malware now exploits Gemini for device control. | Confirms capability gating + audit are load-bearing, not optional. |
| **Phone-as-dispatcher** | Anthropic Claude Computer Use + Dispatch | Phone delegates a task → Claude executes on the user's Mac → result returns. Always requests permission for new apps. | **Structurally identical to termipod's hub model.** Phone directs, hosts execute. Lean into this pattern in framing. |
| **Browser-as-superapp** | ChatGPT Atlas (macOS, Windows soon) | Sidebar assistant + agent mode + persistent cross-tab/cross-session memory. OpenAI merging Atlas + ChatGPT + Codex into one desktop app. | Persistent context is the differentiator: *"Atlas remembers prior sessions while Chrome does not."* Maps to our shared-state model in §2.4. |
| **Hardware AI agents — FAILED** | Rabbit R1 + Humane AI Pin | "Hand intent to LAM, agent manipulates APIs instead of UI." Rabbit sold 100k → mass returns; Humane sold to HP for ~half what they raised. ~$5B combined value lost. | Critical cautionary tales — see §10.4 below. |
| **Persistent floating co-pilot** | Industry standard (Zendesk, Intercom, ChatGPT mobile, etc.) | Bottom-right puck, always-visible, collapse-to-button. Evolved from "always there" → "persistent but **optional**". | Validates termipod's overlay design choice; surfaces the dismiss-toggle gap (§10.5). |

### 10.2 The big shift 2024–2026

Industry consensus, multiple sources: 2024–2025 was *"AI does it
for you"* (full autonomy). 2026 has **rejected** this model.
Users felt out of control with autonomous flows. Convergence on
the **persistent + optional + reviewable** pattern — agent as
co-pilot, not driver. Ratifies our prototype's read-only-first
constraint and the ADR's confirmation gate policy (§5 Q2).

### 10.3 The IAA framework — Intent → Action → Audit

Smashing Magazine's *"Designing for Agentic AI"* (Feb 2026)
formalized the 3-pillar pattern that almost every shipping
agentic UX follows:

1. **Intent Preview** — agent restates the user's goal + proposed
   plan **before** acting. Earns trust before consuming attention.
2. **Autonomous Action** — agent executes in the background.
3. **Audit & Verification** — clear log of what was done; **undo
   or override** affordance.

Our v1.0.464 prototype already does **A** (audit_events row +
steward-did-this banner) but is partial on **I** (steward should
restate goal before navigating; today the prompt encourages this
weakly) and missing **U** (no undo affordance). ADR-023 should
lock all three.

### 10.4 Lessons from Rabbit R1 / Humane Pin failures

The most useful research finding. ~$5B lost between Rabbit R1
(LAM-driven AI gadget) + Humane AI Pin (wearable AI) in 12
months. Failure modes that map directly to risks in this wedge:

- **Demo-grade ≠ ship-grade.** "Ship based on what works today,
  not what you plan to build." The gap between demo capability
  and shipped capability is the single most destructive force in
  consumer agent products. → Our prototype's small-but-real verb
  set respects this.
- **The phone is fierce competition.** "Modern smartphones are
  incredibly powerful and versatile — tough to beat." Users
  evaluate against their existing tap workflow, not against the
  agent's roadmap. → Our test plan's manual-vs-agent baseline
  measurement is the right ship-or-not gate.
- **Capability honesty.** Don't promise what isn't shipped.
  Rabbit's LAM was a demo that didn't translate to production. →
  Our explicit "read-only verbs only at this stage" scope is the
  right move.
- **Substitution beats addition only when net-add is clear.**
  Rabbit tried to *replace* the smartphone with the R1; users
  rejected the substitution. → Our agent-driven UX must be a
  net-add to the existing app, not a replacement for it. The
  overlay coexists with the manual UI.

### 10.5 Twelve axioms (validated against 2026 SOTA)

The principal originally named three (multiplexing, context-
sharing, intention/purpose-first). Research lets us confirm those
and add nine more. Ranked by load-bearing weight:

#### Architectural

1. **Intent over procedure.** User states the goal; system
   determines the steps. Source: Tentackles, Cobeisfresh,
   UXTigers (2026).
2. **Director not operator.** Already in
   [ADR-005](../decisions/005-owner-authority-model.md). The
   user directs; the agent operates. Validated by Apple's
   *"no navigation required"* framing for Siri 2026.
3. **One state, two writers.** §2.4 above. Validated by Atlas's
   persistent cross-tab state and Apple's on-screen content
   awareness — both are concrete instances of the same idea.
4. **URIs as the public API.** §4.3 above. Apple's App Intents
   are typed enums (less general); URIs are LLM-native and
   survive serialization. We've made the more agent-friendly
   choice.

#### Cognitive

5. **Multiplexing.** Principal's framing. Single attention
   pointer + many parallel backend threads. Steward routes the
   right thread to the foreground. Confirmed across all 2026
   sources reviewed — design has rejected "show user everything
   at once."
6. **Context-sharing.** Principal's framing. The state the user
   sees IS the state the agent sees. Atlas's "remembers prior
   sessions" is the canonical instance.
7. **Persistent but optional.** Industry-wide 2026 axiom. Always
   reachable, never imposing. The user can banish the AI at any
   time. Source: UXDesign.cc, Bricxlabs, all reviewed chatbot
   surveys.

#### Trust

8. **Audit as first-class.** Every agent action is reviewable,
   undoable. Smashing's IAA framework + Salesforce's pivot to
   "agentic experience design" both lean on this hard.
9. **Trust through transparency.** *"When people know why a
   system acted, they feel control even when they don't click."*
   (Smashing Mag, 2026.) Validates that the steward's text-first
   responses + system-row footnotes matter more than the
   navigation animation itself.
10. **Capability honesty.** Don't promise what isn't shipped.
    Rabbit/Humane lost $5B claiming demo capability they couldn't
    deliver.

#### Pragmatic

11. **The phone is fierce competition.** Smartphones are *already*
    powerful — AI must add net value, not substitute for what's
    there. Manual mode is the benchmark.
12. **Demo-grade ≠ ship-grade.** Rabbit R1's lesson. The thing on
    stage is not the thing that works for a real user on day 1.
    Always test against real workflows.

#### Coverage check

Termipod's prototype already implements 1–6 + 8 + 9 + 10. Missing
or partial: **7** (no dismiss-to-hide toggle yet — only
expand/collapse), **11** (test plan calls for it, unmeasured
until QA timing data lands), and **12** is always work in progress.

### 10.6 Three new locks ADR-023 should add

Beyond the 13 open questions in §5, the SOTA research adds three
specific locks that aren't currently captured:

- **Lock IAA explicitly.** Intent Preview + Autonomous Action +
  Audit/Undo as the three pillars, not just Audit alone. Steward
  must restate goal before navigating; user must be able to undo.
  Currently only Audit is wired.
- **Lock dismissal model.** Three states for the overlay, not
  two: **Hidden / Puck / Panel**. Add a Settings toggle. The
  "persistent but optional" axiom is non-negotiable in 2026 UX.
- **Lock the comparison benchmark.** *"Beat manual-mode tap
  count + total time on the demo arc by ≥40%."* The Rabbit
  lesson: if you can't quantify the win, you don't ship. Should
  appear in the test plan + the ADR's "Consequences" section as
  the explicit ship-or-not gate.

---

## 11. References

### Internal

- ADR-005 — UX principal/director model (steward operates the
  system; user directs).
- ADR-021 — ACP capability surface (the negotiation pattern this
  reuses for mobile capabilities).
- ADR-022 — observability surfaces (the cost model the steward's
  UI driving will show up in).
- `spine/information-architecture.md` — 5-tab IA, axiom that the
  overlay must respect.
- `reference/permission-model.md` — confirmation gating that the
  write-intent policy mirrors.
- `how-to/test-agent-driven-prototype.md` — 10-scenario QA plan
  for the v1.0.464-alpha prototype.

### External — feeds §10 SOTA research

**Apple Intelligence + App Intents (iOS):**
- [App Intents — Apple Developer documentation](https://developer.apple.com/documentation/appintents)
- [Integrating actions with Siri and Apple Intelligence](https://developer.apple.com/documentation/appintents/integrating-actions-with-siri-and-apple-intelligence)
- [Get to know App Intents — WWDC25](https://developer.apple.com/videos/play/wwdc2025/244/)
- [Apple Intelligence & Siri in 2026 (Medium)](https://medium.com/@taoufiq.moutaouakil/apple-intelligence-siri-in-2026-fe509d8813fd)

**Google Gemini Agent + Project Astra (Android):**
- [Google preps 'Gemini Agent' as your '24/7 digital partner' (9to5Google)](https://9to5google.com/2026/05/06/gemini-agent-planner-upgrade/)
- [Gemini AI may soon navigate Android apps (Sammy Fans)](https://www.sammyfans.com/2026/02/04/gemini-ai-may-soon-navigate-android-apps-for-users/)
- [Android malware taps Gemini to navigate infected devices (The Register)](https://www.theregister.com/security/2026/02/19/android-malware-taps-gemini-to-navigate-infected-devices/4397008)
- [Google I/O 2026 — Android 17, Gemini AI & Smart Glasses (Eastern Herald)](https://easternherald.com/2026/05/09/google-io-2026-android-17-gemini-smart-glasses/)

**Anthropic Claude Computer Use + Dispatch:**
- [Anthropic's Claude Computer Use Agent (Tech Insider)](https://tech-insider.org/anthropic-claude-computer-use-agent-2026/)
- [Anthropic says Claude can now use your computer (CNBC)](https://www.cnbc.com/2026/03/24/anthropic-claude-ai-agent-use-computer-finish-tasks.html)
- [Anthropic gives Claude computer access from a mobile device (CIO Dive)](https://www.ciodive.com/news/anthropic-claude-computer-access-AI/815730/)
- [Claude API computer use tool docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool)

**ChatGPT Atlas (browser-as-superapp):**
- [ChatGPT Atlas — OpenAI](https://chatgpt.com/atlas/)
- [ChatGPT Atlas vs Perplexity vs Microsoft Edge (Genesys Growth)](https://genesysgrowth.com/blog/chatgpt-atlas-vs-perplexity-vs-microsoft-edge-(copilot-mode))
- [OpenAI's Desktop Superapp: ChatGPT, Codex, and Atlas Merging (ALM Corp)](https://almcorp.com/blog/openai-desktop-superapp-chatgpt-codex-atlas-browser/)

**Rabbit R1 / Humane AI Pin failures (cautionary):**
- [The UX Fails of AI Tech: Rabbit R1 & Humane AI Pin (VAExperience)](https://blog.vaexperience.com/the-ux-fails-of-ai-tech-rabbit-r1-humane-ai-pin/)
- [AI Product Failures 2026: Sora, Humane & Rabbit R1 (Digital Applied)](https://www.digitalapplied.com/blog/ai-product-failures-2026-sora-humane-rabbit-lessons)
- [The Rabbit R1 flop (Medium / Julien Pate)](https://medium.com/@julien_pate/the-rabbit-r1-flop-a-commercial-disaster-or-a-rough-draft-of-our-ux-less-future-9202df67a9e4)
- [With the Humane AI Pin now dead, what does the Rabbit R1 need to do to survive? (TechRadar)](https://www.techradar.com/computing/artificial-intelligence/with-the-humane-ai-pin-now-dead-what-does-the-rabbit-r1-need-to-do-to-survive)

**Agentic UX patterns + the IAA framework:**
- [Designing for Agentic AI: Practical UX Patterns (Smashing Magazine)](https://www.smashingmagazine.com/2026/02/designing-agentic-ai-practical-ux-patterns/)
- [State of Design 2026: When Interfaces Become Agents (Tejj on Medium)](https://tejjj.medium.com/state-of-design-2026-when-interfaces-become-agents-fc967be10cba)
- [How to Embrace the Great UX Paradigm Shift to Agentic Experience Design (Salesforce)](https://www.salesforce.com/blog/ux-shift-to-agentic-experience-design/)
- [Designing User Interfaces for Agentic AI (Codewave)](https://codewave.com/insights/designing-agentic-ai-ui/)
- [Next-Gen Agentic AI in UX Design: Evolving the Double-Diamond Process (UXmatters)](https://www.uxmatters.com/mt/archives/2026/03/next-gen-agentic-ai-in-ux-design-evolving-the-double-diamond-process.php)

**AI-native UX principles:**
- [Intent by Discovery: Designing the AI User Experience (UXTigers)](https://www.uxtigers.com/post/intent-ux)
- [AI Native Interfaces: Designing Beyond Prompts and Workflows (Cobeisfresh)](https://www.cobeisfresh.com/blog/ai-native-interfaces-designing-beyond-prompts-and-workflows)
- [AI-First UI UX Design Principles for zero-click web (Tentackles)](https://tentackles.com/blog/ai-first-ux-ui-principles-zero-click)
- [7 New Rules of AI in UX Design for 2026 (Millipixels)](https://millipixels.com/blog/ai-in-ux-design)
- [UX principles — Apps SDK (OpenAI Developers)](https://developers.openai.com/apps-sdk/concepts/ux-principles)

**Floating overlay / persistent chatbot patterns:**
- [Where should AI sit in your UI? (UX Collective)](https://uxdesign.cc/where-should-ai-sit-in-your-ui-1710a258390e)
- [16 Chat UI Design Patterns That Work in 2026 (Bricxlabs)](https://bricxlabs.com/blogs/message-screen-ui-deisgn)
- [Chatbot Interface Design: A Practical Guide for 2026 (Fuselab)](https://fuselabcreative.com/chatbot-interface-design-guide/)
- [What's Changing in Mobile App Design? UI Patterns That Matter in 2026 (Muzli)](https://muz.li/blog/whats-changing-in-mobile-app-design-ui-patterns-that-matter-in-2026/)

---

## 12. Open question — agent-conjured surfaces (Tier 1 / 2 / 3)

**Status: Open** — added 2026-05-10 from principal Q during the
v1.0.466 build. Frames a missing axis from §5: the prototype lets
the steward *navigate to* surfaces that already exist in the APK
but not *conjure* new ones. The natural extension matches what
Claude.ai artifacts and ChatGPT Canvas do — let the agent emit the
surface, let the client render it.

### 12.1 The framing

Today's URI dispatch (`mobile.navigate`) routes to hand-coded
screens. If the steward wants to show a custom diagram, an
experiment-config form, or a chart that doesn't have a written
screen, it falls back to a transcript text bubble. That ceiling
caps how far the agent-driven mode can go without an APK rebuild.

Three classes of remedy, in order of cost:

### 12.2 The three tiers

**Tier 1 — fenced code blocks rendered visually in transcripts.**
Cheapest. Today the agent's markdown output is rendered via
`flutter_markdown` and ` ``` ` fences appear as code text. Tier 1
intercepts specific languages — `svg`, `html`, future `mermaid` —
and renders them as widgets via the existing
`MarkdownElementBuilder` extension point. No new MCP tools, no
protocol change, security model is per-renderer.

**Tier 2 — WebView artifacts.** Same model as Claude.ai/ChatGPT
Canvas. Agent emits an HTML+CSS+JS document via a new
`artifact.html` MCP tool; hub stores it (extending the existing
artifacts primitive — `project_artifacts_wedge`); mobile opens the
artifact in a `webview_flutter` view inside a `sandbox=""` iframe.
Agents already speak HTML+CSS+JS fluently from training, so prompt
cost is near zero. The hard work is the sandbox: no native bridge,
isolated origin, strict CSP.

**Tier 3 — server-driven UI (SDUI).** A typed JSON schema
describing native widgets — `{type: "form", fields: [...]}` —
parsed at runtime into real Flutter widgets from a fixed
vocabulary (label, button, input, list, chart, image, …). Native
feel, no security holes, but the agent has to learn the schema
and the vocab is forever bound to the APK version. References:
Stac, Mirai, Airbnb's epoxy, Lyft's protobuf-driven UI.

### 12.3 Tradeoff matrix

|  | Tier 1 (fenced code) | Tier 2 (HTML artifact) | Tier 3 (SDUI) |
|---|---|---|---|
| Vocab ceiling | Bounded by chosen renderers | Unlimited | Finite, app-version-bound |
| New surfaces require APK rebuild | Per renderer (rarely) | Never | When primitive added |
| Native feel | Inline (good) | Breaks (web inside app) | Preserved |
| Security model | Per-renderer constrain | Sandbox required | Trivial |
| Agent learning cost | ~0 (ubiquitous code fences) | ~0 (HTML in training corpus) | Has to learn schema |
| Time to "looks like a real app screen" | N/A — inline only | Long (CSS work) | Instant |
| Storage on mobile | Inline-only, ephemeral | Cacheable as sandboxed asset | N/A |
| Agent state across renders | None | Per artifact (localStorage) | Driven by hub |

### 12.4 Cost analysis

APK size delta over the v1.0.466 baseline (~40–50 MB):

|  | Plugin / code | Big optional deps | Realistic total |
|---|---|---|---|
| Tier 1 | +0 (markdown already in) | flutter_svg ~100 KB, flutter_html ~150 KB | **+100–300 KB** |
| Tier 2 | webview_flutter ~250 KB (engine OS-shared) | flutter_inappwebview ~1–2 MB if chosen | **+250 KB – 2 MB** |
| Tier 3 | Renderer ~50–150 KB for ~30 primitives | fl_chart ~500 KB, google_maps ~1 MB | **+200 KB – 3 MB** |

Runtime cost per surface:

|  | First-render | Memory | CPU / battery |
|---|---|---|---|
| Tier 1 | <50 ms | trivial | trivial |
| Tier 2 | 200–500 ms first WebView (engine warm-up); ~50 ms after | 30–80 MB per WebView instance | ~1.5–3× native when JS runs |
| Tier 3 | <50 ms | trivial — same as hand-written | identical to native |

**Where the costs actually hurt.** Tier 2's real cost is memory +
warm-up, not APK; mitigation = single shared WebView swapping
content via `loadHtmlString`. Tier 3's real cost is the libraries
brought in *for* primitives; mitigation = lazy-load big libs only
when first needed. Tier 1 has effectively no runtime cost — it
rides the markdown pipeline that already runs every transcript
paint.

### 12.5 Storage on mobile

For Tier 2 specifically: Flutter's `path_provider` gives the app
sandbox dirs (documents + cache); `dart:io` writes any file at
runtime; `webview_flutter` loads from `file://` paths or in-memory
strings via `loadHtmlString()`. So an agent-emitted HTML artifact
becomes a *reusable app asset* — written once to the sandbox, read
back on subsequent opens, deleted via the artifacts CRUD. Versioning
+ archive + share + "delete if unused for 90 days" all become
standard work on the existing artifacts table; the new artifact
kind is `ui_html` whose payload is the document body.

This is a small architectural win over Claude.ai web: the browser
keeps artifacts in IndexedDB scoped to one conversation; we get
team-scoped, app-sandboxed, network-shareable durability.

### 12.6 Recommended progression

1. **Ship Tier 1 first.** Cheapest, fastest, learns what artifact
   shapes the steward actually wants to produce. Plan:
   [`../plans/agent-artifact-rendering-tier-1.md`](../plans/agent-artifact-rendering-tier-1.md).
2. **Tier 2 if Tier 1 hits expressiveness limits** — e.g. the
   steward keeps wanting to emit interactive previews that
   inline rendering can't carry.
3. **Tier 3 only if Tier 2's web-in-app feel breaks the demo arc.**
   Most teams never need it; the only forcing function is "this
   *must* feel like a real app screen and Tier 2 doesn't."

The picks are different per use case: occasional rich artifact =
Tier 2, everyday native screens that don't yet exist = Tier 3.

### 12.7 ADR-023 implications

Add as a new question alongside the existing 12 (§5):

> **Q14 — Agent-conjured surfaces.** Should the steward be able to
> emit content that the client renders as a visual artifact, beyond
> URI navigation to existing screens? If yes — at which tier (1 / 2
> / 3)? The MVP claim ("steward operates the app for the user")
> implies *some* level; locking the tier is an ADR-023 input.

Three locks the ADR will need to make:

- **Lock the renderer-registry shape** — language string ↔ renderer
  mapping, lazy-loaded, fail-closed (unknown language renders as
  code). Keeps Tier 1 extensible without regressions.
- **Lock the HTML allowlist** — explicit tag whitelist for the
  Tier 1 HTML fence; no `<script>`, `<iframe>`, `<object>`,
  `<embed>`, `<style>`, or `javascript:` URLs. Tier 2 gets a
  separate sandbox decision when it lands.
- **Lock the artifact storage contract** — Tier 2's `ui_html` kind
  reuses the existing artifacts primitive (versioning, sharing,
  retention) rather than minting a parallel store.

## 13. Open question — floating-surface capacity

> Added 2026-05-10 in response to a principal question during
> v1.0.470 QA: "How many overlay/floating widgets does Flutter
> support? There may be many team stewards and other important
> pages needing to float in the future."

### 13.1 Framework ceiling vs UX ceiling

Flutter has **no hard cap** on overlays. `Overlay` hosts an
arbitrary list of `OverlayEntry` instances; our shell is just a
`Stack` in `MaterialApp.builder` and we can add more `Positioned`
children whenever we want. The framework cost is roughly linear in
N — one render pass per overlay, GestureArena entries scale with N,
hit-testing walks the stack top-down.

The **UX ceiling** is much lower than the framework ceiling and
that's the one that matters:

- Each persistent floating chat carries its own SSE stream → `N
  pucks ≈ N concurrent SSE connections + N message lists + N
  drag/resize state machines`. At N=5 the bandwidth cost alone is
  significant on cellular.
- Z-order conflict — who's on top, who steals taps, who can be
  dragged through whom. Tractable for 1-2; combinatorial mess at
  ≥3.
- Visual attention budget — every floating element is a permanent
  claim on the user's eye. Industry SOTA all converge on **one
  visible at a time**:
  - Slack mobile: one floating-DM bubble at a time.
  - iOS Picture-in-Picture: hard one-at-a-time enforced by OS.
  - Apple Intelligence floating prompt: singleton.
  - Discord PiP allows two but immediately docks the rest.

### 13.2 The three patterns we could pick

When the steward count grows (per-project stewards, per-host stewards,
per-member stewards from F-1) plus other "important pages" (active
project chat, ongoing approval thread, live metric stream), the
options are:

**A. N-pucks.** Spawn one puck per floating subject. Naive,
familiar (Messenger Chat Heads), but multiplies SSE cost and
turns the screen into a graveyard of icons.

**B. Single shell, multi-conversation list inside.** One puck →
one expanded panel → list/tab strip of every live conversation
inside the panel. Tap a row to switch which transcript renders.
This is what Slack DMs do.

**C. Edge-dock + single panel.** Tiny icon-rail glued to one
screen edge (left or right). Each icon represents one floating
subject. Tap any icon to swap which conversation occupies the
(single) expanded panel. This is what Discord PiP does on
desktop.

### 13.3 Recommended lock for ADR-023

**Pattern B — single shell, multi-conversation inside.** Reasons:

1. **One SSE stream until expansion** — when collapsed we can keep
   the global steward stream live and lazy-attach others on first
   tap. Bandwidth scales with active conversations, not registered
   subjects.
2. **One drag/resize state machine** survives — we keep the
   v1.0.466 layout-persist scaffolding and don't fork it per
   conversation.
3. **Maps cleanly to the URI grammar.** The router already knows
   how to address every entity (`termipod://stewards/<id>`,
   `termipod://projects/<id>/chat`, `termipod://attentions/<id>`).
   Each list row is just a URI; the panel body becomes "render
   transcript for whatever URI the user picked."
4. **Composes with the agent-driven mode** — the steward can
   `mobile.navigate("termipod://overlay/<uri>")` to set the
   panel's current conversation, the same way it navigates the
   page underneath.

Pattern C (edge-dock) is a future *cosmetic* upgrade if the
conversation list outgrows a vertical scroll. We don't need it for
MVP; defer until N reaches the order where a list view feels heavy
(probably 5+ pinned conversations).

Pattern A (N-pucks) is rejected — it inverts the "one shell, one
attention claim" axiom and the SSE cost is a real concern on
cellular. Documenting the rejection here so we don't drift back.

### 13.4 Q15 for ADR-023

> **Q15 — Floating-surface capacity model.** When floating
> conversations grow beyond the team-general steward (per-project,
> per-host, attention threads, multi-steward), do we expose them as
> N independent pucks, a single multi-conversation panel, or an
> edge-dock + single panel? Locking the model up front prevents the
> N-pucks regression once feature pressure mounts.

The recommended answer is Pattern B; ADR-023 ratifies or revises.
