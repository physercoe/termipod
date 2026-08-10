# Debug why an agent reads as idle / working / blocked

> **Type:** how-to
> **Status:** Current (2026-08-10)
> **Audience:** contributors, operators, testers
> **Last verified vs code:** 2026.805.1022 (pane-state-manifests P4)

**TL;DR.** For an engine with no structured driver, the fleet's idea of
what an agent is doing comes from **screen rules** — a per-engine TOML
manifest matched against the pane's visible text. When that verdict looks
wrong, `pane_explain` shows the whole evaluation: every rule, whether it
matched, what it was looking for, and a bounded preview of what it was
looking at. Two modes: **live** reads a running agent's pane, **supplied**
evaluates text you paste. Neither ever moves the full screen off the host.

---

## 1. When you need this

The symptom is always one of three:

| Symptom | Usually |
|---|---|
| An agent sits at an approval dialog and nothing raises attention | its family has no manifest, or the blocking rule needs a `visible_blocker` it does not carry |
| An agent reads `working` forever | a spinner-shaped rule matches its idle screen |
| An agent reads `idle` while clearly busy | no rule matched at all, and the fallback is `idle` by design |

The third is the one that looks like a bug and is not: a known engine
whose screen matches nothing classifies as idle with
`fallback_reason: default_known_agent_idle_fallback`. `pane_explain` is
how you tell "no rule matched" apart from "the wrong rule matched".

## 2. Which engines are classified at all

```bash
curl -sH "Authorization: Bearer $TOKEN" \
  "$HUB/v1/teams/$TEAM/pane_explain" | jq .
```

```json
{ "families": [
  { "family": "claude-code", "manifest_id": "claude",  "manifest_version": "1", "source": "vendor" },
  { "family": "codex",       "manifest_id": "codex",   "manifest_version": "1", "source": "vendor" }
] }
```

**An engine that is absent has no rules and is never classified.** That is
the answer to most "why does this agent never show a state" questions, and
it is deliberate (plan D-3): classifying an engine with another engine's
rules produces confident, wrong attention. `source` says whether the
manifest is `vendor` (byte-exact from upstream) or `overlay` (ours) —
which decides who to talk to about a rule that surprises you.

## 3. Explain a live pane

```bash
curl -sH "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"agent_id":"01K..."}' \
  "$HUB/v1/teams/$TEAM/pane_explain" | jq .
```

The hub resolves the agent's host and pane, and the **host** captures and
evaluates — the screen never crosses to the hub. Refusals name their
precondition rather than collapsing into one error:

| Code | Meaning |
|---|---|
| `404` | no such agent in this team |
| `409` | the agent has no host, has no tmux pane, or is terminated |
| `422` | its family has no manifest (`{"error":"unmapped_family","family":…}`) |
| `503` | that host's embedded manifests failed to load; detection is off there |
| `504` | the host did not answer in time |

## 4. Explain a screen you already have

The same route, with text instead of an agent — upstream's `--file` mode.
No host is involved, so this works against a screen pasted out of a bug
report, and it is how you check a rule change before shipping it:

```bash
curl -sH "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"family":"codex","screen":"• Working (4s • esc to interrupt)\n› 1. Yes, proceed\n"}' \
  "$HUB/v1/teams/$TEAM/pane_explain" | jq '.explain.state, .explain.matched_rule'
```

`agent_id` and `screen` are **exclusive**. They answer different questions
— "what is that agent doing" versus "what would the rules say about this
text" — and a body carrying both would have to pick one silently.

## 5. Reading the record

```json
{
  "mode": "live",
  "family": "codex", "pane_id": "%7", "host_id": "host-1",
  "screen_bytes": 96, "screen_lines": 3,
  "osc_title": "my project",
  "explain": {
    "manifest_id": "codex", "manifest_version": "1", "source": "vendor",
    "state": "blocked",
    "matched_rule": { "id": "live_strong_blocker", "priority": 980, "region": "whole_recent" },
    "visible_blocker": true,
    "rules": [ { "id": "…", "matched": false, "priority": 100,
                 "evidence": { "contains": ["? for shortcuts"],
                               "region_bytes": 40, "region_preview": "› 1. Yes, proceed" } } ]
  }
}
```

Four fields carry most of the diagnosis:

- **`mode`** — `live` or `supplied`. Never inferred: a pasted screen must
  not read as a fact about a running agent.
- **`rules[]`** — *every* rule, not just the winner. An unmatched rule
  carries the same evidence as a matched one, because "did not match" is
  an unfalsifiable claim without the region it read.
- **`evidence.region_preview`** — what that rule actually looked at,
  bounded to 240 characters per rule. If a rule you expected to fire shows
  a preview that is empty or truncated at the wrong place, the **region**
  is the bug, not the matcher.
- **`osc_title`** — empty means every `osc_title` rule was evaluated
  against nothing and *could not* have fired. Under tmux the title comes
  from `#{pane_title}`; an engine that never sets OSC 0/2 leaves it blank.

Winner selection is highest `priority`, ties to the **earliest rule in
file order**. Two rules matching is normal; the one with the higher
priority wins, and that ordering is upstream's semantics, which is why the
vendored manifests are byte-exact.

### What is deliberately not in the record

- **The full screen.** Only per-rule bounded previews travel; the capture
  stays on the host. `screen_bytes` / `screen_lines` are there so you can
  see the pane was 60 rows without shipping 60 rows.
- **`osc_progress`.** tmux does not surface OSC 9;4 to a client, so the
  region is always empty and the handful of vendored rules referencing it
  are inert here. Documented rather than worked around.

## 6. From the desktop

**Inspect → Open → “Explain an agent pane…”** lists running agents that
have a pane; engines with no manifest appear greyed with the reason rather
than hidden. The card shows the verdict, the manifest that produced it,
and every rule with matched / unmatched distinguished by a glyph as well
as by colour; click a rule to see what it wanted and what it read.

For the supplied mode, paste a screen into a scratch tab and press
**Read as pane state**, then choose the engine.

## 7. Who may call it

`pane_explain` refuses **agent-kind tokens** with `403`. The record carries
bounded previews of a terminal, and the egress proxy in front of a spawned
agent forwards every path — so without the refusal, one agent could read
another agent's screen through a debugging tool. It is a director tool;
operator, owner, user and host tokens pass.

This is narrower than the automatic path deliberately: the pane-state
attention row raised on a blocked streak carries a **rule id and never any
pane text**, because it publishes to everyone watching a feed. This verb
answers one person who asked.

---

**See also**

- [`plans/pane-state-manifests.md`](../plans/pane-state-manifests.md) — the
  lane, its decisions, and what each wedge shipped.
- [`reference/attention-kinds.md`](../reference/attention-kinds.md) — the
  `idle` row a blocked classification raises, and how to tell its two
  raisers apart.
- The state-authority order — **structured driver > screen manifest >
  nothing** — is decision **D-2** of the plan above. It is not yet in the
  spine; [`spine/agent-lifecycle.md`](../spine/agent-lifecycle.md) is an
  axiom doc that predates screen rules and does not mention them.
