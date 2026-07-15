# Design Review: TermiPod Desktop (`desktop/`)

> **Type:** discussion
> **Status:** Open — for director review
> **Audience:** contributors weighing the desktop workbench's next moves
> **Last verified vs code:** v0.3.43
> **Freshness:** snapshot

**TL;DR.** An agent-authored design review of the desktop workbench —
an overall verdict plus four issue groups (security posture, broken
research-loop links, structural debt, strategic prioritization) and a
suggested sequence. A point-in-time snapshot (2026-07 sweep); copied
in for review, not yet acted on. Companion to
[research-app-product-landscape.md](research-app-product-landscape.md).

*Review date: July 2026. Scope: the design documents (ADR-050/051/053, the desktop-control-plane plan, the desktop-research-surface and research-material-data-model discussions) plus a full three-track code sweep — research surfaces, shell/state layer, and the Tauri Rust core (~23,300 LoC TypeScript, 58 Tauri commands). Companion: [research-app-product-landscape.md](research-app-product-landscape.md) (features of leading products worth borrowing/integrating).*

---

## Overall verdict

This is an unusually well-conceived prototype. The design documentation is better than most production products': the app is derived from *work types* (J1–J7) rather than screens, the two-halves split (control plane vs. research workbench) is the right frame, the build·embed·integrate·interop rule is the right discipline, and the data-ownership law (hub owns names, hosts own bytes) is consistently respected — the library, vault, and attachment designs all honor it. Implementation quality is also high: the PDF reader is production-grade, the dedup/merge logic in the library is thoughtful, and the vault crypto is sound. The code has essentially zero stray TODOs; deferrals are documented in file headers.

The issues split into four groups, ordered by urgency: **security posture** (real vulnerabilities, cheap to fix now), **broken loops in the research workflow** (the gaps that undercut the "one app" thesis), **structural debt** (god components and a leaking platform seam), and **strategic prioritization** (the moat features are the thinnest ones).

---

## 1. Security — fix before this becomes a daily driver 🔴

The app concentrates SSH private keys, hub bearer tokens, a vault, arbitrary-file access, and PTYs in one process, while rendering plenty of untrusted content (PDFs, scraped web metadata, agent-generated markdown, iframed websites). That combination makes the webview's trust boundary the whole ballgame, and right now it's effectively unguarded:

1. **`hub_request` / `hub_request_bytes` / `hub_sse_open` are an open proxy.** They accept arbitrary URL, method, headers, and body with no allowlist — any script in the webview can make requests to any internal host with a chosen bearer token (SSRF + token exfiltration). *Fix:* restrict targets to the active profile's `baseUrl` plus a static allowlist of the discovery API hosts (OpenAlex, Crossref, arXiv, PubMed, S2, Unpaywall, CORE). This is a small Rust-side change.
2. **`csp: null`, and file commands are unconfined.** `doc_read`/`doc_write`, `attachment_read`/`attachment_delete`, `workspace_list`, `pty_open`, and `local_agent_run` take arbitrary absolute paths/programs. With no CSP, one XSS anywhere (a malicious PDF annotation URL, a scraped abstract, agent output rendered as HTML) escalates to arbitrary read/write/exec as the user. *Fix:* set a strict CSP (the SPA is local, so this is tractable); confine file commands to registered roots (workspace folders, attachment roots, app-data) using the same canonicalize+`starts_with` guard already written for `storage_read` — the pattern exists, it's just not applied uniformly.
3. **Browser-build secret fallback** stores SSH keys/passwords/vault key in plaintext `localStorage` (`sec:` prefix). *Fix:* in the browser build, disable secret-dependent features (SSH, vault, voice key) rather than degrade the storage. A "desktop app only" notice already exists for the terminal — reuse the pattern.
4. **SSH accepts any host key** (`check_server_key → Ok(true)`). The README lists host-key pinning as future work; promote it — TOFU with a stored fingerprint is ~50 lines and removes a silent MITM on the exact path (breakglass) used when things are already wrong.
5. Minor: `Access-Control-Allow-Origin: *` on the `drawio://` handler; token strings transiting webview JS (acceptable for now, but the "token never in JS" claim in comments overstates — either fix the code or the comment).

The good news: the parts that are supposed to be careful *are* careful — no shell interpolation anywhere, bounded indexers, per-session SSH actors, sound vault crypto (X25519+HKDF+AES-GCM, Argon2id recovery, real zero-knowledge split). Two crypto notes worth acting on: the **Rust↔Dart vault interop is self-documented as UNVERIFIED** — write that cross-device test before anyone depends on recovery; and empty AAD on the GCM envelopes means no context binding — cheap to add (bind bundle version + purpose string).

## 2. The research loop has two broken links 🟠

The stated goal is "all research computer work in one app." The app's *reading* half is genuinely excellent, but the loop a researcher actually runs — *discover → read → annotate → think → write → cite → publish* — breaks in two places:

1. **There is no library→Author citation bridge.** The Author surface cannot cite the library at all; citation support is a hand-rolled APA/BibTeX copy-to-clipboard tab in ReadSurface. This is the single highest-value missing feature: it's the join between J1 and J2, and without it the "one app" claim fails exactly where a researcher feels it most (writing). *Suggestion:* adopt **CSL-JSON as the internal citation format** (the `Reference` shape is already ~90% of it), embed **citeproc-js** for styles, add an "insert citation" command in the Author editor (`@`-trigger or palette) that writes a stable key (`[@ref:...]` pandoc syntax), and add `.bib` export per document. Pandoc-compatible markdown keeps the deferred Quarto/Typst export path clean.
2. **Discovery is single-source with no cross-source merge.** A "Discover" panel that searches one API at a time and can't unify the same paper across OpenAlex/S2/Crossref is a per-source query tool, not a discovery layer. The strong-key dedup (`doi → arxivId → …`) already exists at import time — lift it to search time: fan out to 2–3 keyless sources concurrently, merge on strong keys, and prefer the richest record per field (S2 for TLDR, OpenAlex for topics, Unpaywall for OA PDF). The `SOURCES` registry makes this a contained change.

Two more workflow-level gaps, lower urgency:

3. **Library sync is manual, local-wins, non-timestamped.** ADR-053's whole point is agents curating the library — but the next manual sync will silently clobber whatever an agent wrote (the code acknowledges this). Before agents actually use `reference_update` in anger, add per-field timestamps (or a hub `version` counter + 409 like the vault already has) and a 3-way merge. Longer-term, the hub already streams SSE — a live library subscription would dissolve the "Sync" button entirely.
4. **Annotation anchoring is geometry-only** (page + PDF-point rects). Fine for byte-identical PDFs, silently wrong across re-typeset editions (arXiv v1→v2 is the common case!). *Suggestion:* store a W3C-style text-quote selector (exact + prefix/suffix) alongside the rects; render from geometry, but re-anchor from text when the geometry misses. Also: `Annotation.hubId/syncedAt` are dead fields — either wire annotation sync or delete them until it exists.

## 3. Structural debt — four patterns worth paying down 🟡

1. **The platform seam leaks everywhere.** `platform.ts` is the intended boundary, but `isTauri()` appears 63 times across 22 files and raw `invoke()` in 15. Every leak site is a browser-build bug waiting to happen and makes the "same frontend, two targets" claim fragile. *Suggestion:* a `services/` layer — one interface per capability (files, attachments, keychain, agentRunner, sshTransport…), with Tauri and browser (or "unsupported") implementations chosen once at startup. Mechanical refactor, big payoff.
2. **Two god components own the workbench.** `ReadSurface.tsx` (2,250 LoC) and `PdfCanvas.tsx` (1,767) each bundle 5–7 responsibilities. This will hurt the first time two people (or two agents) touch them concurrently. Extract `read/` and `pdf/` module folders; the internal sub-components are already visible in the code, so the seams exist.
3. **Persistence boilerplate is duplicated in ~7 stores** (same try/catch localStorage + monotonic-ID patterns), while `persist.ts` sits underused. One `createPersistedStore` helper (or zustand's `persist` middleware with a custom storage adapter) removes a whole class of drift. Same story for `AppShell`'s 8 overlay `useState` booleans → one overlay store, and the hardcoded palette command array → a registry that surfaces can contribute to (this also becomes the extensibility story).
4. **One ErrorBoundary, zero frontend tests.** The boundary wraps only the surface switch; a crash in chrome/overlays/companions blanks the app. And for 23k LoC there are no JS tests at all (only `vault.rs` has Rust tests). Don't aim for coverage — aim for the three things that silently corrupt data: library dedup/merge, sync reconciliation, and annotation geometry mapping. Those are pure functions; a vitest setup plus ~30 tests would cover the scary parts.

Also worth a mention: `app.css` at 5,951 lines in one file, and `hub/client.ts` as a ~100-method facade — both fine at prototype scale, both worth splitting on next touch (the client already documents itself as "facade over subclients"; make that literal).

## 4. Strategic observations — where to steer the design 🔵

1. **The moat features are the thinnest surfaces.** The docs correctly identify the multi-run comparison wall (J5) as "the headline BUILD — no embeddable OSS component exists" and decision-capture-with-provenance (J6) as a differentiator. Yet J5 is a 200-line first cut and J6 is a 109-line interim, while J1 (reading — where Zotero already exists) got 4,000+ lines of excellence. That's the natural gravity of building for oneself (reading feels urgent daily), but it inverts the strategy: **the reader competes with mature free tools; the compare wall and run-linked decision log compete with nothing.** Cap further reader investment and put the next big block into J5 (config-diff panel, live multi-host curves, pin-to-canvas) and J6 (decisions that link to the runs/references that justify them — the research-material element model gives the schema).
2. **The shell contradicts the vision's core claim.** ADR-050 and the plan both argue desktop's entire reason to exist is *simultaneity* — yet the shell is a modal one-job-at-a-time switch (splits exist only inside surfaces). Paper-beside-draft, transcript-beside-compare-wall, canvas-beside-reader are all impossible today. Before adding more surfaces, add a minimal shell-level split layer: even just "pin one secondary surface to the right half" (not a full docking framework) would deliver the J1↔J2 and J5↔J7 pairings that motivated the whole project. The `JOBS` registry + `workbench` store give a clean place to model it.
3. **Decide what "one app" means before it decides itself.** The app now contains a fleet cockpit, library manager, PDF/EPUB reader, markdown editor, diagram editor, spatial canvas, web browser, terminal, SFTP client, voice input, and a password vault. The build·embed·integrate·interop rule is the defense against this becoming an unmaintainable everything-app — but it needs enforcing per addition ("would a Zotero live-sync beat re-implementing curation?", "is the in-app browser earning its X-Frame-Options caveats?"). The differentiator worth protecting is the **agent-fleet + provenance loop**; everything else should be judged by whether it feeds that loop.
4. **The companion context deserves to be a protocol.** `AgentCompanion` injecting per-surface context (paper + notes in Read, draft in Author) with insert-back is the germ of the app's best idea — the agent as a lab member present in every surface. Right now each surface hand-rolls it. Define a small `SurfaceContext` contract (what am I looking at → structured context; what can you insert → typed targets) that every surface implements — Canvas, Compare, and Record would gain the companion for free, and future MCP-style exposure of "what the director is looking at" to hub agents falls out of the same interface.

## Suggested sequence

1. **Now (days):** proxy allowlist, CSP, file-command root confinement, browser-build secret gating, SSH TOFU pinning, vault interop test.
2. **Next (1–2 weeks):** citation bridge (CSL-JSON + citeproc + insert-command + bib export); discovery fan-out/merge; the `services/` platform layer.
3. **Then:** shell-level split pane; sync 3-way merge; ReadSurface/PdfCanvas decomposition + the ~30 data-integrity tests.
4. **Strategic block:** deepen J5/J6 into the moat surfaces; formalize the SurfaceContext protocol.

**The short version:** the thinking is excellent and ahead of the implementation in exactly one place (simultaneity), the implementation is ahead of the thinking in one place (reader polish vs. moat surfaces), and the security posture is the only thing that's urgent. A genuinely promising foundation for the "one app for research" goal.

---

## Appendix — implementation survey snapshots (July 2026)

**Scale:** ~23,346 LoC TS/TSX; 58 Tauri commands across 12 Rust modules; `app.css` 5,951 LoC; i18n 1,828 LoC (en+zh, ~850 keys each); hub client ~100 methods / 23 sections.

**Shell:** 7 jobs (fleet/read/author/debug/canvas/compare/record) via a `JOBS` registry → ActivityBar; fleet is the only 3-region layout (Navigator | Focus | AttentionDock); 13 zustand stores + TanStack Query (clean server/UI state division; query cache persisted, 1-week TTL); multi-profile hub connections with per-profile keychain tokens; ⌘K palette (hardcoded, 13 commands); one ErrorBoundary; a11y light.

**Research half:** ReadSurface (2,250) = library + discovery + reader, no stubs; PdfCanvas (1,767) = pdf.js + full Zotero-style annotation toolbat (highlight/underline/note/image/ink), geometry in PDF user-space points; EPUB read-only; Author = CodeMirror markdown split-preview + save-to-disk + agent assist (no citations); Canvas = zettelkasten board wired to library; Compare/Record = thin first cuts; discovery = 6 sources + Unpaywall/scrape enrichment, single-source-at-a-time; Zotero import = sql.js in-webview one-shot parse (privacy-preserving); library dedup = hubId→externalId→doi→arxivId→title+year; hub sync = manual two-phase local-wins (ADR-053 `reference_items`).

**Rust core:** open hub proxy (no allowlist) 🔴; unconfined file/pty/agent commands 🔴; `csp: null` 🔴; SSH no host-key check 🔴; browser plaintext-secret fallback 🔴; vault crypto sound (X25519+HKDF+AES-GCM; Argon2id 19MiB/t=2; empty AAD; Rust↔Dart interop untested) 🟡; no shell interpolation anywhere ✅; traversal guards on storage/drawio ✅; bounded indexers ✅; pty/ssh actor separation clean ✅; voice = DashScope realtime ASR via Rust WS (key in keychain) ✅.
