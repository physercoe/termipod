# Agent-driven mobile UI — overlay steward + intent channel

> **Type:** discussion
> **Status:** Open
> **Audience:** principal · contributors · reviewers
> **Last verified vs code:** v1.0.463

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

## 10. References

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
- Apple App Intents (iOS 16+) — OS-level prior art for the
  whitelisted-verb pattern.
- Android App Actions / Gemini extensions — comparable Android
  prior art.
