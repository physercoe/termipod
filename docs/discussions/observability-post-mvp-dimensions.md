# Observability — dimensions deferred post-MVP

> **Type:** discussion
> **Status:** Open (2026-05-09)
> **Audience:** contributors · reviewers
> **Last verified vs code:** v1.0.443

**TL;DR.** [ADR-022](../decisions/022-observability-surfaces.md) and
its plan docs ([insights-phase-1](../plans/insights-phase-1.md) /
[insights-phase-2](../plans/insights-phase-2.md)) frame the
Insights surface around three industry lenses: FinOps Inform, SRE
Golden Signals, DORA-for-AI. Those cover the **manager / ops**
view. They do not fully cover the **AI-product**, **mobile-client**,
or **self-host operational** views. This doc enumerates the
dimensions outside the current ADR scope, marks them post-MVP, and
records the trigger conditions that would justify pulling them in.
Open until any one of these graduates to its own ADR.

---

## 1. Where this fits

ADR-022 §Decision Tier-1 = spend / latency / reliability /
capacity / concurrency. Tier-2 = engine arbitrage, lifecycle flow,
tool-call efficiency, unit economics, snippet-template usage,
multi-host distribution. Tier-3 was named informally as "governance,
security, knowledge curves." This doc takes the Tier-3 bucket
seriously and adds three observability *frames* the ADR didn't
consult:

- **AI-product quality** — user-judgment signals, not engine-emitted
  data. Closest industry analogue: Anthropic's "human preference"
  framing, or what LLMOps teams call "evals in production."
- **Mobile RUM** (Real User Monitoring) — device-side performance
  and reliability. Closest analogue: Crashlytics / Sentry / Datadog
  Mobile RUM.
- **Self-host operational** — backup freshness, retention
  adherence, TLS expiry. Closest analogue: traditional SRE infra
  monitoring, distinct from the application-level Golden Signals.

All three are real for termipod. None are MVP. This doc says *why
not yet* and *when to reconsider*.

## 2. Dimensions deferred post-MVP

### 2.1 AI-product quality signals

Spend / latency / errors are engine-emitted; they say nothing about
whether the user *got value*. The user-judgment side has its own
metric family:

| Signal | Capture path required | Frame |
|---|---|---|
| Per-turn 👍/👎 / thumb-rate | New mobile widget on transcript turn + new `agent_events.kind=user_signal` (or new `audit_events.action`) | Anthropic preference / RLHF-in-production |
| Intervention rate | Detect principal-issued correction inputs within a turn boundary; aggregate per session/project | Steering quality |
| Retry rate | Edit-distance between consecutive user prompts within a session; threshold > 0.7 → retry | Output quality proxy |
| Session abandonment | Sessions closed mid-turn vs at natural completion (last event was user input, no agent response) | Conversation success |
| Steering depth distribution | Number of corrections per turn distribution | Persona/template effectiveness |

**Why post-MVP:** Capture requires a new event kind plus a new
mobile widget. Without capture, no aggregation has data to render.
Useful only at scale where multiple users + multiple sessions per
day produce statistical signal — under MVP single-user / few-session
load, individual judgments dominate and aggregates are noise.

**Trigger to revisit:** Two users from outside the original cohort
report a quality regression that the current Insights surface
didn't flag. That means we're shipping reliability ✓ but quality ✗.

### 2.2 Mobile RUM (Real User Monitoring)

Termipod is mobile-first. None of FinOps / SRE / DORA cover the
device-side experience.

| Signal | Notes |
|---|---|
| Crash rate / ANR | Crashlytics-shape; needs Firebase or custom collector |
| App startup p50 / p95 | Cold / warm / hot — app-launch instrumentation |
| Screen-to-screen navigation time | UX perf signal; instrument via NavigationObserver |
| Network failure rate per endpoint | Distinguishes mobile-side from hub-side |
| Bandwidth per session | Cellular-cost-sensitive; SSE adds steady drip |
| Battery drain | Background SSE/WS connections are the suspect |
| Memory / CPU on device | Long sessions with large transcripts are the worry |

**Why post-MVP:** Crashlytics or equivalent is a third-party
dependency the project hasn't picked yet; rolling our own collector
adds a hub-side endpoint plus an aggregation pipeline. Until users
report perf or crash issues, the work doesn't pay back. Anecdotal
performance via TestFlight / device testing covers MVP.

**Trigger to revisit:** A device test session reports a crash or a
perceptible perf regression on a real user's device that we can't
reproduce locally. That's the moment captured RUM data starts
paying for itself.

### 2.3 Cache & sync health

[ADR-006](../decisions/006-cache-first-cold-start.md) makes
cache-first a load-bearing UX claim. We do not measure whether the
cache actually delivers.

| Signal | Why |
|---|---|
| Snapshot cache hit rate per surface | If it's < 50%, "cache-first" is theatre |
| SSE reconnect rate / dropped event count | Eventual-consistency lag visibility |
| Mobile-vs-hub clock skew | Stale-banner accuracy depends on this |
| Stale-snapshot age distribution | "How often do users see stale data" |
| Live-fetch failure rate that triggered cache fallback | Validates the fallback path is the rare case |

**Why post-MVP:** All five signals require new instrumentation in
`hub_provider.dart` + a new `cache_stats` block on `/v1/hub/stats`.
At MVP scale (few hundred snapshots/day per device), the signals
are noisy. The signals also overlap with the "is my hub healthy"
question that Phase 1 W1's stats endpoint answers from a different
angle.

**Trigger to revisit:** A user reports the offline / stale banner
firing more often than expected, or the post-MVP rollup work
([ADR-022 D5](../decisions/022-observability-surfaces.md)) lands
and we want to verify cache-first is still the right shape.

### 2.4 Drift detection — agent behavior over time

Same prompt, different model version → different output. With
frame profiles ([ADR-010](../decisions/010-frame-profiles-as-data.md))
and engines that update independently of the hub, behavioral
regression is real but invisible.

| Signal | Capture path |
|---|---|
| Tool-call diversity (Shannon entropy per session) | Aggregate over `agent_events.kind=tool_call.tool_name` |
| Distribution shift in turn length | Compare last 7d vs prior 30d |
| Refusal-rate trend | `agent_events.kind=text` content-pattern match for known refusal phrases |
| Failed-task pattern across model upgrades | Joins agent_events to model identity in `turn.result.by_model` |

**Why post-MVP:** Drift detection is a research-grade exercise; the
literature is still catching up. Without baseline regression
fixtures (a standard set of prompts run weekly), the data is
non-comparable.

**Trigger to revisit:** A user reports a quality regression
anchored to a specific model upgrade window, and we want to confirm
or deny.

### 2.5 Self-host operational health

For self-hosted hubs (the dominant deployment per
[ADR-018](../decisions/018-tailnet-deployment-assumption.md)), the
hub box is also the operator's box. They want classic infra
visibility.

| Signal | Notes |
|---|---|
| Backup freshness | DataPort export age (per `project_todo_data_export.md`'s backup/restore) |
| Disk-free trend with retention forecast | When does the DB blow the disk? |
| TLS cert expiry | If the hub runs behind a public-facing TLS endpoint |
| Retention adherence | Are old `agent_events` being purged per declared policy? |
| systemd / process restart count | Hub crash signal |

**Why post-MVP:** The deployment story is "tailnet, single hub,
self-hosted" — operators are technical and read disk-usage from
`df` directly. No mobile glance is asked for these today.

**Trigger to revisit:** A self-hoster reports a hub OOM or disk-full
incident that retention-trend data would have predicted.

### 2.6 Product analytics — engagement & retention

If termipod ever positions as a product (vs a personal lab tool),
the engagement lens applies:

| Signal | Frame |
|---|---|
| DAU / WAU / MAU per device | Standard PLG |
| Feature adoption funnel | First-spawn time, time-to-first-value, onboarding completion |
| Retention curves | Did the user come back tomorrow / next week? |
| Screen-frequency distribution | Which screens get used vs ignored |
| Snippet/template stickiness | Which presets become recurring |

**Why post-MVP:** Termipod is positioned today as a personal /
team-internal tool, not a public product
([positioning.md](positioning.md)). Engagement analytics are net
overhead until product-vs-tool framing changes.

**Trigger to revisit:** Public release / public marketing of
termipod, or any decision that moves the project toward a managed
hosted offering.

### 2.7 Non-LLM infra cost

ADR-022 D3's "spend" tile captures token / model spend. It does
not capture the *infra* cost of running termipod itself.

| Signal | Notes |
|---|---|
| Hub VPS compute / storage / egress | Self-host: known ahead of time as a flat cost; managed: variable |
| A2A relay bandwidth cost | Egress on the hub VPS, per ADR-003 |
| Storage growth × $/GB at chosen retention | Linked to the disk-free trend in §2.5 |
| Per-team showback / chargeback | Multi-team hubs splitting infra cost |

**Why post-MVP:** Self-host with flat-rate VPS hosting makes infra
cost a constant — interesting at the planning level, not the daily
glance. Managed-offering or per-team chargeback makes it a daily
question.

**Trigger to revisit:** Any move toward managed hosting or
multi-team chargeback semantics.

### 2.8 Formal SLO + error-budget burn rate

Phase 1's "errors" tile shows raw counts. SRE practice formalizes
this as an error budget: declare an SLO ("99% turn success"),
measure burn against it, alert when burn rate would exhaust the
quarter's budget early.

**Why post-MVP:** Requires a declared SLO contract — we don't have
one. Termipod's user expectation is "this works most of the time
on a personal lab tool," not a 99.x% commitment. SLO formalization
is an enterprise-readiness move.

**Trigger to revisit:** Any contractual reliability commitment to
an external user.

### 2.9 Tool-use diversity & subagent tree depth

Two agent-research signals that detect specific failure modes:

| Signal | Failure mode it catches |
|---|---|
| Tool-call Shannon entropy per session | Agent collapsed to one tool ("everything's a Bash") — sign of poor agent design |
| Subagent tree depth distribution per project | Stewards bypassing delegation (per [ADR-016](../decisions/016-subagent-scope-manifest.md)) — manager doing IC work |

**Why post-MVP:** Both are research-grade signals. Useful for
benchmarking termipod's own agent designs against alternatives,
not for daily operations.

**Trigger to revisit:** A claim that "termipod stewards delegate
better than X" needs evidence — these two metrics are how you
measure it.

## 3. What stays in MVP scope

[ADR-022](../decisions/022-observability-surfaces.md) Tier-1 +
Tier-2 cover the manager / ops view fully. The dimensions in §2 are
genuinely outside that view. None of them block the Phase 1 / Phase
2 wedges; none of them will retroactively invalidate the ADR's
shape (the `/v1/insights` endpoint stays as designed; new
dimensions add new endpoints or new event kinds, not new shape).

## 4. Why not amend ADR-022 now

Three reasons to defer rather than amend:

1. **Each post-MVP dimension has its own capture pipeline.** AI
   quality needs a new event kind; mobile RUM needs a new collector;
   cache health needs new hub-side endpoints. None of those are
   "wire up another aggregator on existing data" — they're new
   data paths. ADR-022's shape (one `/v1/insights` endpoint
   parameterized by scope) doesn't fit them; each will graduate
   into its own ADR with its own capture decisions.
2. **MVP scale doesn't render the data usefully.** §2.1 / §2.2 /
   §2.3 specifically need multi-user multi-session loads to produce
   statistical signal. Pre-launch, we'd be measuring noise.
3. **ADR-022 stays minimal.** The doc is more useful as "the
   contract for the surface we're building right now" than as a
   sprawling registry of every observability dimension that exists.

## 5. When to graduate any of these

Each subsection in §2 names its own trigger. The general pattern:
graduate to a dedicated ADR + plan when *one of* the following
holds:

- A user reports a problem the missing dimension would have caught.
- A scale increase puts the dimension above the noise floor (rough
  rule of thumb: 5+ active users + 50+ sessions/day gives §2.1 /
  §2.3 statistical signal).
- A positioning shift (managed offering, public product, contractual
  SLO) makes the dimension contractually relevant.

## 6. References

- [ADR-022 — observability surfaces](../decisions/022-observability-surfaces.md)
  (the in-scope decisions; this doc is the out-of-scope companion).
- [Discussion: observability-gap](observability-gap.md) — the
  narrative that produced ADR-022.
- [insights-phase-1.md](../plans/insights-phase-1.md) — Phase 1
  wedges.
- [insights-phase-2.md](../plans/insights-phase-2.md) — Phase 2
  wedges.
- [ADR-006 — cache-first cold start](../decisions/006-cache-first-cold-start.md)
  — load-bearing claim §2.3 would validate.
- [ADR-010 — frame profiles as data](../decisions/010-frame-profiles-as-data.md)
  — drift surface (§2.4).
- [ADR-018 — tailnet deployment assumption](../decisions/018-tailnet-deployment-assumption.md)
  — self-host context (§2.5).
- [Positioning](positioning.md) — product-vs-tool framing
  underlying §2.6.
- Anthropic preference framing — research underpinning §2.1.
- Datadog Mobile RUM / Sentry — industry analogues for §2.2.
