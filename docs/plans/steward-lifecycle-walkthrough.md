# Steward-driven project lifecycle walkthrough

> **Type:** plan
> **Status:** Open (2026-05-10)
> **Audience:** principal · contributors · QA
> **Last verified vs code:** v1.0.480

**TL;DR.** Prove that the floating steward overlay can drive a full
research-project lifecycle — read, create, edit, write, delegate via
A2A — using the existing `seed-demo --shape lifecycle` portfolio and
the MCP tool surface that already ships in `hub/internal/hubmcpserver/
tools.go`. No new tools land; this is a wedge that exercises what
exists and surfaces the gaps. The companion test doc
[`how-to/test-steward-lifecycle.md`](../how-to/test-steward-lifecycle.md)
is the step-by-step QA script.

---

## Why now

ADR-023's overlay (v1.0.464–480) gives us a persistent floating
steward + `mobile.navigate` (read-only). The principal's directive
on 2026-05-10: *"the next step is to create / edit / write etc.
action; we have a seed-demo for project lifecycle UI test, now we
should use steward to do these work step by step, and especially
test the A2A protocol works well."*

Two problems we need to surface:

1. **Coverage hole.** The current
   [`test-agent-driven-prototype.md`](../how-to/test-agent-driven-prototype.md)
   only covers navigation (Scenario 10 explicitly asserts that
   *write attempts must NOT succeed*). We have never end-to-end
   tested the steward calling `documents.create` / `runs.create` /
   `agents.spawn` / `a2a.invoke` from the overlay surface.
2. **A2A blind spot.** A2A relay (ADR-003) shipped, but there's no
   regularly-exercised QA scenario where the team's general
   steward delegates to a *different* agent and surfaces the
   reply back into the overlay chat. Worker delegation is the
   load-bearing claim of the multi-agent positioning
   (`positioning_vs_competitors.md`).

This wedge is **not** about adding new MCP tools. The hub already
exposes 39 tools. The wedge is about *exercising* them via the
overlay and writing down the step-by-step recipe that future
contributors can re-run on every release.

---

## Goal

After this wedge:

1. A QA tester can run the lifecycle walkthrough end-to-end in
   ≤ 20 minutes and answer YES/NO on each scenario without
   external help.
2. Every scenario has explicit failure modes — when something
   breaks, the tester knows what to report.
3. The A2A scenario produces a worker reply visible in the
   steward overlay chat without any direct hub log inspection.
4. Every gap discovered (missing tool, missing UI confirmation,
   missing failure mode surface, drifted MCP description) is
   captured as a follow-up wedge or memory entry — not silently
   patched over.

---

## Scope (the seven wedges)

Sized to be runnable in a single QA pass; each wedge is a single
overlay-chat directive + verification.

| # | Verb / surface | Steward action | Verify in UI |
|---|---|---|---|
| W1 | **read** — `projects.list` | `What projects do I have?` | Steward replies with 5 names from the lifecycle seed |
| W2 | **edit** — `projects.update` | `Set the goal of the idea project to 'evaluate sparse attention for sub-1B LMs'.` | Project Detail header shows new goal after the steward navigates there |
| W3 | **create** — `documents.create` | `Add an idea memo to the idea project: 'Hypothesis — sparse attention beats dense @ <250M params on long-context retrieval.'` | Documents tab on project shows new entry |
| W4 | **create** — `plans.create` + `plans.steps.create` | `Draft a method plan for the method project with three steps: literature scan, ablation table, scaling sweep.` | Plan widget on project shows 3 steps |
| W5 | **edit** — `plans.steps.update` | `Mark step 1 of the method plan as done.` | Step 1 status pill flips to ✓ |
| W6 | **create** — `runs.create` + `runs.attach_artifact` | `Log a run on the experiment project, seed=42, then attach a fake eval_curve artifact pointing at /tmp/curve.json.` | Runs tab on project shows new row + artifact link |
| W7 | **A2A** — `agents.spawn` + `a2a.invoke` | `Spawn a worker agent on host <host-id>, ask it to give me a one-line title for the method project.` | Worker reply appears in overlay chat as a steward bubble; A2A audit row visible in Activity |

Each wedge ends with the steward navigating the user to the result
via `mobile.navigate` — exercising the read-only verb we already
ship as the audit trail of the write action.

---

## Non-goals

- **New MCP tools.** Whatever the steward needs that doesn't
  exist (e.g. `documents.update` for typed-doc section edits)
  becomes a follow-up wedge, not part of this one.
- **Action-aware intent pill rendering.** ADR-023 D10 reserves
  this for after write actions land in the wire format. The
  walkthrough verifies write actions WORK; the pill rendering
  upgrade (verb = "created", "edited", "wrote") is a separate
  wedge that consumes the proven foundation.
- **Voice input.** Deferred — see
  [`voice-input-overlay-v1.md`](voice-input-overlay-v1.md)
  status block.
- **Permission gating UX.** `agents.spawn` may return 202 +
  attention_id when policy gates it. v1 of the walkthrough
  pre-approves the policy so the gate doesn't fire; the
  permission UX gets its own walkthrough later.
- **Multi-host load-balancing.** W7 spawns on a specific host;
  cross-host delegation is a follow-up.

---

## Pre-conditions (assumed configured)

The principal said "assume the hub and hosts has configured." That
means:

1. Hub running, ≥ v1.0.480-alpha.
2. ≥ 1 host registered + `connected`. Two hosts ideal so W7 can
   spawn the worker on a *different* host than the steward — the
   honest A2A test.
3. Phone / emulator with the matching APK installed and the
   overlay toggle on (Settings → Experimental → Steward overlay).
4. Hub seeded with `--shape lifecycle` (5 research-*-demo
   projects).
5. Worker engine available on the chosen host. Recommend
   `claude-code` since the spawn template ships in the seed; any
   ACP-conformant engine works.
6. Policy permits `agents.spawn` for the team without manual
   approval. (For test environments, the default policy permits.)

If any precondition isn't met, the walkthrough's own first
scenario surfaces it before the writes start.

---

## Done criteria

- [ ] All 7 scenarios in the test doc pass on a fresh seed.
- [ ] The A2A worker reply renders as a steward bubble in the
      overlay chat (no need to dig in hub logs).
- [ ] Every scenario completes in ≤ 90s wall-time on a typical
      CPU host (the worker reply included).
- [ ] At least one full re-run on a *different* host than v1's
      passes identically.
- [ ] Failure modes documented in the test doc match what
      actually appears when each subsystem is broken (verified
      by deliberate fault injection on at least three:
      A2A relay down, host disconnected, policy gate-on).
- [ ] Any tool description that misled the steward during a
      run gets a docstring fix in the same wedge.

---

## Recommended sequence

1. Land the test doc skeleton (this commit). Run it manually
   end-to-end on one host. Note every gap.
2. For each gap that's a *steward grammar* problem (steward
   chose the wrong tool), update the MCP tool description in
   `tools.go` so the next run picks correctly. No new tools.
3. For each gap that's a *UI confirmation* problem (steward
   wrote, but the mobile UI didn't refresh to show it), file a
   follow-up wedge on the affected provider's invalidation
   path. Don't block the walkthrough on it — the steward write
   succeeded, which is what this wedge verifies.
4. For the A2A scenario specifically: if the worker reply
   doesn't surface in chat, that's a *blocker*. The whole
   multi-agent positioning rests on it. Keep the wedge open
   until W7 is green.
5. After all 7 are green twice (different hosts), close the
   wedge and resume:
   - voice-input-overlay-v1 (modality on top of proven actions)
   - action-aware intent pill (ADR-023 D10 carryover)
   - the post-MVP P2 from `agent-events-shared-provider.md`

---

## Open questions

- **Q1 — How does the worker reply land in the overlay chat?**
  `a2a.invoke` returns the JSON-RPC envelope synchronously to
  the steward. The steward then needs to surface it via a
  text frame on its own session (`POST /agents/{id}/events
  kind=text`). Verify on first run that the overlay's SSE
  picks this up. Variant: steward summarizes worker reply in
  a turn-final response. Pick whichever the engine actually
  does and document it.

- **Q2 — Do we exercise `runs.attach_artifact` with a real
  file or a placeholder URI?** v1 plan: placeholder URI
  (`file:///tmp/curve.json`) — the artifact row exists, the
  bytes don't. A "real artifact upload" path is a follow-up
  wedge.

- **Q3 — What's the canonical worker handle / spawn template
  for W7?** Likely the existing `worker.summarizer` template if
  it ships, otherwise spawn a fresh handle. Picking this is
  part of the first dry-run.

---

## References

- [ADR-023 — agent-driven mobile UI](../decisions/023-agent-driven-mobile-ui.md)
- [ADR-003 — A2A relay required](../decisions/003-a2a-relay-required.md)
- [`how-to/test-agent-driven-prototype.md`](../how-to/test-agent-driven-prototype.md) — companion (read-only)
- [`how-to/test-steward-lifecycle.md`](../how-to/test-steward-lifecycle.md) — companion (write + A2A; ships with this plan)
- [`hub/internal/server/seed_demo_lifecycle.go`](../../hub/internal/server/seed_demo_lifecycle.go) — the 5-project portfolio
- [`hub/internal/hubmcpserver/tools.go`](../../hub/internal/hubmcpserver/tools.go) — 39 MCP tools the steward dispatches against
