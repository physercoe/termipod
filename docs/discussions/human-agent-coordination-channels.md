# The human–agent coordination surface: a spectrum

> **Type:** discussion
> **Status:** Open (2026-06-12) — raised from the multi-agent collaboration
> thread: TUI, email, the app attention queue, and a messenger bridge are all
> ways a director and an agent coordinate, each a different point on one
> spectrum. This doc reasons about that spectrum from first principles and
> reflects the current TermiPod design. No primitive is added here; it names
> one for discussion.
> **Audience:** contributors · maintainers
> **Last verified vs code:** v1.0.817
> **Freshness:** snapshot (refresh when an email or messenger adapter ships, or
> when the attention-routing policy in `escalation.go` / `budget.go` changes)

**TL;DR.** A human↔agent **coordination surface** (the *transport* a director
and an agent coordinate over — TUI, email, the app attention queue, a messenger
bridge) is fundamentally an **attention-router across an asymmetry**: agents
emit events continuously and cheaply 24/7, while human attention is scarce,
intermittent, and non-resumable. Every surface is a different trade-off on the
same axes (initiative, synchrony, richness, reach, actionability, durability,
trust, fan-in). The load-bearing rule is that a surface carries **two opposite
flows** — *notify* (agent→human) wants reach; *direct* (human→agent, especially
the `propose→approve` gate) wants authenticated audit — and the two must not be
forced through one pipe. TermiPod has bet correctly on the **rich/authoritative
end** (the mobile cockpit + `attention_items` as a fan-in authority surface) and
has two additive **gaps at the ambient/async end**: no **email** (the night→day
handoff) and no **messenger bridge** (ambient reach). Both are *adapters and
projections* over the canonical hub attention substrate, governed by a
**tier × urgency × clock** routing policy — never new systems of record.

> **Terminology (glossary-first).** This doc says **coordination surface** (or
> *surface*) for the human↔agent *transport*. It deliberately avoids the bare
> word **channel**, which is already a hub primitive — the event-log channel of
> [ADR-019](../decisions/019-channels-as-event-log.md) (see *channel message* in
> [`glossary.md`](../reference/glossary.md)). A surface may *render* a channel,
> but the two are different concepts; §8 asks whether *surface* should itself
> become a named primitive.

> **Scope.** This is the **human↔agent** transport question — how a director
> receives an agent's output and injects direction. It is the sibling of
> [`multi-agent-dev-collaboration.md`](multi-agent-dev-collaboration.md) (which
> coordinates *agents with each other* over a substrate) and
> [`code-native-hub.md`](code-native-hub.md) (the hub as a *code*-aware layer).
> Its direct parents in that doc are
> [§3.8](multi-agent-dev-collaboration.md#38-the-ai-native-critique-github-is-human-native-and-runs-cold)
> (cold vs warm) and §6.2 / §6.4 (the human's clock), reused here on the
> transport axis.

---

## 1. The axiom: a surface is an attention-router across an asymmetry

Strip everything away and a human↔agent surface exists to bridge one structural
asymmetry:

> **Agents produce events continuously, cheaply, around the clock. Human
> attention is scarce, intermittent, expensive, and non-resumable.**

The surface's whole job is to **match the right item to the human's attention at
the right moment, carry enough context to decide, let them act, and leave a
durable authenticated record** — spending as little of that scarce attention as
possible. Every point on the spectrum is a different compromise on *how* it
spends attention.

A second axiom is inherited from the substrate-warmth thread
([§3.8](multi-agent-dev-collaboration.md#38-the-ai-native-critique-github-is-human-native-and-runs-cold)),
now mirrored onto the human:

> **The surface sets the human's re-tokenization tax.** A cold ping ("PR #218
> needs review") forces the director to rebuild context from scratch before
> acting. A warm surface lands them *inside* the relevant transcript turn with
> the decision pre-framed. Coordination cost is dominated by this reload, not by
> the message.

## 2. The surface carries two opposite flows — never conflate them

- **Agent → human (notify):** report, escalate, ask, surface an error or an
  approval. Read-mostly, low-risk; wants **reach + immediacy**. In the hub this
  is the `attention_items` fan-out: a `notice` (answerless FYI → the Me-page
  **Messages** slice) or a `request_*` (awaits a **decision**).
- **Human → agent (direct):** approve, correct, steer, answer. Write,
  high-consequence; wants **authentication + auditability**. In the hub this is
  `POST /decide` and the `propose→approve` governed action
  ([`governed-actions-and-propose-verb.md`](../how-to/agent-collaboration.md)).

The single most load-bearing conclusion: **these two flows want opposite
properties, so they should not share one pipe.** Notifications can be sprayed
widely (cheap, promiscuous); direction — anything crossing the approval gate —
must be funnelled through a trusted, recorded surface. Most surface-design
failures collapse the two.

## 3. The dimensions that *generate* the spectrum

| Axis | Question it answers |
|---|---|
| **Initiative** | Push (interrupts the human) vs pull (human comes to look) |
| **Synchrony / latency** | Live back-and-forth vs async-that-sits (maps to day vs night) |
| **Richness / warmth** | Bandwidth + on-demand drill-in vs a single cold line |
| **Reach** | Meets the human where they *already* are, vs requires adopting/opening a bespoke surface |
| **Actionability** | Act *in place*, vs only be told to go elsewhere |
| **Durability / auditability** | Queryable record vs ephemeral scrollback |
| **Identity / trust** | How strongly is the injector of direction authenticated |
| **Fan-in/out** | One human ↔ many agents/hosts aggregated, vs one-surface-per-agent |
| **Framing** | Process-you-supervise / inbox-you-triage / person-you-chat-with / correspondence |

Every surface is a coordinate in this space. The "spectrum" is the projection
onto the dominant trade-off: **ambient-reach ⟷ richness-and-authority.**

## 4. The spectrum (ambient/reachable → rich/authoritative)

| Surface | Strong on | Weak on | Best use |
|---|---|---|---|
| **Messenger bridge** (Telegram / Slack / WhatsApp / SMS) | reach, push-native, ambient, inline quick-reply | weak auth, low richness, semi-ephemeral, fatigue-prone | **notifications** + low-stakes acks ("yes, continue") |
| **Email** | universal reach, durable, async, considered, batchable | high latency, weak auth, low interactivity | **digests / morning reports**, the **night→day handoff**, async approvals via step-up link |
| **App cockpit / `attention_items`** (TermiPod) | fan-in across the fleet, authenticated, durable, actionable-in-place, warm drill-in, push-capable | lower ambient reach (must adopt + open the app) | **the authority surface** — triage, governed approvals, warm review |
| **TUI** (`hub-tui`, Claude Code) | max synchronous bandwidth + fidelity, fully actionable, immediate | single-seat, ephemeral, desk-bound, no fan-out, no push | director-at-a-desk deep work; the **builder's own warm session** |
| **Breakglass SSH/tmux** | raw power, total fidelity | bypasses governance, least safe/auditable, unstructured | last-resort direct intervention only |

## 5. The day/night overlay

The spectrum is also a **clock axis** (the asymmetry of
[§6.2](multi-agent-dev-collaboration.md), §6.4): TUI and live chat are *daytime
synchronous*; an email digest plus a persistent attention queue are the
*nighttime async* substrate. **The surface choice is the implementation of the
day/night handoff** — overnight, agents batch into the durable `attention_items`
queue and emit a **morning digest**; in the day, the director escalates
synchronously. A surface strategy that ignores the clock either wakes the human
at night or starves the agents by day.

## 6. SOTA / empirical practice

- **On-call alerting (PagerDuty / Opsgenie):** severity-tiered routing,
  escalation policies, push-to-phone, **ack/resolve in place**. The empirical
  scar tissue is **alert fatigue** — undisciplined pushing destroys the surface;
  you *must* tier by severity and batch the low tiers. This directly indicts a
  naive "notify on everything" bridge.
- **ChatOps (Slack + bots, GitHub's Slack app):** act-from-chat with inline
  buttons, durable thread — great reach + actionability, but **weak authority**
  (anyone in-channel can fire actions). Industry answer: **defense-in-depth** —
  high-stakes actions started in chat *bounce to an authenticated surface*.
- **CI/CD approval gates (GitHub Environments "required reviewers"):**
  deliberately **split the notify surface from the act surface** — email/Slack
  tells you; a signed link takes you to the authenticated app to actually
  approve. The canonical "separate notification from authority" pattern.
- **Agent products, revealed preferences:** **Devin** (Cognition) is
  Slack-native (bets on reach); **Codex cloud / Cursor background agents /
  Copilot Workspace** use a web dashboard + the GitHub PR surface (bets on the
  forge); **Claude Code / Codex CLI** are TUI, synchronous, single-session (bets
  on richness for the hands-on operator); **TermiPod** is a bespoke mobile
  cockpit with a structured fleet-wide attention queue (bets on fan-in +
  authority + warmth).
- **HCI foundations:** interruption-and-resumption-lag research (Mark, Gonzalez,
  Czerwinski) quantifies the cost a push imposes; **mixed-initiative
  interaction** (Horvitz, 1999; and §6.7) says the system should interrupt only
  when *expected value > expected cost* — interruption is a decision the router
  should *compute*, not a default.
- **Messaging vs email, empirically:** messaging ≈ minutes-latency, near-100%
  open; email ≈ hours/days. Time-critical → messaging; considered/durable →
  email. Complements, not substitutes.

## 7. Design principles (the synthesis)

1. **Separate notification from authority.** Spray notifications anywhere the
   director is; funnel *direction*, especially `propose→approve`, through an
   authenticated, audited surface. A low-trust surface may *initiate* but must
   **step-up-auth** for anything above `tier:mechanical`
   ([ADR-049](../decisions/049-multi-agent-collaboration-via-github.md) tiers).
2. **Surfaces are projections + adapters over a canonical hub substrate — never
   the system of record.** This mirrors [`code-native-hub.md`](code-native-hub.md)
   ("git stays on the hosts; the hub holds references") and the hub's
   data-ownership law ([`blueprint.md`](../spine/blueprint.md)). Here:
   `attention_items` + events + `audit_events` stay canonical; messenger / email
   / TUI are **views and input-adapters** over them. An item acked in Telegram is
   the *same* hub item resolved in the app — idempotent, hub-mediated — which
   kills split-brain.
3. **Route by tier × urgency × clock — it's a policy, not a surface.** Low-stakes
   FYI → ambient feed / nightly digest. Time-critical block → push + messenger.
   High-stakes approval → app `attention_item` with full warm context. This
   generalizes PagerDuty severity routing and is the natural home of
   `escalation.go` / `budget.go`.
4. **Every notification carries a warm deep-link.** Not "PR #218 needs review"
   but a link that lands the director on the approval card with the inline spec
   rendered, or at the exact transcript turn. Minimize the re-tokenization tax of
   §1. The `attention_item` + Insight deep-link + `mobile_navigate` already
   embody this; any new adapter must **preserve** it (deep-link *back into* the
   cockpit, never re-render cold).
5. **Budget attention explicitly (anti-fatigue).** Coalesce, rate-limit, tier.
   An unbudgeted bridge is a fatigue machine that trains the director to ignore
   it — the PagerDuty lesson, encoded.

## 8. Reflection on the current TermiPod design

**Where it sits.** TermiPod has bet hard on the **rich/authoritative end** — the
mobile cockpit as a *unified, authenticated, fan-in, actionable, durable, warm*
surface: `attention_items` as the structured triage queue (`notice` →
**Messages**, `request_*` → **Requests** awaiting a `decision`), on-device push
as the interrupt, SSE for live, voice for low-friction input, the Insight
transcript for warm drill-in, and `escalation.go` / `budget.go` doing routing.
For a **multi-host / multi-agent fleet** this is the *correct* primary bet: a
per-pane TUI or a per-bot chat does not aggregate across a fleet; a structured
attention queue does. The breakglass SSH/tmux is correctly framed as last-resort
(it bypasses governance — keep it that way). `hub-tui` serves the
director-at-a-desk.

**The two real gaps — both adapters, not new cores:**

- **No async / night surface (email).** The cockpit is pull-with-push; it has no
  durable, batchable, universally-reachable async lane. Email is the natural
  **night→day handoff artifact**: agents work overnight, batch into the attention
  queue, and emit a **morning digest** that deep-links back into the cockpit.
  Email also reaches the director when the app isn't installed or open. This is
  the missing *nighttime-async* half of the §5 clock.
- **No ambient-reach surface (messenger bridge).** The cockpit requires
  *adopting and opening* a bespoke app. A Telegram/Slack adapter meets the
  director where they already live — for **notifications + low-stakes acks**. By
  Principle 1 it must be **notify-and-quick-ack only**, with high-tier approvals
  bouncing to the authenticated cockpit for step-up. That distinction is the
  difference between a reach win and a security hole.

**The recommended shape** falls straight out of Principle 2: keep
`attention_items` / events / `audit_events` as the canonical substrate (they
already are); add **email** and **messenger** as *projection + input adapters*
over it — exactly as A2A is already a relay over the hub — governed by a
**tier × urgency × clock** routing policy (extend `escalation.go` /
`budget.go`). TUI and the app stay first-class clients of the same substrate. No
new system of record; the spectrum becomes a fan-out of one authority core.
This is the architecture TermiPod is *already* shaped for, which is why the gaps
are additive, not structural.

## 9. Open questions / what to prototype

- **Is "coordination surface" a first-class primitive?** Should the hub name a
  *surface* (a routing policy + adapter registry over `attention_items`), or does
  it stay implicit in `escalation.go`? This is the analog of the
  `Change`-vs-`Deliverable` call in [`code-native-hub.md`](code-native-hub.md) —
  worth naming via the term-precision process, not settling casually (and note
  the collision with the ADR-019 *channel* primitive).
- **Start with email.** The cheapest high-value experiment is the **nightly
  digest**: project the day's unresolved `attention_items` into one email that
  deep-links back into the cockpit. It tests Principles 2 and 4 with no new
  authority surface.
- **Then the messenger notify-only adapter.** Fan out `notice` + `request_*` to a
  bridge with inline ack for `tier:mechanical`, and a step-up bounce for
  anything higher — measuring the reach win against the fatigue cost (Principle
  5).
- **Routing policy schema.** What exactly does tier × urgency × clock evaluate
  to, and where does the director configure it (a Settings surface over
  `budget.go`)?

This doc **stays Open**; it resolves into an ADR (or folds into one) once the
*surface*-as-primitive question (§8) is settled and the email-digest experiment
has validated the projection-adapter shape.

---

## See also

- [`multi-agent-dev-collaboration.md`](multi-agent-dev-collaboration.md) — the parent thread; §3.8 (cold vs warm), §6.2 / §6.4 (the human's clock), §6.7 (mixed-initiative).
- [`code-native-hub.md`](code-native-hub.md) — the projection-adapter pattern (metadata not bytes), reused here for surfaces over the attention substrate.
- [`blueprint.md`](../spine/blueprint.md) — the data-ownership law the adapters must not violate.
- [ADR-019 — Channels as event log](../decisions/019-channels-as-event-log.md) — the `channel` primitive this doc disambiguates *surface* from.
- [ADR-049 — Multi-agent collaboration via GitHub](../decisions/049-multi-agent-collaboration-via-github.md) — the `tier:` ladder reused by the routing policy.
- [`agent-collaboration.md`](../how-to/agent-collaboration.md) — the propose→approve gate carried by the authority surface.
