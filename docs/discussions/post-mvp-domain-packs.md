# Domain-pack extensibility — post-MVP discussion

Status: **discussion note, 2026-04-27**. Triggered by the question
"can the architecture support OS-like plugin/app extensibility for
domain workflows (ML, bioinfo, legal, …) as a paid commercialization
layer?"

Long answer: the architecture is **better positioned than you'd
expect** for content-shaped domain packs, but the "OS plugin" framing
is misleading and may push toward expensive solutions to non-problems.

---

## 1. Reframe the question first: code packs vs content packs

The "OS app store" analogy implies *executable extensions* — code that
runs in a sandbox, has API access, owns UI surfaces. That model is
huge engineering (sandbox, permission model, supply chain trust,
runtime widget loading) and it is mostly unnecessary for what a
domain pack actually contains.

When you list what differentiates ML from bioinfo from legal, you're
really listing:

| Surface | ML | Bioinfo | Legal |
|---|---|---|---|
| Project archetypes | ablation-sweep, hyperparam-search, paper-repro | genome-assembly, variant-calling, diff-expression | contract-review, discovery, compliance-audit |
| Parameter schemas | model_size, optimizer, lr | genome_build, coverage_threshold | jurisdiction, practice-area |
| Worker personas | ml-worker, evaluator | aligner, caller, annotator | clause-extractor, precedent-searcher |
| Steward decomposition recipe | sweep-then-synthesize | QC-then-pipeline | identify-then-summarize |
| Vocabulary | epoch, batch, ablation | locus, contig, p-adjusted | holding, dicta |
| Artifact kinds | checkpoint, eval_curve, figure | BAM, VCF, count_matrix | redline, brief, exhibit |
| Default channels | #experiments, #papers | #wet-lab, #qc-flags | #matters, #precedents |
| Visualizations | loss curves, confusion matrix | coverage tracks, MA plot | redline diff, citation graph |
| Integrations | trackio, wandb, tensorboard | NCBI, Ensembl | Westlaw, LexisNexis |

Of those nine rows, **seven are pure content** (text/JSON/YAML). Two
need code (visualizations and integrations). And of those two:
- Visualizations can mostly be expressed declaratively (Vega-Lite-style
  chart specs) and rendered by a generic viewer.
- Integrations are the only row that genuinely needs domain code —
  and most of that code lives in the host-runner side (pollers /
  adapters), not in the mobile app.

**This is the most important strategic point.** A "pack" is
overwhelmingly a content bundle, not an app. Once you accept that,
the engineering shrinks by an order of magnitude and the business
model becomes much cleaner.

---

## 2. Audit: what the current architecture already supports cleanly

The codebase has a surprising amount of pack-shaped infrastructure
already, even though nobody designed it as such:

| Capability | Where it lives today | Pack-readiness |
|---|---|---|
| **Project templates** | `hub/templates/projects/` + `projects` table with `is_template=1`, `parameters_json`, `template_id`, `on_create_template_id` | First-class concept; just embedded vs DB-per-team |
| **Agent templates** | `hub/templates/agents/*.yaml` (steward.research, ml-worker, briefing) | YAML format already factored — `steward.research.v1.yaml` proves the pattern |
| **Persona prompts** | `hub/templates/prompts/*.md` referenced by agent templates | Pure markdown, separable |
| **Parameter schemas → forms** | `parameters_json` on projects + mobile project-create sheet's auto-form (v1.0.152) | Generic schema-to-form engine works for any JSON Schema-shaped object |
| **Engines** | `agent_families` table + editor (deferred per memory) — claude-code, codex, etc. | Already a per-team customization point |
| **Snippets** | Per-user, with categories | User-level pack equivalent |
| **Action-bar profiles** | Multi-profile per-panel | User-level pack equivalent |
| **Tier policy** | `tiers.go` static + per-project `policy_overrides_json` | Already overridable per project |
| **Artifact kinds** | `artifacts.kind` is a free-form string | Schema doesn't constrain enum — domain-pack can introduce new kinds |
| **Channels** | First-class entity, project- and team-scoped | Pack can declare default channels |
| **Vocab axes** | `docs/vocabulary.md` already names 21 axes + 4 theme presets | Axis-tagged keys exist but are app-level, not per-team-overridable yet |
| **MCP tool catalog** | Hardcoded server-side (now consolidated in `mcp_authority.go` + `mcp_more.go`) | Static; pack can't add new MCP tools without a server change |
| **Steward decomposition recipes** | Embedded in `prompts/steward.*.md` | Pure prompt content — the "intelligence" of a pack lives here |

**Honest reading:** ~70% of what a domain pack would need is *already*
a structured data type. The system was designed to be parametric
(template-driven, policy-overridable, multi-engine) for other reasons,
and that decision pays off here.

---

## 3. What's missing (and what's hard vs easy)

### Easy gaps (each is a wedge or two)

1. **Per-team templates.** Today templates are `//go:embed`'d into the
   binary — same set for everyone. To support packs, templates need
   to be DB-backed and team-scoped. The DB schema is already
   pack-friendly; the missing piece is an upload/install endpoint and
   a per-team scoping rule.
2. **Pack manifest format.** A YAML file that bundles
   project_templates, agent_templates, prompt_files, default_channels,
   vocab_overlay, artifact_kind_definitions, dependencies. ~1-2 days
   of design, ~200 LoC of parser/installer.
3. **Vocab overlay per team.** The vocab-audit framework already
   separates strings by axis; extend it to allow per-team JSON
   override. ~100 LoC + i18n plumbing.
4. **Default-channel materialization.** When a pack is installed,
   auto-create its default channels for the team. ~50 LoC.
5. **Pack list/install/activate endpoints.** REST CRUD on a `packs`
   table; nothing exotic. ~1 wedge.

Total for the data-pack story: **2-3 wedges of focused work.**

### Hard gaps (each is a multi-month effort, may not be worth it)

1. **Custom UI screens.** Flutter is AOT-compiled. Three options for
   runtime UI:
   - **Theme + string overlay only** (cheap, limited) — what you'd
     have for free if vocab overlay ships
   - **Declarative dashboard DSL** (medium, powerful for charts) —
     Vega-Lite-style spec; ~1 month for a generic renderer
   - **WebView for pack screens** (heavy, breaks UX) — last resort

   Realistically, ML pack and bioinfo pack will both want custom plots
   (loss curves, coverage tracks). The Vega-Lite-style approach is the
   right answer. But it's not a one-wedge job.

2. **Custom artifact viewers.** Bioinfo wants IGV (genome browser).
   Legal wants redline diff. ML wants TensorBoard. These are full
   apps. Solution: use the existing artifact `uri` field to deep-link
   to external viewers (open in browser/WebView). Don't build them in.

3. **Domain-specific integrations.** trackio/wandb/tensorboard are
   baked into host-runner. NCBI/Westlaw/etc. would need similar work.
   Two paths:
   - **Sidecar daemons:** the pack declares "needs
     `bioinfo-companion-daemon` running"; ops installs that
     separately. Clean separation, no plugin runtime needed.
   - **Go plugins / WASM:** technically possible, operationally
     fragile. Skip.

4. **Marketplace + entitlement.** Account system, license keys,
   payment processor, refund flows, tax handling, takedown process,
   third-party developer onboarding, certification/quality bar.
   **This is its own product.** Don't underestimate it.

5. **Code-bearing packs (third-party developers).** If you let third
   parties ship code, you inherit Apple's App Store problem at 1% of
   the headcount. Avoid until the data-only model has proved value.

---

## 4. Phased roadmap (post-MVP)

The right sequence — each phase delivers value standalone, no phase
forces the next:

**Phase 1 — First-party packs as data (1-2 wedges)**
Ship "ML pack" as a subdirectory under `templates/` with project
archetypes, worker personas, steward decomposition recipes specific
to ML. Don't change architecture; just split the embedded content.
Prove that having domain-specific templates makes the demo measurably
better. Cheapest learning per dollar.

**Phase 2 — Templates DB-backed, per-team uploadable (2 wedges)**
Move templates out of the embedded FS into the `projects` / new
`agent_templates` table, keyed by team. Add
`POST /v1/teams/{team}/packs/install` taking a YAML manifest +
bundle. Mobile UI gets a "Packs" screen under team settings.

**Phase 3 — Pack manifest + first-party content marketplace (2-3 wedges)**
Define the manifest format precisely. Ship a small set of
Anthropic-published packs (ML, bioinfo, generic SWE). The
"marketplace" at this stage is just a curated list in the mobile app
— no payment, no third-party submissions.

**Phase 4 — Vocab overlay + custom defaults (1 wedge)**
Per-team string overlay extending the existing axis-tagged framework.
Default channels materialize on pack install. Now an installed ML
pack actually *feels* like ML when the team uses it.

**Phase 5 — Commercial layer (the "marketplace" proper, ~6 months)**
Account/billing, license server, third-party developer onboarding,
payment processor integration, revenue share, certification process,
quality gating, takedowns. **This is when the business model goes
live.** Don't start it until phases 1-4 prove there's a market.

**Phase 6 — Declarative dashboards / chart spec DSL (1-2 months)**
A Vega-Lite-style renderer in mobile. Packs can ship dashboard specs
that render natively.

**Phase 7 (probably never) — Code-bearing packs**
Third-party UI widgets, custom MCP tools, custom integration daemons.
Heavy lift, ongoing maintenance, security hazards.

---

## 5. Strategic risks

1. **Vertical-specific tools own their domains.** Bioinfo has Galaxy +
   Nextflow + Seven Bridges. ML has W&B, MLflow, ClearML. Legal has
   Relativity, Disco. They're entrenched and have years of domain
   depth. Your pack needs to do something they don't —
   orchestrating-agents-on-mobile is your wedge, but the agentic
   angle has to be the *value*, not the templates.

2. **Pack quality is your reputation.** A bad ML pack makes Termipod
   look unreliable. First-party packs (Phase 1-3) protect you;
   third-party packs (Phase 5+) are how you get sued.

3. **Marketplace is its own product.** App stores, theme stores,
   plugin stores all carry significant non-engineering cost: legal
   review, billing disputes, takedowns, abuse, dev relations. Don't
   enter without a clear answer to "who runs this, full-time."

4. **Long-tail pareto.** In every marketplace, top 3-5 items earn
   80%+ of revenue. Tail is overhead. Plan to maintain the top 5
   yourself; let community fill the rest if it does.

5. **Lock-in concern is real.** If a buyer's project data is encoded
   against pack-specific artifact kinds, vocab, channels — what
   happens when they uninstall? Either pack content has to be
   exportable cleanly, or you've created a hostage situation. Design
   exports from day one.

6. **Open-source positioning.** The Obsidian / JetBrains / Sketch
   model is "free core, paid plugins." It works because the core is
   genuinely useful standalone. Your MVP needs to be valuable to a
   research demo without ANY pack — otherwise the freemium funnel
   breaks.

7. **First-party vs third-party question.** Are you building a
   marketplace where you sell packs, or where third parties sell
   packs? Very different businesses. The first is content production
   at scale (more like a publisher). The second is platform operation
   (more like an exchange).

---

## 6. Architectural decisions to make NOW so you're not blocked later

These are pre-MVP decisions that cost almost nothing today but
preserve future optionality:

| Decision | What to do now | Why |
|---|---|---|
| **Template ID scoping** | Don't hardcode template IDs in mobile screens; always look up by `template_id` field. | If templates become per-team, hardcoded IDs break. |
| **Parameter form generality** | Ensure `parameters_json` form engine handles arbitrary JSON Schema, not just our specific shapes. | Future-proofs for third-party params. |
| **Vocab consistency** | Continue using axis-tagged keys per `docs/vocabulary.md` standing rule for all role-bound strings. | This IS the foundation of pack-level vocab overlay. Do it now or pay later. |
| **Artifact kind discipline** | Don't enum-constrain `artifacts.kind`; document the standard kinds but allow new ones. | Already the case. |
| **Steward template format** | Keep agent templates as YAML + linked prompts (separate files). | Already the case. Don't fork into a single mega-format. |
| **Tier policy stays per-project overridable** | Already shipped. Document this as the official customization point for "what tier is this in MY domain." | A bioinfo pack might tier `data deletion` as Strategic where ML pack treats it as Routine. |
| **Don't build code-execution paths yet** | Resist any urge to ship a "plugin runtime" before content packs prove value. | One-way door. |
| **Make every list endpoint filterable by `template_id`** | Audit list APIs; ensure they accept template filtering. | When packs ship, "show me only this pack's stuff" needs to work. |

---

## 7. The single highest-leverage move

If you do only one thing in this direction post-MVP:

**Ship Phase 1.** Split the existing `templates/` into `templates/core/`
(generic) + `templates/ml/` (your locked Candidate A demo) + a third
pack stub (e.g. `templates/swe/` for general software work). Keep
them all embedded. Don't build pack infrastructure yet.

This costs almost nothing, but it teaches you:
- Whether domain-specific recipes actually outperform generic ones
  (the assumption underlying the whole business model)
- What's truly shared vs domain-specific (the manifest format will
  fall out of this)
- Whether users / customers prefer one over the other (revenue
  signal)

If the answer is "domain recipes are clearly better and users want
more of them," you have evidence to invest in Phase 2. If the answer
is "the generic steward is fine, domain detail doesn't matter much,"
you've saved 6+ months of marketplace work.

---

## 8. The bottom line

**The architecture is genuinely well-suited for a content-pack model.**
The template/parameter/policy/channel/artifact-kind primitives are
already pack-shaped. The vocab framework is already designed for
overlay. The MCP catalog is consolidated cleanly. You are 2-3 wedges
from a usable per-team pack system.

**The "OS plugin" framing is the wrong analogy.** It pushes toward
sandbox runtimes, code execution, supply chain problems — all
expensive solutions to problems your customers don't have. A
content-pack model gives you 80% of the commercial value at 20% of
the engineering cost.

**The hard part isn't the code; it's the marketplace.**
Account/billing/legal/takedowns/dev-relations is a separate business.
Don't underestimate it.

**Validate Phase 1 first.** Ship one domain pack as embedded content.
See if it moves the needle. The architecture decisions in §6 cost
nothing and keep doors open.
