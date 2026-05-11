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

## Open questions

These need answers before the matching wedge starts. Surfaced 2026-05-10
during a pre-implementation review; folded into the plan so the next
contributor has to address them before opening a PR.

### Blocking W1 (chassis)

**Q1 — How does the registry hook into `flutter_markdown`?**
The plan says "registry maps a fence language string to a widget,"
but `MarkdownBody.builders` is keyed by *element tag* (`code`,
`pre`), not by fence *language*. To dispatch by language you have
to intercept `code` and read `element.attributes['class']`
(`language-svg` etc.). Confirm the installed `flutter_markdown`
version exposes that field before treating "language → widget" as
the chassis abstraction. If it doesn't, W1 needs a different
hook point (e.g. fork the markdown parser, or pre-transform the
fenced blocks before they reach `MarkdownBody`).

**Q2 — Does `flutter_html` support per-CSS-property allowlisting?**
W3 specifies a property whitelist (`color`, `background-color`,
`font-weight`, `text-align`, `padding`, `margin`, `border`).
Older `flutter_html` versions only allowlist *tags* + the `style`
*attribute* — not individual properties inside `style`. If
property-level filtering needs custom CSS-parser code, the W3 LOC
estimate (350) is light. Pin a version, skim its API, restate
the W3 scope or pick a different sanitiser.

**Q3 — Streaming partial fences.** Agent text arrives in chunks
during SSE. A ` ```svg ` fence may be half-written every
intermediate frame; "parse failure → fall back to code block"
means the user sees `code-block → SVG widget` swap when the
closing fence arrives, with visible flicker. Pick a policy:
- **(a)** Buffer the fence body invisibly until the closing
  ` ``` ` arrives, render the widget once.
- **(b)** Render as code while the fence is open, swap to widget
  on close (current implicit policy — visible flicker).
- **(c)** Detect "in-progress fence" and show a small spinner
  placeholder while incomplete.
This is the dominant UX risk in W2 and a likely follow-up bug
report if undecided.

### Blocking W2 / W3

**Q4 — Theme propagation.** SVGs often have hardcoded fills;
HTML `style="color: black"` is invisible on dark theme. Policy:
- **(a)** Transform SVG `fill="#000"` / HTML inline `color: black`
  to `Theme.of(context).colorScheme.onSurface` when in dark mode.
- **(b)** Trust the agent to generate theme-appropriate output;
  document it in the steward template.
- **(c)** Pass current theme as a sanitiser hint and let the
  renderer make the call.
Cheapest is (b); cleanest is (a). Pick before W2 lands.

**Q5 — Link tap routing.** W3 allowlists `<a href>` for `http(s):`,
`mailto:`, `termipod:`, `muxpod:`. Plan doesn't wire
`flutter_html`'s `onLinkTap` callback to either `url_launcher`
(for `http(s):`/`mailto:`) or the existing `DeepLinkService`
(for `termipod:`/`muxpod:`). Two-line decision but it must be
explicit — taps with no handler are silent UX bugs.

**Q6 — Max-height cap.** Plan clamps SVG/HTML `maxWidth` to the
parent constraint. Doesn't cap height. A 5000px-tall SVG would
dominate the transcript. Propose: cap at 1.5× viewport height;
overflow scrolls inside an `InteractiveViewer`-or-tap-to-expand.

**Q8 — Surface scope.** W1 wires `agent_feed.dart` and
`doc_viewer_screen.dart`. The **steward overlay chat** renders
raw `Text` widgets, not `MarkdownBody`. If the agent emits an
SVG fence in an overlay session, it lands as literal
`<svg>...</svg>` text. Either:
- **(a)** Overlay chat adopts the registry too (one extra wire-up
  in W1).
- **(b)** Plan explicitly excludes the overlay; steward template
  must avoid fences in overlay sessions (which the agent can't
  easily distinguish from non-overlay sessions).
Pick before W1's "files touched" list is final.

### Nice-to-have (not blocking)

**Q11 — APK split alignment.** The deferred voice-input plan
proposed a `full`/`lite` APK split. +250 KB from `flutter_svg` +
`flutter_html` is small enough to bundle in both flavors, but
worth a one-liner in the rollout to note it isn't a split-axis.

**Q14 — Tier 1 → Tier 2 escalation triggers.** Discussion §12
says "Tier 1 first; Tier 2 only if Tier 2's web-in-app feel
breaks the demo arc." Plan doesn't restate this as a closing
criterion. Without trigger language, W3 lands and the question
"is Tier 1 enough?" never gets answered. Recommend: append a
"Done = …" bullet that names the signals which would trigger a
Tier 2 follow-up wedge.

**Q15 — QA harness coverage.** The new
[`how-to/test-steward-lifecycle.md`](../how-to/test-steward-lifecycle.md)
exercises write verbs but doesn't render artifacts visually.
Add one scenario (e.g. *"steward, draw me a 3-node architecture
diagram of this project"*) so Tier 1 has an explicit regression
check post-merge.

**Q16 — Data-URI size cap.** W2's security constraints allow
`<image href="data:...">` for local images, no size threshold.
A 50 MB base64 blob could OOM the parser. Cap at e.g. 256 KB.

**Q17 — Rollout ordering.** Step 5 (update
`steward.general.v1.md` to advertise the fence affordances) must
wait for ALL of W1+W2+W3. Plan should call out: don't merge the
prompt update before the renderers, or the steward emits
unrenderable fences.

## Status

Open — kicked off 2026-05-10 from principal QA on the v1.0.466
prototype. No commits yet. Open-questions block added 2026-05-10
during pre-implementation review; awaiting principal sign-off
before W1 starts. Discussion at
[`../discussions/agent-driven-mobile-ui.md` §12](../discussions/agent-driven-mobile-ui.md).
