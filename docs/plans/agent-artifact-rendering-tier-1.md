# Agent artifact rendering — Tier 1 (transcript code-fence renderers)

> **Type:** plan
> **Status:** Open
> **Audience:** contributors
> **Last verified vs code:** v1.0.466

**TL;DR.** The agent already emits fenced code blocks in its
markdown output; today they render as code text. This plan turns
specific fence languages into visual artifacts via the existing
`flutter_markdown` element-builder pipeline — first SVG, then
sanitised HTML. Cheapest wedge on the artifact-rendering axis from
[`../discussions/agent-driven-mobile-ui.md` §12](../discussions/agent-driven-mobile-ui.md):
no new MCP tools, no protocol change, no security sandbox to
design, no APK-size dominance.

## Goal

Agent-emitted markdown can include visual artifacts inline:
` ```svg ` and ` ```html ` fences render as widgets instead of
code text. Renderer registry is extensible so future languages
(`mermaid`, `chart.json`, `dot`) plug in without touching
unrelated code.

## Non-goals

- **Interactive artifacts.** No JS execution, no event handlers.
  That's Tier 2 territory and needs the sandbox decision.
- **Full HTML pages.** Tier 1's HTML fence is for *fragments* —
  formatted text, structural markup, simple tables. Anything
  needing scripts opens a sandboxed WebView (Tier 2).
- **Charts.** A `chart.json` fence is tempting but pulls in a chart
  library and a typed schema — that's Tier 3 territory and should
  wait for evidence that Tier 1 isn't enough.
- **Hub-side rendering.** No Mermaid → SVG round-trip in this
  wedge; if Mermaid demand surfaces, it lands as a follow-up that
  reuses the SVG renderer from W2.
- **Replacing existing markdown rendering.** Plain text, headings,
  lists, links keep their current behaviour.

## Wedges

### W1 — Renderer-registry chassis

**Scope.** Extract a `CodeFenceRenderer` interface and a registry in
`lib/widgets/markdown_builders.dart`. The registry maps a fence
language string (case-insensitive) to a function returning a
widget. Default behaviour for unregistered languages is unchanged
(passthrough to the existing code-block render).

**Files touched:**
- `lib/widgets/markdown_builders.dart` — registry + default
  passthrough renderer.
- `lib/widgets/agent_feed.dart` — pass the registry into
  `MarkdownBody.builders` (current builders mechanism).
- `lib/screens/projects/doc_viewer_screen.dart` — same wiring so
  project documents get the same treatment.
- `test/widgets/code_fence_registry_test.dart` — new.

**Test plan:**
- Unknown language → fenced text renders as code (no regression).
- Registered language → registered widget renders.
- Missing language tag (` ``` ` with no language) → existing
  monospace block renders.
- Registry can register the same language twice (later wins) so
  hot-overrides work without restart.

**LOC estimate:** ~150 mobile.

### W2 — SVG code-fence

**Scope.** Register a renderer for ` ```svg ` fences. Body of the
fence is parsed by `flutter_svg`'s `SvgPicture.string`. Renderer
clamps `maxWidth` to the parent constraint and preserves intrinsic
aspect ratio. On parse failure, fall back to the default code-block
render so a malformed SVG doesn't blank the transcript.

**Security constraints baked in:**
- `<image href="...">` referencing remote URLs → strip the `href`
  attr or replace with a placeholder. Local `data:` URIs allowed.
  (Stops SVG-as-tracker.)
- `<script>` inside SVG → strip. (`flutter_svg` doesn't execute
  scripts but malicious SVGs may still confuse downstream parsers.)
- `<foreignObject>` → strip. (Allows arbitrary HTML/JS embedding.)

**Dependencies:** `flutter_svg` (~100 KB).

**Files touched:**
- `pubspec.yaml` — add dep.
- `lib/widgets/markdown_builders.dart` — register `svg` renderer.
- `lib/widgets/svg_fence_renderer.dart` — new; sanitisation +
  rendering.
- `test/widgets/svg_fence_renderer_test.dart` — new.

**Test plan:**
- Valid simple SVG → SVG widget rendered.
- Invalid SVG → falls back to code block, no crash.
- SVG with `<image href="https://...">` → href removed in render.
- SVG with `<foreignObject>` → element stripped.
- SVG embedded in a longer transcript → other markdown elements
  still render normally above and below.

**LOC estimate:** ~250 mobile.

### W3 — Sanitised HTML code-fence

**Scope.** Register a renderer for ` ```html ` fences. Body is
parsed by `flutter_html` with an explicit allowlist of tags +
attributes. Anything outside the allowlist is stripped (not
escaped — the caller should never see the source on the page).

**Allowlist:**
- Tags: `p`, `span`, `div`, `h1`–`h6`, `ul`, `ol`, `li`, `table`,
  `tr`, `td`, `th`, `thead`, `tbody`, `em`, `strong`, `code`,
  `pre`, `br`, `hr`, `img`, `a`, `blockquote`.
- Attributes: `style` (whitelisted properties: `color`, `background-color`,
  `font-weight`, `text-align`, `padding`, `margin`,
  `border`); `href` on `<a>` (must be `http(s):`, `mailto:`,
  `termipod:`, or `muxpod:` — `javascript:` rejected); `src` on
  `<img>` (must be `https:` or `data:image/`).

**Dependencies:** `flutter_html` (~150 KB).

**Files touched:**
- `pubspec.yaml` — add dep.
- `lib/widgets/markdown_builders.dart` — register `html` renderer.
- `lib/widgets/html_fence_renderer.dart` — new; sanitisation +
  rendering using `flutter_html`'s `tagsList` + `customRenders`.
- `test/widgets/html_fence_renderer_test.dart` — new.

**Test plan:**
- HTML with allowlisted tags only → renders correctly.
- HTML with `<script>alert(1)</script>` → script stripped, alert
  never fires.
- HTML with `<a href="javascript:void(0)">` → href stripped,
  link non-functional.
- HTML with `<iframe src="...">` → iframe stripped.
- HTML with inline event handlers (`onclick="..."`) → handlers
  stripped.
- HTML with disallowed CSS properties (`position`, `transform`)
  → properties stripped, layout stays sane.

**LOC estimate:** ~350 mobile.

## Total budget

- ~750 LOC mobile, no hub change.
- +250 KB APK (flutter_svg + flutter_html combined).
- ~3–5 working days, parallelisable W2 and W3 once W1 lands.

## Dependencies on other plans

- None blocking. Builds on the existing
  `flutter_markdown` integration shipped pre-rebrand.

## Rollout

1. W1 lands first; merges with no behavioural change (registry
   only, no registered renderers yet).
2. W2 lands next; SVG works.
3. W3 lands; HTML works.
4. Bump alpha tag once all three are merged.
5. Update steward template prompt
   (`hub/templates/prompts/steward.general.v1.md`) to mention the
   `svg` and `html` fence affordances so the steward knows they
   exist. (Otherwise the agent ships text and the renderer is
   dead code.)

## Test plan (cross-wedge)

- Manual: ask the steward "draw me a small architecture diagram"
  → expect SVG fence in transcript → renders inline.
- Manual: ask the steward "summarise the deliverables in a
  styled table" → expect HTML fence → renders inline.
- Manual: paste a malformed SVG into a steward reply via injection
  → expect graceful fallback to code block.
- Manual: paste a `<script>` HTML payload via injection → expect
  no execution, no visible script tag.
- Regression: open every existing screen that uses
  `MarkdownBody` (`agent_feed`, `doc_viewer_screen`,
  `task_edit_sheet`, `markdown_section_editor`) and confirm
  no rendering changes for non-`svg`/`html` content.

## Status

Open — kicked off 2026-05-10 from principal QA on the v1.0.466
prototype. No commits yet. Discussion at
[`../discussions/agent-driven-mobile-ui.md` §12](../discussions/agent-driven-mobile-ui.md).
