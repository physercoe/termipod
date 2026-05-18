---
name: Tool catalog structure and naming
description: ADR-031 asked whether an agent can *find* the right MCP tool. This doc asks the companion question — is the catalog itself well-grounded for the contributors who extend it? Audit finds the catalog functional but organized by accretion history, not domain: two unreconciled naming conventions, apparent duplicate pairs across the split, four catalog sources in two shapes, one tool's identity spread across catalog def + dispatch case + tier row + roles pattern with no single registration point, and a code layout that doesn't match the domain grouping the docs already use. §3 re-grounds the naming question against the MCP 2025-11-25 spec and Anthropic's tool-design guidance — the scheme is eval-sensitive, so it is decided empirically, not by analogy. Recommends promoting to an ADR (O-A naming + O-B single registration point), sequenced before or fused with ADR-031 W2, which rewrites every catalog entry anyway.
---

# Tool catalog structure and naming

> **Type:** discussion
> **Status:** Open (2026-05-18) — raised while implementing ADR-031 W1, when verifying the catalog topology surfaced a deeper structural question. Revised the same day: the naming recommendation was re-grounded against the MCP 2025-11-25 spec and Anthropic's tool-design guidance after the first draft picked a delimiter by REST analogy (§3). Companion to the [agent-tool-ergonomics discussion](agent-tool-ergonomics.md) — that doc is agent-facing, this one is contributor-facing.
> **Audience:** contributors
> **Last verified vs code:** v1.0.630-alpha (+ ADR-031 W1 `tools.get`, landed unreleased)
> **Last verified vs practice:** MCP spec rev. 2025-11-25; Anthropic tool-design guidance, retrieved 2026-05-18

**TL;DR.** ADR-031 asked whether an *agent* can find the right
tool. The companion question — is the catalog well-grounded for the
*contributors* who extend it? — has a less comfortable answer. The
catalog works and ships, but it is organized by **accretion
history, not by domain**. It carries two unreconciled naming
conventions, exposes apparent duplicate tools across that split,
spreads one tool's identity across four files with no single
registration point, and uses a code layout that does not match the
domain grouping the docs already use. This is "clear enough" at ~75
tools and accruing the exact lockstep debt that already burned
v1.0.591 and v1.0.630. §3 checks the naming question against current
practice (MCP spec + Anthropic guidance): the namespacing scheme is
**eval-sensitive** — decided by evidence, not by analogy.
Recommendation: promote to an ADR; settle the naming convention
*before or fused with* ADR-031 W2, which rewrites every catalog
entry anyway.

---

## 1. The question

ADR-031 and its [discussion](agent-tool-ergonomics.md) close the
**agent-facing** gap: discovery (`tools.get`), description depth
(two-tier), error recovery (hint envelope). They explicitly put
tool renaming and code structure *out of scope* (rollout plan §2).

That leaves a **contributor-facing** question untouched: when a
contributor adds or changes a tool, is the catalog's naming,
categorization, and code structure grounded and predictable enough
to do it right without a map? The audit below says: not yet.

## 2. What the catalog looks like today

The agent-facing catalog is `mcpToolDefs()` in `server/mcp.go`,
composed of **four sources in two shapes** (see the ADR-031 rollout
plan §0.1):

| Source | Shape | Origin |
|---|---|---|
| `mcpToolDefsBase()` | `[]map[string]any` literals | `server/mcp.go` — "the original happy-path" |
| `mcpToolDefsExtra()` | `[]map[string]any` literals | `server/mcp_more.go` — "the second batch" |
| `orchestrationToolDefs()` | `[]map[string]any` literals | `server/mcp_orchestrate.go` |
| `authorityToolDefs()` | typed `toolDef` struct → maps via `hubmcpserver.ToolCatalog()` | `hubmcpserver/tools.go` |

Five structural problems fall out of this.

### 2.1 Two naming conventions, no rule

The ~75 catalog entries split into two camps:

- **`snake_case` verbs (~25)** — `post_message`, `get_feed`,
  `get_project_doc`, `get_task`, `get_event`, `get_audit`,
  `list_agents`, `list_channels`, `request_approval`,
  `request_help`, `journal_read`, `update_own_task_status`,
  `pause_self`, `shutdown_self`, … — concentrated in `base` /
  `extra`.
- **`noun.verb` (~50)** — `documents.get`, `projects.list`,
  `agents.spawn`, `runs.create`, `tasks.update`, `a2a.invoke`,
  `tools.get`, … — the `authority` + `orchestration` surface.

Neither `docs/reference/hub-mcp.md` §4 nor
`docs/reference/coding-conventions.md` mandates either form. A new
tool can be named either way and no review rule catches it.

The `noun.verb` half is not even internally consistent: the verb is
sometimes itself `snake_case` (`channels.post_event`,
`hosts.update_ssh_hint`), the noun is sometimes `snake_case`
(`project_channels.create`, `team_channels.create`), and depth
varies (`a2a.cards.list` is three levels).

### 2.2 Apparent duplicate tools across the split

Because the two conventions grew independently, the catalog appears
to expose the **same operation twice under two names**:

| `snake_case` | `noun.verb` | Same operation? |
|---|---|---|
| `list_agents` | `agents.list` | Apparently — both list team agents |
| `get_task` | `tasks.get` | Apparently — both fetch one task by id |
| `get_audit` | `audit.read` | Apparently — both read audit events |

Whether these pairs are intentional (different scopes / shapes) or
accidental redundancy is **itself unclear from the catalog** — and
that unclarity is the point. An agent picking between `list_agents`
and `agents.list` has no signal; a contributor adding a fourth
agent tool has no precedent to follow.

### 2.3 Categorization by chronology, not domain

The code groups tools as `base` ("happy-path") + `extra` ("second
batch", whose file comment says the split is "purely for
review-ergonomics") + `orchestration` + `authority` (by *which
package* it came from). None of these is a domain.

Meanwhile `hub-mcp.md` §3 documents the catalog grouped **by
domain** — projects / plans / runs / documents+reviews /
agents+hosts / channels+a2a / tasks+schedules / templates / misc.
**The doc and the code disagree on the catalog's structure.** A
contributor reading the doc cannot predict which `.go` file holds a
given tool.

### 2.4 One tool's identity is spread across four places

Adding a tool means editing, in lockstep:

1. a catalog definition (one of four sources, one of two shapes),
2. a dispatch case (one of two mechanisms — see §2.5),
3. a `tiers.go` `toolTiers` row,
4. for any worker-callable tool, a `roles.yaml` allow pattern.

There is **no single registration point**. CLAUDE.md warns of
"three things in lockstep"; it is really four. The class is not
hypothetical: `documents.get` shipped in v1.0.630 missing its
`tiers.go` row and left `TestEveryCatalogEntryHasTier` red on
`main` until ADR-031 W1 incidentally fixed it. `request_project_steward`
(v1.0.591) shipped a dispatcher case with no catalog entry. Same
class, twice.

### 2.5 Two dispatch mechanisms

`dispatchTool` runs an explicit `switch` for the ~30 `base`/`extra`/
`orchestration` tools, then a `default` that falls through to
`hasAuthorityTool` → `dispatchAuthorityToolRaw`. Which mechanism a
tool uses is decided by *which registry it landed in* — i.e. by
history, not by any property of the tool.

## 3. Current practice — what the spec and Anthropic's guidance say

Verified 2026-05-18 against the MCP specification and Anthropic's
published tool-design guidance, so the recommendation rests on
evidence rather than analogy. (An earlier draft of this doc picked
`noun.verb` "because it matches REST" — that is corrected here.)

**The MCP tool-name format is now standardised — and that does not
settle the question.** MCP specification revision 2025-11-25
(SEP-986) fixes the format: 1–128 characters, case-sensitive, drawn
from `A-Z a-z 0-9 _ - .`. Both `snake_case` and dotted `noun.verb`
are therefore spec-legal. But the dot is a *recent* addition, and
clients still filter it inconsistently — a documented bug class
(e.g. Cursor dropping dotted tool names despite their spec-validity).
termipod is a **multi-engine** control plane: one catalog is served
to claude-code, codex, gemini-cli and kimi-code MCP clients. A name
format is only as safe as the *least* tolerant client. `snake_case`
(`[A-Za-z0-9_-]`) is accepted by every MCP client and every major
function-calling API; dotted names are not yet.

**Anthropic's guidance prescribes evaluation, not a delimiter.**
"Writing effective tools for AI agents" recommends namespacing tools
by service and by resource (`asana_search`; `asana_projects_search`,
`asana_users_search` — note: `snake_case`), and states plainly that
"selecting between prefix- and suffix-based namespacing [has]
non-trivial effects on our tool-use evaluations," varying by model.
The grounded takeaway: **the namespacing scheme is an empirical
choice, settled by a tool-use eval, not by a REST analogy.**

**"More tools don't always lead to better outcomes."** The same
guidance argues for *consolidating* tools to an agent's workflow
rather than mirroring an API 1:1 — its example replaces
`list_users` / `list_events` / `create_event` with one
`schedule_event`. This sharpens §2.2 (overlapping tools are a
measured cost, not just untidy) and sits in tension with
`hub-mcp.md` §5's rule "one MCP tool = one REST call; composition is
the agent's job." That rule optimises for *contributor* simplicity
and a clean audit trail; Anthropic's guidance optimises for *agent*
success. Both are legitimate — the catalog ADR must decide which
wins where, explicitly (Q6).

**The catalog should be navigable, not fully preloaded.** In
November 2025 Anthropic shipped the Tool Search Tool
(`defer_loading: true` — tools discovered on demand), Programmatic
Tool Calling, and the "code execution with MCP" pattern (MCP tools
as on-disk modules an agent imports as needed). This is independent
confirmation of ADR-031's `tools.get` direction — and it means the
catalog-structure work should assume tools are *looked up*, not all
loaded into context every turn. Structure for lookup.

## 4. First principles — what a well-grounded catalog needs

1. **One naming convention**, namespaced by resource and enforced by
   lint. The *scheme* (prefix vs suffix, delimiter) is eval-sensitive
   and model-dependent (§3) — pick it by evaluation where possible;
   absent an eval, default to the universally client-safe form
   (`snake_case`, §3). A naming convention is a glossary-class
   decision (`docs/reference/glossary.md`, the choose-terms-precisely
   convention) — a colliding or ambiguous tool name gets read,
   copied, and built on.
2. **One registration point.** A tool is declared once; its
   catalog entry, dispatch route, tier, and role-eligibility are
   derived from that declaration or CI-locked against it. This
   structurally retires the §2.4 lockstep class — the same outcome
   `validate-at-every-boundary.md` argues for: make the bad state
   unrepresentable, don't test for it after the fact.
3. **Code structure mirrors the domain taxonomy** the docs already
   use (`hub-mcp.md` §3). A contributor can predict a tool's file
   from its domain.
4. **No silent duplicates.** If two tools overlap, that is a
   recorded decision with a rationale, not an artifact of two
   conventions growing in parallel — and "fewer, well-shaped tools"
   is itself the practice (§3).
5. **Predictability over cleverness.** Given an intent, a
   contributor can guess the tool's name and location; given a
   tool, a reader can guess its domain and authority tier.

## 5. Options

The four are largely composable; O-A and O-B are the high-value
core.

- **O-A — One naming convention.** Settle a single scheme:
  resource-namespaced, one delimiter, lint-enforced. The *principle*
  is not in doubt; the *delimiter* is the open decision (§3, Q1) —
  `resource_verb` snake_case (`documents_get`, `agents_spawn` —
  Anthropic's own form, client-safe everywhere) vs `resource.verb`
  dotted (spec-legal since 2025-11-25, but interop-risky for a
  multi-engine deployment). Whichever wins, migrate the minority
  with grandfathered aliases + a deprecation hint (the existing
  `request_decision` / `templates_propose` aliases are precedent;
  ADR-032's v1.1.0 cutoff shim is a second). Resolve the §2.2
  duplicate pairs in the same pass. Migration cost is asymmetric:
  ~25 renames toward dotted, ~50 toward snake_case.
- **O-B — Single registration point.** One declaration per tool
  (a `toolDef`-like struct carrying name, schema, tier, role
  eligibility, dispatch target). Catalog, dispatch, `toolTiers`,
  and the `roles.yaml` cross-check are derived or CI-locked from
  it. Retires §2.4 and §2.5; subsumes ADR-031 W5's lint (which is
  otherwise a band-aid over the symptom).
- **O-C — Domain file layout.** Re-home the catalog into files
  matching `hub-mcp.md` §3's domains, so doc and code agree.
- **O-D — Full re-home.** O-A + O-B + O-C together — the "do it
  properly once" option.

## 6. Relationship to ADR-031 and the defect record

ADR-031 is the **agent-facing** half: it makes the catalog
*discoverable and self-documenting to the LLM*. This doc is the
**contributor-facing** half: it makes the catalog *grounded and
predictable to the humans who extend it*. They are complementary,
not competing — and keeping them as separate ADRs avoids muddying
an agent-ergonomics decision with a code-structure one.

One concrete coupling: **ADR-031 W2 rewrites every catalog entry**
to add the `short` field. A naming migration (O-A) also touches
every entry. Doing them in two separate passes means editing all
~75 entries twice. Sequencing matters (§7, Q5).

## 7. Open questions

- **Q1 — naming scheme.** The principle (one convention,
  resource-namespaced) is settled; the delimiter is not.
  `resource_verb` snake_case vs `resource.verb` dotted — decide by a
  tool-use eval if feasible, else default to snake_case for
  multi-engine client safety (§3). Sub-rules either way: multi-word
  resources (`project_channels`?) and multi-word verbs
  (`post_event`, `update_ssh_hint`); whether three-level names
  (`a2a.cards.list`) are allowed or flattened.
- **Q2 — are the §2.2 pairs duplicates?** Audit `list_agents` vs
  `agents.list`, `get_task` vs `tasks.get`, `get_audit` vs
  `audit.read`. Each pair is either a deliberate distinction (then
  document it) or redundancy (then deprecate one).
- **Q3 — migration shape.** Grandfathered aliases (soft, agents'
  in-flight templates keep working) vs hard cutover at a version
  boundary (clean, breaks unrendered templates). ADR-032's v1.1.0
  shim is the precedent for the soft path.
- **Q4 — registration mechanism.** Generate-everything-from-one-
  struct (strongest, biggest change) vs keep the sources separate
  but add a CI lint that asserts catalog × dispatch × tier × role
  agree (cheaper, leaves the four files but kills the drift).
- **Q5 — sequencing vs ADR-031 W2.** Three orders: (a) O-A before
  W2 — the naming is settled when W2 rewrites entries, one pass;
  (b) O-A fused into W2 — one combined wedge; (c) O-A after W2 —
  every entry edited twice. (a) or (b) are clearly preferable;
  this is the most time-sensitive open question.
- **Q6 — the relay rule vs tool consolidation.** `hub-mcp.md` §5
  mandates "one MCP tool = one REST call." Anthropic's guidance
  says consolidate tools to the agent's workflow (§3). Decide
  explicitly whether the relay rule holds catalog-wide or yields to
  consolidation for specific agent-facing tools.

## 8. Recommendation

Promote this to an ADR once Q1–Q6 resolve — provisionally *"tool
catalog: one naming convention + single registration point."* Scope
the ADR to **O-A + O-B**; treat O-C (domain layout) as a follow-on
nicety, not a blocker.

On **Q1**, absent a tool-use eval the grounded default is
`resource_verb` **snake_case** — it is Anthropic's own form, and it
is the only name format every MCP client and function-calling API
accepts, which matters precisely because termipod is multi-engine
(§3). Pick dotted only if an eval shows it wins for the engines in
use. Either way, state the decision *and its basis* in the ADR.

Act on **Q5 first**: because ADR-031 W2 touches every catalog
entry, settle the naming convention (O-A's decision, if not its
full migration) *before* W2 runs — or fuse the two — so the ~75
entries are edited once, not twice. If W2 is imminent and the ADR
is not ready, that argues for pausing W2 briefly rather than
paying the double edit.

## 9. See also

- [agent-tool-ergonomics discussion](agent-tool-ergonomics.md) — the
  agent-facing companion.
- [ADR-031](../decisions/031-agent-tool-ergonomics.md) +
  [rollout plan](../plans/agent-tool-ergonomics-rollout.md) — §0.1 has
  the catalog topology this doc audits.
- [validate-at-every-boundary discussion](validate-at-every-boundary.md)
  — the make-bad-states-unrepresentable argument behind O-B.
- [`docs/reference/hub-mcp.md`](../reference/hub-mcp.md) — the current
  MCP surface; §3's domain grouping is what O-C would make the code
  match, §5 is the relay rule weighed in Q6.
- [`docs/reference/glossary.md`](../reference/glossary.md) — canonical
  for collision-prone terms; a naming convention is glossary-class.
- [MCP specification — Tools (rev. 2025-11-25)](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
  — the SEP-986 tool-name format.
- [Anthropic — Writing effective tools for AI agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
  — namespacing, the eval-sensitive scheme choice, "more tools isn't better."
- [Anthropic — Code execution with MCP](https://www.anthropic.com/engineering/code-execution-with-mcp)
  — the Nov 2025 navigable-catalog direction.
