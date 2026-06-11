# Making the AI-native hub fit for code

> **Type:** discussion
> **Status:** Open (2026-06-11) — raised from the multi-agent dev-collaboration
> thread: the hub is an AI-native coordination substrate but not *code*-native,
> while GitHub is code-native but human-native. This doc reasons about closing
> that gap. No primitive is added by this doc; it proposes one for discussion.
> **Audience:** contributors · maintainers
> **Last verified vs code:** v1.0.817
> **Freshness:** snapshot (refresh when the hub gains a code-change primitive,
> or when the dev-coordination substrate decision changes)

**TL;DR.** "Code's own primitives" are **git's** (commit / branch / diff /
merge / blame), not **GitHub's** — GitHub is one *human-native coordination
layer* over git. So the hub doesn't become code-native by absorbing git; it
becomes code-native by providing a **warm, agent-shaped coordination layer over
git** where GitHub's is cold and human-shaped. The hub already owns the
*coordination half* (steward→task orchestration, Runs, propose→approve,
events/attention, A2A, **references**, session lineage) and lacks only the
*code half*: a first-class **`Change`** primitive (a proposed, reviewable,
mergeable revision that references git objects living on the hosts). Add that
one primitive, keep git on the hosts (preserving the "owns names + events +
references, **not bytes**" law), and reuse everything else. Two architectures
follow — **(A)** hub-over-git as the native end-state, **(B)** hub as a warm
side-channel *over* GitHub as the near-term hybrid — chosen by work-shape.

> **Scope.** This is about the **product hub** becoming code-native — coordinating
> *agents that write code on the director's behalf*. It is the mirror of
> [`multi-agent-dev-collaboration.md`](multi-agent-dev-collaboration.md), which
> is about coordinating *this repo's own development* on GitHub. That doc's §3.8
> ("GitHub is human-native, and runs cold") is the direct parent of this one:
> there the AI-native answer was "durable forge + a warm channel"; here we ask
> what it takes for the hub to **be** the code-aware warm layer.

---

## 1. Reframe: code's primitives are **git's**, not GitHub's

The load-bearing move. When we say code "has its own primitives," those
primitives — **commit, branch, diff, merge, blame** — belong to **git**, which
is already agent-friendly: content-addressed, distributed, mergeable, and driven
by agents every day. **GitHub is not those primitives.** GitHub is one
*coordination layer* stacked on git: pull requests, issues, line-comments,
review threads, the merge button, CI wiring. And *that* layer is the part that
is human-native — asynchronous, document-grained, and **cold** (a participant
reconstructs context from a thread before acting).

So the question is not "how does the hub absorb code/git." It is: **how does the
hub provide a coordination layer over git that is warm and agent-shaped, instead
of cold and human-shaped?** Git stays the byte-versioning substrate — and it
stays on the **hosts**, which is exactly what the hub's data-ownership law wants
(the hub "owns names, policies, events, references — metadata, *not bytes*"; see
[`blueprint.md`](../spine/blueprint.md)).

## 2. What code-native coordination actually requires

GitHub's value *over raw git* is seven coordination primitives:

1. A versioned source-of-truth (git itself).
2. The **diff** as a first-class reviewable unit.
3. A **merge / integration gate** (with hot-file conflict handling).
4. **CI / verification** against a proposed change.
5. **Line-anchored review**.
6. **Blame / provenance**.
7. The **pull request** — the object bundling 2–6 into one reviewable unit.

A code-native hub must offer all seven. The claim of this doc is that it already
offers six of them in other clothing.

## 3. The hub already has the *coordination half* — it lacks only the *code half*

Set the hub's existing primitives against what code coordination needs:

| Code-coordination need | The hub already has |
|---|---|
| Decompose → execute → integrate | **Steward → Task → worker** — orchestrator-worker, native ([ADR-029](../decisions/029-tasks-as-first-class-primitive.md), [`tasks-as-first-class-primitive.md`](tasks-as-first-class-primitive.md)) |
| Verification with metrics/artifacts | **Run** |
| A typed approval / merge gate | **propose → approve** governed actions + **audit_events** ([`governed-actions-and-propose-verb.md`](governed-actions-and-propose-verb.md)) |
| Review / escalation surface | **attention_items** + **events** |
| Warm, fine-grained relay into a *live* builder | **A2A** + **persistent sessions** (the warmth GitHub structurally lacks) |
| Reference code without holding its bytes | **references** — a first-class hub primitive |
| Provenance richer than git-author | **agent / session lineage** |

The hub is missing essentially **one** primitive: a first-class **`Change`** — a
proposed, reviewable, mergeable code revision — plus the git-reference plumbing
to anchor it. Everything else in the list is **reuse**, not new construction.

## 4. The answer: add one primitive, reference git on the hosts, reuse the rest

Introduce a hub **`Change`** primitive: a proposed code revision that references
a **base + head commit** (by *reference* — the bytes live in git on the host
where the host-runner already owns the agent's worktree), carrying warm review
state. Wire the seven needs to existing machinery:

| Code primitive | Hub realization |
|---|---|
| Versioned truth | **git on the host**; the hub holds **references** to commits/branches |
| Diff / PR | the new **`Change`** primitive (base→head refs + review state) |
| Merge gate | **`propose(change.merge)` → approve → host-runner performs the git merge**; the ARB-style hot-file baton becomes a hub-held lock |
| CI | a verification **Run** against the change; result → event / attention_item |
| Line review | review as **events anchored to (file, line, commit)** — but **relayed warm** into the builder's live session over A2A, not a cold thread |
| Blame / provenance | git blame **plus** which agent-session / task / plan-step produced it |
| Coordination object | `Change` + its merge-action + its verification Run + its review-events |

**The hub never becomes a git host.** Git stays on the hosts; the hub owns the
*coordination metadata about* the change. The data-ownership law ("metadata, not
bytes") is preserved — and that is precisely why the **references** primitive is
the keystone: a `Change` is a *reference pair plus review state*, never a byte
store.

## 5. Two architectures — and the choice is work-shaped

**Option A — Hub-over-git (the native end-state).** The `Change` primitive +
git-on-hosts + reuse of Runs/propose/events/A2A. Review is warm (relayed into
live sessions), the merge gate is typed governance, CI is a Run, and the whole
flow surfaces in the mobile cockpit beside projects/tasks/runs. *Best when:*
internal agent-fleet code work where warmth, cross-host / NAT-piercing reach,
typed governance, and a unified director surface matter.

**Option B — Hub as the warm side-channel *over* GitHub (near-term hybrid).**
Keep GitHub as the durable, cold substrate (PRs, CI, public audit); make the hub
the **warm relay layer** on top — it mirrors PR state as hub events and streams
review into live builder sessions. This is literally the
[§3.8](multi-agent-dev-collaboration.md#38-the-ai-native-critique-github-is-human-native-and-runs-cold)
hybrid ("durable forge + warm channel") with the hub *as* the channel. *Best
when:* you want GitHub's ecosystem, durability, and external-contributor reach,
and only need to buy back warmth. Far lower build cost than A.

**Read:** **B is the pragmatic next step** — it cashes in the warmth win without
rebuilding diff/merge/review UX — and **A is the long-term native form** for
fully-internal agent code work. Choose by work-shape, exactly as the parent doc
concluded for substrates in general. A third option (dual-write federation:
every change is *both* a hub `Change` and a GitHub PR, kept in sync) is the most
complex and probably not worth it.

## 6. Why bother — and when GitHub still wins

**Upside GitHub structurally cannot match:**

- **Warm review** — notes relayed into live context, no cold reload (the
  dominant correction cost in
  [§3.8](multi-agent-dev-collaboration.md#38-the-ai-native-critique-github-is-human-native-and-runs-cold)).
- **Typed, audited governance** — `propose(merge)` capturing the *reasoning*,
  not a comment thread plus a convention.
- **Unified mobile cockpit** — code changes first-class beside projects /
  agents / runs, on the director's phone.
- **Executable learning goes native** — a recurring review finding becomes a hub
  **policy / Run check** enforced fleet-wide, not a prose line (the terminal form
  of the teaching loop, dev-collab doc §6.12).
- **Richer provenance** — which agent, session, task, plan-step produced the
  change, beyond git's author field.
- **(Forward-looking) semantic / structural diff & review** — diff at the
  behaviour/AST level, not just lines.

**When GitHub still wins — don't over-build:**

- **Open-source / human-collaborative** work *wants* the human, document-grained
  surface and GitHub's network effects (external contributors, integrations, a
  durable public record).
- GitHub's diff/merge/review UX is fifteen years polished; re-creating it is a
  large, low-margin build.
- [ADR-049](../decisions/049-multi-agent-collaboration-via-github.md)
  deliberately keeps *this repo's* development on GitHub to avoid building
  product infra for dev coordination.

So a code-native hub is **additive for the product's agent fleets** — it does
**not** replace GitHub for human OSS work.

## 7. The naming / ontology question (glossary-first)

The proposed primitive needs a precise, non-colliding name —
[`glossary.md`](../reference/glossary.md) is canonical and `Run`, `Artifact`,
`Deliverable`, `Document` are already taken. GitHub's "pull request" and
GitLab's "merge request" are both *coordination-object* names. Candidates:
**`Change` · `Changeset` · `Revision` · `Patch`**. A second, deeper question:
is a code change a **new** primitive, or a **specialization of `Deliverable`**
(a coding Task's ratifiable output)? It rhymes with Deliverable (a unit of
directed output under review) but carries diff/merge/conflict semantics
Deliverable does not. This is a genuine ontology call for the glossary, not one
to settle casually — flag it for the term-precision process before any
implementation.

## 8. Open questions / what to prototype

- **Start with B.** The cheapest experiment is the **warm side-channel over
  GitHub**: mirror an `agent/*` PR's state into hub events and relay a review
  note into a live builder session, measuring the token saving from skipping the
  cold reload. If the warmth win is real, it justifies more.
- **The merge mechanics of A.** Where exactly does the git merge run (host-runner
  on the head's host?), how does the hot-file baton become a hub lock, and how is
  a conflict surfaced as an attention_item?
- **Reference integrity.** A `Change` referencing host-side commits must survive
  host churn / GC / respawn — what does the hub guarantee about a reference whose
  bytes moved or vanished?
- **The `Change` ↔ `Deliverable` decision** (§7) gates the schema.
- **Does this collapse into the existing surfaces?** A coding Task whose
  Deliverable is a `Change`, verified by a Run, merged by a governed action —
  most of the lifecycle may already be expressible; the prototype should test how
  much is genuinely new vs naming.

This doc **stays Open**; it resolves into an ADR (or folds into one) only once
the naming/ontology call is made and Option B's warmth win is measured.

---

## See also

- [`multi-agent-dev-collaboration.md`](multi-agent-dev-collaboration.md) — the parent thread; §3.8 (cold GitHub / warm channel), §5.7 (session warmth), §6.12 (executable learning).
- [ADR-049 — Multi-agent collaboration via GitHub](../decisions/049-multi-agent-collaboration-via-github.md) — why *this repo's* dev stays on GitHub.
- [`blueprint.md`](../spine/blueprint.md) — the data-ownership law (metadata, not bytes) this design must not violate.
- [`governed-actions-and-propose-verb.md`](governed-actions-and-propose-verb.md) — the propose→approve gate reused as the merge gate.
- [`tasks-as-first-class-primitive.md`](tasks-as-first-class-primitive.md) · [ADR-029](../decisions/029-tasks-as-first-class-primitive.md) — the Task primitive that produces a `Change`.
- [`orchestration-contract.md`](orchestration-contract.md) — the hub's agent-coordination contract a `Change` would ride on.
