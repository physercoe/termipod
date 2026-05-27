# Agent-side tooling landscape

> **Type:** discussion
> **Status:** Open (2026-05-27) — landscape capture; no decisions taken
> **Audience:** contributors
> **Last verified vs code:** v1.0.723
> **Freshness:** snapshot (refresh when one of the four projects crosses a 2× star delta or changes architecture)

**TL;DR.** Four trending GitHub projects are reshaping what a Claude
Code / Codex / Cursor / OpenCode instance ships with by default:
**affaan-m/ecc** (~182k stars, skill/agent/rule/hook megapack +
GitHub App + security scanner), **garrytan/gstack** (~104k stars,
23 role-typed skills), **colbymchenry/codegraph** (~29.4k stars,
pre-indexed code MCP), and **Lum1104/Understand-Anything**
(~38.7k stars, tree-sitter + LLM hybrid knowledge graph). All four
sit **below** the agent — they are tools the agent calls, skills it
loads, configs it reads. Unlike the harnesses surveyed in
[multi-agent-harness-landscape.md](multi-agent-harness-landscape.md),
none of them compete with TermiPod's positioning; they are
candidates to **bundle at agent-spawn time so every TermiPod-spawned
worker arrives equipped**. The single highest-leverage borrow is
auto-installing `codegraph` per-project at spawn (50–70% token /
cost / tool-call reductions in the upstream benchmarks); the
second is curating a small named subset of ECC's skill catalogue
as TermiPod agent-kind variants; the third is running ECC's
`agentshield` scanner against our MCP catalogue and hooks in CI
to defuse the [security-audit.md](security-audit.md) findings
faster. None of this is harness work — it is plumbing that makes
spawned workers immediately productive.

Companion to
[multi-agent-harness-landscape.md](multi-agent-harness-landscape.md),
which surveyed the harness layer that **competes** with TermiPod.
This doc surveys the tools layer that **runs under** TermiPod's
spawned [agents](../reference/glossary.md#agent).

---

## 1. The four projects

| Project | Stars | Category | License | Tech |
|---|---|---|---|---|
| **affaan-m/ecc** | ~182k | Skill / agent / rule / hook pack + GitHub App + security scanner | MIT | Node + Python + Bash |
| **garrytan/gstack** | ~104k | 23-skill toolkit organized by org role | MIT | TypeScript + Bash |
| **colbymchenry/codegraph** | ~29.4k | Pre-indexed code knowledge graph as MCP server | MIT | TypeScript |
| **Lum1104/Understand-Anything** | ~38.7k | Tree-sitter + LLM hybrid → committable knowledge graph | MIT | TypeScript + Python |

Star counts are point-in-time (May 2026) and meant only to signal
adoption order, not technical merit. ECC and gstack are skill
packages; codegraph and Understand-Anything are code-intelligence
indexers. Both halves matter for the agent-spawn experience but
through different mechanisms.

---

## 2. codegraph — pre-indexed code MCP

**Pitch**: the [agent](../reference/glossary.md#agent) calls
`codegraph_search` / `codegraph_callers` / `codegraph_callees` /
`codegraph_impact` instead of running grep + find + reading a dozen
files. Tree-sitter parses → SQLite + FTS5 in `.codegraph/codegraph.db`
→ MCP tools query the index. Zero-config, native file-watching,
20+ languages, auto-detects 8 agent harnesses (Claude Code, Cursor,
Codex CLI, opencode, Hermes Agent, Gemini CLI, Antigravity, Kiro).

**Benchmark claim** (v0.9.4, 7 real-world codebases, May 24 2026):
median **35% cheaper · 57% fewer tokens · 46% faster · 71% fewer
tool calls**. Largest gains on VS Code-sized repos (26% / 78% / 85%
fewer cost / tokens / tool-calls); narrowest on small repos like
Gin (21% cost, 40% fewer tool-calls) where native grep is already
efficient. Numbers are upstream-reported and not independently
reproduced by us; treat as directional, not authoritative.

**MCP tool surface**:

| Tool | Purpose |
|---|---|
| `codegraph_search` | Find symbols by name |
| `codegraph_context` | Build task-relevant context |
| `codegraph_trace` | Trace call path between symbols, inline bodies |
| `codegraph_callers` / `codegraph_callees` | Walk call graph |
| `codegraph_impact` | What's affected before a change |
| `codegraph_node` | Symbol details + source |
| `codegraph_explore` | Grouped source + relationship map |
| `codegraph_files` | Indexed file structure |
| `codegraph_status` | Index health |

**Watch loop**: native FSEvents / inotify / ReadDirectoryChangesW;
debounce default 2 s (range 100 ms–60 s via
`CODEGRAPH_WATCH_DEBOUNCE_MS`); during pending syncs, tool responses
prepend `⚠️ file pending`; offline-edit reconciliation by size +
mtime + content-hash.

**Framework awareness**: detects URL → handler patterns across 14
frameworks (Django, Flask, FastAPI, Express, NestJS, Laravel, Rails,
Spring, Gin, chi, gorilla, Axum, actix, Rocket, ASP.NET, Vapor,
React Router, SvelteKit).

**Cross-language bridging** is the differentiator: synthesizes edges
across language boundaries — Swift ↔ Obj-C `@objc` bridges, React
Native legacy bridges (`NativeModules.X` ↔ `RCT_EXPORT_METHOD`),
TurboModules spec ↔ native impl, Expo `requireNativeModule()` ↔
Expo DSL, Fabric / Paper views ↔ native managers. Edges are tagged
`provenance: 'heuristic'` so downstream tools can distinguish
synthesized links from direct ones.

**Install** (bundled Node, no compile step):

```bash
curl -fsSL https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.sh | sh
# or
npx @colbymchenry/codegraph
```

---

## 3. Understand-Anything — tree-sitter + LLM hybrid

**Pitch**: "graphs that teach > graphs that impress." Tree-sitter
handles the deterministic structural layer (imports / exports /
calls / inheritance); the LLM handles the semantic layer
(plain-English summaries, architectural-layer assignments,
business-domain mapping, guided tours, language-concept callouts).
Output is `.understand-anything/knowledge-graph.json` — committable,
team-shareable.

**Six analyzer agents in pipeline**:

1. `project-scanner` — files + languages + frameworks
2. `file-analyzer` — functions / classes / imports + graph nodes / edges (parallel, 5 concurrent, 20–30 files / batch)
3. `architecture-analyzer` — layer assignment (API / Service / Data / UI / Utility)
4. `tour-builder` — dependency-ordered learning guide
5. `graph-reviewer` — referential integrity check
6. `domain-analyzer` — business domains + flows + process steps

**Commands**: `/understand` (scan + build), `/understand-dashboard`
(interactive viewer), `/understand-chat "how does payment work?"`,
`/understand-diff` (ripple-effect analysis),
`/understand-explain src/auth/login.ts`, `/understand-onboard`
(auto-generate onboarding guide), `/understand-domain`,
`/understand --auto-update` (post-commit hook),
`/understand src/frontend` (scoped).

**Differentiates from codegraph**: codegraph is deterministic-only
— fast, reproducible, cheap to run; the agent does the explanation
work at query time. Understand-Anything spends LLM tokens up-front
to produce **static, committable semantic artifacts**; slower to
build, but the explanations are reusable across many sessions
without re-spending. Different cost models — query-time vs
build-time amortisation.

**MCP integration**: not explicitly documented in the repo's surface
today (primary integration is slash commands inside the host agent).
That is a gap upstream could close; for us it means borrowing the
*pattern* (committable JSON artifact) is easier than borrowing the
*runtime* (no MCP entry point to wire to).

---

## 4. ECC — the skill / rule / agent megapack

**Pitch**: don't reinvent the agent's "personality" per repo;
install 61 agents + 246 skills + 76 commands + 102 static-analysis
rules + multi-harness adapters. The most-starred Claude Code
configuration repo as of May 2026.

**Five-layer surface**:

- **Agents** (`agents/*.md`, 61 total) — markdown + YAML frontmatter:
  ```yaml
  ---
  name: code-reviewer
  description: Reviews code for quality, security, and maintainability
  tools: ["Read", "Grep", "Glob", "Bash"]
  model: opus
  ---
  ```
  Examples: planner, architect, code-reviewer, security-reviewer,
  language-specific reviewers (Python, Go, TypeScript, Java, Rust,
  C++, F#, Kotlin, HarmonyOS).
- **Skills** (`skills/[domain]/SKILL.md`, 246 total) — workflow
  definitions. Categories include tdd-workflow, backend-patterns,
  frontend-patterns, django-patterns, springboot-patterns,
  mle-workflow, autonomous-loops, plus 230+ more.
- **Hooks** (`hooks/hooks.json` + `scripts/hooks/*.js`) —
  event-driven; matcher expressions like
  `tool == "Edit" && tool_input.file_path matches "\\.(ts|tsx|js|jsx)$"`;
  events PreToolUse / PostToolUse / Stop / SessionStart / SessionEnd.
- **Rules** (`rules/common/` + `rules/[language]/`) — always-follow
  guidelines; users copy to `~/.claude/rules/ecc/`.
- **MCP configs** (`mcp-configs/mcp-servers.json`) — pre-configured
  server entries for GitHub, Supabase, Vercel, Exa, Context7,
  Playwright, Sequential Thinking.

**AgentShield** (`ecc-agentshield` npm package): security scanner
with **102 static-analysis rules · 14 secret-detection patterns ·
1282 tests**. Three Opus 4.6 agents in an attacker / defender /
auditor adversarial pipeline. Output: terminal (A–F graded), JSON,
Markdown, HTML; exit code 2 on critical findings.
`npx ecc-agentshield scan --opus`.

**Continuous-learning "instinct" system**:

```
Session → extract patterns → /instinct-status (view with confidence)
       → /instinct-import   (reuse others' patterns)
       → /evolve            (cluster into reusable skills)
```

TTL default 30 days; `/prune` removes stale.

**Multi-harness adapter**: same repo ships to Claude Code (plugin
marketplace + manual), Cursor (`.cursor/agents/ecc-*.md` via
`./install.sh --target cursor`), Codex (`AGENTS.md` +
`.codex/config.toml` merge), OpenCode (`.opencode/opencode.json`),
GitHub Copilot (`.github/copilot-instructions.md`), Zed.

**Distribution**: MIT, free OSS core; ECC Pro is **$19/seat/month**
GitHub App for private repos.

---

## 5. gstack — Garry Tan's opinionated toolkit

**Pitch**: install Garry Tan's exact production setup. 23 commands
organized by org-role personas.

**Roles and headline commands**:

| Role | Commands |
|---|---|
| **CEO / Product** | `/office-hours` (6-question product interrogation), `/plan-ceo-review` (4-mode scope challenge), `/autoplan` (CEO → design → eng auto-chained) |
| **Designer** | `/design-consultation`, `/design-shotgun` (4–6 mockup variants), `/design-html` (mockups → 30 KB zero-dep HTML/CSS), `/plan-design-review`, `/design-review` |
| **Eng Manager** | `/plan-eng-review`, `/review` (staff-level + auto-fix), `/investigate` (root-cause), `/pair-agent` (multi-agent via shared browser) |
| **Release Manager** | `/ship` (sync + test + audit + push + PR), `/land-and-deploy`, `/canary` (post-deploy monitoring) |
| **Doc Engineer** | `/document-release`, `/document-generate` (Diátaxis framework) |
| **QA** | `/qa` (test + fix + re-verify atomically), `/qa-only` (report only) |
| **Security / Ops** | `/cso` (OWASP + STRIDE), `/retro` (weekly per-person), `/browse` (~100 ms Chromium) |

**Skill format**: bash / TypeScript shell scripts symlinked to
`SKILL.md` manifests Claude Code reads as capability descriptors.
Auto-installed to `~/.claude/skills/gstack-*/`,
`~/.codex/skills/gstack-*/`,
`~/.config/opencode/skills/gstack-*/`,
`~/.cursor/skills/gstack-*/`.

**GBrain integration**: `/setup-gbrain` (Supabase cloud or PGLite
local) + `/sync-gbrain` (re-index repo via `gbrain sources add` +
`gbrain sync --strategy code`). Writes a `## GBrain Search Guidance`
block into `CLAUDE.md` so the agent prefers `gbrain search` /
`code-def` / `code-refs` over grep. Per-repo trust tier
(read-write / read-only / deny, sticky per remote).

**Install**:

```bash
git clone --single-branch --depth 1 https://github.com/garrytan/gstack.git \
  ~/.claude/skills/gstack && cd ~/.claude/skills/gstack && ./setup
```

---

## 6. Cross-cutting architectural patterns

Three patterns appear in 2+ of these projects and are worth borrowing
as conventions, not as code:

**P1. Multi-harness install adapters.** ECC, codegraph, gstack, and
Understand-Anything all auto-detect 5+ host agent CLIs and install
into the right path (`~/.claude/skills/`, `~/.codex/skills/`,
`~/.config/opencode/skills/`, etc.). When TermiPod ships its own
skill-pack or auto-installs codegraph, the install-path resolution
should follow this convention from day one. Pattern: detect which
[engine](../reference/glossary.md#engine) is running, slot into the
engine's known directory.

**P2. SQLite-FTS5 as the "small index next to source" pattern.**
codegraph uses `.codegraph/codegraph.db` (SQLite + FTS5);
Understand-Anything uses `.understand-anything/knowledge-graph.json`;
ECC's "instinct" lives in project-local files with TTL. We already
use modernc SQLite in the [hub](../reference/glossary.md#hub). For
project-scoped state that agents care about (codegraph indices,
onboarding tours, instinct collections), the per-project-dir
SQLite-FTS5 file is the standard 2026 shape and worth following
when we add similar artefacts.

**P3. Tree-sitter as the universal extraction layer.** codegraph and
Understand-Anything both use tree-sitter for AST extraction; omo
(per
[multi-agent-harness-landscape.md](multi-agent-harness-landscape.md))
also uses it. If we ever ship our own code-aware tooling (e.g. a
steward-callable repo-audit skill), tree-sitter is the right
starting point — multi-language, deterministic, well-maintained.

---

## 7. What's borrowable for TermiPod

The borrow lens here is sharper than the previous landscape doc
because we are not asking "do they out-architect us?" but "should
every TermiPod-spawned worker have these by default?"

### 7.1 Tier A — directly actionable, high leverage

**A1. Auto-install codegraph per-project at spawn time.** Single
biggest token-saver in the field. Mechanism: in
`hub/internal/hostrunner/launch_*.go`, after worktree creation,
check whether `.codegraph/codegraph.db` exists; if not, run
`codegraph init` against the worktree. Pass the codegraph MCP
server config into the agent's MCP catalogue so the worker can
call `codegraph_search` / `codegraph_callers` / etc. from turn 1.
**Why this fits us specifically**: TermiPod spawns workers into
[worktrees](../reference/glossary.md#worktree) that the operator
may not have touched; without this, every worker re-greps the
same repo, repeatedly. The bundled-Node install means there is no
Go-side dependency. A new `mcp_install:` array in
`agent_families.yaml` keyed by engine kind covers the surface.

Cost: one new YAML field, ~150 LOC in the launch path, one new
operator dependency (codegraph CLI on each host). 50–70% token /
cost reduction per worker turn (upstream-reported, treat as
directional) is the kind of ratio that compounds quickly across a
fleet.

**A2. Run AgentShield against our own MCP catalogue and hooks as
CI.** ECC's `ecc-agentshield` npm package: 102 rules + 14 secret
patterns + MCP risk profiling. We have the
[security-audit.md](security-audit.md) findings (4 critical, 6
high, 1 medium) outstanding from the 2026-05-25 codex review.
AgentShield is purpose-built for the surface those findings live
in. `npx ecc-agentshield scan --opus` in CI on the hub repo would
surface issues we would otherwise wait for an external audit on.
Not a TermiPod feature; an internal-hygiene action.

Cost: one CI step. Decide on advisory vs blocking after the first
run shows the noise floor.

### 7.2 Tier B — selective borrow

**B1. ECC's skill-pack curation as agent-kind variants.** ECC's 61
named agents and 246 skills map cleanly onto our
[agent-kind](../reference/glossary.md#agent-kind) concept. We
currently ship a small set of bundled kinds in
`hub/internal/agentfamilies/`. ECC's curation is MIT-licensed and
battle-tested; cherry-picking 5–10 high-value kinds (planner,
code-reviewer, security-reviewer, qa, ship-manager) saves us
writing them. The borrow is the YAML / markdown content; we adapt
the frontmatter to our schema.

Cost: 1–2 days of curation per kind, on demand. Pure content, no
code.

**B2. Understand-Anything's "guided tour" pattern as a steward
MCP tool.** Their `/understand-onboard` generates a
dependency-ordered learning guide. For TermiPod, the equivalent
is a [steward](../reference/glossary.md#steward)-callable MCP
tool `project.generate_onboarding_tour(project_id)` that produces
a structured document a newly-spawned worker reads first. This
shape fits our existing `documents.create` primitive — the tour
is a document, the steward owns its lifecycle. The hard part
(LLM-driven dependency ordering) is solved upstream by the agent;
we are borrowing the *pattern*, not the runtime — no
Understand-Anything dependency required.

Cost: medium. New MCP tool + a template for the tour format.

**B3. gstack's "role-typed slash commands" as agent-spawn presets.**
`/qa` / `/ship` / `/review` are agent-side ergonomics, not control
plane. But the *naming convention* — `ship-manager` /
`qa-engineer` / `code-reviewer` as named steward overlays the
operator can pick from mobile — would let users say "spawn a
release manager for v1.0.725" without writing the prompt. Slots
into our existing kind / category system.

Cost: small. UI work + a half-dozen template files. Pairs with
B1.

### 7.3 Tier C — watch, don't copy

- **ECC's "instinct" continuous-learning system** (sessions extract
  patterns → cluster into skills). Conceptually appealing but adds
  a whole memory / learning subsystem that overlaps with our memory
  store and template system. The TTL-based invalidation is a real
  design surface; not worth the complexity until a real user
  complaint about "the agent forgets what we learned" lands.
- **Understand-Anything's interactive web dashboard.** Cool but
  desktop-flutter is post-MVP per
  [desktop-and-web-targets.md](desktop-and-web-targets.md). The
  committable JSON output is the borrowable artefact; the viewer
  is decorative.
- **gstack's full skill catalogue.** Garry Tan's YC-startup-coaching
  opinion is highly specific; cherry-pick (B1 / B3 above), don't
  adopt wholesale.
- **ECC Pro paid tier.** Their GitHub App business model — a
  separate product, not a borrow.

### 7.4 Tier D — non-fit (don't reinvent)

- **codegraph as something WE build.** The repo exists, is MIT, is
  actively maintained at ~29.4k stars. Reimplementing it ourselves
  would be vanity work. Right move: depend on it (Tier A1) until
  it stops being maintained, then revisit.
- **gstack's GBrain backend.** Their persistent knowledge base is
  Supabase-cloud-or-PGLite-local. We already have SQLite + the
  hub's `documents` table. Don't pull in a competing memory store.

---

## 8. The strategic implication

The previous landscape doc's takeaway was *"the harness layer is
winning; we're in the same lane."* This research's takeaway is
different:

**Every TermiPod-spawned worker should arrive with codegraph
indexed and a curated skill-pack loaded.** That is the right
default. Our control plane gets compounding leverage from making
this trivial — the operator taps "spawn worker on project X" from
mobile, and that worker shows up with a pre-indexed code graph +
the 5 most useful named agents (planner, code-reviewer,
security-reviewer, qa, ship-manager) + 10 most useful skills
(tdd-workflow, root-cause-debug, etc.) ready to use.

Without TermiPod, that setup is an hour of CLI fiddling per repo
per developer. With TermiPod auto-installing it at spawn, it is
free.

This is a real product wedge, not a research finding. The
competition surveyed in
[multi-agent-harness-landscape.md](multi-agent-harness-landscape.md)
is fighting on coordination protocols; we have those. The
ergonomic gap that actually matters to a user is **"the agent I
just spawned does not know my codebase yet"** — and that is
exactly what codegraph + a skill-pack fix at spawn.

Tier A1 (auto-install codegraph) is the smallest wedge with the
highest leverage. The shape:

- New field `mcp_install:` in
  `hub/internal/agentfamilies/agent_families.yaml` (per-engine,
  optional list of MCP packages to ensure at spawn).
- Launch-path hook in `hub/internal/hostrunner/launch_*.go` to run
  `<pkg> init` if a sentinel file is missing.
- One operator dependency on each host (codegraph CLI on PATH).
- Wedge sizing: ~150 LOC + 1 reference doc + 1 sweep test. Half-
  day to ship.

---

## 9. Open questions

- **Should `mcp_install:` be per-engine or per-agent-kind?**
  Per-engine is simpler (claude-code workers always get codegraph);
  per-kind allows specialised stewards to skip the index (a doc
  reviewer doesn't need call-graph traversal). Per-kind is more
  flexible at slightly more YAML.
- **How does TermiPod handle the codegraph CLI dependency on
  hosts?** Options: (a) document as an operator-installed
  prerequisite, (b) bootstrap install via host-runner on first
  spawn, (c) bundle the codegraph binary in the host-runner
  release. (a) is cheapest; (b) is the right MVP after the first
  field complaint; (c) is the production answer if/when we own
  host-runner releases more tightly.
- **Where should "skill packs" live in our taxonomy?** Existing
  templates under `hub/templates/prompts/` are pure prompts. A
  curated agent-kind (B1) is prompt + tool gate + model + frontmatter
  — closer to ECC's `agents/*.md` format. Worth a small ADR to
  decide whether kinds live in YAML (current) or markdown +
  frontmatter (ECC) before we start curating.
- **Should B2 (onboarding-tour MCP) ride on the existing
  `documents.create` primitive, or be its own tool?** Riding
  documents.create keeps the surface small; a dedicated tool would
  let the steward signal *intent* ("this is a tour") that mobile
  could render specially.
- **Refresh cadence for this doc.** Per the §9 open question in
  [multi-agent-harness-landscape.md](multi-agent-harness-landscape.md),
  if we adopt a 90-day Stale-by-default policy these landscape
  docs should be the first to use it. The four projects here are
  all <12 months old and high-velocity; the snapshot will rot
  quickly.

---

## 10. Sources

- affaan-m/ecc — https://github.com/affaan-m/ecc
- ECC-Tools / .github — https://github.com/ECC-Tools/.github
- ECC landing — https://ecc.tools/
- AgentShield — https://github.com/affaan-m/agentshield
- colbymchenry/codegraph — https://github.com/colbymchenry/codegraph
- codegraph CLAUDE.md — https://github.com/colbymchenry/codegraph/blob/main/CLAUDE.md
- Lum1104/Understand-Anything — https://github.com/Lum1104/Understand-Anything
- garrytan/gstack — https://github.com/garrytan/gstack
- gstack skills doc — https://github.com/garrytan/gstack/blob/main/docs/skills.md
- gstack + GBrain integration — https://github.com/garrytan/gstack/blob/main/USING_GBRAIN_WITH_GSTACK.md
- CodeGraph 2026 guide — https://tosea.ai/blog/codegraph-claude-code-cursor-guide-2026
- Adjacent code-graph MCPs (for cross-reference, not deep-dived):
  - https://github.com/CodeGraphContext/CodeGraphContext
  - https://github.com/techsavvyash/codegraph (SCIP + Neo4j + MCP)
  - https://github.com/tirth8205/code-review-graph
  - https://github.com/websines/codegraph-mcp (Rust)
  - https://github.com/JudiniLabs/mcp-code-graph
  - https://github.com/NabiaTech/codegraph-mcp

---

## 11. Related

- [multi-agent-harness-landscape.md](multi-agent-harness-landscape.md)
  — companion doc surveying the harness layer that *competes* with
  TermiPod; this doc surveys the tools layer that *runs under*
  TermiPod's spawned workers.
- [integrating-open-source-agents.md](integrating-open-source-agents.md)
  — older doc on engine-pluggability; the four projects here
  assume the engine is already pluggable and ask "what should it
  carry?"
- [security-audit.md](security-audit.md) — the audit findings
  Tier A2 (AgentShield in CI) would help close faster.
- [consumer-side-dispatch-contracts.md](consumer-side-dispatch-contracts.md)
  — the discipline Tier A1's `mcp_install` rollout should follow
  (allowlist of known-good install packages, not denylist of
  problem ones).
- [ADR-027](../decisions/027-local-log-tail-driver.md),
  [ADR-029](../decisions/029-tasks-as-first-class-primitive.md),
  [ADR-032](../decisions/032-message-routing-envelope.md) — the
  existing primitives Tier A/B picks would extend.
