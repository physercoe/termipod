# Doc uplift — industry-standard foundations before lifecycle engineering

> **Type:** plan
> **Status:** Draft (2026-05-05) — not yet started; prerequisite to project-lifecycle MVP
> **Audience:** contributors (doc authors, hub backend, mobile)
> **Last verified vs code:** v1.0.351

**TL;DR.** Doc audit (2026-05-05) identified 8 structural gaps against
industry standards (arc42, C4, Diátaxis): zero visual diagrams across
90 docs; no consolidated database-design doc; no consolidated API
reference; `blueprint.md` mixes 7 concerns at 979 lines; no system-
flow document; no architecture-overview doc with C4 L1/L2 diagrams; no
cross-cutting concerns doc; tutorials placeholder unfilled. Because
the MVP-demo audience includes **reviewers and their AI agents
inspecting the codebase**, the docs are part of the demo deliverable.
This plan ships **all 10 P0/P1/P2 items** before lifecycle
engineering (W1 of the lifecycle plan) starts. Estimated effort:
~25–35 working days; can compress to ~3 calendar weeks with two
contributors. Critical-path: P0.2 (architecture-overview) → P1.6
(blueprint refactor) → P2.8 (OpenAPI). Output: roughly 8–10 new docs
+ 6–8 diagrams added to existing spine docs + significant blueprint
restructure with full link audit.

---

## 1. Why this plan exists

Two prompts converged on 2026-05-05:

1. **Audit finding:** termipod's docs are strong on
   discipline (doc-spec.md, status blocks, ADR rigor, glossary, lint
   enforcement) but weak on *industry-grade structure*: no diagrams
   anywhere, no consolidated DB / API / architecture views,
   `blueprint.md` carries 7 concerns. An AI agent or reviewer
   onboarding cold cannot answer "what is the X schema / API / flow"
   from one canonical doc.

2. **Demo-readiness reframe:** the MVP-demo audience includes
   reviewers, who routinely use AI agents to inspect codebases as
   part of their review. The docs are not separate from the demo —
   they're a co-deliverable. A reviewer's agent reading these docs
   should reach the same comprehension as their human reading them,
   and that requires industry-standard structure (visual + textual,
   single canonical entry per topic, generated where possible).

Conclusion: foundations come first. The project-lifecycle MVP plan
([`project-lifecycle-mvp.md`](project-lifecycle-mvp.md)) is paused
until this plan ships at minimum P0+P1.

---

## 2. Scope

10 work items derived from the audit, grouped by priority.

### 2.1 P0 — Foundational, low cost (4 items)

- **P0.1** Add Mermaid diagrams to existing spine docs (state machines,
  ER for primitives) — closes the "zero diagrams" gap
- **P0.2** Create `reference/architecture-overview.md` (C4 L1 + L2)
- **P0.3** Create `reference/database-schema.md` (full DDL + ER + index
  strategy + mobile cache parallel)
- **P0.4** Create `reference/api-overview.md` (endpoint index +
  cross-cutting conventions extracted)

### 2.2 P1 — Structural, medium cost (3 items)

- **P1.5** Create `spine/system-flows.md` (sequence diagrams for 8
  critical cross-component flows)
- **P1.6** Refactor `blueprint.md` — extract protocols, primitives,
  forbidden patterns into separate docs; keep blueprint as the *axiom*
  doc only
- **P1.7** Create `reference/cross-cutting.md` (security boundaries,
  observability, error handling, performance budgets)

### 2.3 P2 — Generated + completeness, high cost (3 items)

- **P2.8** Hand-write `reference/openapi.yaml` covering all hub HTTP
  endpoints; defer code-annotation tooling decision to post-MVP
- **P2.9** Author `tutorials/` content (currently placeholder per
  doc-spec § 5)
- **P2.10** Create `reference/quality-attributes.md` (perf budgets,
  security boundaries, scalability targets, offline guarantees)

---

## 3. Industry-standard alignment

The 10 items map cleanly onto recognized doc frameworks. Citing
explicitly so reviewers can verify we meet the bar.

| Industry concept | Termipod artifact (post-uplift) |
|---|---|
| **arc42 §5 Building Block View** | `architecture-overview.md` (C4 L1+L2) + `system-flows.md` |
| **arc42 §6 Runtime View** | `system-flows.md` (sequence diagrams) |
| **arc42 §7 Deployment View** | `architecture-overview.md` §deployment + `how-to/install-hub-server.md` |
| **arc42 §8 Cross-cutting Concepts** | `cross-cutting.md` |
| **arc42 §9 Architecture Decisions** | ✅ `decisions/` (already present) |
| **arc42 §10 Quality Requirements** | `quality-attributes.md` |
| **arc42 §11 Risks and Technical Debt** | ✅ `roadmap.md` + `discussions/` (partial) |
| **arc42 §12 Glossary** | ✅ `glossary.md` (already present) |
| **C4 L1 Context** | `architecture-overview.md §1` |
| **C4 L2 Containers** | `architecture-overview.md §2` |
| **C4 L3 Components** | `architecture-overview.md §3` per-container subsections |
| **Diátaxis: Tutorials** | `tutorials/` (P2.9) |
| **Diátaxis: How-to** | ✅ `how-to/` (already present) |
| **Diátaxis: Reference** | ✅ `reference/` (already present, expanded) |
| **Diátaxis: Explanation** | ✅ `spine/` + `discussions/` (already present) |
| **OpenAPI 3.x** | `reference/openapi.yaml` (P2.8) |
| **ER diagrams** | In `database-schema.md` (P0.3) |
| **Sequence diagrams** | In `system-flows.md` (P1.5) |
| **State diagrams** | In spine docs (P0.1) |

---

## 4. Sequencing + dependency graph

```
                 ┌───────────────────────────────┐
                 │  P0.2 architecture-overview   │  (foundational; informs others)
                 └────────┬──────────────────────┘
                          ↓
         ┌────────────────┼────────────────┬────────────────┐
         ↓                ↓                ↓                ↓
  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
  │  P0.1 spine  │ │  P0.3 DB     │ │  P0.4 API    │ │  P1.7 cross- │
  │  diagrams    │ │  schema      │ │  overview    │ │  cutting     │
  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘ └──────────────┘
         ↓                ↓                ↓
         └────────────────┼────────────────┘
                          ↓
                 ┌──────────────────┐
                 │  P1.5 system-    │
                 │  flows           │
                 └────────┬─────────┘
                          ↓
                 ┌──────────────────┐
                 │  P1.6 blueprint  │  (largest single refactor)
                 │  refactor        │
                 └────────┬─────────┘
                          ↓
         ┌────────────────┼────────────────┐
         ↓                ↓                ↓
  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
  │  P2.8        │ │  P2.9        │ │  P2.10       │
  │  OpenAPI     │ │  tutorials   │ │  quality-    │
  │              │ │              │ │  attributes  │
  └──────────────┘ └──────────────┘ └──────────────┘
```

**Critical path:** P0.2 → P0.1/P0.3/P0.4 → P1.5 → P1.6 → P2.8 (~5
weeks solo, ~3 weeks with two contributors).

**Parallelism opportunities:**
- After P0.2: P0.1 / P0.3 / P0.4 / P1.7 fully parallel
- After P1.6: P2.8 / P2.9 / P2.10 fully parallel
- P2.9 (tutorials) can start anytime after P0.2 ships

**Solo path:** P0.2 → P0.1 → P0.3 → P0.4 → P1.7 → P1.5 → P1.6 → P2.8 →
P2.10 → P2.9 (~25–35 days).

**Two-contributor path:** A on critical (P0.2 → P1.5 → P1.6 → P2.8);
B on cross-cuts (P0.1, P0.3, P0.4, P1.7, P2.9, P2.10). Joins at end
for cross-link audit. ~3 calendar weeks.

---

## 5. Per-item specs

### 5.1 P0.1 — Mermaid diagrams in spine docs

**Goal.** Add visual artifacts inside existing spine docs for
entity-internal state machines and ER. Closes the "zero diagrams"
gap without restructuring.

**Diagrams to add:**

| Doc | Diagram type | Subject |
|---|---|---|
| `spine/agent-lifecycle.md` §5.2 | stateDiagram | Agent operating states (idle / active / suspended / retired) |
| `spine/sessions.md` §4.3 | stateDiagram | Session lifecycle (open / active / distilling / closed) |
| `spine/blueprint.md` §6 | erDiagram | Core primitives (Project / Plan / Run / Artifact / Document / Review / Channel / Agent) — chassis-only ER |
| `spine/information-architecture.md` §6 | flowchart | Tab → Screen hierarchy |
| `spine/governance-roles.md` §1 | flowchart | Role inheritance / permission scope |

**Files modified.**
- `docs/spine/agent-lifecycle.md` — diagram block at §5.2
- `docs/spine/sessions.md` — diagram block at §4.3
- `docs/spine/blueprint.md` — diagram block at §6 (becomes irrelevant if P1.6 splits §6 out; do this AFTER P1.6 then, or in the new `data-model.md`)
- `docs/spine/information-architecture.md` — diagram block at §6
- `docs/spine/governance-roles.md` — diagram block at §1

**Acceptance.**
- [ ] All 5 diagrams render in GitHub's markdown preview (Mermaid native)
- [ ] Diagrams cite the specific doc section they describe
- [ ] No prose removed; diagrams complement, not replace
- [ ] Lint clean

**Effort.** 1–2 days.

**Dependencies.** P0.2 lands first (informs blueprint diagram scope);
P1.6 affects where the blueprint ER diagram lives (defer that one until
P1.6).

---

### 5.2 P0.2 — Architecture overview (C4 L1 + L2)

**Goal.** New doc giving a 30-second-readable system overview with
C4 Level 1 (system in environment) + Level 2 (containers + their
relationships). Becomes the cold-start onboarding read for any new
agent or human.

**File added.**
- `docs/reference/architecture-overview.md` (~300 lines)

**Content outline.**
1. TL;DR
2. System context (C4 L1 with Mermaid diagram) — termipod-as-a-box,
   external actors: Director (human), Reviewer, Engines (Claude/Codex/
   Gemini), MCP servers, GPU hosts
3. Containers (C4 L2 with Mermaid diagram) — Mobile app · Hub · Host-
   runner · Agents · A2A relay · Audit log · Snapshot cache · External
   engines · MCP servers
4. Per-container summary — purpose, tech stack, scope of state, key
   responsibilities, what it does NOT do
5. Communication patterns — sync HTTP (mobile↔hub), SSE (hub→mobile
   streaming), spawn (hub→host-runner), stream-json (host-runner↔
   engine), MCP (engine↔tool servers), A2A (agent↔agent via hub)
6. Tech stack table (Flutter / Dart / Riverpod, Go / SQLite / SSH,
   embed.FS, …)
7. Deployment topology (single-tenant Tailnet per ADR-018)
8. Quality goals at a glance (cache-first, attention-scarce,
   stochastic-authority — pull from blueprint axioms)
9. Reading-order guide for new contributors / agents
10. Cross-references

**Acceptance.**
- [ ] Mermaid C4 L1 diagram renders
- [ ] Mermaid C4 L2 diagram renders
- [ ] Each container has a 1-paragraph summary + tech stack
- [ ] Reading-order guide names 5–10 next docs in priority order
- [ ] Cross-links from `README.md` (added to "Where to start" section)
- [ ] Cross-links from `blueprint.md` § Reference architecture (now
      forwards here)

**Effort.** 2 days.

**Dependencies.** None (foundational).

---

### 5.3 P0.3 — Database schema reference

**Goal.** Single doc inventorying every hub-side table + every mobile-
side cache table, with one master ER diagram, FK graph, index
strategy, migration history pointer.

**File added.**
- `docs/reference/database-schema.md` (~400 lines)

**Content outline.**
1. TL;DR
2. Scope: hub-side authoritative store + mobile-side cache; both in
   one doc for full-system visibility
3. Hub-side ER diagram (Mermaid `erDiagram`) — every table with PK +
   FK relationships
4. Per-table summary — link to detailed reference where one exists
   (audit-events.md, project-phase-schema.md, etc.); inline summary
   where it doesn't (agents, projects, runs, plans, attention_items,
   reviews, documents, channels, …)
5. Index strategy (hot-path queries; covering vs partial; system-wide
   patterns)
6. Migration history pointer (existing `hub/migrations/*.sql`
   listing; doc lists current head)
7. Mobile-side schema (`HubSnapshotCache` tables + SharedPreferences
   keys + flutter_secure_storage entries) — separate ER diagram
8. Data ownership rules (hub-side authoritative vs device-only
   secrets) — pulls from blueprint §4 + ADR-006
9. Versioning + backwards-compat policy
10. Cross-references to per-feature schema docs

**Acceptance.**
- [ ] Hub ER diagram complete (all current tables + the 5 new tables
      from project-phase-schema.md)
- [ ] Mobile ER diagram complete (existing + new cache tables)
- [ ] Per-table row count: at least table name, primary purpose, link
      to detailed doc
- [ ] Cross-link from blueprint §6 (or its successor after P1.6)
- [ ] Lint clean

**Effort.** 3 days (longer than P0.2 because we need to inventory
existing tables — requires reading hub migrations).

**Dependencies.** P0.2 (cites the architecture-overview's container
diagram).

---

### 5.4 P0.4 — API overview (endpoint index + cross-cutting)

**Goal.** Top-level index of every hub HTTP endpoint, organized by
resource group, with cross-cutting conventions (auth, errors, ETags,
idempotency, pagination, rate limits) extracted from where they
currently live in `hub-api-deliverables.md §2`.

**File added.**
- `docs/reference/api-overview.md` (~250 lines)

**Content outline.**
1. TL;DR
2. Base URL + versioning (`/v1/teams/{team}/...`)
3. Authentication overview — bearer tokens, role resolution, actor
   kinds (links to permission-model.md)
4. Endpoint groups (each links to a per-resource detail doc):
   - Projects + lifecycle → `hub-api-deliverables.md` (TBD: rename
     to `hub-api-projects.md` for consistency)
   - Agents → `hub-agents.md`
   - Documents + sections → `hub-api-deliverables.md §7`
   - Templates → `hub-api-deliverables.md §8`
   - Runs (TBD: extract from blueprint §6.5)
   - Channels (TBD: extract)
   - Attention items (links to `attention-delivery-surfaces.md`)
   - Audit events (links to `audit-events.md`)
5. Cross-cutting conventions:
   - Content type: application/json
   - Error response: RFC 7807 problem-detail
   - Status code policy
   - Pagination (cursor-based)
   - ETags + conditional requests
   - Idempotency keys
   - Rate limiting (links to `rate-limiting.md`)
6. Versioning policy (`/v1/`, when to bump)
7. Webhooks (out of MVP scope; placeholder)
8. Cross-references

**Acceptance.**
- [ ] Every existing endpoint listed under a group with a 1-line
      description
- [ ] Cross-cutting conventions section extracts from `hub-api-
      deliverables.md §2` (which then links here as the canonical
      location)
- [ ] `hub-api-deliverables.md §2` simplified to a back-link
- [ ] Hub maintainers signed off on group taxonomy (review checkpoint)
- [ ] Lint clean

**Effort.** 2 days.

**Dependencies.** P0.2 (architecture-overview cites this); P0.3
(schema doc cross-references endpoint groups for each resource).

---

### 5.5 P1.5 — System-flows doc (sequence diagrams)

**Goal.** New spine doc with sequence diagrams for the 8 critical
cross-component flows. Each flow documents who-talks-to-whom in what
order with what payloads.

**File added.**
- `docs/spine/system-flows.md` (~500 lines)

**Flows covered (8):**

1. **Project creation** — mobile → hub /projects (POST) → template
   hydration → steward spawn → audit emit → mobile cache invalidate
2. **Session lifecycle** — director taps Direct Steward → POST distill
   → hub spawn / resume → SSE stream → director input → engine reply
   loop → close → distillation artifact written
3. **Phase advance** — POST /phase/advance → criteria check →
   audit emit → cache invalidate → mobile re-render
4. **Run lifecycle** — steward POST /runs → host-runner spawn → engine
   start → metric emit → run.completed audit → metric criterion
   evaluation → ratify-prompt attention item
5. **Attention item** — created by hub → SSE push → mobile renders on
   Me tab → director acts → resolution → audit emit
6. **Audit event emission** — any mutation → recordAudit() → SSE feed
   → mobile activity tab + cache update
7. **Auth + token resolution** — request with bearer → hub resolves
   token → actor row → role gate → handler runs → audit stamps actor
8. **Cache-first cold start** — mobile launches → reads cache → renders
   → revalidates with ETag → conditional response → diff applied

**Content outline.**
1. TL;DR
2. Reading guide — where each flow's prose narrative lives in other
   docs (this doc gives the visuals; prose stays canonical there)
3. Per-flow section: 1-paragraph summary + Mermaid sequence diagram
   + cross-references
4. Cross-cutting timing notes (sync-vs-async, idempotency, retry
   semantics)
5. Cross-references

**Acceptance.**
- [ ] All 8 sequence diagrams render
- [ ] Each diagram mirrors the prose narrative in the cited
      sister doc; verify match during dress-rehearsal
- [ ] Cross-link from blueprint (or its successor) and from each
      sister doc
- [ ] Lint clean

**Effort.** 3–4 days (sequence diagrams are dense; each takes 30–60
min to author + verify).

**Dependencies.** P0.1 (Mermaid skills warmed up), P0.2 (containers
named consistently), P0.3 (data flow grounds in schema), P0.4 (API
endpoints named consistently).

---

### 5.6 P1.6 — Blueprint refactor

**Goal.** Extract `blueprint.md` (979 lines, 7 concerns) into focused
spine docs per doc-spec § 6 lifecycle rules. Keep `blueprint.md` as
the *axiom* doc — definition of what the system is, philosophy, and
the three system axioms — only.

**Files added (3 new spine docs).**
- `docs/spine/protocols.md` (~250 lines) — extracts blueprint §5
  (protocol layering: edge matrix, ACP scope, driving modes, A2A
  topology, AG-UI)
- `docs/spine/forbidden-patterns.md` (~150 lines) — extracts blueprint
  §7 (forbidden patterns; cross-references the IA's forbidden patterns
  in §8 of `information-architecture.md` for mobile-IA concerns)
- `docs/reference/data-model.md` (~300 lines) — extracts blueprint §6
  (core primitives: Projects, Plans, Schedules, Agents, Runs,
  Artifacts, Documents, Reviews, Channels, Briefings, Attention).
  Note: this is conceptual data model; physical schema lives in
  `database-schema.md` (P0.3) and per-feature refs

**Files restructured.**
- `docs/spine/blueprint.md` — keep §1 Purpose, §2 Axioms, §3 Ontology
  (definitions), §4 Data ownership law, §8 Reference architecture
  (now a one-paragraph summary forwarding to architecture-overview.md),
  §9 Roadmap (forwards to roadmap.md). Drops §5 (→ protocols.md), §6
  (→ data-model.md), §7 (→ forbidden-patterns.md). Final length:
  ~250 lines.

**Files moved (archived).**
- None — the original blueprint content is split, not deleted.
  Original blueprint.md history preserved in git.

**Cross-link audit.**

This is the riskiest part of the plan. `blueprint.md` is heavily
referenced. Audit:

```bash
grep -rn "blueprint.md#" docs/ | wc -l   # count anchor refs
grep -rn "blueprint.md\b" docs/ | wc -l  # count file refs
```

Each anchor reference (e.g., `blueprint.md§6.5`) needs review:
- If the section moved to `data-model.md`, update to
  `data-model.md§<new-section>`
- If the section stayed in blueprint, leave alone
- If the section moved to `protocols.md`, update accordingly

Per-file audit table (TBD during execution; partial seed):

| Refs to blueprint.md from | Likely impact |
|---|---|
| All ADRs (~19 files) | Some refs to §3 (ontology) survive; most §6 references move |
| `agent-lifecycle.md`, `sessions.md`, `governance-roles.md` | Many refs survive; data-model refs move |
| `information-architecture.md` | Few refs (own forbidden patterns, etc.) |
| `reference/*.md` (recent) | The 6 new lifecycle refs already cite blueprint heavily; need updates |
| `discussions/*.md` | Various refs; update as encountered |

**Acceptance.**
- [ ] 3 new spine/reference docs exist + lint clean
- [ ] `blueprint.md` shrunk to ~250 lines, axiom-only
- [ ] Every cross-reference to blueprint anchor sections resolves
      (link audit run)
- [ ] `lint-docs.sh` reports zero broken-link FAILs
- [ ] No prose lost (all original blueprint content preserved
      verbatim in target docs except for editorial transition prose)
- [ ] Status block on blueprint updated (axiom, current 2026-05)

**Effort.** 5–7 days (split + audit; the split itself is mechanical
but the cross-link update is tedious).

**Dependencies.** P0.2 (architecture-overview must exist for
blueprint §8 to forward to), P0.3 (data-model.md cites
database-schema.md).

---

### 5.7 P1.7 — Cross-cutting concerns

**Goal.** Single umbrella doc covering security boundaries,
observability strategy, error handling convention, performance
budgets, offline guarantees. Pulls scattered partials into one
view; arc42 § 8.

**File added.**
- `docs/reference/cross-cutting.md` (~400 lines)

**Content outline.**
1. TL;DR
2. Security boundaries — token model (links to permission-model.md),
   secret storage (device vs hub), network boundaries (Tailnet per
   ADR-018), MCP allowlists, agent sandboxing (post-MVP per memory)
3. Observability — audit_events as primary observability surface,
   logs + metrics (whatever exists today), the Activity feed as
   user-facing telemetry, hub-side admin tooling
4. Error handling — RFC 7807 problem-detail, retry strategy, idempotency,
   user-facing error patterns (cards, banners, toasts per design system)
5. Performance budgets — cache-first cold-start (ADR-006), revalidation
   timing, list pagination defaults, mobile render targets
6. Offline guarantees — what works offline (cached read, queued
   mutations), what doesn't (steward sessions, run dispatch), conflict
   resolution
7. Internationalization — current en/zh/ja support; how to add a
   locale (links to `lib/l10n/`)
8. Accessibility — color-not-as-only-signal, tap targets, VoiceOver/
   TalkBack expectations
9. Cross-references

**Acceptance.**
- [ ] All 8 sub-sections present
- [ ] Cross-links to existing per-topic refs (permission-model,
      audit-events, attention-delivery-surfaces, ADR-006, ADR-018)
- [ ] No content duplication — this doc is the umbrella, sub-topics
      stay canonical in their own files
- [ ] Lint clean

**Effort.** 3 days.

**Dependencies.** Independent; can run parallel to most other items.

---

### 5.8 P2.8 — OpenAPI specification

**Goal.** Hand-written OpenAPI 3.x spec covering all hub HTTP
endpoints. Enables client generation, drift detection, automated
review tooling. Defer code-annotation tooling decision (swaggo,
etc.) to post-MVP.

**File added.**
- `docs/reference/openapi.yaml` (size depends on endpoint count;
  estimated ~1500–2500 lines for current MVP surface plus the new
  lifecycle endpoints)

**Content outline.**
- `openapi: 3.1.0` header, info, servers, security schemes
- Tags grouping by resource (Projects, Lifecycle, Documents,
  Templates, Agents, Runs, Channels, Attention, Audit)
- Per-endpoint: `summary`, `operationId`, `parameters`, `requestBody`,
  `responses` (200, 4xx, 5xx with problem-detail schema), `security`
- Reusable `components/schemas` for: Project, Deliverable,
  Component, Criterion, Document, Section, Audit Event, Problem
  Detail, Attention Item, Agent, Run, Template Spec, …
- Examples for the trickier endpoints (composed-overview, ratify,
  section distill)

**Acceptance.**
- [ ] `openapi.yaml` validates against OpenAPI 3.x schema (use
      `swagger-cli validate` or equivalent in CI)
- [ ] Every endpoint listed in `api-overview.md` (P0.4) appears in
      the spec
- [ ] At least one endpoint per group has a complete `examples` block
- [ ] CI lint job added: `validate-openapi.sh`
- [ ] Cross-link from `api-overview.md` and `architecture-overview.md`

**Effort.** 7–10 days (hand-writing OpenAPI is slow; covers ~30+
endpoints once the lifecycle endpoints are real).

**Dependencies.** P0.4 (taxonomy locked), P1.5 (flows visible per
endpoint). Can start before lifecycle endpoints ship — fill in
placeholders, complete during W5b/W6 of the lifecycle plan.

**Tooling note.** Once shipped, evaluate swaggo / oapi-codegen for
generation-from-code post-MVP. Decision deferred.

---

### 5.9 P2.9 — Tutorials content

**Goal.** Fill the `tutorials/` placeholder per Diátaxis. Goal is
**learning-oriented** walk-throughs, not task-oriented (those are
how-tos).

**Files added (3 starter tutorials).**
- `docs/tutorials/00-getting-started.md` — install hub + mobile +
  create first project end-to-end (~250 lines)
- `docs/tutorials/01-author-a-project-template.md` — write a custom
  project template YAML; instantiate; verify in mobile (~300 lines)
- `docs/tutorials/02-build-a-worker-agent.md` — author a worker
  template + prompt; have steward spawn it; observe in mobile
  (~250 lines)

**Acceptance.**
- [ ] Each tutorial has a clear learning objective stated upfront
- [ ] Steps are runnable end-to-end (verified by walking through them
      personally)
- [ ] Outputs explicit at each step (what to expect)
- [ ] Cross-references to reference docs (without overwhelming)
- [ ] `tutorials/README.md` index added

**Effort.** 4–5 days.

**Dependencies.** P0.2, P0.3, P0.4 (tutorials reference these). Can
run parallel to most.

---

### 5.10 P2.10 — Quality attributes

**Goal.** Quantified perf / security / scalability / offline
scenarios per arc42 § 10. Industry-standard but rarely shipped early;
ship now because demo audience expects it.

**File added.**
- `docs/reference/quality-attributes.md` (~300 lines)

**Content outline.**
1. TL;DR
2. Performance — cold-start <Xms, list-render <Yms, cache-revalidate
   <Zms; budget ranges with rationale; current measurements where
   available
3. Security — threat model summary, boundary diagram (links to
   `cross-cutting.md §security`), explicit non-goals (e.g., no
   client-side E2E encryption in MVP)
4. Scalability — current capacity (single-tenant Tailnet per
   ADR-018), expected limits (~100 projects, ~10 directors, ~100
   active agents per team), scaling plans (post-MVP)
5. Reliability — SSE reconnection budget, retry semantics, queued-
   mutation TTLs (24h per A4/A5)
6. Maintainability — code style (links to coding-conventions.md),
   doc-spec rigor (links to doc-spec.md), test strategy
7. Portability — supported platforms (Android primary, iOS
   secondary, web N/A in MVP), supported hub OS (Linux focus)
8. Cross-references

**Acceptance.**
- [ ] All 7 sub-sections present
- [ ] At least one quantified target per scenario (or explicit
      "TBD post-measurement" with rationale)
- [ ] Cross-links to ADRs that explain each choice
- [ ] Lint clean

**Effort.** 2 days.

**Dependencies.** P0.2, P0.4, P1.7 (cross-cutting structure exists).

---

## 6. Test + verification strategy

### 6.1 Lint

Existing `bash scripts/lint-docs.sh` enforces:
- Status block presence + format
- No broken links
- No memory-dir links
- Stale-doc warnings (non-failing)
- Glossary contract (`lint-glossary.sh`)

Pass after every doc lands.

### 6.2 Diagram validation

Mermaid blocks must render. CI gate (proposed): use a Mermaid
renderer in CI to validate syntax. If GitHub's preview misrenders, the
diagram is broken even if syntactically valid; spot-check by viewing
the file on github.com.

### 6.3 OpenAPI validation

CI gate (P2.8 ships with this): `swagger-cli validate` (or equiv) on
`openapi.yaml`. Reject PR on validation failure.

### 6.4 Cross-link audit (P1.6)

Mechanical: after blueprint refactor, run
`bash scripts/lint-docs.sh` to catch broken anchors. Fix iteratively
until clean.

### 6.5 Content review checkpoint

Two review checkpoints required (suggested):
- After P0 (4 items shipped) — internal review against the audit
  finding list; sign off that gaps are closed
- After P1 (3 items shipped) — review against arc42 / C4 / Diátaxis
  alignment table in §3; sign off that industry standards are met

---

## 7. Demo-readiness criteria (docs as demo artifact)

The demo audience includes reviewers + their AI agents. Doc-side
acceptance for demo-readiness:

- [ ] An AI agent given the docs and a question like "what schema
      stores project phase state?" returns a single canonical answer
      from `database-schema.md` or `project-phase-schema.md` without
      needing to grep the codebase
- [ ] An AI agent given "what's the request lifecycle for ratifying
      a deliverable?" finds the sequence diagram in `system-flows.md`
- [ ] An AI agent given "what's the chassis architecture?" lands on
      `architecture-overview.md` from `README.md` in 1–2 navigation
      steps
- [ ] A reviewer reading the docs and the demo together perceives
      the docs as the *source of truth* for the demo's claims —
      not a marketing surface
- [ ] OpenAPI spec validates and matches actual hub behavior at
      demo-time
- [ ] All ADRs cited by demo narration are linked from
      `decisions/README.md` (existing) and reachable from the
      relevant feature doc

---

## 8. Risks + mitigations

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| 1 | Blueprint refactor breaks dozens of cross-links across all docs | High | Cross-link audit in §5.6; lint runs after every move; incremental section-by-section rather than big-bang |
| 2 | OpenAPI spec drifts from hub implementation immediately after authoring | Med-High | Treat OpenAPI as authoritative; PR review gates require OpenAPI updated alongside endpoint changes; defer code-generation tooling decision but commit to spec discipline |
| 3 | Mermaid diagrams misrender in some markdown viewers (older GitHub Enterprise, etc.) | Low | GitHub.com renders modern Mermaid; document this as a soft constraint in `doc-spec.md §3` |
| 4 | This plan stalls and lifecycle work waits indefinitely | Med | Set hard checkpoints: P0 done by week 1, P1 by week 2-3, P2 by week 4; if P2 slips, ship P0+P1 and start lifecycle engineering |
| 5 | Diagram authoring time is underestimated (sequence diagrams are dense) | Med | Solo path budgets 25–35 days; inflated 1.5x for diagram authoring; review at P1.5 boundary |
| 6 | Tutorials become outdated immediately because the system evolves | High | Mark tutorials with `Last verified vs code: vN.N.N`; make outdated tutorials visible via the existing stale-doc warn lint |
| 7 | API overview taxonomy fights with feature-team preferences | Med | Make P0.4's group taxonomy a review checkpoint; iterate before locking |
| 8 | OpenAPI spec authoring is slower than estimated (per spec maturity learning curve) | Med-High | Schedule a 2-day spike at start of P2.8 to establish patterns + reusable components; remaining endpoints fill faster |
| 9 | "Quality attributes" requires real measurements we don't have | Med | Use "TBD post-measurement" placeholders with rationale where needed; ship the structure, fill the numbers post-measurement |
| 10 | Cross-cutting doc duplicates content from sub-topic docs | Low-Med | Discipline: cross-cutting summarizes + links; sub-topic docs stay canonical |

---

## 9. Open follow-ups (post-MVP doc work)

Captured here so they don't lose context after demo.

1. **Code-annotation tooling for OpenAPI** (swaggo, oapi-codegen).
   Decision deferred per P2.8.
2. **Generated ER diagram from migrations** — currently manual.
   Tooling TBD.
3. **Diagram-as-code for C4** (Structurizr DSL) vs Mermaid — Mermaid
   is fine for MVP; revisit if multi-level views grow.
4. **Doc-test pipeline** — extract code blocks from docs, run them
   to detect drift. Useful for tutorials.
5. **Multi-language tutorials** — currently English; translate to
   zh/ja post-MVP.
6. **Per-language doc index** — `docs/README.zh.md` /
   `docs/README.ja.md` exist (per memory) but the spine + reference
   layer are English-only. Decide if/when to translate.
7. **Architecture-decision log automation** — scripts to generate
   `decisions/README.md` from ADR status blocks.

---

## 10. Cross-references

- [`doc-spec.md`](../doc-spec.md) — doc taxonomy + status block
  contract this plan extends
- [`README.md`](../README.md) — top-level index updated by P0.2 +
  P1.6
- [`spine/blueprint.md`](../spine/blueprint.md) — refactor target of
  P1.6
- [`reference/glossary.md`](../reference/glossary.md) — vocabulary
  doc; informs all new docs
- [`plans/project-lifecycle-mvp.md`](project-lifecycle-mvp.md) —
  paused until at least P0+P1 ship; resumes immediately after
- [`plans/demo-script.md`](demo-script.md) — demo narrative; reviewer
  audience drives this plan's rationale (§7)
- arc42 framework — https://arc42.org/overview
- C4 model — https://c4model.com
- Diátaxis framework — https://diataxis.fr
- OpenAPI 3.x specification — https://spec.openapis.org/oas/latest.html
