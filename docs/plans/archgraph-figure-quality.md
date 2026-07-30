# Archgraph figure quality — publication-grade model schematics in Inspect

> **Type:** plan
> **Status:** **Shipped** — W1–W5 all landed 2026-07-29 (#430–#436: hybrid
> classification, KV-cache-per-token class, heterogeneous schematic + pattern
> strip, zoom/annotation/SVG-PNG export, arch diff) and ship in desktop
> `2026.730.1242`; reviewed against live HF configs
> **Audience:** principal · contributors
> **Last verified vs code:** origin/main `ae3b99c2`
> **Parent:** [debug-code-logs-diffs-models.md](debug-code-logs-diffs-models.md)
> §5a (the config-only architecture schematic). This plan upgrades the
> schematic's *data model* (hybrid/linear-attention architectures) and its
> *rendering* (figure-grade visual language), and adds an arch diff + export.

**TL;DR.** The Inspect tab's architecture schematic
(`desktop/src/ui/ArchSchematicView.tsx` over the pure spec
`desktop/src/state/archSchematic.ts`) renders one vertical column of uniform
cards inside a single dashed ×N container. That template was right for
2023-era homogeneous decoders; 2026 architectures broke it. Kimi K3
(released 2026-07-27, weights on HF) interleaves Kimi Delta Attention with
Gated MLA at 3:1 — our classifier would misrender it as a *uniform* MLA-MoE
stack, which is actively wrong, not merely sparse. Meanwhile two reference
figure sets show the quality bar: the K3 blog's architecture figure (a
hand-authored inline SVG React component — **the same stack we use**, so the
ceiling is proven) and Sebastian Raschka's LLM architecture gallery
(93 models, standardized cards, a model-diff tool). Five staged work items:
**W1** classifier vocabulary + config-derived chips, **W2** heterogeneous
layer-pattern spec, **W3** renderer shape/edge/typography vocabulary,
**W4** zoom-in panels + annotation layer, **W5** arch diff + SVG/PNG export.

---

## 0 · Evidence and references

- **Kimi K3 blog** (`kimi.com/blog/kimi-k3`): 2.8T params, 896 experts /
  16 active ("Stable LatentMoE" + shared expert), Kimi Delta Attention (KDA)
  hybridized with Gated MLA at 3:1, Attention Residuals (weighted `w`/`α`
  streams from embedding + earlier block summaries into every sublayer),
  NoPE, MXFP4/MXFP8 QAT, 1M context. The figure is an inline SVG React
  component (`BlockAttnRes` CSS module, ~280 styled elements, KaTeX math
  fonts, `.dark` theme overrides) — not an image. Weights:
  `huggingface.co/moonshotai/Kimi-K3` (released 2026-07-27).
- **Raschka gallery** (`sebastianraschka.com/llm-architecture-gallery`):
  93 model cards, each with a hand-drawn architecture figure (2994×2754),
  standardized spec rows (attention variant, QK-Norm, RoPE/NoPE/YaRN, exact
  layer-type breakdown, KV-cache-per-token classed low→very-high, MTP,
  license/context), links to each model's HF `config.json`, and a
  side-by-side **diff tool** between any two models.
- Both figures were pulled and inspected at pixel level during the
  2026-07-28 audit; the technique list in §3 is read off the actual images,
  not descriptions.

## 1 · Current state (all verified at `ae3b99c2`)

- **Classifier** `desktop/src/state/checkpoint.ts` `classifyArch()`:
  `ArchTemplate` is exactly `dense-gqa | moe | mla | mla-moe | unknown`.
  `FAMILY` maps ~30 `model_type` ids (through `kimi_k25`, `glm_moe_dsa`,
  `qwen3_5*`); unknown ids title-case gracefully but get **no chips** —
  `commonChips()` asserts RoPE/RMSNorm/SwiGLU by family membership, not by
  reading the config. `estimateParamsFromConfig()` covers dense/GQA/MLA ×
  dense/MoE (+ `first_k_dense_replace`).
- **Spec** `desktop/src/state/archSchematic.ts`: pure 7-node stack —
  embed → [norm, attn, norm, ffn|moe]×N → final norm → head — plus two
  residual edges and a `layers` count. One container level; no notion of
  per-layer heterogeneity (`first_k_dense_replace` appears only as a
  detail-panel row). Unit-tested without React.
- **Renderer** `desktop/src/ui/ArchSchematicView.tsx`: React Flow lazy
  chunk; uniform 260×56 cards colour-banded by `ArchNodeKind`; one dashed
  ×N container; smoothstep main flow + two side-routed residual beziers;
  click → text detail panel (`archNodeDetails`); shared `useContextMenu`
  (copy details / fit view); pan/zoom.
- **Already on the surface, reusable:** VRAM card computes KV-cache bytes
  (GQA *and* compressed MLA latent) — `ui/ModelView.tsx`; FLOPS card; dtype
  histogram (would show MXFP4 tensors); tensor tree with structural ×N
  collapse; **HF repos are pinnable Inspect roots**
  (`surfaces/InspectRepoAdd.tsx` + `state/forge.ts`), so any gallery
  model's `config.json` — including K3's — is openable today.
- **Not present anywhere:** model-vs-model comparison
  (`surfaces/CompareSurface.tsx` does not touch checkpoints), schematic
  image export, linear-attention concepts.

## 2 · Gap analysis

| # | Gap | Consequence today |
|---|-----|-------------------|
| G1 | No heterogeneous layer stacks (`layer_types` / interleave patterns / sliding-window cadence) | K3 (3:1 KDA:MLA), Kimi Linear, Qwen3-Next, Gemma 3 (5:1 sliding:global) all render as a uniform block — **misleading**, not just lossy |
| G2 | No linear-attention vocabulary (KDA / gated-deltanet / conv / gated attention) in templates or node kinds | K3's `kv_lora_rank` keys → classified plain `mla-moe`; family shows as title-cased `Kimi_k3` |
| G3 | No intra-block structure (router→experts fan-out, Q/K/V+gate internals) | The single most recognisable element of both reference figures is absent |
| G4 | Chips family-asserted, not config-read | QK-Norm, NoPE/partial RoPE, YaRN `rope_scaling`, `sliding_window`, MTP sit unread in configs we already parse |
| G5 | No arch diff | The gallery's most-used feature; ours would be pure `ArchCard` × 2 |
| G6 | KV-cache-per-token not on the arch card | The calculation already exists in the VRAM estimator |

## 3 · The six figure techniques to adopt (from the reference figures)

1. **Multi-panel composition** — macro stack plus dotted-border zoom-in
   panels (attention internals, MoE router→experts) linked by dotted leader
   lines.
2. **Nested grouping** — dashed sublayer boxes (attn / FFN-MoE) inside the
   ×N container; nested repeat groups (`3×` / `1×`) to express interleave
   patterns visually.
3. **Shape vocabulary** — operator glyphs (⊕ add, ⊗ gate-multiply, σ
   sigmoid), trapezoids for down/up projections, expert chips drawn as a
   `1 2 … N` row, router box with a mini histogram.
4. **Distinct edge streams** — auxiliary flows (K3's AttnRes) in their own
   colour, visually separate from the main top→bottom flow and from
   residual skips.
5. **Annotation layer** — callouts with leader lines ("layer 1 dense,
   layers 2–93 MoE", context length), corner legends, a monospace
   per-layer pattern table.
6. **Typographic hierarchy** — bold component names, muted dims, math
   italics for weights/gates, novelty-highlight colour against neutral
   grey for standard Linear layers.

All six are node-type/layout problems, not engine problems: React Flow
supports custom SVG node shapes, parent/group nodes, multi-handle coloured
edges, and an absolutely-positioned annotation layer. No replatforming.

## 4 · Decisions

- **D-1 Keep the pure-spec / renderer split.** Everything W2 adds
  (groups, glyph nodes, panels, annotations, edge streams) lands in
  `archSchematic.ts` as data, unit-tested without React; the renderer only
  grows a shape library. This is the same discipline that made the current
  schematic testable.
- **D-2 Config-derived first, curated overlay second.** Everything a
  `config.json` can honestly yield (layer pattern, dims, chips, KV class)
  is derived. Narrative annotations a config cannot yield (e.g. AttnRes
  semantics) come from a small **per-family overlay template** shipped in
  code — never invented from field names. Provenance badge stays.
- **D-3 Verify config keys against real HF configs before coding.** The
  audit's key names for `kimi_k3` / `kimi_linear` / `qwen3_next` /
  `gemma3` came from training memory. W1's first task is to pin the real
  `config.json` of each (HF roots already work in Inspect) and lock the
  exact key names in fixtures. **Do not skip this.**
- **D-4 Honest fallback.** A config the vocabulary doesn't cover renders
  the current uniform stack (never a guessed hybrid), with the existing
  `unknown`-tolerant behaviour. Misrendering is worse than plainness —
  that is the whole reason this plan exists.
- **D-5 Diff rides a schema-pane picker, not CompareSurface.** The compare
  surface is transcript-oriented; arch diff is two `ArchCard`+config pairs
  side-by-side inside the Inspect model view ("compare against…" → file /
  HF pin picker). Pure `diffArchCards()` first, UI second.
- **D-6 Export is SVG-first.** The schematic is already SVG inside React
  Flow; export serialises the laid-out figure (inlined styles, both
  themes), PNG via canvas as a convenience. This is what makes the figures
  usable in docs/slides — the reference figures exist *because* they are
  publishable.

## 5 · Work items

### W1 — Classifier vocabulary + honest chips (state only)

- Pin real configs for `kimi_k3`, `kimi_linear`, `qwen3_next`, `gemma3`
  (+ keep `deepseek_v3`, `kimi_k2` as regression fixtures); freeze them as
  test fixtures with exact key names (D-3).
- `ArchTemplate` gains hybrid/linear members (shape decided against the
  real configs — expected: `linear-hybrid` with a sub-descriptor rather
  than one enum per lab).
- `FAMILY` sweep: `kimi_k3`, `kimi_linear`, `qwen3_next`, MiniMax,
  current-gen ids. (Bug-class reminder: adding an option must sweep every
  offer surface — `TEMPLATE_LABEL`, chips, param estimator guards.)
- Config-derived chips replacing family assertion where the config
  disagrees or adds: QK-Norm, NoPE / partial RoPE, YaRN (`rope_scaling`),
  sliding window, MTP (`num_nextn_predict_layers`-style), gated attention.
  Family assertion stays only as fallback for silent configs.
- KV-cache-per-token on the arch card (lift the existing VRAM math),
  classed low / moderate / high / very-high (G6).
- `estimateParamsFromConfig` extended for linear-attention layers (or an
  explicit honest `null` with a badge until the math is verified).
- **Accept:** fixture tests for all six configs produce correct family,
  template, chips, KV class; K3 no longer classifies as uniform `mla-moe`.

### W2 — Heterogeneous stack spec (state only)

- `ArchSchematic` v2: `groups` (nested repeat containers with counts —
  the `3×`/`1×` idiom), per-group sublayer membership, `patternStrip`
  (ordered per-layer kind list derived from `layer_types` /
  `first_k_dense_replace` / cadence fields), `streams` (auxiliary edge
  flows with kind), `panels` (zoom-in specs, W4 consumes), `annotations`.
- Builders for: uniform (current), first-K-dense, ratio interleave
  (K3 3:1, Gemma 3 5:1), explicit `layer_types` arrays (Qwen3-Next).
- **Accept:** pure unit tests — K3 fixture yields nested 3×/1× groups +
  93-entry pattern strip; Gemma 3 yields 5:1; DeepSeek-V3 yields
  first-3-dense; uniform models yield exactly today's spec (golden test —
  no regression for the common case).

### W3 — Renderer shape/edge/typography vocabulary

- Node shape library: glyph nodes (⊕ ⊗ σ), trapezoid projection cards,
  expert-chip row, router card; sublayer dashed group nodes; nested ×N
  containers; per-stream edge colours; pattern strip rendered as a compact
  vertical stripe beside the stack.
- Typography: bold/muted hierarchy, math-italic weight labels, novelty
  accent colour (design-tokens; both themes — the K3 blog's `.dark`
  overrides are the precedent). Token ratchet applies
  (`scripts/lint-desktop-tokens.sh` after `npm run sync:tokens`).
- **Accept:** K3 fixture renders nested groups with distinct KDA vs Gated
  MLA cards and an AttnRes stream; uniform models look ≥ today (screenshot
  sanity per device-verify discipline); i18n keys added to **both** dicts.

### W4 — Zoom-in panels + annotation layer

- Dotted-border detail panels for attention internals and MoE
  router→experts fan-out, linked by dotted leader lines; opened per D-2
  from config facts + per-family overlay templates; replaces nothing —
  the click detail panel stays.
- Annotation callouts + legend from spec `annotations`.
- **Accept:** MoE panel is config-derived for any MoE model; K3/DeepSeek
  overlay adds family narrative; models with no overlay get config-only
  panels with no invented claims (D-4).

### W5 — Arch diff + export

- `diffArchCards(a, b)` pure (template/dims/chips/KV rows with
  changed-row marking) + "compare against…" picker in the model view
  (file or HF pin); renders two schematics side-by-side with the diff
  table between.
- SVG export (inlined styles, light + dark) + PNG convenience; offered in
  the schematic context menu **and** any toolbar surface (parity rule).
- **Accept:** round-trip test — exported SVG re-renders standalone;
  diff of K3 vs K2 fixture highlights attention template, expert count,
  context; diff of a model against itself is empty.

## 6 · Non-goals (recorded, not scheduled)

- Importing the Raschka gallery's data or images — HF pinning + our parser
  reproduce any of its models live; the gallery is a quality bar, not a
  data source.
- Weights-derived structure in the schematic — the ONNX op-graph and the
  module graph (W4b of the parent plan) already cover "what the tensors
  actually do"; the schematic stays config-only by design.
- Hand-curated per-model narrative beyond the per-family overlays (D-2).
- Mobile parity for the schematic (desktop-first; mobile keeps the arch
  card).

## 7 · Verification discipline

- Spec/classifier tests run via `node --test src/state/*.test.ts` —
  **not run by CI** (only the electron suite is); run manually, per the
  standing Inspect-tab discipline.
- Fresh-worktree desktop verify: `npm ci` in `desktop/` and
  `desktop/electron/`, `npm run sync:tokens` before the token lint,
  `NODE_OPTIONS=--max-old-space-size=6144` for the vite build.
- Every fixture config committed verbatim from HF (D-3) with its source
  URL + revision in a comment.
