# AI-native codebase legibility — designing for agents that hallucinate and forget

> **Type:** discussion
> **Status:** Open (no decision yet; recommendations in §7)
> **Audience:** contributors
> **Last verified vs code:** v1.0.640

**TL;DR.** Agents that read this codebase will sometimes confidently
assert things that aren't there (hallucinate) and sometimes miss
things that are (forget). You can't prompt that away entirely, so the
defence is structural: **make wrong assumptions cheap to detect and
expensive to act on.** This doc separates the two failure modes,
audits where the repo already guides an agent well versus where it
actively misleads one, and argues the core point — AI-nativeness is
not a one-time design property but a *freshness SLA*. A doc that is
300 versions stale is more dangerous to an agent than no doc, because
the agent can't tell.

---

## 1. Two failure modes, two cures

They look similar but have different causes and different fixes, so
keep them apart:

| Mode | What it is | Root cause | Structural cure |
|---|---|---|---|
| **Hallucinate** | Confidently asserting a tool / test / function / behaviour that isn't there | Plausible-but-absent; the model fills a gap | Verification friction must be *low and mandated* |
| **Forget / miss** | Failing to find something that *is* there | Poor discoverability; split or unsignposted sources of truth | A single, signposted source of truth per concept |

The design goal stated as a slogan: **guide, don't mislead.**
Misleading content feeds hallucination (it looks authoritative and
is wrong); missing guidance feeds forgetting (the truth exists but
isn't found).

## 2. The motivating evidence

This doc was prompted by a concrete miss during the
[`agent-driven-system-probing.md`](agent-driven-system-probing.md)
work. An agent (Claude, in-session) asserted — confidently, more than
once — that the L1 contract-conformance checks were greenfield and
needed building. They were not: `tool_registry_test.go` already
CI-locked the catalog↔dispatcher↔handler lockstep. The error was
caught only because (a) the human pushed back, and (b) verification
was *one grep away*. Both halves matter: the prompt to check, and the
cheap check. That single episode is the whole thesis in miniature.

A second miss in the same session: nearly overlooking that the tool
catalog has **two** registries (authority `hubmcpserver/toolspec.go`
and native `server/native_tools.go`). Nothing signposted the split —
CLAUDE.md's "MCP tools need three things in lockstep" reads as though
there is one catalog. That is *misleading by omission*.

## 3. What is already AI-native here

Credit where due — this repo is above average on agent-legibility:

- **`CLAUDE.md`** carries layout, domain model, conventions, and an
  "Easy to get wrong" trap list — a genuine agent onboarding doc.
- **`doc-spec.md` + status blocks + the seven primitives** let an
  agent answer "what is this, do I trust it, is it current?" in
  ~30 seconds.
- **`glossary.md` + `lint-glossary.sh`** attack the single biggest
  hallucination source — term collisions (*session*, *kind*, *fork*
  meaning two things in adjacent layers).
- **Append-only numbered ADRs** make decision history legible and
  greppable.
- **Behaviour-is-data** (YAML templates) and the single `ToolSpec`
  registration point (ADR-033) keep related behaviour local.
- **Lockstep CI guards** (`*_meta_test.go`, the new
  `tool_contract_sweep_test.go`) make drift fail loudly instead of
  silently misleading the next reader.

The mechanisms are right. The problem is drift *within* them.

## 4. What actively misleads an agent

Grounded in what the probing session actually hit:

1. **Stale docs at scale.** `lint-docs.sh` reports ~170 stale-doc
   warnings — non-failing, so nobody acts on them. The status block's
   `Last verified vs code:` is the right instrument but only if the
   number is acted on. Example:
   [`../plans/single-agent-demo-test.md`](../plans/single-agent-demo-test.md)
   reads `Last verified vs code: v1.0.312` while the code is at
   v1.0.640 — a ~328-version gap. An agent reading it as current is
   misled *with confidence*, which is the worst kind. A stale,
   authoritative-toned doc is more dangerous than a missing one.
2. **The two-registry split** has no signpost (§2). An agent assumes
   one catalog and concludes a tool "doesn't exist" when it lives in
   the other registry.
3. **Index files that overflow their load budget.** When the
   always-loaded index for a concept grows past what gets read, it
   silently drops guidance — a guide that doesn't fully load is worse
   than a short one, because the omission is invisible. (The project's
   own memory index has hit this; the same disease applies to any
   oversized single-file index.)

The pattern across all three: the repo has the right *mechanisms* but
*tolerates drift* in them. That is the crux.

## 5. The core principle — legibility is a freshness SLA

AI-nativeness is often discussed as a static design property ("is the
structure clean?"). For a *living* codebase that framing is wrong.
The structure here is already clean. What degrades agent-legibility
over time is **drift between the map and the territory** — stale
status blocks, renamed-but-not-updated references, indexes that lag
the code.

So the right unit is not "is it well-designed once" but "**how fast
does the map track the territory, and is the lag visible?**" A
freshness SLA — e.g. "no load-bearing doc may sit more than N
versions behind code without either a re-verify or a `Stale` status"
— turns drift from an invisible hazard into a tracked, failable
signal. The instruments already exist (status blocks, `lint-docs.sh`
warnings); the SLA is the discipline of acting on them.

## 6. A legibility checklist

Cheap heuristics for "is this corner of the repo agent-legible?":

- **One source of truth per concept.** If a fact lives in two places,
  one will drift; if it lives in two *registries*, signpost both.
- **Every load-bearing doc declares its freshness**, and the lag is
  bounded (§5).
- **Traps are named, not just avoided.** The thing an agent will get
  wrong belongs in CLAUDE.md's "Easy to get wrong", explicitly.
- **Invariants are executable.** Prefer a `*_meta_test.go` that fails
  on drift over a prose paragraph that quietly rots.
- **Claims are citable.** The codebase should make "grep for it,
  cite the `file:line`" a one-step operation (good naming, full-word
  grep-friendliness — already a doc-spec rule).
- **Indexes fit their load budget.** An index that truncates is not
  an index.

## 7. Recommendations

1. **Done — anti-hallucination prompts in CLAUDE.md.** "Before
   claiming a tool/test/function exists or doesn't, grep and cite the
   `file:line`"; the read-the-meta-tests pointer; and the two-registry
   note in "Easy to get wrong". These directly target the §2 misses.
2. **Adopt a freshness SLA (§5).** Decide the bound (versions or
   time), then promote `lint-docs.sh`'s stale-doc report from warning
   toward a tracked metric — starting with the worst offenders (the
   v1.0.312 plan). Doc-only, no code.
3. **Make this an exploratory-probe target.** Tie back to
   [`agent-driven-system-probing.md`](agent-driven-system-probing.md)
   §8: point a reviewer-class agent at the codebase and have it report
   *where it had to guess or got misled*. That turns "is this
   AI-native?" from one-shot opinion into a repeatable, evidence-
   producing audit — and it is exactly the kind of finding the report
   contract (that doc §2) is built to carry.

## 8. Open questions

- **What is the freshness bound?** N versions, or elapsed time, or
  "any touch of the underlying code re-arms the staleness clock"?
- **Warning → failure, when?** `lint-docs.sh` stale warnings are
  non-failing today (170 of them). Flipping to failing needs the
  backlog cleared first — same shape as the glossary new-term gate's
  staged rollout.
- **Who owns re-verification?** A doc bumped to a new
  `Last verified vs code:` is a claim someone read it against current
  code. Is that a release-time chore, a per-PR touched-doc rule, or a
  periodic agent-run sweep (recommendation 3)?

## 9. Related

- [`agent-driven-system-probing.md`](agent-driven-system-probing.md)
  — the harness that could *audit* legibility (recommendation 3); its
  reviewer role is the consumer that most needs a legible codebase.
- [`../doc-spec.md`](../doc-spec.md) — status blocks and the
  `Last verified vs code:` instrument the freshness SLA builds on.
- [`../reference/glossary.md`](../reference/glossary.md) — collision
  control, the strongest existing anti-hallucination mechanism.
- [`../../CLAUDE.md`](../../CLAUDE.md) — where the anti-hallucination
  prompts (recommendation 1) now live.
