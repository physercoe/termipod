# Orchestration layer

> **Type:** axiom
> **Status:** Current (2026-05-19)
> **Audience:** contributors
> **Last verified vs code:** v1.0.631-alpha

**TL;DR.** Termipod coordinates many agents and a human into one
*directed system* that realizes a principal's intent. That
coordination is a **layer** — the orchestration layer — and like
every layer beneath it (the inference engine's turn labels, the agent
harness's tool schemas) it is a **contract**: a typed discipline over
what crosses agent boundaries. The unit of the layer is not the
message, it is the **loop** — directive → action → outcome → signal
back → correction. This axiom states the layer's invariants. The
concrete message schema is [ADR-032](../decisions/032-message-routing-envelope.md);
the concrete loop-closure runtime is [ADR-034](../decisions/034-orchestration-loop-closure.md);
the full design rationale is [`orchestration-contract.md`](../discussions/orchestration-contract.md).
This axiom is stable; those ADRs hold the revisable specifics.

---

## 1. The layer

Termipod is a stack of **typing disciplines**. An LLM is a token
distribution — pure potential. The inference engine types it with role
and control labels into a turn-taking mind. The agent harness types
*that* with tool schemas, an event loop, and a permission gate into an
*agent*. The **orchestration layer** is the top of that stack: it
types a *set of agents and humans* into a *directed system that
realizes an intent*. Each layer is a contract that converts the layer
below from potential into directed behaviour by **typing** it —
imposing a chosen classification that licenses the operations the
layer above needs.

This is the layer [`protocols.md`](protocols.md) does not cover.
protocols.md types the *edges* between components — which protocol
rides which edge (A2A on the agent↔agent peer edge, MCP on the
agent↔hub RPC edge). The orchestration layer types the **payload and
the loop** that ride those edges: *what a message is*, and *how a
directive is carried to a closed outcome*. Edges and payload are
orthogonal; both are needed.

**"Orchestration layer" is a layer, not a component, and not a role.**
The component ontology — Hub, Host-runner, Agent
([`blueprint.md`](blueprint.md) §3) — is a different decomposition.
The steward "orchestrates" as its *role*; the orchestration layer is
the *contract within which it does so*. The layer is a lens, not a
fourth box.

## 2. The contract — messages are typed

Everything that crosses an agent boundary — principal→agent,
agent→agent, agent→principal, system→agent — is a typed **message
envelope**, not prose. An untyped message is the orchestration layer's
equivalent of a raw token stream: the receiver must guess its meaning.
The v1.0.626 / v1.0.630 band-aids (collapsed kinds, an `[A2A from
@sender]` body prefix) were exactly that — the layer running without
its type labels.

The envelope carries **who sent it** (`from`), **who receives it**
(`to`), **what kind of act it is** (`kind` — directive / question /
report / notification), the body, **what directive it serves**
(`cause` — the lineage reference), and its transport thread. The
concrete schema, the four-value `kind` set, and the rules of
composition are [ADR-032](../decisions/032-message-routing-envelope.md).

Two invariants hold regardless of the schema's details:

- **The hub composes; the agent authors no envelope fields.** Source,
  lineage, and routing are facts the hub knows and the agent would
  only guess. Composition is therefore the single point where the
  contract is *enforced* — see §4.
- **The envelope is bidirectional.** The forward path (a directive
  outward) and the return path (an outcome back) are the *same*
  contract with different field values. A contract that types only the
  forward path is half-duplex, and you cannot regulate a system
  without feedback.

## 3. The loop is the unit

The unit of the orchestration layer is not the message — it is the
**loop**: a directive opens it, work advances it, a terminal report
closes it. `cause` is what makes the loop expressible: every message
names the directive it serves, so the tree of delegated and
fanned-out work threads back to one originating intent.

Two invariants govern the loop:

- **The loop-closure invariant.** A directive is not `done` until a
  terminal report carrying its `cause` has reached the issuer's inbox
  — transitively, the principal's for a root directive. The hub tracks
  the open set and structurally refuses to let a directive vanish.
- **No node may be a silent sink.** Every hop — principal→steward,
  steward→worker, and back — can swallow a directive. Liveness is
  therefore a *system* guarantee, not a per-agent hope: a hop that
  goes quiet past its deadline escalates one level up, ultimately to
  the principal. The principal learns that something is wrong instead
  of discovering it by debugging.

The runtime that enforces these — per-hop deadlines, the deadline
sweep, stall escalation, lifecycle hooks, the terminal-reason
taxonomy, the directive trace — is [ADR-034](../decisions/034-orchestration-loop-closure.md).

## 4. Enforcement is hard, not hoped

A contract is two-sided: a schema, and the disposition of each party
to honour it. The schema alone is inert — an envelope no agent is
bound to read is the band-aid prefix again. The orchestration layer
lives in the **symbolic, deterministic regime** (it is above the
stochastic LLM), so its enforcement must be deterministic too. It is
not safe to enforce a contract of this layer with only a soft
mechanism — a prompt — because a prompt is a per-agent hope.

Enforcement has three tiers:

- **Soft** — the persona prompt installs an agent's *comprehension* of
  the envelope and the loop.
- **Hard, gate** — a deterministic **message-admission pipeline** at
  the hub compose boundary validates and routing-checks every
  envelope, fail-safe (a malformed message degrades to a defined safe
  state, never a crash, never a silent drop).
- **Hard, lifecycle** — hub-side hooks at loop events refuse to let an
  agent abandon an open directive and check that an outcome is
  synthesized, not blindly relayed.

The soft tier makes an agent a *good* participant; the hard tiers make
the contract *true* regardless of any single agent's behaviour.

## 5. Realization efficiency is the measure

The quality of the orchestration layer's design is **realization
efficiency**: the cost — in latency, hop count, clarification
round-trips, human interventions — to convert a principal directive
into a realized, observed outcome. It is a property of the *contract*,
not of the models. Because every message carries `cause`, this is a
*measured* quantity, not a vibe: the envelope is an instrumentation
surface, and per-directive loop latency and fidelity feed
[ADR-022](../decisions/022-observability-surfaces.md)'s insights. A
design that closes loops quickly and without losing signal is a good
one; one that needs many round-trips is not.

## 6. Derivation from the system axioms

The orchestration layer is forced by [`blueprint.md`](blueprint.md)'s
axioms:

- **A1 (human attention ≪ agent output).** Realization efficiency
  *is* the economy of scarce attention; the return path and the
  loop-closure invariant exist because the principal cannot watch
  every hop and must instead be reached when it matters.
- **A3 (agents are stochastic executors with authority).** The
  admission pipeline and the loop-closure runtime are the rule that
  exists *before* the action — they bound a stochastic agent's
  messaging and guarantee its directives resolve. A soft prompt alone
  would leave the bound to chance.

A2 (work spatially bound to compute) shapes *where* enforcement runs:
composition is on hub-server, because A2A crosses hosts and only the
hub sees both ends and owns the directive registry.

## 7. Cross-references

- [ADR-032](../decisions/032-message-routing-envelope.md) — the
  concrete message envelope and admission pipeline.
- [ADR-034](../decisions/034-orchestration-loop-closure.md) — the
  concrete loop-closure runtime.
- [`../discussions/orchestration-contract.md`](../discussions/orchestration-contract.md)
  — the full design rationale (the typing-stack enunciation, the
  lifecycle walkthrough, the spine-synthesis pass).
- [`../discussions/feedback-loop-closure.md`](../discussions/feedback-loop-closure.md)
  — the half-duplex diagnosis behind §3.
- [`protocols.md`](protocols.md) — the edge layer this rides on top of.
- [`blueprint.md`](blueprint.md) — the three system axioms §6 derives from.
