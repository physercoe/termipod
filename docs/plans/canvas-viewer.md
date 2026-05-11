---
name: Canvas viewer
description: Sandboxed WebView for `canvas-app` artifacts — agent writes HTML/JS/CSS files via tool-call file writes, then bundles them into a canvas-app artifact whose body is a typed multi-file manifest (Artifact File Manifest V1, shared with code-bundle). Read-only interaction (user clicks/plays inside the canvas); agent edits via new artifact version. Unblocks brainstorm + experiment-dash heroes.
---

# Canvas viewer

> **Type:** plan
> **Status:** Proposed (2026-05-11)
> **Audience:** principal · contributors
> **Last verified vs code:** v1.0.497

**TL;DR.** `canvas-app` is the only one of the 11 closed-set
artifact kinds without a viewer ([artifact-type-registry](artifact-type-registry.md)
W1, v1.0.489-alpha). This plan ships it as a sandboxed
`webview_flutter` that renders a multi-file HTML bundle the agent
wrote via standard file-write tool calls. The interaction model is
**read-only for the agent** — the user clicks/plays/scrolls inside
the canvas, but no state flows back as agent input automatically.
When the user asks the agent to change the canvas, the agent emits
a new artifact version (new files) and the mobile viewer
re-downloads.

This plan **elevates the multi-file artifact body to a first-class
typed concept** — the *Artifact File Manifest* (AFM-V1) — because
it's the second user of the shape (code-bundle was first, v1.0.494)
and locking the schema now prevents two viewers from drifting on
parsing rules.

## Goal

After this plan ships:

- `canvas-app` artifacts have a mobile viewer behind the existing
  `_ArtifactViewerLauncher` dispatch in `artifacts_screen.dart`.
- An **Artifact File Manifest V1** (AFM-V1) schema is defined and
  shared between code-bundle (existing) and canvas-app (new).
  Parsing lives in a single helper consumed by both viewers; the
  hub seeder produces the same shape.
- The canvas viewer **merges** the manifest into a single
  self-contained HTML document before handing it to
  `WebViewController.loadHtmlString`. Sub-files become inline
  `<script>`/`<style>` blocks or `data:` URIs.
- The WebView is **network-restricted, not script-restricted**:
  navigation delegate rejects every URL except `about:blank` and a
  small CDN allowlist for common library hosts. No CSP injection.
- The artifact-version refresh path piggybacks on the existing
  `listArtifactsCached` poll — when the agent emits new files, the
  mobile sees a new sha and the viewer reloads.

## Non-goals

- **Bidirectional bridge.** User clicks inside the canvas do NOT
  round-trip to agent input. Canvas state is WebView-local; if
  the user wants the agent to know, they tell the agent in chat.
- **postMessage protocol.** No JS↔Dart channel. Deferred to a
  future plan (`canvas-viewer-interactive.md` if product asks).
- **CSP enforcement.** Agent-authored canvases are trusted (same
  model as Claude Artifacts).
- **In-canvas editing.** The viewer is display-only; authoring is
  a separate wedge if it materialises.
- **Hero integration.** This plan ships the viewer + launcher.
  Embedding the canvas inline in `idea_conversation` /
  `experiment_dash` overview widgets is a Wave 3 wedge under
  chassis-followup hero-consolidation.

## Artifact File Manifest V1 (AFM-V1) — locked schema

The body of a `code-bundle` or `canvas-app` artifact is JSON
matching this shape:

```jsonc
{
  "version": 1,
  "entry": "index.html",          // optional; canvas-app only
  "files": [
    {
      "path": "index.html",        // relative path, no leading "/"
      "content": "<!doctype html>…",
      "mime": "text/html"          // optional; inferred from path if absent
    },
    {
      "path": "chart.js",
      "content": "const svg = …",
      "mime": "text/javascript"
    },
    {
      "path": "style.css",
      "content": ".container { … }",
      "mime": "text/css"
    }
  ]
}
```

**Field rules:**

- `version` (required, integer = `1`) — schema version. Future
  schema changes get `version: 2`; parsers reject unknown
  versions with a clear error.
- `entry` (optional, string) — relative path of the canvas
  entry HTML. Resolution order: explicit `entry` field →
  file named `index.html` → first file with `.html`/`.htm`
  extension in declaration order → error. Ignored by
  code-bundle viewers (no entry concept there).
- `files` (required, non-empty array) — at least one entry.
  Each entry has:
  - `path` (required, string) — relative POSIX path. No
    leading `/`, no `..` segments (rejected by parser).
  - `content` (required, string) — UTF-8 text. Binary assets
    (PNG, audio) must be inlined as `data:` URIs inside HTML/
    CSS by the agent. AFM-V1 deliberately does NOT carry
    `binary: true` — keeps the agent's mental model simple
    ("everything is text").
  - `mime` (optional, string) — IANA MIME type. If absent,
    derived from the path extension via the existing
    `languageForPath` map (`code_bundle_viewer.dart`)
    extended for `.html`/`.css`/`.svg`.

**Compat with v1.0.494 code-bundle:** existing seed code emits
`{files: [...]}` without `version` or `entry`. The parser treats
a missing `version` as `1` for forward-compat (so old artifacts
still render), and `entry` is canvas-app-only — code-bundle
ignores it. No migration needed; future agent prompts produce
the explicit `version: 1` form.

**Schema name:** "Artifact File Manifest" — qualified because
"manifest" is already taken in the codebase for ADR-016's
**operation-scope manifest** (`roles.yaml`). The two concepts
never appear in the same context, but the qualification keeps
search legible.

## Where the manifest pattern lives

Comprehensive audit as of v1.0.497:

| Site | Pattern | Status after this plan |
|---|---|---|
| `lib/widgets/artifact_viewers/code_bundle_viewer.dart` — `parseCodeBundle` | Implicit `{files: [{path, content}]}` (v1.0.494, W5) | **Refactor to consume shared parser**; behavior unchanged. |
| `hub/internal/server/seed_demo_lifecycle.go` — `demoRunBundle()` + `seedCodeBundleArtifact()` | Same implicit shape, served as the W5 seed | **Refactor**: add explicit `version: 1` field to emitted JSON; payload otherwise identical. |
| `lib/widgets/artifact_viewers/canvas_viewer.dart` — *new* | AFM-V1 with explicit `version` + `entry` | New consumer of the shared parser. |
| `hub/internal/server/seed_demo_lifecycle.go` — *new* `seedCanvasArtifact()` | AFM-V1 demo bundle | New producer. |

**No other places.** Everything else in the codebase named
"manifest" refers to ADR-016's operation-scope governance
(`hub/internal/server/mcp_authority_roles.go`,
`hub/config/roles.yaml`), which has nothing to do with artifact
bodies.

**Candidates that *could* adopt AFM later (out of scope here):**

- A future `notebook` artifact kind (Jupyter `.ipynb` is itself
  cell-of-files-of-cells JSON — AFM is a poor fit for that
  internal shape but a fine wrapper if we ever bundle
  notebook + sidecar assets).
- A future "report" prose-document variant with embedded
  figures (today `prose-document` is single-blob markdown; a
  multi-asset version could ride AFM).
- Project template export/import as a single bundle (today
  templates are YAML structures in the hub, not artifacts).

None of those are MVP-bound; flagging only so the AFM-V1 schema
gets versioned thoughtfully.

## Wedges

### W1 — Extract shared AFM parser

**Scope.** Extract the manifest-parsing primitive from
`code_bundle_viewer.dart` into a shared module before adding a
second user. Both viewers consume the same parser; both seed
producers emit the same schema.

**Files touched:**
- `lib/services/artifact_manifest/artifact_manifest.dart` —
  *new*. Defines:
  - `class ArtifactFileManifest { int version; String? entry;
     List<ArtifactFile> files; }`
  - `class ArtifactFile { String path; String content;
     String mime; }`  *(mime resolved at parse time —
     never null after parsing)*
  - `ArtifactFileManifest? parseArtifactFileManifest(dynamic
     decoded)` — accepts the three shapes the W5 viewer
     already handles (`{files: [...]}` map / flat list /
     single `{path, content}`) plus the new AFM-V1
     `{version, entry?, files}` form. Returns null on
     unrecognisable input.
- `lib/widgets/artifact_viewers/code_bundle_viewer.dart` —
  delete the inline parser; wire to the shared one. Tests
  unchanged.
- `hub/internal/server/artifact_manifest.go` — *new* Go
  mirror with `ArtifactFileManifestV1` struct + a small
  helper for seed code. Also enforces the **10 MB body cap**
  (Q12 locked) when artifacts of kind `code-bundle` or
  `canvas-app` are created via `handleCreateArtifact` —
  applies retroactively to code-bundle since no production
  bundle today comes close. Reject with `400` +
  `"<kind> body exceeds 10 MB cap"`.

**LOC estimate:** ~150 mobile (~80 net after deletion) + ~80 hub.

### W2 — Canvas viewer + launcher

**Scope.** New `lib/widgets/artifact_viewers/canvas_viewer.dart`
mirroring the structure of `code_bundle_viewer.dart`:

- `ArtifactCanvasViewer` (Riverpod consumer) resolves
  `blob:sha256/<sha>` via `HubClient.downloadBlobCached`, parses
  using the W1 shared `parseArtifactFileManifest`.
- `inlineCanvasBundle(ArtifactFileManifest)` merges the manifest
  into a single self-contained HTML document. Algorithm:
  1. Pick the entry per AFM-V1 resolution order (above).
  2. For each `<script src="X">` in the entry where `X` resolves
     to a manifest file: replace with `<script>…inlined…</script>`.
  3. For each `<link rel="stylesheet" href="X">` similarly:
     replace with `<style>…</style>`.
  4. For each `<img src="X">` where `X` is a manifest file:
     replace with `data:<mime>;base64,…`.
  5. CDN-allowlisted external URLs (see W4) pass through
     untouched; everything else is silently kept but won't load
     thanks to the nav delegate.
- `ArtifactCanvasViewerScreen` fullscreen route uses
  `WebViewController.loadHtmlString(html, baseUrl: 'about:blank')`.
  JS enabled; navigation delegate (W4) gates outbound requests.
- `_ArtifactViewerLauncher` gains a `canvasApp` branch.
- Filter pill in `artifacts_screen.dart` gains `canvas-app`.
- Manual refresh button on AppBar — re-fetches the blob and
  re-renders. Auto-detection of new artifact versions is a
  follow-up if testers complain.

**LOC estimate:** ~300 mobile + 1 dep (`webview_flutter: ^4.x`,
~2 MB APK cost).

### W3 — Hub seed: demo canvas artifact

**Scope.** Seed a canvas-app artifact on the lifecycle demo so
the tester sees the viewer in action without an agent emitting
one. Three-file AFM-V1 bundle (`index.html` + `chart.js` +
`style.css`) that renders an interactive SVG line chart of the
same synthetic eval data the metric-chart artifact uses.
Attached to the ratified experiment-results deliverable in both
demo projects (mirrors the W5 code-bundle seed pattern).

**Files touched:**
- `hub/internal/server/seed_demo_lifecycle.go` —
  `demoCanvasBundle()` + `seedCanvasArtifact()` helper.

**LOC estimate:** ~120 hub.

### W4 — Navigation delegate + CDN allowlist

**Scope.** The WebView's `setNavigationDelegate` blocks any URL
except:

- `about:blank` — the loaded HTML's own context.
- `data:` URIs — inlined images / fonts.
- A small CDN allowlist for common library hosts:
  `cdn.jsdelivr.net`, `unpkg.com`, `cdnjs.cloudflare.com`,
  `esm.sh`. HTTPS only.

Blocking happens at navigation request time
(`NavigationDecision.prevent`). Agents that try to fetch from
non-allowlisted hosts (e.g., a tracker beacon) silently fail,
matching the "trust agent but limit blast radius" stance.

**Implementation note:** the allowlist lives in
`canvas_viewer.dart` as a `const Set<String>`; revisiting
requires a code change, not a runtime toggle. That's
intentional — adding a new CDN host is a deliberate decision,
not a settings knob.

**LOC estimate:** ~30 mobile (folded into W2 commit).

## Open questions

All schema + behavior decisions are locked. The remaining items
are either explicitly deferred or graceful-failure paths.

**Locked decisions:**

- **Q1 (locked) — Entry file resolution.** Explicit `entry` field
  → `index.html` fallback → first `.html`/`.htm` in declaration
  order → error. See AFM-V1 schema section.
- **Q4 (locked) — Manifest body shape.** String-only `content`;
  agent inlines binary assets as `data:` URIs. No `binary: true`
  flag. See AFM-V1 schema.
- **Q8 (locked) — CDN allowlist.** `cdn.jsdelivr.net`, `unpkg.com`,
  `cdnjs.cloudflare.com`, `esm.sh`. HTTPS only. See W4.
- **Q10 (locked) — Schema versioning.** `version: 1` field;
  missing version treated as `1` for v1.0.494 code-bundle
  compat; unknown versions rejected with a clear error.
- **Q11 (locked) — CSS `url(...)` inlining: option (a).** The
  inliner does NOT rewrite `url(...)` references inside
  `<style>` blocks. Agents must inline image references as
  `url(data:...)` when authoring CSS. Documented as a known
  limit; revisit if real agent output trips on it.
- **Q12 (locked) — Bundle size cap: 10 MB.** Hub validator
  enforces a 10 MB ceiling on canvas-app (and code-bundle —
  retroactively) artifact bodies. Generous for SVG/D3 work,
  well under PDF's 32 MB, fits the agent_events payload
  envelope without strain. Implemented in the W1 hub mirror
  alongside the shared parser.
- **Q13 (locked) — URL→manifest-path resolution rules.** When
  the inliner sees `<script src="X">` / `<link href="X">` /
  `<img src="X">`:
  - Strip a leading `./` from `X`.
  - Compare against `files[].path` exactly (case-sensitive,
    POSIX-style).
  - Reject (no inlining; leave the tag as-is) anything with
    `..` segments, leading `/`, or a scheme (`http:`,
    `https:`, `data:`). Cross-origin references pass through
    to the navigation delegate which then enforces the W4
    CDN allowlist.
  Same rule applies to `<link href>` and `<img src>`. Documented
  in `canvas_viewer.dart`'s inliner so future contributors don't
  reinvent the matcher.

**Deferred / accepted:**

- **Q5 (deferred) — Auto-refresh on artifact version change.**
  MVP ships a manual refresh button. If testers reflexively
  back-out-and-tap-in instead, the existing `listArtifactsCached`
  poll already detects new shas; wiring the viewer to listen is
  ~10 LOC.
- **Q6 (accepted) — APK weight.** `webview_flutter` adds ~2 MB
  on Android. Re-examine under artifact-type-registry Q10 if
  APK split lands.
- **Q7 (graceful failure) — Android WebView availability.**
  Some old Android devices lack a current WebView system
  component. Detection: catch initialisation errors and render
  the existing "Cannot render canvas" card; don't try to
  install / prompt-to-update from inside the app.

## Total budget

- **W1 shared parser + 10 MB cap**: ~80 LOC mobile net + ~80 LOC hub
- **W2 canvas viewer**: ~300 LOC mobile + 1 dep
- **W3 seed**: ~120 LOC hub
- **W4 nav delegate**: folded into W2

Total ~500 LOC mobile + ~200 LOC hub + 1 dep. Lands in 1-2
commits — W1 standalone (refactor; pure mechanical), then
W2+W3+W4 together (the actual feature).

## Why this is more urgent than Tier 2 inline html/code-fence

Hero integration: `idea_conversation` (brainstorming) and
`experiment_dash` (interactive plots) both want to surface an
agent-authored canvas as a primary visual. Tier 1 markdown fences
already render code as syntax-highlighted text — useful for code
review, useless for "play with the chart, ask the agent to
tweak it." Until the canvas viewer ships, those heroes either
fall back to static images (loses interactivity) or nothing
(loses the visual). Tier 2 inline HTML in the transcript is a
related but distinct primitive that mostly serves *short*
in-line widgets; the heroes need fullscreen + launchable
canvases this viewer provides.

## Rollout

1. **W1 shared parser refactor** → one commit, no version bump
   (pure refactor; behavior preserved).
2. **W2 + W3 + W4** in a single commit → alpha tag bump
   (v1.0.498-alpha estimated).
3. **Hero embedding** (Wave 3 wedge under
   `chassis-followup-ordering`) is the next step but explicitly
   not in this plan.

## References

- `docs/plans/artifact-type-registry.md` — W1 set `canvas-app` as
  one of the 11 MVP kinds; this plan fills its viewer slot.
- **Artifact rendering tiers** (framing): Tier 1 = markdown
  code-fence (already shipped by `markdown_builders.dart`),
  Tier 2 = WebView artifact (this plan), Tier 3 = server-driven
  UI (post-MVP). Default progression 1→2→3.
- `lib/widgets/artifact_viewers/code_bundle_viewer.dart` —
  the existing manifest consumer this plan refactors to share.
- `hub/internal/server/seed_demo_lifecycle.go` —
  `seedCitationArtifact` + `seedCodeBundleArtifact` set the
  precedent for `seedCanvasArtifact`.
- `hub/internal/server/mcp_authority_roles.go` — name-clash
  reference: the codebase's existing "manifest" is ADR-016
  governance, unrelated to AFM-V1.
