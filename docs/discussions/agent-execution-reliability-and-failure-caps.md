---
name: Agent execution reliability and failure caps
description: Comprehensive SOTA research on bounding AI-agent execution for production reliability — retry caps, loop-breakers, failure detection, and recovery — before committing to a termipod design. Surveys six layers of practice: classical reliability primitives (retry + exponential backoff + jitter, circuit breakers, retry budgets, error-type discrimination, timeouts, idempotency, bulkheads); supervision trees / let-it-crash (Erlang OTP restart-intensity {MaxRestarts, Period} → escalate-up, the canonical failure cap); durable execution (Temporal checkpoint/replay, retry policy, non-retryable error types); agent-loop breakers and loop detection (LoopGuard max_steps/max_repeat/max_flat_steps, result-aware no-progress detection, two-tier nudge→stop escalation, hard turn/time fail-safe); per-framework knobs (LangGraph recursion_limit=25, CrewAI max_iter=20/max_retry_limit=2, OpenAI Agents SDK max_turns=5 + guardrails + HITL, AutoGen max_consecutive_auto_reply with reset-on-human, Claude Agent SDK maxIterations≈10 / stepCountIs); coding-agent specifics (pass@k retry budget ≈3, tests as fail-to-pass verifier); and observability/HITL (OpenTelemetry spans, escalation-to-review-queue). Distils eleven cross-cutting principles, maps them onto termipod's existing time/spend/stuck breakers (loop_sweep.go / budget.go / escalation.go / reconcile.go) and its host-runner→hub→principal supervision hierarchy, and lays out a four-tier design menu (iteration cap → failure-streak cap → result-aware loop detection → step-level retry-with-backoff) for a future failure-cap ADR. No ADR opened; no code changed.
---

# Agent execution reliability and failure caps

> **Type:** discussion
> **Status:** Open (2026-05-30) — research commissioned before drafting a
> failure-cap ADR, because reliability is production-critical and the
> current failure/loop handling has not been benchmarked against SOTA.
> Feeds, but does not pre-empt, a future ADR *"Execution loop-breakers:
> failure-cap policy"* (the §3.6 deferred code in
> [`coordination-basis-and-decision-classification.md`](coordination-basis-and-decision-classification.md)).
> **Audience:** contributors
> **Last verified vs code:** v1.0.752

**TL;DR.** termipod bounds agent execution by **time and spend** only
(`loop_sweep.go`: 20 min inactivity → escalate, 2 h absolute → terminate;
`budget.go`: spend cap → pause). It has **no failure-count cap, no
iteration cap, no result-aware loop detection, and no in-loop exception
recovery** — so a thrashing agent burns budget until a coarse 2 h / spend
backstop. This doc surveys the state of the art for bounding agent loops
across six layers — classical reliability, supervision trees, durable
execution, agent-loop detection, framework knobs, and observability —
distils the principles that matter, and maps them onto termipod's
existing machinery and its **host-runner → hub → principal supervision
hierarchy**, which already *is* the shape SOTA recommends (Erlang/OTP
restart-intensity), just keyed on the wrong signal. The recommendation
is a **four-tier menu** for the future ADR, starting with the universal
fail-safe every framework ships (an **iteration cap**) and a
**failure-streak cap** that defaults to *escalate, not terminate*.

---

## 1. The problem, and why it is production-critical

An agent loop is `sense → decide → act → observe → repeat`. It fails in
recognisable ways:

- **Thrash / no-progress loop ("doom loop").** The agent calls the same
  tool with the same arguments and gets the same result, repeatedly,
  making no progress. A widely-cited example: an agent ran 847 reasoning
  steps at ~$47/min and never produced an answer; "same tool with
  identical arguments repeatedly" is the single most-reported issue in
  some agent trackers.
- **Failure streak.** The same operation fails N times in a row (a
  compile that won't pass, an API that keeps 500-ing).
- **Cost / time runaway.** No hard stop, so spend grows unbounded.
- **Transient vs permanent error confusion.** Retrying a 400/401 wastes
  budget; not retrying a 503/429 gives up too early.
- **Crash / exception.** A panic or unhandled error kills the process
  mid-task.
- **Deadlock / silent stall.** The loop stops advancing but nothing
  notices.

Reliability is the load-bearing concern in production: elite teams that
adopt comprehensive evaluation + observability report ~2.2× better
reliability than non-elite teams (Galileo/industry surveys). A harness
whose only backstops are a 2 h timer and a dollar cap is leaving most of
the established failure-handling surface on the table.

---

## 2. The state of the art, by layer

### 2.1 Classical reliability primitives (distributed systems)

Decades-proven, and the substrate everything else builds on:

- **Retry with exponential backoff + jitter.** Retry *transient*
  failures; double the delay each attempt and add randomness so clients
  don't retry in lockstep ("thundering herd"). Canonical formula:
  `delay = min(max_delay, base · 2^attempt)`, then
  `delay · (0.5 + rand(0,0.5))`. Jitter is described as *mandatory*, not
  optional (AWS Prescriptive Guidance).
- **Error-type discrimination.** Retry transient errors (HTTP 408 / 429 /
  503); **never** retry client/permanent errors (400 / 401) — they won't
  succeed on retry. This is the most important input to a failure
  classifier.
- **Circuit breaker (closed → open → half-open).** When failures indicate
  a *systemic* problem (not a transient blip), stop retrying entirely for
  a cool-down, then probe with a half-open trial. Retries handle transient
  faults; circuit breakers handle "the thing is down."
- **Retry budget.** Cap total retry traffic to ~10–20 % of normal load so
  retries can't amplify an outage.
- **Timeouts** shorter than retry intervals; **idempotency tokens** so a
  retried side-effect doesn't double-apply; **bulkheads** to isolate one
  failing dependency from the rest.

### 2.2 Supervision trees / "let it crash" (Erlang OTP) — the canonical failure cap

The most directly applicable model, because **termipod's host-runner is
literally a supervisor** (a deterministic deputy that spawns, monitors,
and reaps agent processes — [`blueprint.md` §3.2](../spine/blueprint.md)).

- **"Let it crash."** Separate fault-handling from business logic; let a
  faulty process die and let a supervisor decide what to do. The agent
  (stochastic) does the work; the deputy (deterministic) handles failure
  — exactly termipod's audit boundary.
- **Restart strategies:** *one-for-one* (restart only the failed child),
  *one-for-all*, *rest-for-one*.
- **Restart intensity `{MaxRestarts, PeriodSeconds}` — this is the failure
  cap.** If a child crashes more than `MaxRestarts` times within
  `PeriodSeconds`, the supervisor **gives up, terminates the subtree, and
  reports the failure *upward*.** This "prevents infinite restart loops
  from silently consuming CPU and memory." A 2026 line of work explicitly
  ports this pattern to AI-agent systems (Zylos, "Supervisor Trees and
  Fault Tolerance Patterns for AI Agent Systems").

The shape — *a sliding-window failure count that, when exceeded, escalates
one level up the hierarchy* — is precisely what termipod's
host-runner → hub → principal chain wants, and precisely what its current
breakers do for *time/spend* but not for *failures*.

### 2.3 Durable execution (Temporal, Azure Durable Functions)

The production-grade "never lose work" layer:

- **Checkpoint + replay.** Persist execution state externally; on crash,
  timeout, or network failure, deterministically replay to the last
  checkpoint and resume. (Temporal + OpenAI Agents SDK shipped a public
  preview integration in 2025.)
- **Declarative retry policy** per step: `initial_interval`,
  `backoff_coefficient` (default 2×), `maximum_interval` (default cap
  100 s), `maximum_attempts`, and crucially **`non_retryable_error_types`**
  — the error taxonomy made configuration.
- **Start-to-close timeouts** that trigger the retry policy.
- Idempotency so replay doesn't double-apply side-effects — the
  reversibility lever ([`blueprint.md` §2](../spine/blueprint.md)) in
  another guise.

### 2.4 Agent-loop breakers and loop detection (the agent-specific SOTA)

This is the layer most specific to LLM agents, and the richest:

- **The hard iteration/time fail-safe is universal.** "Every agent run
  should have a hard stop based on number of turns (LLM calls) or total
  execution time — your absolute fail-safe."
- **LoopGuard-style detection** tracks three counters: `max_steps` (total
  cap), `max_repeat` (same tool called ≥3×), `max_flat_steps` (≥4 steps
  with no new progress signal).
- **Result-aware, not call-count-aware.** The trip condition for a
  no-progress loop is *same tool + same args + **same output***. Same
  input with a **different** output is genuine progress and must **not**
  trip. (zeroclaw "result-aware loop detection"; opencode "doom loop
  detection".)
- **Failure streak** is a distinct signal: same tool **failing** N
  consecutive times.
- **Two-tier escalation (nudge → stop).** First detection injects a
  self-correction prompt ("you appear to be repeating X; try a different
  approach"); only if the pattern persists is there a hard stop. This
  matches termipod's *escalate-before-terminate* instinct and the
  reversibility corollary.
- Clear tool success/failure states are themselves a fix: one report
  cut tool calls 14 → 2 by making "done" legible to the agent — i.e.
  **verifiability lowers loop risk**, echoing the classification axes in
  [`permission-model.md`](../reference/permission-model.md).

### 2.5 What the frameworks actually ship (knobs + defaults)

| Framework | Iteration / turn cap | Retry cap | Failure / loop detection | On-limit behaviour | Human-in-the-loop |
|---|---|---|---|---|---|
| **LangGraph** | `recursion_limit` (default **25**) | — | — | raises `GraphRecursionError` | `interrupt()` |
| **CrewAI** | `max_iter` (default **20**) | `max_retry_limit` (default **2**) | — | returns best answer / error | — |
| **OpenAI Agents SDK** | `max_turns` (default **5**) | — | input/output/tool **guardrail tripwires** | raises `MaxTurnsExceeded` | approvals / human review pause |
| **AutoGen** | `max_turns`; `max_consecutive_auto_reply` | — | — | terminate; **counter resets if a human replies** | `human_input_mode` |
| **Claude Agent SDK** | `maxIterations` (≈**10**) / `stepCountIs(50)` | fallback-model handling | self-correction loop | `ResultMessage.subtype` = success vs hit-limit | permission / approval gate |
| **Temporal (durable)** | — | `maximum_attempts` + backoff | `non_retryable_error_types` | retry exhausted → fail | — |
| **Erlang/OTP supervisor** | — | `{MaxRestarts, Period}` | crash | escalate up the tree | — |

Two observations. (1) **The iteration/turn cap is universal** — every
agent framework has one (25 / 20 / 5 / ≈10); it is the cheapest, bluntest,
non-negotiable backstop, and **termipod has none.** (2) Beyond that, the
field splits into *count-based* caps (LangGraph, CrewAI, OpenAI, AutoGen),
*classification-based* policies (Temporal `non_retryable_error_types`,
OpenAI guardrails), and *supervision* (OTP). Most agent frameworks pick
the simple count cap; the sophisticated failure classifier is rarer.

### 2.6 Coding-agent specifics (termipod's MVP domain)

- **Retry budget ≈ 3 is the empirical norm.** pass@1 ≈ single-shot;
  pass@3 = up to **three consecutive attempts**, resolved if any succeeds
  (SWE-bench Pro). So a small bounded retry, not unlimited, is standard.
- **Tests are the verifier.** SWE-bench Verified uses human-validated
  *fail-to-pass* unit tests as the success oracle. Coding's strong,
  cheap verification (compile / type-check / test) is exactly the
  verifiability axis that should gate retries and reset the failure
  counter — termipod's coding MVP is the *best* case for this.
- Scaffold-dependence: model, tool access, **retry budget**, and evaluator
  version all materially move outcomes — so the retry/verify policy is a
  first-class lever, not an afterthought.

### 2.7 Observability and human-in-the-loop (the substrate)

- **OpenTelemetry is becoming the agent-tracing standard** — capture every
  LLM call, tool call, and decision as a span; correlate with infra and
  cost signals. Observability is being called the "control plane" for
  agent ops.
- **Escalation policies route problematic sessions to a human review
  queue** on threshold breach — structurally identical to termipod's
  attention queue. HITL is the recovery path of last resort across the
  field.

---

## 3. Eleven cross-cutting principles (the distillation)

1. **An iteration/turn cap is mandatory and universal.** Cheap, blunt,
   non-negotiable. Do this first.
2. **Three different signals, three different handlers:** (a) *transient
   error* → retry with backoff; (b) *no-progress loop* → same tool+args+
   output → detect + nudge; (c) *failure streak* → same op failing N× →
   cap + escalate. Don't conflate them.
3. **Be result-aware, not call-count-aware.** Same call, different output
   = progress; don't trip.
4. **Discriminate error types.** Retry transient (429/503/timeout), never
   permanent (400/401); make the taxonomy explicit
   (`non_retryable_error_types`).
5. **Two-tier: nudge before you break.** Inject a self-correction prompt
   first; hard-stop only if it persists.
6. **Default to the cheap-to-recover outcome.** Escalate/pause, don't
   terminate, when the classifier is uncertain — false positives must be
   reversible (the §3.1 reversibility corollary).
7. **Reset on progress or intervention.** Consecutive counters reset when
   genuine progress happens or a human steps in (AutoGen).
8. **Supervision restart-intensity → escalate up.** A sliding-window
   failure count that, when exceeded, terminates the unit and reports one
   level up (OTP) — the architecturally-native pattern for termipod.
9. **Bound the retry budget; back off with jitter.** ~10–20 % of traffic;
   never retry in lockstep.
10. **Checkpoint for safe retry/rollback.** Durable state makes retries and
    breaks non-destructive — reversibility again.
11. **Verification gates the loop.** A cheap success oracle (tests for
    code) decides success/failure, feeds the counter, and is itself the
    strongest loop-prevention (legible "done").

---

## 4. Mapping to termipod — most of the substrate already exists

What is already in place (verified in code) is closer to the SOTA shape
than it looks; the gap is the *signal*, not the *architecture*.

| SOTA element | termipod today |
|---|---|
| Supervision hierarchy | **host-runner → hub → principal** ([`blueprint.md` §3](../spine/blueprint.md)) — already an OTP-shaped tree |
| Restart-intensity escalate-up (time) | `loop_sweep.go` — inactivity 20 m → `escalateStall`, absolute 2 h → `terminateLoopTimedOut` (ADR-034) |
| Spend cap → pause | `budget.go` — `budget_cents` → host pause + attention |
| Stall / stuck escalation | `escalation.go` (widen assignees), `runner.go` (stuck-pane → attention) |
| Crash detection / restartability | `reconcile.go` — dead pane → `crashed` |
| Result subtype (success vs limit) | `terminal_reason` on `turn.result` (`driver_stdio.go`) |
| Tracing/spans substrate | `agent_events` log + the AG-UI stream |
| HITL review queue | the **attention queue** (`request_approval` / `request_select` / `request_help`) |
| Verification oracle | deliverable **reviews** + acceptance criteria; coding tests |

**The gaps** (all confirmed absent): an **iteration/turn cap**; a
**consecutive-failure counter + cap**; **result-aware loop detection**;
**error-type discrimination** for retries; **bounded retry-with-backoff**;
and **in-loop `recover()`** (a panic surfaces as a terminal state via
reconcile, not as a bounded, classified failure). Critically, the
*pieces a failure-cap needs* — a per-event ingest hook (`accumulateSpend`
is the sibling), a counter column pattern (`spent_cents`), a
pause+attention action, a policy-override mechanism (`loop_*` columns /
`policy.yaml`) — **all already exist**. A failure-cap is largely a
re-application of the spend-cap machinery to a new counter.

---

## 5. Design menu for the failure-cap (for the future ADR)

Four tiers, ascending cost/risk. The recommendation is to ship **Tier 0 +
Tier 1** as the MVP failure-cap and defer 2–3.

- **Tier 0 — iteration / turn cap (do first; lowest risk).** Count turns
  per loop-entity (the `agent_events` turn stream already exists); break
  at `K` turns → escalate as a decision-kind attention item. No classifier.
  This is the universal fail-safe every framework ships and termipod
  lacks. Cheapest possible win.
- **Tier 1 — failure-streak cap (the OTP restart-intensity).** Add
  `agents.consecutive_failures`; increment on a *classified-failure*
  event (`turn.result` error subtype, `tool_result` `is_error`), reset on
  progress (a successful tool result, a task/deliverable advance). At
  `N` → **pause + decision-kind attention** ("agent failed N× on X; last
  error …; [retry-with-guidance] / [reassign] / [abort], recommend …").
  **Default escalate, not terminate** (principle 6). Classifier starts
  *conservative* — only count clear failures — to bound false positives.
  This is a near-sibling of `accumulateSpend`.
- **Tier 2 — result-aware loop detection (LoopGuard).** Fingerprint
  `(tool, args, output)`; on a no-progress repeat, **inject a
  self-correction nudge** first (Tier-2a), hard-stop + escalate only if it
  persists (Tier-2b). Needs tool-call fingerprinting over `agent_events`.
- **Tier 3 — bounded retry-with-backoff at the step level.** For
  `plan_executor.go` steps and transient tool errors: retry up to `R`
  (≈3, the coding-agent norm) with exponential backoff + jitter, gated by
  an explicit `non_retryable_error_types` set. Temporal-style.

Orthogonal, small, and worth doing regardless: **`recover()` in driver
goroutines** to convert a panic into a clean `failed` terminal state with
a reason, instead of crashing the goroutine.

**Why this is ADR-worthy and not a silent patch:** the **failure
classifier** (what counts as a failure vs progress vs transient) is the
load-bearing, opinionated decision — false positives pause healthy
agents, false negatives miss the thrash — and the cap value, the
escalate-vs-terminate default, and the reset semantics are all real
design choices. That is exactly what an ADR records.

## 6. Open questions for the ADR

- **Classifier signal source.** Lean on the engine's `terminal_reason` /
  `tool_result.is_error` (cheap, engine-dependent) vs a termipod-side
  heuristic over `agent_events` (engine-agnostic, more work)? Probably
  both, with the engine signal preferred when present.
- **Counter granularity.** Per-agent (`agents.consecutive_failures`) vs
  per-loop-entity (`loop_entities`) vs per-task? The OTP model is
  per-supervised-child → per-agent is the natural default.
- **Cap defaults.** Turn cap `K` and failure cap `N` — borrow the field's
  numbers (turns ≈ a few × the engine's own `maxIterations`; failures
  ≈ 3, the pass@3 norm) and make both `policy.yaml`-overridable like
  `loop_*`.
- **Interaction with existing breakers.** The failure-cap should compose
  with, not duplicate, `loop_sweep`'s time breaker and `budget`'s spend
  breaker — likely the same pause+attention sink, a new trigger.
- **Engine coverage.** Five engines, varying signal quality
  (claude-code M4 JSONL is richest). Tier 0 (turn count) is
  engine-agnostic; Tier 1's classifier quality varies by engine.

## 7. Sources

Classical reliability / circuit breaker / backoff:
- [AWS Prescriptive Guidance — Retry with backoff](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/retry-backoff.html)
- [DesignGurus — resilient microservices (circuit breaker, bulkhead, retries)](https://www.designgurus.io/answers/detail/what-are-design-patterns-for-resilient-microservices-circuit-breaker-bulkhead-retries)
- [OneUptime — Go retry + circuit breaker pattern](https://oneuptime.com/blog/post/2026-01-30-go-retry-circuit-breaker-pattern/view)

Supervision trees / let-it-crash:
- [Learn You Some Erlang — Supervisors](https://learnyousomeerlang.com/supervisors)
- [Software Patterns Lexicon — The Supervisor Pattern in OTP](https://softwarepatternslexicon.com/erlang/creational-design-patterns-in-erlang/the-supervisor-pattern-in-otp/)
- [Zylos — Supervisor Trees and Fault Tolerance Patterns for AI Agent Systems (2026)](https://zylos.ai/research/2026-03-16-supervisor-trees-fault-tolerance-ai-agent-systems)

Durable execution:
- [Temporal — Durable Execution meets AI](https://temporal.io/blog/durable-execution-meets-ai-why-temporal-is-the-perfect-foundation-for-ai)
- [Temporal — Activity Execution (timeouts + retry policy)](https://docs.temporal.io/activity-execution)
- [InfoQ — Temporal + OpenAI agent durability (2025)](https://www.infoq.com/news/2025/09/temporal-aiagent/)

Agent-loop detection / doom loops:
- [AWS / DEV — Prevent AI agent reasoning loops wasting tokens](https://dev.to/aws/how-to-prevent-ai-agent-reasoning-loops-from-wasting-tokens-2652)
- [Agent Patterns — Infinite Agent Loop](https://www.agentpatterns.tech/en/failures/infinite-loop)
- [opencode — doom loop detection PR #3445](https://github.com/anomalyco/opencode/pull/3445)
- [Action Verification and Retries in LLM Agent Execution Loops](https://ingramhaus.com/action-verification-and-retries-in-llm-agent-execution-loops)

Framework knobs:
- [LangGraph — GRAPH_RECURSION_LIMIT](https://docs.langchain.com/oss/python/langgraph/errors/GRAPH_RECURSION_LIMIT)
- [CrewAI — Agents (max_iter / max_retry_limit / max_rpm)](https://docs.crewai.com/en/concepts/agents)
- [OpenAI Agents SDK — Runner (max_turns / MaxTurnsExceeded)](https://openai.github.io/openai-agents-python/ref/run/)
- [AutoGen 0.2 — Terminating conversations (max_consecutive_auto_reply)](https://microsoft.github.io/autogen/0.2/docs/tutorial/chat-termination/)
- [Claude Agent SDK — How the agent loop works](https://code.claude.com/docs/en/agent-sdk/agent-loop)

Coding agents / verification:
- [SWE-bench Pro (arXiv 2509.16941) — pass@k retry budget](https://arxiv.org/pdf/2509.16941)
- [SWE-bench Verified — fail-to-pass tests](https://www.emergentmind.com/topics/swe-bench-verified-issues)

Observability / HITL:
- [OpenTelemetry — AI Agent Observability](https://opentelemetry.io/blog/2025/ai-agent-observability/)
- [Galileo — The Enterprise Guide to AI Agent Observability](https://galileo.ai/blog/ai-agent-observability)

## 8. Cross-references

- [`coordination-basis-and-decision-classification.md`](coordination-basis-and-decision-classification.md) — §3.6 raised this gap; this doc is its research backing.
- [`../spine/orchestration-layer.md`](../spine/orchestration-layer.md) — loop-closure invariant + "no silent sink" (the existing time-based breaker rationale).
- [ADR-034](../decisions/034-orchestration-loop-closure.md) — the time-based loop-closure runtime this would extend.
- [`../spine/blueprint.md`](../spine/blueprint.md) — A1/A3 + the reversibility corollary the escalate-not-terminate default rests on.
