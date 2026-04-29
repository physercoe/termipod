# Attention kinds — author's decision tree

> **Type:** reference
> **Status:** Current (2026-04-29)
> **Audience:** contributors (humans + AI agent maintainers)
> **Last verified vs code:** v1.0.335

**TL;DR.** When an agent needs the principal to weigh in, it picks one
of three interaction shapes — `approval_request` (binary), `select`
(n-ary structured), or `help_request` (open-ended free text) — based on
the cardinality of the answer space. This page is the canonical
authoring reference; the MCP tool docstrings carry the short form, this
page carries the long form with worked examples.

---

## 1. The three shapes

| Kind | Answer space | MCP tool | Mobile rendering |
|---|---|---|---|
| `approval_request` | binary {approve, reject} | `request_approval` | Approve / Deny |
| `select` | closed enumerated set of N options | `request_select` | one button per option |
| `help_request` | open / unlistable / free text | `request_help` | text composer + Skip |

Two more attention kinds exist but are not agent-callable:

- `permission_prompt` — Claude SDK's pre-tool gate; emitted by the
  engine's permission hook, not by the agent calling a tool.
- `template_proposal` — produced by `templates_propose`; the principal
  reviews a structured diff, not a free-text reply. Use that tool when
  the right artifact is the template body itself.
- `idle` — emitted by host-runner when an agent is paused awaiting
  input. State signal, not a request.

## 2. The decision tree

Apply in order; stop at the first rule that fires.

1. **Can you write down every valid answer ahead of time?**
   - **No** → `request_help`. The principal will type the answer.
   - **Yes**, and there are exactly two of opposite polarity (yes/no,
     do/don't, ship/abort) **and you have a concrete proposed action**
     → `request_approval`. Default outcome on no reply is "rejected"
     (safer for risky actions).
   - **Yes**, and there are N comparable options the principal picks
     among → `request_select`. Default on no reply is "no decision"
     (the agent should treat timeout as a non-answer, not a reject).

3. **Tiebreaker for ambiguous binary cases.** If you have a proposed
   action and want the principal to gate it (approve = "yes do that",
   reject = "no don't") → `request_approval`. If both options are
   equal-status paths and you want the principal to pick a direction
   (no proposal, just a question) → `request_select` with N=2.

4. **Tiebreaker when in doubt between any two kinds: pick the more
   open one.** Errors that compress the answer space (open → constrained)
   force the principal into a wrong-shape answer and the agent into
   re-prompting. Errors that expand it (constrained → open) are minor
   friction. So: `help_request` ≻ `select` ≻ `approval_request`.

## 3. `help_request` — when and how

Use `request_help` when you need the principal's *words*, not their
*choice*:

- **Clarification**: "Did you mean X or Y, or something else?"
- **Direction**: "How would you approach this?"
- **Opinion**: "What's the right tradeoff here?"
- **Hand-back**: "I can't proceed — situation too complex / missing
  context / I've hit a wall and need you to take over."

The `mode` field tunes the urgency framing:

- `mode: clarify` (default) — routine question, agent expects to
  continue after the answer.
- `mode: handoff` — agent is genuinely blocked; the principal may want
  to take over rather than just answer. Severity defaults to `major`
  (vs `minor` for clarify) unless explicitly downgraded.

Ship a brief `context` field with your own framing — what you've
already considered, what you tried, why you're stuck. The principal
shouldn't have to read the whole transcript to understand what you're
asking.

### Worked example — clarify

```json
{
  "name": "request_help",
  "arguments": {
    "question": "Should I refactor the auth flow before or after the cache layer?",
    "context": "Both modules touch User; auth changes are larger but cache is on the critical path. Either order works; preference matters.",
    "mode": "clarify"
  }
}
```

### Worked example — handoff

```json
{
  "name": "request_help",
  "arguments": {
    "question": "I can't reproduce the migration failure locally — same Postgres version, same data shape. Can you take a look?",
    "context": "Tried: full reset, reseeding from snapshot, swapping the migration tool. Each pass succeeds locally but fails in CI on the same column type cast.",
    "mode": "handoff",
    "severity": "major"
  }
}
```

## 4. Anti-patterns

These are the cases where agents reach for the wrong kind. The fix is
always to apply rule 4 (when in doubt, pick more open).

| Anti-pattern | Why it's wrong | Use instead |
|---|---|---|
| `request_approval` for "should I write the test first or the impl first?" | Both are valid actions; this is a path-choice not a risk gate | `request_select` with `["test first", "impl first"]` |
| `request_select` with `["yes", "no", "let me think"]` | "Let me think" leaks open-endedness through the option labels | `request_help` (with `mode: clarify`) |
| `request_select` for "name this PR" with three candidate titles | Principal probably wants to write their own; options leak the agent's preference | `request_help` (suggested options can go in `context`) |
| `request_approval` for "I'm stuck, can you help?" | A binary approve doesn't carry information; agent will need a follow-up | `request_help` (`mode: handoff`) |
| `request_help` for "delete this file?" | Open answer space invites the principal to type "yes" or "no" — they want a button | `request_approval` |

## 5. Resolution semantics

All three kinds resolve through the same `/v1/teams/{team}/attention/{id}/decide`
endpoint, but `decide` carries different fields per kind:

```text
approval_request → { decision: "approve" | "reject" }
select           → { decision: "approve", option_id: "<picked option>" }
                 → { decision: "reject" }    # dismiss; no option chosen
help_request     → { decision: "approve", body: "<free-text reply>" }
                 → { decision: "reject", reason: "..." }    # dismiss
```

For `help_request`, an `approve` without a `body` is a 400 — the
principal must either type a reply (approve+body) or dismiss
(reject). The agent's long-poll on `request_help` returns the last
decision dict verbatim, so `body` flows back without a second hop.

## 6. Severity, not kind

Severity is orthogonal to kind. A `help_request` can be `minor` (idle
question) or `critical` (production blocker). An `approval_request`
can be `minor` (cosmetic) or `critical` (deletes data). Don't pick a
kind to convey urgency — pick by answer-space cardinality and use
`severity: minor | major | critical` to convey urgency.

## 7. Vendor neutrality

These kinds are platform-level and engine-agnostic. Claude has
`AskUserQuestion` as an in-stream tool; Codex/Gemini will have their
own. Each engine's host-runner translator may also emit a
`help_request` attention so the question reaches the principal even
when they're not actively viewing the chat — that's a rendering
choice per engine, not a coupling between the engine's tool and this
kind.

## 8. Where to look in the code

- `hub/internal/server/mcp_more.go` — `mcpRequestApproval`, `mcpRequestSelect`,
  `mcpRequestHelp` handlers + tool definitions.
- `hub/internal/server/handlers_attention.go` — `decide` endpoint;
  validates `body` for help_request.
- `lib/screens/me/me_screen.dart` — `_ApprovalActions` (binary +
  select) and `_HelpRequestActions` (free text).
- `hub/internal/server/tiers.go` — `request_approval`, `request_select`,
  `request_help` are all `TierRoutine` (meta tools; the wrapped action
  carries the real tier).
