---
name: The orchestration contract
description: Frames termipod's multi-agent layer as the top of a stack of typing disciplines — LLM → inference decoding → agent harness → orchestration — where each layer is a contract that types the layer below from potential into directed behaviour. The orchestration layer is the only one with no designed contract: it has band-aids (v1.0.626/630 body prefixes). Enunciates what "typing" means (form < type < schema < contract < protocol); notes the stack extends below the LLM and that a contract is two-sided — schema plus the disposition to honour it. Argues the forward path (message-routing-to-agents / ADR-032) and the return path (feedback-loop-closure) are one bidirectional contract; specifies the concrete envelope `{from,to,kind,text,cause,thread}` with a four-value kind set, a lineage `cause` field as the spine, hub-server composition, and a lifecycle walkthrough ratifying the design. Records the 2026-05-18/19 decisions: drop the legacy shim, Layer-B closure enforcement is MVP, realization efficiency is the design metric.
---

# The orchestration contract

> **Type:** discussion
> **Status:** Open (2026-05-19) — raised because [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
> is scoped as a delivery mechanism (retire the A2A band-aids) when
> the work is really the orchestration layer's missing type system.
> Sits above [`message-routing-to-agents.md`](message-routing-to-agents.md),
> [ADR-032](../decisions/032-message-routing-envelope.md), and
> [`feedback-loop-closure.md`](feedback-loop-closure.md) — it does not
> replace them; it frames them as one contract and specifies it.
> ADR-032 was revised and ADR-034 drafted from this discussion
> (2026-05-19); both Proposed.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** Termipod is a stack of *typing disciplines*. An LLM is a
token distribution — pure potential. The inference engine's role and
control-token labels type that stream into a *turn-taking mind*. The
agent harness types the mind into an *agent*. Each layer is a
**contract** that converts the layer below from potential into
directed behaviour, by *typing* it. The **orchestration layer** — many
agents plus humans, coordinated to realize a principal's directive —
is the top of that stack, and it is **the only layer with no designed
contract.** It has band-aids: v1.0.626 collapsed message kinds,
v1.0.630 stuffed `[A2A from @sender]` into the body as prose — layer-3
running without role labels. This doc argues: (1) the message-routing
work is the orchestration layer's missing type system, not an A2A bug
fix; (2) the forward path (routing *to* agents) and the return path
(routing *back to* the principal) are **one bidirectional contract**;
(3) a **lineage field** — every message carrying the directive it
serves — is the contract's spine, closing the loop *and* making
efficiency measurable. It enunciates what "typing" means (§2),
specifies the concrete envelope `{from,to,kind,text,cause,thread}`
(§6), and ratifies it against the project lifecycle (§8). It resolves
[`feedback-loop-closure.md`](feedback-loop-closure.md)'s §9 Q1 and
recommends re-scoping [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
before W1.

---

## 1. The stack: each layer types the layer below

A useful way to see termipod: a stack where every layer is a contract,
and a contract is a *typing discipline* — an agreed scheme for what the
bytes of the layer below **mean**, plus the obligations each party
takes on. Typing is what lets the next layer up treat the thing as a
structured object rather than an undifferentiated stream.

| Layer | Raw material | Contract imposed | What it becomes |
|---|---|---|---|
| **L0 — LLM** | token distribution | — | pure potential, maximum entropy |
| **L1 — inference decoding** | token stream | role labels (`system`/`user`/`assistant`), stop, thinking + tool spans | a *mind taking turns* |
| **L2 — agent harness** | turns | tool schemas, event loop, hooks, state machine, permission gate | an *agent* — senses, acts, remembers, can be interrupted |
| **L3 — orchestration** | a set of agents + humans | **(this doc's subject)** | a *directed system* that realizes an intent |

The load-bearing claim — and it is the principal's, restated — is that
**the constraint is what creates the capability.** An untyped token
stream is potential and nothing else; you cannot act on it, only
sample it. Role labels are what let you read the stream as a
conversation. Tool schemas are what let the conversation act on the
world. At each step a contract *narrows* what the layer below could be,
and that narrowing is precisely what makes it *useful* to the layer
above. Potential becomes behaviour by being typed.

L0 through L2 each have a designed, deliberate contract — in the
engines, and in termipod's drivers and harness. **L3 does not.** The
agents exist; the humans exist; what coordinates them into a system
that realizes a directive is, today, a set of band-aids. That gap is
this doc's subject.

## 2. What "typing" means

§1 calls each layer a "typing discipline." Precisely:

A **type** is a *classification that licenses operations and carries
guarantees.* To *type* a stream is to assign its parts to categories
such that the layer above knows what it can **do** with each part and
what **holds** of it. A raw token stream is untyped — you can only
sample it. Label spans `user` / `assistant` / `tool` and you have
*typed* it: now "take the user's turn," "extract the tool call,"
"stop at the assistant boundary" are well-defined operations. The type
is what makes those operations *exist*.

Typing is **lossy and intentional.** A token stream carries
near-unlimited latent structure; role-labelling *selects* the
speaker/turn distinction as load-bearing and discards the rest. To
type is therefore to **choose which distinctions matter** — and that
choice is the design act. §1's slogan is exact: the right choice of
distinctions creates the operations the next layer needs; a wrong or
absent choice leaves potential unrealized. The orchestration contract
is a choice of which distinctions in agent-to-agent communication are
load-bearing — `from`, `kind`, `cause` (§6).

The neighbouring terms, ordered from least to most committed:

- **Form** — structure / shape alone, with no commitment to meaning or
  operations. A JSON object has form. Form is *necessary* for type,
  not *sufficient*.
- **Type** — form *plus* the operations it licenses and the guarantees
  it carries. Typing imposes an *interpretation* on the layer below.
- **Category** — the bins a type sorts into. Informally, a bucket;
  formally (category theory) objects plus the morphisms between them —
  apt, since a type system *is* a category: types are objects,
  licensed operations are morphisms. This doc uses the informal sense.
- **Schema** — a *written specification* of a type: the concrete field
  list. The envelope in §6.1 is the written schema of the L3 message
  type.
- **Contract** — a schema *plus the obligation of each party to honour
  it.* The schema is the classification; the contract adds mutual
  commitment. (See §2(c) of §3 — a contract is two-sided.)
- **Protocol** — a contract *plus sequencing*: the state machine of
  who sends what, when.

So "**each layer types the layer below**" means: each layer imposes a
*chosen interpretation* on the raw stream beneath it — a classification
that licenses the operations the layer above needs. The
producer-and-consumer-agreed version of that interpretation is a
*contract*; the contract plus its temporal ordering is a *protocol*.
The orchestration layer needs all three: a message **type** (the
envelope schema), agreed as a **contract** (both ends honour it), run
as a **protocol** (the loop — open → advance → close, §8).

## 3. The stack has no bottom — what is below the LLM

§1's table starts at L0, the LLM, as if it were bedrock. It is not.
"The LLM is a token stream — without labels, just potential" is true
*looking up*; looking *down*, that token distribution is itself what
you get when you **type a corpus.** The discipline recurses below L0:

| Layer | Raw material | Typing discipline | What it becomes |
|---|---|---|---|
| L0 — LLM (deployed) | — | — | a distribution conditioned to act as an assistant |
| L−1 — post-training | base model | RLHF / instruction tuning | an *assistant* — disposed to honour roles + instructions |
| L−2 — pretraining | tokenized corpus | autoregressive next-token objective | a *base model* — corpus statistics internalized |
| L−3 — architecture | differentiable computation | transformer: attention, residual stream, positions | a *sequence model* |
| L−4 — tokenizer | byte / character stream | a fixed vocabulary + segmentation | *tokens* |
| ↓ below the model | a corpus | writing — persistence, transmissibility | a *corpus* |
| | language-in-use | language — discretizing experience into shared symbols | *meaning* |
| | the world | physical regularity | *structure to be tracked* |

Three consequences, and the third is a design decision, not a
curiosity.

**(a) There is no raw bottom.** Every layer is *potential* relative to
the layer above and *structure* relative to the layer below. A corpus
is structured text beside noise, but pure potential beside a trained
model. "Raw" and "typed" are not properties of a layer — they are
roles a layer plays in a relation. So "the orchestration layer is a
contract" (§4) was never a special claim. It is the *universal* one.
We notice it at L3 only because L3 is the single place termipod's
contract is missing.

**(b) The seam runs through the LLM.** Everything *below* L0 is a
**learned, statistical, soft** typing discipline — gradient descent
installs dispositions that hold only *probabilistically*. Everything
*above* L0 — control tokens, tool schemas, the orchestration envelope
— is **symbolic, imposed, hard** — a parser enforces it exactly. The
LLM is the **transducer** between the two regimes. That is why it
feels pivotal though it is structurally a middle layer: it is the
boundary where statistics becomes symbol. It is also why an agent
harness is the engineering feat it is — it bolts hard symbolic
scaffolding onto a soft statistical core, and the join holds only in
the zone the soft core was trained to be compatible with.

**(c) A contract is two-sided — schema *and* disposition.** A typing
discipline at layer N binds only if layer N−1 was shaped to honour it.
The label `user:` is an inert string; it does work *only because* L−1
post-training installed the disposition to treat what follows it as
instruction. The label is the **schema**; post-training is the
**obligation**. This is the §1 definition — contract = schema +
obligations — observed from below: post-training is *how the
obligation half is compiled into the model.*

Run that upward and it becomes a design instruction. The envelope (§6)
is the L3 **schema**. What installs the L3 **obligation** — an agent's
disposition to honour the envelope? Two mechanisms, and the contract
needs **both**: a *soft* one — the prompt layer (the rollout's W4) —
and a *hard* one — runtime enforcement at the hub (§6.5, §10). A schema
with no disposition to honour it is the `[A2A from @sender]` prefix
again: structure no party is bound to read.

A closing observation. The recurring verb of the whole stack is
**routing**. Attention routes between tokens (soft, learned). The
harness routes between tool calls. Orchestration routes between agents
(hard, symbolic). It is one operation — *deciding what attends to
what* — refracted through the soft and hard regimes at every scale.
Message-routing is not *a* feature of the orchestration layer; routing
is the operation every layer performs, and L3 is where termipod must
make it explicit.

## 4. The orchestration layer is a contract — and it is missing

What would the L3 contract type? The thing that crosses agent
boundaries: **messages.** A message between two agents, or between an
agent and the principal, is the L3 stream. Untyped, it is L3's
equivalent of a raw token stream — the receiver must *guess* its
meaning from prose.

That is exactly the state today. [`message-routing-to-agents.md`](message-routing-to-agents.md)
documents it: three semantically distinct sources — principal direct,
peer A2A, system wake — all collapse into one `input.text` event whose
`producer` column never reaches the engine. v1.0.630's `[A2A from
@sender]` prefix is the tell: the receiving agent recovers provenance
by *pattern-matching English prose it hopes to notice*. That is L3
running the way L1 would run if you deleted the role labels and asked
the model to infer from wording who was speaking. It works until it
doesn't, and it cannot be measured, audited, or reasoned about.

[ADR-032](../decisions/032-message-routing-envelope.md)'s envelope is
the first piece of the L3 contract. This reframing matters because it
changes the bar the work is held to. As a bug fix, the envelope is
done when the band-aids are gone. As a contract, it is done when L3
messages are as well-typed as L1 turns: every message carries who sent
it, what kind of act it is, and what it is in service of.

## 5. Forward and return are one contract, not two

There are four docs in this cluster and they fragment one thing:

- [`message-routing-to-agents.md`](message-routing-to-agents.md) — discussion, forward path.
- [ADR-032](../decisions/032-message-routing-envelope.md) — decision, forward path.
- [`message-routing-rollout.md`](../plans/message-routing-rollout.md) — plan, forward path.
- [`feedback-loop-closure.md`](feedback-loop-closure.md) — discussion, return path.

The first three route messages *to* agents. The fourth routes signal
*back to* the principal. They are treated as separate problems —
`feedback-loop-closure.md` even calls itself ADR-032's "symmetric
half." This doc's claim: **they are not two halves, they are one
contract, and designing them apart is the mistake.**

A protocol that types the forward path and leaves the return path
untyped is a **half-duplex contract.** Control theory is blunt about
the consequence: you cannot regulate a system without feedback. A
directive issued into a system with no typed return path is an
open-loop command — fire and hope. The principal's own framing in
`feedback-loop-closure.md` — *"if there is something wrong, principal
should know instead of debugging"* — is the demand for a closed loop.

The unit of the orchestration layer is therefore not the message, it
is **the loop**: directive → action → observable outcome → signal back
→ correction. Forward and return are the same loop traversed in two
directions, and they are the **same envelope with different field
values** — a message is `{from, to, kind, cause, …}` whether it
carries a directive outward or an outcome back. If the return path is
designed separately, it will grow its *own* band-aids — the precise
failure mode that produced the A2A prefix hack. The v1.0.626 and
v1.0.630 incidents are not two bugs; they are one missing contract
billed twice.

## 6. The envelope: the schema, and who fills it

This is the concrete result of the 2026-05-18/19 design conversation —
the L3 message type.

### 6.1 The schema

```
{
  from:   { role, handle, agent_id },   // who sent it
  to:     { role, handle, agent_id },   // who receives it
  kind:   "directive" | "question" | "report" | "notification",
  text:   "<message body>",
  cause:  <ULID> | null,                // lineage — the directive this serves
  thread: { transport: "session" | "a2a" | "attention", id: <ULID> }
}
```

`role` ∈ `principal | peer_steward | peer_worker | system` — ADR-032
D-1's taxonomy, unchanged (it is *source-of-message* semantics, an
axis orthogonal to L1's *turn-position* `user/assistant`). Both
endpoints are explicit: a return message and the directive-trace both
need `from` *and* `to`.

This **revises ADR-032 D-1** (`{from, text, reply_via, thread}`): adds
`to` and `kind`, adds the lineage field `cause`, drops `reply_via` as a
stored field (it is derived — §6.5), and makes `thread` a tagged union
rather than three nullable siblings.

### 6.2 `kind` — the four illocutionary forces

`kind` is what the sender is *doing* by sending the message. It is a
**closed enum**, reasoned not by speech-act aesthetics but by what the
loop machinery must decide — does this message *open*, *close*,
*advance*, or *not touch* a directive's loop?

| `kind` | Meaning | Loop effect | Examples |
|---|---|---|---|
| `directive` | "do this" | **opens** a loop | principal→steward; steward→worker dispatch; A2A delegation; schedule fire |
| `question` | "I need an answer/decision to proceed" | opens a **blocking** sub-loop | worker→steward escalation; steward→principal approval |
| `report` | "here is an outcome / progress" | **advances or closes** a loop | worker→steward result; steward→principal status |
| `notification` | "the system informs you of a state change" | loop-**neutral** | stall escalation; operator action; infra event |

The set has a deliberate shape — **two openers, one closer, one
neutral.** It is complete: every message source in termipod's
lifecycle maps to exactly one (ratified in §8), and none is unused.
`kind` is **orthogonal to `from.role`** — the *authority* of a
directive (a principal command vs a peer's collaboration ask) comes
from `from.role`, not `kind`. The enum is closed but extensible by a
future ADR; an unknown `kind` falls back to `notification` (the inert
one), never to `directive`.

Two candidates were considered and dropped: `acknowledgement` —
receipt is a hub-observable transport fact, not an agent-authored
message; `signal` / progress-tick — `feedback-loop-closure.md` §5.4's
progress-tick is UI-only and never enters model context, so it is not
a message in this contract.

### 6.3 `cause` — the lineage reference

`cause` is the contract's **spine**. A bidirectional envelope is not
enough: messages can flow both ways and the loop still be *open*,
because the principal receives outcomes with no way to tell *which
directive each one closes*. `cause` fixes that.

`cause` is **neither an enum nor free-text — it is a reference** (a
foreign key): the ULID of the directive/task entity the message
belongs to. It is **hub-validated** (admission stage 1 — §10 — checks
it resolves to a live entity). It is nullable: `null` means the
message is not tied to a tracked directive (incidental chat, a bare
infra `notification`); every `directive`/`question`/`report` carries
one. The directive *tree* is reconstructed by walking `entity.parent`
links (single parent pointer — the 2026-05-19 Q1 resolution); a
denormalized root id may be added later if rollup queries need it.

`cause` must **not** be a new primitive — it *is* the Task identity
([ADR-029](../decisions/029-tasks-as-first-class-primitive.md)) or the
principal-directive `correlation_id` (`feedback-loop-closure.md` §6.1)
propagated. Glossary discipline: this is correlation propagation
stamped onto the envelope, not a new concept.

### 6.4 `thread` — transport, not lineage

`thread` and `cause` are different fields, and conflating them is
exactly the current schema's failure.

- **`thread`** = the **transport / conversational channel** the
  message rides — *which ongoing exchange* it belongs to at the
  delivery layer. Used for delivery, rendering continuity, resubscribe
  dedup.
- **`cause`** = the **causal lineage** — which directive it realizes.
  Used for loop closure, the directive trace, efficiency metrics.

They are orthogonal — proof by example: a worker's terminal `report`
and the original principal `directive` are in **different threads**
(the directive was typed in a chat session; the report returns via an
A2A task) but share the **same `cause`**. Conversely two messages in
the **same thread** can serve **different causes**. A transport handle
does not thread a directive across hops — the thread changes at every
hop; the `cause` does not. **All lineage lives in `cause`; `thread`
carries none.**

### 6.5 Three rules: closure, derivation, authorship

**Closure.** A `report` whose `cause = E` from **E's assignee** closes
E **when it carries a terminal outcome** (it sets E's
`terminal_reason`); a `report` carrying a non-terminal outcome —
`blocked`, interim progress — *advances* E without closing it.
`directive` and `question` open loops; a terminal `report` closes
them; `notification` touches none. A
`question` is closed the same way: its answer is a `report` with
`cause` = the question entity. Abnormal closes (timeout, cancel) are
not `report`s — the Layer-B runtime terminates the loop with a reason
(the `feedback-loop-closure.md` §9 Q-T taxonomy) and surfaces it as a
`notification`. **Self-echo** (a message with `from == to`, e.g. a
fan-out steward seeing its own posts) is filtered before routing and
excluded from the trace — it never counts as loop activity.

**Derivation.** `reply_via` is *not* a stored field and the agent
never computes it. The hub derives it deterministically from `from` +
`thread.transport` + `kind`, and **renders it as an explicit
instruction** into the agent-facing text ("Reply via:
`a2a.invoke(handle="…")`"). For a `notification` the rendering states
the *kind's contract* — "informational re directive X; no reply is
routed; act if it concerns work you own" — **not** a bald "no reply
expected," which would wrongly imply no *action* is expected.

**Authorship.** The hub composes the **entire** envelope; the agent
authors **zero** envelope fields. The agent contributes only `text`
and the *choice of affordance* (§6.6). This is load-bearing: it is
*why* the contract can be enforced — if agents filled the schema there
would be nothing to enforce, and `from`/`cause` would be unverifiable.

### 6.6 Who composes it, and how `kind` is determined

Composition is on **hub-server** (`internal/server`) — the process
that owns the `agent_events` table; the three write sites
(`handlers_agent_input.go` principal input, `tunnel_a2a.go` A2A relay,
`task_notify.go` system wakes) all live there. The **host-runner**
(`internal/hostrunner`) does *not* compose: its `input_router.go`
polls the `agent_events` rows and the driver *renders* them per
engine. It cannot compose authoritatively — A2A crosses hosts (a
steward on a VPS → a NAT'd worker), and only hub-server owns the
directive/task registry that `cause` requires. So: **compose +
admission-gate = hub-server; render + deliver = host-runner driver.**

`kind` is inferred from the **tool the agent calls** — verified
against both tool registries, no tool schema carries an explicit
`kind` field, but tool *identity* determines it:

| affordance | → `kind` |
|---|---|
| principal `/input` | `directive` (MVP: always) |
| `a2a_invoke` / `delegate` | declared by the call's explicit `kind` parameter |
| `request_help` / `request_select` / `request_approval` | `question` |
| `tasks_update(status=…)` / `tasks_complete` / turn-completion | `report` |
| hub-originated | `directive` (schedule fire) · `notification` (escalation, operator, infra) |

`a2a_invoke` and `delegate` carry an **explicit `kind` parameter**
(`directive | question | report`; `notification` is system-only). The
agent declares intent; the hub validates it against the admission
pipeline (§10) and stamps the envelope — [ADR-032](../decisions/032-message-routing-envelope.md)
D-6. This replaces inferring A2A directive-vs-report by reply-detection
heuristics: an explicit, hub-validated declaration is the tighter
design (2026-05-19 decision). It is still consistent with §6.5's
authorship rule — the agent picks a typed tool with a typed argument;
the hub composes and validates.

## 7. Realization efficiency is the design metric

The principal's claim: *the efficiency of the system's operating — how
easy, how fast, how cheaply a directive is realized — is itself an
indicator of the level of the design.* This gives the orchestration
layer a **quality metric** it has so far lacked.

Name it **realization efficiency**: the cost — in latency, hop count,
clarification round-trips, human interventions — to convert a
principal directive into a realized, observed outcome. Two
sub-measures:

- **Loop latency** — wall-clock from directive issued to outcome
  observed by the principal.
- **Loop fidelity** — how much signal survives the hops. A steward
  that relays a worker outcome without comprehending it is a
  degradation node (`feedback-loop-closure.md` §6.7, "synthesis is not
  relay"); fidelity measures that loss.

The orchestration layer's design quality *is* these numbers — a
property of the *contract*, not of the models. This is what the
principal meant by the system's "intelligence," better stated as
realization efficiency.

Crucially: **this metric is unmeasurable without the `cause` field of
§6.3.** You cannot compute loop latency for a directive whose
returning signals you cannot attribute to it. So the envelope must be
designed as an *instrumentation surface*, not just a delivery surface:
lineage-tagged messages let [ADR-022](../decisions/022-observability-surfaces.md)'s
insights compute per-directive realization efficiency directly. The
contract that delivers the work is the same contract that measures
whether the design of the work is any good.

## 8. Lifecycle walkthrough — ratifying the design

The schema is only ratified if every message in a project lifecycle
maps cleanly onto it. The canonical path — **principal directs a
mission, steward spawns a worker, worker does it, replies back, turn
ends**:

| # | message | `from` | `to` | `kind` | `cause` | `thread` | loop effect |
|---|---|---|---|---|---|---|---|
| 1 | principal: "Build feature X" | principal | steward | `directive` | D1 (minted) | session | **opens D1** (root) |
| 2 | steward dispatches the task | peer_steward | worker | `directive` | T1 (minted, parent=D1) | session | **opens T1** |
| 3 | worker finishes | peer_worker | steward | `report` | T1 | a2a | **closes T1** |
| 4 | steward synthesizes to principal | peer_steward | principal | `report` | D1 | session | **closes D1** |

The loop closes; realization efficiency = latency from msg 1 to msg 4,
fidelity = whether msg 4 preserved msg 3's result. Note the happy path
uses only `directive` and `report` — `question` and `notification` are
for the variants.

**Variant scenarios**, each still `{from,to,kind,cause,thread}`:

- **Worker blocked.** Msg 3 is a `report` (`kind=report`, `cause=T1`)
  carrying a `blocked` outcome — it *advances* T1 to `status=blocked`,
  which is **still open** (`blocked` is a live status, not a terminal
  reason). The steward then re-dispatches a new `directive` or
  escalates to the principal (`report`, `cause=D1`).
- **Worker needs a decision mid-task.** worker→steward `question`
  (`cause=T1`) opens sub-entity Q1 (parent=T1); steward→worker `report`
  (`cause=Q1`) closes Q1; the worker resumes T1. The steward's answer
  is a `report` even though its text says "do A" — it *closes the
  question*; genuinely new work would be a separate `directive`.
- **Fan-out.** The steward dispatches T1a/T1b/T1c (three `directive`s,
  all parent=D1). Three `report`s close them. D1 closes only when the
  steward emits its own `report` with `cause=D1` — a sibling's report
  closes *that sibling*, not the parent.
- **Stall.** A worker goes silent past its deadline; the Layer-B
  runtime detects it and the hub emits a `notification` to the steward
  (`cause=T1`); if the steward is also silent, a `notification` to the
  principal (`cause=D1`). No reply is routed — but the rendering says
  "act."
- **Schedule fire.** system→steward `directive`, `cause` = a freshly
  minted directive entity for the scheduled work.

Every case lands on the four-value `kind` set with no leftover. The
design covers the lifecycle.

## 9. Terms

The principal invited sharpening; the convention requires it
(`docs/reference/glossary.md` is canonical for collision-prone terms).
The form / type / schema / contract / protocol ladder is enunciated in
§2. In addition:

- **"Feedback loop"** is overloaded. Separate the **control loop** —
  directive → outcome → correction, the thing being regulated — from
  the **return transport** — the messages carrying signal back.
  `feedback-loop-closure.md`'s Layer A / Layer B split already does
  this; keep it.
- **"Intelligence of the system"** — avoid the word; it imports the
  model-capability sense. The intended concept is **realization
  efficiency** (§7): a property of the contract.
- **Proposed new terms** — *orchestration contract*, *lineage field* /
  `cause`, *realization efficiency*. When this resolves into an ADR,
  these go to `glossary.md` for review before they harden.

## 10. What this means for the in-flight work

The next session was set to start [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
W1. **Do not start W1 as scoped** — the plan is right for a delivery
mechanism and under-scoped for a contract. The re-scope:

1. **The envelope is `{from,to,kind,text,cause,thread}` from W1** —
   bidirectional, lineage-bearing. ADR-032 D-1 is revised to §6.1.
2. **ADR-032 evolves** (it is still `Proposed`, so revisable rather
   than superseded): widen it from "envelope metadata on `input.text`"
   to "the orchestration contract." A companion ADR covers the
   Layer-B liveness runtime.
3. **Layer-B closure enforcement is MVP** (2026-05-19 decision, Q7) —
   not deferred. The contract needs **hard runtime enforcement**, not
   only the soft prompt. Three tiers, all MVP:
   - *soft* — the prompt (W4): installs the agent's *comprehension* of
     the envelope.
   - *hard, gate* — a hub-server **message-admission pipeline** at the
     compose boundary (borrowing Claude Code's permission pipeline:
     `validateEnvelope → routing-legality → context`, "fail safe not
     correct," `deny > allow`; ADR-016's worker→non-parent A2A block is
     already a deny rule of this kind).
   - *hard, lifecycle* — hub-side orchestration **hooks** (borrowing
     Claude Code's hook system): `PreMessageDeliver`, `PreAgentIdle`
     (refuse idle while open directives exist — the loop-closure
     invariant), `PostDirectiveOutcome` (synthesis-not-relay check).
4. **The L2 affordances encode `kind`** — §6.6. `a2a_invoke` and
   `delegate` gain an explicit `kind` parameter (2026-05-19 decision);
   the remaining tools map to a `kind` by identity.
5. **Drop the legacy shim** (2026-05-19 decision, Q4) — solo use, no
   legacy data. ADR-032 D-4 is *deleted*: no backward-compat shim, no
   v1.1.0 cutoff. The envelope is the only accepted body shape from the
   rollout commit; cut over on a drained hub.
6. **The contract is stated as a `spine/` axiom** —
   [`spine/orchestration-layer.md`](../spine/orchestration-layer.md),
   written 2026-05-19 (Q5). It serves blueprint axioms A1 and A3. The
   axiom states the *invariants*; ADR-032 and ADR-034 hold the
   revisable schema and runtime. The synthesis pass behind it is the
   Appendix.
7. **Re-wedge `message-routing-rollout.md`** against this scope.

This **resolves `feedback-loop-closure.md` §9 Q1** ("one ADR or
two?"): one *contract*. The forward and return envelope are one
schema, one ADR. The Layer-B runtime is a separable second ADR — but
it ships in the same MVP rollout, not after it.

## 11. Open questions

The 2026-05-18/19 discussion resolved the design; the decisions are
now recorded in **[ADR-032](../decisions/032-message-routing-envelope.md)**
(revised — the envelope + admission pipeline) and
**[ADR-034](../decisions/034-orchestration-loop-closure.md)** (drafted
— the loop-closure runtime). Resolved: lineage as a single parent
pointer (Q1); hub-server composition (Q2); the W1 schema (Q6 → §6.1);
drop the shim (Q4); Layer-B closure enforcement in MVP (Q7); the
self-echo case (the closure rule, §6.5); A2A `kind` — an explicit
`kind` parameter on `a2a_invoke`/`delegate`, not heuristic
reply-detection (ADR-032 D-6); the deadline clock — a hub-server sweep
(ADR-034 D-3); escalation — one level up the chain, reaching the
principal (ADR-034 D-4). The spine-synthesis pass (Q5) is done — see
the Appendix.

Genuinely still open:

- **`cause` for incidental messages.** Is trivial principal chatter
  modelled as a degenerate `directive` (closes instantly) or as
  `cause = null`? Minor; deferred to implementation.
- **Deadline-default tuning.** ADR-034 sets per-hop deadlines from the
  agent family / template; the default *values* want on-device
  calibration — post-MVP.
- **Layer-A awareness surfaces** — the principal's inbox, read-state,
  and the Requests/Messages/Agents reframe (`feedback-loop-closure.md`
  §5). A separate mobile/IA track: it *consumes* ADR-034's runtime but
  is not yet designed or ADR'd. `feedback-loop-closure.md` §9 Q2/Q6
  (read-state granularity, notification budget) live here.

## 12. Recommendation

1. Treat the message-routing work as **the orchestration layer's type
   system**, not an A2A bug fix. The bar is "L3 messages as well-typed
   as L1 turns."
2. Adopt the envelope **`{from,to,kind,text,cause,thread}`** (§6) —
   one bidirectional schema, `cause` as the spine, `reply_via` derived,
   hub-server composed.
3. Enforce the contract in **three tiers — soft prompt + hard
   admission gate + hard lifecycle hooks — all MVP** (§10). A
   half-enforced contract is the half-duplex problem again.
4. Adopt **realization efficiency** (loop latency + loop fidelity) as
   the design-quality metric; design the envelope as an instrumentation
   surface so the metric is computable.
5. **Hold `message-routing-rollout.md` W1** until it is re-wedged
   against [ADR-032](../decisions/032-message-routing-envelope.md)
   (revised) + [ADR-034](../decisions/034-orchestration-loop-closure.md);
   lock both ADRs after on-device verification.

---

## Appendix — Spine placement (synthesis pass)

The 2026-05-19 spine-synthesis pass, reconciling the orchestration
contract against the existing `spine/` axiom docs — the Q5
prerequisite.

**Finding: no collision; one gap.**

- **`spine/protocols.md`** types every *inter-component edge* by
  relationship type (control / supervision / RPC / peer / observation)
  and names the protocol per edge (A2A on the agent↔agent peer edge,
  MCP on the agent↔hub RPC edge, …). It says *which protocol on which
  edge*. The orchestration contract says *what a message carries* —
  the payload schema and the loop protocol riding those edges.
  Complementary, not colliding: protocols.md is the edge layer, the
  orchestration contract is the message layer on top. protocols.md §1
  (relationship types) and §8 (A2A topology) gain a one-line
  forward-reference; no rewrite.
- **`spine/information-architecture.md`** is mobile surfaces and
  attention tiers; it has no message/envelope concept (verified). No
  collision. The return path's *Layer A* (inbox, read-state, the
  Requests/Messages/Agents reframe — `feedback-loop-closure.md` §5)
  *is* IA work and reconciles into information-architecture.md when
  Layer A is built — deferred, flagged.
- **`spine/blueprint.md`** axioms are unchanged. The orchestration
  contract derives from **A1** (filtering — realization efficiency
  economizes scarce attention) and **A3** (governance — the admission
  pipeline and loop closure bound stochastic agents). The new axiom
  cites them; it does not amend them.

**Recommendation: a new `spine/` axiom doc**, written once
[ADR-032](../decisions/032-message-routing-envelope.md) and
[ADR-034](../decisions/034-orchestration-loop-closure.md) are Accepted
(an axiom states *decided* architecture; the ADRs are Proposed).
protocols.md was itself extracted from `blueprint.md` §5; a sibling
axiom for the message + loop contract fits that precedent.

**Two cautions for that axiom:**

1. **Naming.** "Orchestration" is already used informally — the
   steward is "manager / orchestrator" (blueprint §3.3), and ADR-008
   is the orchestrator-worker pattern. The axiom must define "the
   orchestration contract" / "orchestration layer" precisely against
   that usage, or pick a distinct term (candidates: *the message
   contract*, *the directive loop*). Resolve in `glossary.md` first.
2. **The L0–L3 typing stack is a lens, not an ontology.** blueprint's
   component ontology is Hub / Host-runner / Agent. The L0–L3 stack
   (§1) is an orthogonal *typing* decomposition. The axiom must
   present it explicitly as a lens, not as a competing component
   model.

The synthesis pass is **done**, and the axiom —
[`spine/orchestration-layer.md`](../spine/orchestration-layer.md) —
was written 2026-05-19: it states the invariants of the orchestration
layer, while ADR-032 and ADR-034 (Proposed) hold the revisable schema
and runtime.
