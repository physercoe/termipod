---
name: The orchestration contract
description: Frames termipod's multi-agent layer as the top of a stack of typing disciplines — LLM → inference decoding → agent harness → orchestration — where each layer is a contract that converts the layer below from potential into directed behaviour. The orchestration layer is the only one with no designed contract: it has band-aids (v1.0.626/630 body prefixes). Notes the stack extends below the LLM (the model is itself typed out of a corpus by training) and that a contract is two-sided — a schema plus the disposition to honour it — so the envelope schema and the prompts that teach it are one design. Argues the forward path (message-routing-to-agents / ADR-032) and the return path (feedback-loop-closure) are not two problems but one bidirectional contract; that a lineage/correlation field threading every message back to its originating directive is the contract's spine and the thing that makes loop efficiency measurable; and that realization efficiency — the cost to turn a directive into a realized outcome — is the design-quality metric for this layer. Recommends re-scoping the message-routing work as the orchestration contract rather than an A2A bug fix, and resolves feedback-loop-closure §9 Q1.
---

# The orchestration contract

> **Type:** discussion
> **Status:** Open (2026-05-19) — raised because [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
> is scoped as a delivery mechanism (retire the A2A band-aids) when
> the work is really the orchestration layer's missing type system.
> Sits above [`message-routing-to-agents.md`](message-routing-to-agents.md),
> [ADR-032](../decisions/032-message-routing-envelope.md), and
> [`feedback-loop-closure.md`](feedback-loop-closure.md) — it does not
> replace them; it frames them as one contract. No ADR locked.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** Termipod is a stack of *typing disciplines*. An LLM is a
token distribution — pure potential. The inference engine's role and
control-token labels (`system`/`user`/`assistant`, stop, thinking and
tool spans) type that stream into a *turn-taking mind*. The agent
harness — tool schemas, event loop, hooks, state machine — types the
mind into an *agent* that senses, acts, and remembers. Each layer is a
**contract** that converts the layer below from potential into
directed behaviour, by *typing* it. The **orchestration layer** — many
agents plus humans, coordinated to realize a principal's directive —
is the top of that stack, and it is **the only layer with no designed
contract.** It has band-aids: v1.0.626 collapsed message kinds,
v1.0.630 stuffed `[A2A from @sender]` into the body as prose. That is
layer-3 running without role labels. This doc argues three things:
(1) the message-routing work is the orchestration layer's missing type
system, not an A2A bug fix; (2) the forward path (routing *to* agents)
and the return path (routing *back to* the principal) are **one
bidirectional contract**, not two docs; (3) a **lineage field** —
every message carrying the directive it serves — is that contract's
spine, the thing that closes the loop *and* makes the system's
efficiency measurable. It also notes the stack has no raw bottom (the
LLM is itself typed out of a corpus) and that a contract is two-sided
— a schema plus the disposition to honour it — so the envelope and the
prompts that teach it are one design, not two wedges. It resolves
[`feedback-loop-closure.md`](feedback-loop-closure.md)'s §9 Q1 and
recommends re-scoping [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
before W1 starts.

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

## 2. The stack has no bottom — what is below the LLM

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
contract" (§3) was never a special claim. It is the *universal* one.
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

Run that upward and it becomes a design instruction. ADR-032's
envelope is the L3 **schema**. What installs the L3 **obligation** —
an agent's disposition to actually read and honour the envelope?
**The prompt layer.** That is what the rollout's W4 ("teach
per-persona prompts to read the envelope") really is. So the schema
wedge and the prompt wedge are not sequential niceties — they are the
*two halves of one contract*, exactly as control-token syntax and
post-training are the two halves of L1. **A schema with no disposition
to honour it is the `[A2A from @sender]` prefix again: structure no
party is bound to read.** Whether a *soft* disposition (a prompt) is a
sufficient obligation, or the contract also needs *hard* enforcement,
is §9 Q7.

A closing observation. The recurring verb of the whole stack is
**routing**. Attention routes between tokens (soft, learned). The
harness routes between tool calls. Orchestration routes between agents
(hard, symbolic). It is one operation — *deciding what attends to
what* — refracted through the soft and hard regimes at every scale.
Message-routing is not *a* feature of the orchestration layer; routing
is the operation every layer performs, and L3 is where termipod must
make it explicit.

## 3. The orchestration layer is a contract — and it is missing

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

[ADR-032](../decisions/032-message-routing-envelope.md)'s envelope —
`{from, text, reply_via, thread}` with a 4-role taxonomy — **is the
first piece of the L3 contract.** This reframing matters because it
changes the bar the work is held to. As a bug fix, the envelope is
done when the band-aids are gone. As a contract, it is done when L3
messages are as well-typed as L1 turns: every message carries who sent
it, what kind of act it is, and what it is in service of.

## 4. Forward and return are one contract, not two

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
directions, and they should be the **same contract with different
field values** — a message is `{from, to, kind, caused_by, ...}`
whether it carries a directive outward or an outcome back. If the
return path is designed separately, it will grow its *own* band-aids —
the precise failure mode that produced the A2A prefix hack. The
v1.0.626 and v1.0.630 incidents are not two bugs; they are one missing
contract billed twice.

## 5. The spine: the lineage field

A bidirectional envelope is necessary but not sufficient. Messages can
flow both ways and the loop can still be *open* — because the
principal receives a stream of outcomes with no way to tell *which
directive each one closes*.

The fix is one field. Every message carries the directive it serves: a
**lineage field** — `caused_by` / `in_reply_to` / `intent_id` — a
correlation identifier that threads the whole tree of delegated and
fanned-out work back to the originating directive. It does three jobs
at once:

1. **It closes the loop.** A returning outcome is not "some report"; it
   is "the terminal signal for directive X." This is
   `feedback-loop-closure.md` §6.8's loop-closure invariant made
   mechanical: the hub holds the set of open directives and a directive
   is not `done` until a terminal signal *carrying its lineage* reaches
   the principal's inbox.

2. **It makes efficiency measurable** (see §6).

3. **It must not be a new primitive.** Termipod already has Task
   ([ADR-029](../decisions/029-tasks-as-first-class-primitive.md), the
   first-class unit of steward-dispatched work), `correlation_id` on
   channels ([ADR-019](../decisions/019-channels-as-event-log.md)),
   `parent_agent_id`, and `request_id`. The lineage field should *be*
   the directive/Task identity propagated, not a fifth parallel
   identifier. Glossary discipline: this is correlation propagation, not
   a new concept — it is `feedback-loop-closure.md` §6.1 stamped onto
   the envelope rather than left as a runtime afterthought.

**A design heuristic falls out of §1.** Since each layer types the
layer below, the L3 envelope should be *isomorphic to the L1 turn it
sits above*: a clear *who* (role), a clear *what kind* (directive /
report / question / signal), a clear *in service of what* (lineage).
The agent receiving an envelope is itself an LLM trained on turn-typed
text; an envelope shaped like the turn structure it already parses
costs it almost no comprehension overhead. This argues *against*
exotic envelope fields and *for* keeping the schema small and
turn-shaped — which is also ADR-032 D-4's "no input polymorphism"
instinct, generalized.

## 6. Realization efficiency is the design metric

The principal's claim: *the efficiency of the system's operating — how
easy, how fast, how cheaply a directive is realized — is itself an
indicator of the level of the design.* This is correct and it gives
the orchestration layer a **quality metric**, which until now it has
lacked.

Name it **realization efficiency**: the cost — in latency, in hop
count, in clarification round-trips, in human interventions — to
convert a principal directive into a realized, observed outcome. Two
sub-measures:

- **Loop latency** — wall-clock from directive issued to outcome
  observed by the principal.
- **Loop fidelity** — how much signal survives the hops. A steward
  that relays a worker outcome without comprehending it is a
  degradation node (`feedback-loop-closure.md` §6.7, "synthesis is not
  relay"); fidelity is the measure of that loss.

The orchestration layer's design quality *is* these numbers. A
contract that produces low latency and high fidelity for a directive
is a good design; one that needs many round-trips and loses signal is
a poor one — independent of how clever any single agent is. This is
what the principal meant by the system's "intelligence," and it is
better stated as realization efficiency, because it is a property of
the *contract*, not of the models.

Crucially: **this metric is unmeasurable without the lineage field of
§5.** You cannot compute loop latency for a directive whose returning
signals you cannot attribute to it. So the lineage field is not only
how the loop closes — it is how the orchestration layer becomes
*observable* at all. The envelope should therefore be designed as an
instrumentation surface, not just a delivery surface: lineage-tagged
messages let [ADR-022](../decisions/022-observability-surfaces.md)'s
insights compute per-directive realization efficiency directly. The
contract that delivers the work is the same contract that measures
whether the design of the work is any good.

## 7. Terms

The principal invited sharpening; the convention requires it
(`docs/reference/glossary.md` is canonical for collision-prone terms).

- **Contract / protocol / constraint** are not synonyms. A *contract*
  is the schema plus obligations — what fields exist, what each party
  promises. A *protocol* is a contract plus sequencing — the state
  machine of who sends what, when. A *constraint* is the *effect* — the
  possibilities the contract forecloses. ADR-032 should be read as
  defining a **message contract** (the envelope) and a **routing
  protocol** (hub-side compose and deliver); "constraint" is the
  consequence, not the artifact.
- **"Feedback loop"** is overloaded. Separate the **control loop** —
  directive → outcome → correction, the thing being regulated — from
  the **return transport** — the messages carrying signal back.
  `feedback-loop-closure.md`'s Layer A / Layer B split already does
  this; keep it.
- **"Intelligence of the system"** — avoid the word; it imports the
  model-capability sense. The intended concept is **realization
  efficiency** (§6): a property of the contract.
- **Proposed new terms** — *orchestration contract*, *lineage field*,
  *realization efficiency*. If this discussion resolves into an ADR,
  these go to `glossary.md` for review before they harden; flagged here
  per the glossary-first convention.

## 8. What this means for the in-flight work

The next session was set to start [`message-routing-rollout.md`](../plans/message-routing-rollout.md)
W1. This frame says: **do not start W1 yet.** The plan is correctly
scoped for a delivery mechanism and under-scoped for a contract. The
re-scope:

1. **The envelope is bidirectional from W1.** The schema
   ([ADR-032](../decisions/032-message-routing-envelope.md) D-1) gains a
   lineage field and is designed to carry return messages as well as
   forward ones — same schema, both directions. `reply_via` already
   half-anticipates this (`feedback-loop-closure.md` §5.2 reads it as
   the Request-vs-Message hinge).
2. **`feedback-loop-closure.md` folds in.** Its Layer A (inbox,
   read-state, the three-tab reframe) is the return-path *application*
   of the same envelope; its Layer B (deadlines, stall escalation,
   directive trace) is the *runtime* that the lineage field makes
   possible.
3. **ADR-032 evolves.** It is still `Proposed`, so it can be revised
   rather than superseded: widen it from "envelope metadata on
   `input.text`" to "the orchestration contract — a bidirectional,
   lineage-bearing message contract." Alternatively a sibling ADR
   covers the Layer-B runtime. Either way both derive from **one
   envelope schema** — that is the non-negotiable.
4. **Schema and disposition ship together.** Per §2(c) the contract is
   the envelope schema *and* the disposition to honour it. The schema
   wedge (W1) and the prompt wedge (W4) are halves of one contract,
   co-designed — not "build it, then teach it." Whether the prompt is a
   *sufficient* disposition installer or hard runtime enforcement is
   also needed is §9 Q7.
5. **The rollout plan is re-wedged** against the widened scope after
   this discussion settles.

This **resolves `feedback-loop-closure.md` §9 Q1** ("one ADR or two?").
Answer: one *contract*. The forward envelope and the return envelope
are the same schema and must ship from one ADR. The Layer-B liveness
runtime (deadline clock, escalation, trace view) is separable and *may*
be a second ADR — because it is runtime, not schema — but it is not a
separate contract.

## 9. Open questions

Resolved in the 2026-05-19 discussion:

- **Q1 — lineage as tree or pointer?** *Resolved:* a single parent
  pointer (`caused_by`) per message for MVP; the directive tree is
  reconstructed by walking parents. A denormalized root `intent_id`
  may be added later if directive-level rollup queries need it.
- **Q2 — where is the contract enforced / who stamps lineage?**
  *Resolved:* hub-stamped. A2A tunnels through the hub relay, so the
  hub is on the path of every message — principal input, A2A, system
  wake — and can stamp lineage end-to-end, even to a NAT'd host. Any
  agent-supplied `caused_by` is an optional hint the hub validates.
- **Q5 — naming / where the contract lives.** *Resolved:* the contract
  lands as an axiom under `spine/` — it is a system-level design
  serving blueprint axiom 1 (*Human attention ≪ agent output*). The
  ADR holds the decision rationale; the schema is reference.
  **Prerequisite:** a spine-synthesis pass reconciling the new axiom
  against `spine/protocols.md` and `spine/information-architecture.md`,
  which already hold overlapping protocol / IA material.

Still open:

- **Q3 — self-echo and the loop.** The rollout's W6 self-echo filter
  and the loop-closure invariant interact: a message an agent sent to
  itself, or a fan-out sibling's outcome, both carry lineage — which
  count as *closing* a directive vs noise in the trace?
- **Q4 — cutoff for the legacy shim.** ADR-032 D-4 names v1.1.0 for
  dropping the plain-string shim. Does the shim window cover the
  widened bidirectional + lineage schema, or does lineage ship
  shim-free because it is new?
- **Q6 — is the current W1 envelope schema the best-defended design?**
  ADR-032 D-1's `{from, text, reply_via, thread}` was locked *before*
  this reframe. Under the bidirectional + lineage + two-sided-contract
  lens its rationale needs a fresh review: is `reply_via` the right
  hinge for a return message, is the 4-role taxonomy complete in both
  directions, does `thread` subsume the lineage field or sit beside it?
- **Q7 — enforcing the obligation: soft prompt vs hard runtime.**
  §2(c): a schema binds only if a disposition honours it. Is the prompt
  layer (W4) a sufficient disposition installer, or does the contract
  need *hard* runtime enforcement — validation gates, hooks,
  interception at the hub / host boundary — so a mis-addressed or
  unrouted message fails fast instead of silently degrading? Prior art
  to weigh: Claude Code's permission pipeline (four-stage `validateInput
  → rule match → checkPermissions → prompt`, `deny > ask > allow`) and
  hook system (lifecycle events with a `decision: block` /
  `updatedInput` JSON protocol). See the [`feedback-loop-closure.md`](feedback-loop-closure.md)
  appendix for the same book's coordinator-pattern borrows.

---

## 10. Recommendation

1. Treat the message-routing work as **the orchestration layer's type
   system**, not an A2A bug fix. The bar is "L3 messages as well-typed
   as L1 turns," not "the band-aids are gone."
2. Design **one bidirectional contract.** The forward path
   ([`message-routing-to-agents.md`](message-routing-to-agents.md)) and
   the return path ([`feedback-loop-closure.md`](feedback-loop-closure.md))
   are one loop; one envelope schema serves both directions.
3. Make the **lineage field** the spine — directive identity
   propagated, reusing Task / `correlation_id`, not a new primitive. It
   closes the loop and makes realization efficiency observable.
4. Adopt **realization efficiency** (loop latency + loop fidelity) as
   the orchestration layer's design-quality metric, and design the
   envelope as an instrumentation surface so the metric is computable.
5. Design **schema and disposition together** — the envelope and the
   prompts (and any hard enforcement) that bind agents to it are one
   contract, per §2(c).
6. **Hold `message-routing-rollout.md` W1**; revise
   [ADR-032](../decisions/032-message-routing-envelope.md) to the
   widened scope; resolve §9's remaining open questions (Q3, Q4, Q6,
   Q7); then lock the ADR and re-wedge the plan.
