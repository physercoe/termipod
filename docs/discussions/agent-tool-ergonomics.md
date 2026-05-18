---
name: Agent tool ergonomics
description: Three orthogonal failure modes — discovery, documentation depth, and error recovery — collapsed into one symptom when a project steward called the wrong tool five times trying to read a memo back. The doc names the principles (one canonical input per tool; descriptions are the lookup not the read-every-turn payload; errors hint the next step), audits where termipod has the gap (verbose top-tier descriptions; no meta-discovery tool; no hint field on 4xx errors), and recommends the two-tier description + hint-bearing error + per-persona index design borrowed from claudecode's CLAUDE.md model.
---

# Agent tool ergonomics

> **Type:** discussion
> **Status:** Open (2026-05-18) — captured the day a steward tried five wrong tools to read back a doc by ULID. The companion plan is [`agent-tool-ergonomics-rollout.md`](../plans/agent-tool-ergonomics-rollout.md). This doc is the durable framing.
> **Audience:** contributors · principal
> **Last verified vs code:** v1.0.630-alpha

**TL;DR.** Today's MCP catalog assumes the agent already knows
which tool to pick, how to call it, and how to recover when wrong.
A project steward on 2026-05-18 needed to read a memo back by
ULID, tried `get_project_doc(path=<ULID>)` (filesystem tool —
"file does not exist"), `documents_get` (wrong delimiter, nonexistent),
`search` (separate bug), `journal_read`, `audit_read`,
`get_feed`, `list_channels` — none of them. The actual tool
(`documents.get`) didn't exist as an MCP entry until v1.0.630.
But even if it had, the discovery path was a guessing game.
The principle: **tools don't have to be perfectly named or
known up-front, but the agent must have a structured way to
discover the right one and recover when it picks wrong.** This
doc captures the three failure modes (discovery, documentation
depth, error recovery), the bad fourth idea (input
polymorphism — making one tool accept multiple equivalent
forms), and recommends a two-tier description + hint-bearing
error + per-persona index design that borrows from claudecode's
CLAUDE.md skill model.

---

## 1. The incident as case study

A project steward on Claude Code, after a worker called
`tasks.complete(summary="doc_id=01KRV538…")`, wanted to read the
doc and quote the takeaway back to the principal. The steward's
sequence over ~6 turns:

1. `mcp__termipod__get_project_doc(path="01KRV538…", project_id="01K…")` → `32000 file does not exist`.
2. `mcp__termipod__documents_get(...)` → no such tool available.
3. `mcp__termipod__search(q="hyperparameter")` → sql logic error, no such column.
4. `mcp__termipod__list_channels()` → unrelated; returned channel rows, not docs.
5. `mcp__termipod__journal_read()` → returned the steward's own journal, not the worker's doc.
6. Eventually gave up and asked the principal in chat.

Every step was a reasonable guess in isolation. None had any
signal pointing at the right tool. The actual answer
(`documents.get(document_id=...)`) didn't exist as an MCP
catalog entry — even though the HTTP endpoint did
(`handleGetDocument`). Same MCP catalog × dispatcher × handler
discipline bug as v1.0.591 (`request_project_steward` shipped
without `tools/list` entry).

The principle: **discovery failed**, then **descriptions failed
to disambiguate** (steward saw `get_project_doc` and assumed it
was the get-a-doc tool), then **errors failed to hint** (the
"file does not exist" had no "this tool reads filesystem files;
to read by ULID use…" sentence). Each failure on its own would
have cost one turn; the cascade cost six.

---

## 2. Three orthogonal failure modes

Distinguishing these is load-bearing — each calls for a different
fix, and conflating them produces over-engineered solutions that
help none.

### 2.1 Discovery

> Agent has an intent ("find what this project is about", "read
> this doc by id", "summarise a worker's output") and no tool
> name maps cleanly to the intent. They scan the catalog by
> keyword, guess, and call.

Today's discovery surfaces:

- The MCP `tools/list` response — every tool's name + description
  is in the agent's context window every turn the agent invokes
  the MCP server. (~50 tools, ~30KB of descriptions.)
- The persona prompt — may mention specific tools by name in the
  workflow sections, but doesn't enumerate them.
- The agent's training — generic knowledge of "search", "list",
  "get" patterns from REST APIs.

What's missing:

- An **intent → tool** index. Agent thinks "I need to read a doc
  by id"; there's no table mapping that to `documents.get`.
- A **meta-discovery tool** the agent can call to ask "which tool
  does X". `tools/list` returns names + descriptions, but
  scanning 50 entries to filter by intent is what the LLM ends
  up doing in-context every turn — expensive and lossy.

### 2.2 Documentation depth

> Once the agent has a candidate tool, they need to know how to
> call it: parameter names, required vs optional, return shape,
> failure modes, a worked example.

Today (post v1.0.621 hygiene rule): every tool description ships
all of the above in the catalog payload. The hygiene rule says
descriptions are present-tense contract — no version markers, no
doc references, no rationale prose — but they're still full
shape + examples. Examples:

- `agents.spawn` description is ~2KB (template + inline shape
  examples + return shape + task linkage section).
- `documents.create` is ~1KB.
- `projects.update` is ~600 bytes.

Across ~50 tools, the catalog is ~30KB in every MCP server
session. The Claude API tokens this every dispatch.

Tradeoff:

- **Inline-everything** (current): agent always has full detail;
  no extra round-trip. Costs every dispatch.
- **Lean catalog + meta-detail tool**: catalog has 1-line summary
  per tool; `tools.describe(name)` returns full description +
  examples. Agent pays one extra call only when invoking. Cost:
  one extra round-trip per "first use of unfamiliar tool"; saves
  ~30KB × every turn for the 99% case where the agent is
  invoking a tool it already knows.

Claudecode's CLAUDE.md model is the comparable design: an index
file loaded every turn (compact pointer list) + skill files
loaded on demand (full detail when invoked). Termipod's
equivalent is missing — every tool's full description is the
"index entry," because there is no skill file.

### 2.3 Error recovery

> The tool call fails. The agent has to decide: retry as-is,
> retry with different params, switch to a different tool, or
> escalate.

Today's error shapes:

- HTTP 4xx → `writeErr(w, status, message)` → text body returned
  to the MCP client → bubbles up as JSON-RPC error code (e.g.
  `32000`) + the text message.
- Most messages name what went wrong ("doc not found", "invalid
  json", "project not found").
- Almost none name **what to try instead**.

Comparison:

```
Today:  "file does not exist"
Goal:   {"code": "not_found",
         "message": "path 'OLKRV538…' does not exist in docs_root for project 01K…",
         "hint": "this tool reads filesystem files relative to docs_root.
                  If you have a document ULID, call documents.get(document_id=...) instead.",
         "see_tool": "documents.get"}
```

Cost: one extra field per error path, structured so the agent's
LLM reads `hint` and adjusts. Same way HTTP `Link:
rel=alternate` works in browsers.

---

## 3. The polymorphism temptation

A natural fourth idea — make `documents.get` accept either a
ULID OR a filesystem path. The user proposed this directly.
Tradeoff is sharp:

**Pro (convenience):**
- Matches LLM intuition — one fewer branching decision.
- Reduces "which tool" failures by collapsing the choice.

**Con (semantic):**
- Hides the distinction between filesystem files (`docs_root` —
  human-authored shared context, plan files etc) and DB rows
  (`documents` table — agent-authored memos with versioning,
  reviews, annotations). These are different storage tiers with
  different operations; collapsing the read API doesn't
  collapse the storage.
- Ambiguates errors — "document not found" but at which storage?
  The agent now needs another tool to find out (or another field
  in the error).
- Harder to authorize — different tiers may have different
  ACLs. A polymorphic input forces the authorizer to inspect
  the argument shape.
- Harder for the next contributor to reason about — "what does
  this tool return when called with shape X vs Y?" becomes a
  branching contract.

**Well-tested practices (UNIX, REST, GraphQL, MCP):**
- Each operates on **one canonical input shape per tool**.
- `ls` and `find` are different tools; they don't share an arg
  parser. Composability beats convenience.
- GraphQL even moves further: each return type's fields are
  resolvers, each with its own input shape. Polymorphism is
  pushed up to the orchestration layer (queries that combine
  resolvers), not into the resolver itself.

Termipod has the same fork. The convenience win of polymorphism
is real but small. The cost (semantic ambiguity, authorization
complexity, contributor confusion) is medium and recurring.

**Recommendation:** keep one canonical input per tool. Pay the
cost on the **other three failure modes** (discovery, depth,
error recovery) so the agent never picks wrong, rather than
making each tool magically accept everything.

---

## 4. The recommended design

Four pillars. Each implementable independently; the bundle is
where the leverage is.

### 4.1 Two-tier descriptions

- **Catalog (every dispatch):** 1 line summary + required params.
  Example: `documents.get — Fetch a document by id. Required: document_id (ULID).`
- **`tools.describe(name)` (on-demand):** full body + examples + failure modes + cross-refs.

The catalog stays under ~5KB total (50 tools × 100 bytes). The
detail loads only when the agent commits to a call.

Implementation: each tool ships two strings. `tools/list`
returns the short; new meta-tool `tools.describe` returns the long.

### 4.2 Per-persona intent → tool index

Each persona prompt (steward, coder, critic, etc.) ships a
compact "intent → tool" table. Example for a steward:

| Intent | Tool |
|---|---|
| Read a doc by ULID | `documents.get` |
| Read a filesystem file under project docs_root | `get_project_doc` |
| Search by text content | `search` (events FTS) |
| List recent activity | `get_feed` |
| Read this agent's journal | `journal_read` |
| Read another agent's spawn-task body | `get_task` |
| Read attention/approval queue | `get_attention` |
| Look up an agent by handle | `list_agents` then filter |

~15 lines per persona. The agent uses the index to pick the
tool, then calls `tools.describe(name)` if it doesn't recall
the shape. The persona prompt does NOT enumerate full
descriptions — that's `tools.describe`'s job.

### 4.3 Hint-bearing errors

Every 4xx error path adds a `hint` field. Structure:

```json
{
  "code": "not_found" | "invalid_input" | "permission_denied" | ...,
  "message": "<what happened, with the offending value named>",
  "hint": "<what to do instead, naming the right tool if applicable>",
  "see_tool": "<optional: explicit pointer to the tool name>"
}
```

Cost: per-handler edit. Cheap individually; bulk pass for the ~30
handlers that surface to MCP.

Some specific candidate hints (from this incident):

- `get_project_doc` 404 + path looks ULID-shaped → "this tool
  reads filesystem files; for document ULIDs use documents.get".
- `documents.get` 404 → "this tool reads documents created via
  documents.create; for filesystem files use get_project_doc".
- `search` no-results → "try a shorter query, or list_documents
  / get_feed for full enumeration".
- Any tool called with permission denied → "your role lacks
  this capability; see roles.yaml or escalate via request_help".

### 4.4 No tool input polymorphism

Each tool keeps one canonical input shape. The fix for "agent
picked wrong tool" lives in 4.1-4.3, not in tool 4.

The one exception worth considering: legacy aliases (e.g.
`request_decision` ↔ `request_select`). These should be
documented as deprecated aliases with a deprecation hint in the
description, not as a long-term contract.

---

## 5. What this does NOT solve

- **Bad descriptions** are still bad. Two-tier hierarchy makes
  the cost-of-bad lower (only on-demand instead of every turn)
  but doesn't make descriptions auto-correct.
- **Tool naming chaos.** If two tools have intent-overlap
  (`documents.list` and `get_feed` both return "stuff in this
  project"), the per-persona index helps the agent pick, but
  the underlying name confusion remains. Renaming is a separate
  wedge.
- **Catalog drift.** A `tools.describe` response that lags behind
  the actual handler is a new failure mode. Same defect class
  as MCP catalog × dispatcher discipline; needs the same
  enforcement (test that every tool registered has both
  short + long descriptions).

---

## 6. Open questions

1. **Where does the per-persona index live?** Two options:
   (a) inline in each `templates/prompts/*.md` (persistent, edits
   require touching prompts), (b) a separate `tools-index.md`
   prompt fragment included via `context_files`. Option (a) is
   simpler; (b) decouples the index from persona narrative.
2. **Does the meta-tool register as a tool, or as a built-in MCP
   protocol verb?** MCP spec has `tools/list` and `tools/call`;
   `tools/describe` would be a sibling but isn't part of the
   spec. Termipod-specific MCP tool `tools.describe` is the
   simpler path.
3. **How aggressive should the hint engine be?** Cheap path:
   per-handler hint strings (manually written). Aggressive path:
   automatic hint inference based on the argument shape (the
   `get_project_doc(path=<ULID>)` case is detectable). Cheap
   path first; aggressive only if cheap proves insufficient.
4. **Backward-compat for the catalog change.** Stripping
   descriptions to 1 line breaks anything that read the long
   form from `tools/list`. Mitigation: ship both fields
   (`description` short, `descriptionFull` long), let old
   clients still see something useful. Negotiate a clean break
   in a later cycle.

---

## 7. References

- [validate-at-every-boundary.md](validate-at-every-boundary.md) —
  prior framing on every-boundary discipline, esp. §3 Layer 4
  (description ↔ schema audit lint).
- [`../reference/hub-mcp.md`](../reference/hub-mcp.md) —
  current MCP surface reference; this discussion proposes
  evolving the description format documented there.
- [`../reference/glossary.md`](../reference/glossary.md) —
  canonical names for `documents`, `docs_root`, `project doc`,
  `memo` etc. The index in §4.2 must use glossary forms.
- Claude Code's CLAUDE.md model — external reference for the
  index + skill-load-on-demand pattern this discussion borrows.
