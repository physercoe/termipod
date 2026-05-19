# Loop-closure knobs — configurability reference

> **Type:** reference
> **Status:** Current (2026-05-19)
> **Audience:** contributors, operators
> **Last verified vs code:** v1.0.633

**TL;DR.** The loop-closure runtime ([ADR-034](../decisions/034-orchestration-loop-closure.md))
enforces that a directive's loop reaches a terminal state — via per-hop
deadlines, a reconcile sweep, stall escalation, and two lifecycle hooks.
This page is the canonical status of every knob that tunes that
enforcement: its default, whether it is configurable, and how. Five
knobs are configurable (per-project or via the hook overlay); three are
fixed Go constants — operational or structural — by deliberate MVP
scope.

---

## 1. Status table

| Knob | Default | Configurable | Where to set | Takes effect |
|---|---|---|---|---|
| Inactivity deadline budget | 20 min | ✅ per-project | `projects.loop_inactivity_minutes` — mobile project-edit sheet, or `projects.update` (REST / MCP) | next stamp / progress event |
| Absolute-cap budget | 2 h | ✅ per-project | `projects.loop_absolute_cap_minutes` — same | next stamp / progress event |
| `PreAgentIdle` hook enabled | `true` | ✅ overlay | `<dataRoot>/loop-hooks.yaml` → `pre_agent_idle.enabled` | SIGHUP (or restart) |
| `PostDirectiveOutcome` hook enabled | `true` | ✅ overlay | `loop-hooks.yaml` → `post_directive_outcome.enabled` | SIGHUP (or restart) |
| Synthesis-floor characters | 40 | ✅ overlay | `loop-hooks.yaml` → `post_directive_outcome.min_synthesis_chars` | SIGHUP (or restart) |
| Sweep interval | 45 s | ❌ fixed | `loopSweepInterval` (Go const) | rebuild |
| Escalation-target policy | one level up the chain | ❌ fixed | `escalateStall` (Go) | rebuild |
| Question-kind set | `help_request`, `select`, `approval_request`, `elicit`, `permission_prompt` | ❌ fixed | `questionAttentionKinds` (Go slice) | rebuild |

---

## 2. Configurable knobs

### 2.1 Per-project deadline budgets

The two per-hop budgets ([ADR-034](../decisions/034-orchestration-loop-closure.md)
D-2, §7) are hub defaults that any project may override:

- A `NULL` column = use the hub default (`loopInactivityBudget` /
  `loopAbsoluteCapBudget`). A positive integer (minutes) overrides it
  for **every loop-entity in that project**.
- `loopBudgets(projectID)` resolves the value; the sweep applies it
  wherever it sets a deadline — first-sight lazy-stamp, the escalation
  push, and the per-task progress bump.
- A changed override is picked up on the next stamp (new entity) or the
  next progress event (the bump re-resolves the budget); already-stamped
  idle entities keep their current deadline until then.
- Set it from the **project-edit sheet** ("Loop stall deadline" / "Loop
  hard cap", in minutes — blank clears back to the hub default) or over
  `projects.update`.

### 2.2 Lifecycle-hook config

The two hooks ([ADR-034](../decisions/034-orchestration-loop-closure.md)
D-5, §7) are configured by `<dataRoot>/loop-hooks.yaml`:

- `Server.New()` seeds the file from the bundled default when it is
  absent — it never overwrites an operator edit.
- The embedded default stays the fail-safe fallback: a missing or
  unparseable overlay falls back to it rather than silently disabling a
  hook.
- **SIGHUP hot-reloads** the file — a hook is toggleable without a
  restart, alongside `policy.yaml`.

```yaml
# <dataRoot>/loop-hooks.yaml
pre_agent_idle:
  enabled: true
post_directive_outcome:
  enabled: true
  min_synthesis_chars: 40
```

---

## 3. Fixed knobs — and why

Three knobs are Go constants by deliberate MVP scope:

- **Sweep interval** (`loopSweepInterval`, 45 s) — a daemon-level
  operational constant, not a per-project concern. It only bounds
  detection lag (it must stay ≪ the smallest deadline); there is no
  product reason to expose it.
- **Escalation-target policy** — the sweep always escalates one level up
  the chain (`none → escalated_steward → escalated_principal`). Whether
  a stall should instead go straight to the principal is a real policy
  question; [ADR-034](../decisions/034-orchestration-loop-closure.md) §3
  records it as post-MVP config and it is the natural next
  configurability step.
- **Question-kind set** (`questionAttentionKinds`) — which
  `attention_items.kind` values are loop-bearing questions. This is
  structural (it follows the attention-kind taxonomy), not a tunable.

---

## 4. References

- [ADR-034](../decisions/034-orchestration-loop-closure.md) — the
  loop-closure runtime; §7 holds the configurability amendments.
- [`../plans/message-routing-rollout.md`](../plans/message-routing-rollout.md)
  — the rollout that shipped the runtime (Phase B).
- [`attention-kinds.md`](attention-kinds.md) — the attention-kind
  taxonomy the question-kind set draws from.
