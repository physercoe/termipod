# Read — real browser tabs + PDF-to-attachment

> **Type:** plan
> **Status:** In progress — **W1 + W2 core shipped** (2026-07-22); W2b deferred
> **Audience:** contributors · principal
> **Last verified vs code:** desktop 2026.722.818 (on main)
>
> **Shipped (2026-07-22).** **W1** (real `<webview>` browser tab): new
> `electron/src/webtab.ts` (isolated `persist:webtab` partition, stock-Chrome UA,
> per-partition proxy via `webtab_set_proxy`, permission deny-all-but-fullscreen,
> `will-attach-webview` guest lockdown, popup + http(s)-only navigation policy,
> save-dialog download default); `webviewTag: true` on the main window;
> `frame_check`/`frameRefused`/`frameCheck` deleted from both processes;
> `BrowserView.tsx` rewritten to drive the guest's real history + start-state +
> `did-fail-load` error pane + Ctrl/Cmd+L; Read tab-strip "+" and header "Open
> link" entry points (`openWebTab('')`); tab re-title from `page-title-updated`;
> a `webtab` proxy connection + a Settings "Clear web-tab browsing data" button
> (`webtab_clear_data`). **W2 core** (metadata-driven PDF download): pure,
> unit-tested `electron/src/ipc/download.ts` (`downloadPdfBytes` — content-type
> guard, 200 MB cap, filename resolution) behind `attachment_download`;
> `Attachment.srcUrl?`; `downloadPdfAsAttachment` helper; the three affordances
> (Inspector Info + Read tabs, Discover "Add + PDF"). **Two deliberate deviations
> from the draft below:** (1) progress reuses the sibling `sync:progress` /
> `invokeWithProgress` convention, not a bespoke `attachment-download-progress`
> event; (2) a web tab is mounted only while active (matching the existing PDF/note
> tabs) — cross-tab-switch in-memory state (scroll/forms) is not yet preserved;
> the `persist:` partition still keeps cookies/logins across restarts. Native
> menus, real publisher logins, and airplane-mode are **device-verify**.
> **Deferred: W2b** (§1.7 — a PDF link clicked *inside* a web tab offering
> attach-to-reference); the plan's pre-W2b fallback (a save dialog) is in place.
>
> Feeds the J1 Read surface ([desktop-workbench-jobs.md](desktop-workbench-jobs.md));
> relates to [reference-library-and-reading.md](../discussions/reference-library-and-reading.md)
> (the library/discovery patterns) and ADR-055 (the Electron shell that makes
> both halves possible).

**TL;DR.** Two independently shippable wedges. **W1** replaces the Read
surface's in-app browser tab — today a sandboxed `<iframe>` that almost no
useful site (arXiv, publishers, GitHub, Scholar) permits — with a **real
embedded browser** (Electron `<webview>` guest in an isolated, persistent
session): top-level-like navigation that `X-Frame-Options`/`frame-ancestors`
cannot refuse, real history, cookies that survive restarts, and a hardened
window-open/permission posture — plus, for the first time, a **user-facing
"open a link" entry point** (tab-strip "+" and a header button opening an
empty tab with a focused address bar; today web tabs only spawn from
reference links). The `frame_check` preflight + "refused" panel are deleted
(workaround paydown, in the M4 spirit). **W2** adds a
**Download PDF → managed attachment** affordance wherever a reference or
discovery result carries a `pdfUrl`: one new main-process command
(`attachment_download`, proxy-aware via the existing `proxyFetch`) streams the
PDF into the managed storage layout and registers it on the reference, so
"seen in Discover → in my library → open in the reader" is one click.

---

## 0. Problem

- `surfaces/BrowserView.tsx` (#322) renders external pages in an `<iframe
  sandbox referrerPolicy="no-referrer">`. Every navigation preflights
  `frame_check` (a main-process probe of `X-Frame-Options` / CSP
  `frame-ancestors`) and a refusal renders an "open in system browser" escape
  hatch. In practice nearly every site the reading workflow needs — arxiv.org,
  publisher landing pages, semanticscholar.org, github.com, Google Scholar —
  refuses framing, so the in-app tab is a bounce page. Sites that *do* frame
  still limp: the sandbox attribute, a cookie-less ephemeral context, and
  `no-referrer` break logins, paywalled access, and some CDNs. The director's
  requirement ("links open in a dedicated tab inside the app") is effectively
  unmet.
- There is also **no way to open a web tab that doesn't start from a
  reference**: web tabs spawn only through `openWebTab(url)` at link call
  sites (`OpenLinkContext`), the tab strip renders only when a tab already
  exists and has no "+", and the address bar lives *inside* an already-open
  tab. A user who just wants to browse to a site has no entry point at all.
- Discovery results (Semantic Scholar / OpenAlex / arXiv / PubMed / Crossref,
  plus the Unpaywall backfill) and scraped references carry an open-access
  `pdfUrl`, but the app only *displays* it (a passive "PDF" pill on the
  discover card; a mono string in the inspector's Read tab). Getting the PDF
  into the library today means: open the link, download via the OS browser,
  then "Add attachment" and re-pick the file from disk. The machinery to do
  this in one step already exists piecemeal — `activeAttachmentRoot()`,
  `attachment_write_bytes`/`intoKeyDir` (Zotero `<key>/<file>` layout),
  `addAttachment`, `proxyFetch` — nothing connects them.

Non-goals: no change to the pdf.js/epub.js *reading* pipeline (house style,
ADR-055 §7); no hub/mobile involvement (references stay device-local/synced by
the existing vault flow); no general-purpose browser ambitions (no omnibox
search, extensions, multi-profile).

## 1. Decisions

1. **Embed = `<webview>` tag, not `WebContentsView`, not a child window.**
   The candidates:
   - *Child `BrowserWindow`* (`open_browser_window`, exists): real browser but
     not "a tab inside the app" — fails the requirement.
   - *`WebContentsView`*: Electron's recommended embedder, but it composites
     **above** the renderer DOM. The Read surface is dense with overlays —
     context menus, modals, toasts, the AttentionDock, drag-resized panels —
     and every one would need a hide/show + bounds-sync IPC protocol with the
     main process. That is a standing integration tax and a recurring-bug
     generator (each new overlay must remember the browser exists).
   - *`<webview>`*: an OOPIF-based guest that participates in DOM layout and
     z-order — overlays, panel resizes, and tab switching just work; the
     element carries the navigation API (`goBack`/`goForward`/`reload`/
     `loadURL`, `did-navigate`/`page-title-updated` events). Electron's docs
     discourage it (legacy quirks), but it is supported, ships in Electron 43,
     and for a read-mostly preview browser the risk is acceptable.

   **Choose `<webview>`.** Containment is the insurance: all renderer-side
   usage stays inside `BrowserView.tsx`, and all main-side policy in one new
   `electron/src/webtab.ts` — if webview flakiness ever bites, a
   `WebContentsView` swap touches those two files, not the app. Crucially,
   `X-Frame-Options`/`frame-ancestors` do **not** apply to a webview guest
   (it is a top-level frame, not an embedded one) — arXiv et al. load.

2. **Isolated persistent session: `partition="persist:webtab"`.** One shared
   partition for all web tabs:
   - *Isolation*: the `app://`/`drawio://` scheme handlers and the hub-CORS
     bearer-injection are installed on `session.defaultSession` **only**
     (`main.ts` whenReady) — web content in the webtab partition can reach
     none of them, by construction. No preload is attached to guests, so
     `__ELECTRON_BRIDGE__` never exists there.
   - *Persistence*: a `persist:` partition keeps cookies/localStorage across
     restarts — publisher logins and Cloudflare clearances survive, which is
     half of "useful sites actually work".
   - *Proxy*: apply the app's proxy config to the webtab session
     (`ses.setProxy`, same semantics as the updater: explicit proxy when
     configured, `{mode:'system'}` otherwise).
   - *UA*: strip the `Electron/x.y` (and app-name) tokens from the partition's
     user agent — several sites (Scholar, Cloudflare) degrade or block
     non-Chrome UAs; the remaining string is the stock Chrome UA Electron
     derives from.

3. **Main-side hardening in `webtab.ts`** (all enforced in the main process,
   the authority — not in renderer markup):
   - `webviewTag: true` on the main window's webPreferences (currently off);
     `app.on('web-contents-created')` + `will-attach-webview` **strip any
     `preload`, force `nodeIntegration:false`/`contextIsolation:true`, and
     reject any partition other than `persist:webtab`** — a compromised
     renderer cannot mint a privileged guest.
   - Guest `setWindowOpenHandler`: deny every popup window; a safe
     http(s) `target=_blank` becomes an in-tab navigation of the same guest
     (reading flow stays in the app), anything else goes to
     `shell.openExternal` via the existing `isSafeExternal` guard.
   - Guest `will-navigate`: allowlist `http:`/`https:` only (no `file:`,
     no custom schemes).
   - `setPermissionRequestHandler` on the webtab session: deny by default
     (camera/mic/geolocation/notifications); allow only `fullscreen`.
   - Downloads: `ses.on('will-download')` is the single chokepoint (W2 wires
     it; until then, default to a save dialog into the OS Downloads dir).

4. **Navigation chrome switches from a synthetic stack to the guest's real
   history.** `BrowserView.tsx` keeps its bar (back/forward/reload/address/
   open-external) but the buttons drive `webview.goBack()` etc., enabled from
   `did-navigate`-time `canGoBack()`/`canGoForward()`; the address bar reflects
   `did-navigate`/`did-navigate-in-page`; `page-title-updated` feeds the Read
   tab strip title (today it's frozen at `hostOf(url)`). The `frame_check`
   preflight, the `refused` state, and the "refused" panel are **deleted** —
   renderer helper (`platform.ts frameCheck`), main command + `frameRefused`
   prober, and the (#322) strings. `open_browser_window` and its toolbar
   button stay for now (cheap, occasionally right for side-by-side reading);
   revisit after W1 beds in (§6).
   Load failures (`did-fail-load`: DNS, offline, TLS) render an in-pane error
   with retry + open-external — the replacement for the refused panel, now for
   *real* failures only.

   **New-tab affordance** — a web tab must be openable *without* a reference
   link. Two entry points, both calling the existing `openWebTab` with an
   empty URL:
   - a **"+" button at the end of the Read tab strip** (the strip keeps its
     render-only-when-tabs-exist behaviour; the "+" rides along), and
   - an **"Open link" (globe) button in the Read surface header** actions —
     the entry point when *no* tab is open yet, sitting beside the existing
     WebDAV/assistant buttons.

   An empty-URL web tab renders `BrowserView`'s **start state**: an
   autofocused, centered address input (the existing `normalizeUrl` already
   turns `arxiv.org/...` into `https://…`), an empty pane, and the tab titled
   "New tab" (i18n) until the first `page-title-updated`. `openWebTab` today
   titles tabs `hostOf(url)` — hostless empty URLs take the placeholder
   instead. When a web tab is active, **Ctrl/Cmd+L** focuses its address bar
   (the one browser shortcut worth stealing; it collides with nothing in the
   app's map).

5. **W2 downloads write through the existing managed-attachment layout — no
   new storage concept.** One new main command in `ipc/storage.ts`:

   ```
   attachment_download { root, url, filename?, proxy } → AddedAttachment
   ```

   `proxyFetch(url, …, proxy)` (already proxy-correct and
   redirect-following) → verify HTTP 2xx → stream into `intoKeyDir(root,
   file)` exactly like `attachment_write_bytes` → return the same
   `AddedAttachment` shape. Details:
   - *Filename*: `Content-Disposition` filename → URL path basename →
     `<arxivId | doi-slug | title-slug>.pdf`; sanitized by the existing
     `path.basename` guard in `intoKeyDir`'s caller.
   - *Type check*: accept `application/pdf` and `application/octet-stream`
     (some OA hosts mislabel); reject `text/html` with a typed error ("landing
     page, not a PDF" — the common paywall failure) so the UI can say why.
   - *Cap*: 200 MB hard cap (2× the sync cap; PDFs of scanned books exist),
     enforced on `Content-Length` when present and while streaming when not.
   - *Progress*: emit `attachment-download-progress {url, done, total}` ticks
     (the `sftp-progress` pattern) — most PDFs are seconds, but 100 MB over a
     proxy is not.
   - *Proxy conn*: the renderer passes `proxyForConnection('attachments')`.

6. **The affordance appears in three places, all through one renderer helper.**
   `state/attachments.ts` gains `downloadPdfAsAttachment(refId, url)`:
   resolve the root exactly as `pickAndCopyAttachment` does
   (`activeAttachmentRoot()` with the `resolveDefault()` retry), invoke
   `attachment_download`, then `addAttachment(refId, {source:'managed', key,
   path, file, contentType, srcUrl})`. UI:
   1. **Inspector · Info tab** — attachments header row: when `ref.pdfUrl` is
      set and no attachment already records that `srcUrl`, a **Download PDF**
      button beside "Add" (same `attBusy`/`attErr` plumbing; success toast).
   2. **Inspector · Read tab** — the passive `pdfUrl` mono row gains the same
      button when the item has no attachment (this is where "I want to read
      it" actually happens; on success, open the reader tab directly).
   3. **Discover card** — the passive "PDF" pill becomes **Add + PDF**:
      `importPaper` then `downloadPdfAsAttachment` in one flow (per-card busy
      state; a download failure leaves the imported item + an error toast —
      import must not roll back).

   *Idempotence*: `Attachment` gains an optional `srcUrl?: string` (additive —
   persisted blobs unaffected, mirroring the `spec?` precedent). A matching
   `srcUrl` renders the button as an inert "Downloaded ✓"; re-download after
   deletion works because deletion removes the attachment row.

7. **Browser-tab downloads join the same flow (W2b, small).** The
   `will-download` chokepoint emits `webtab:download {url, filename, mime}` to
   the renderer; the Read surface answers with a chooser: **attach to the
   selected reference** (when one is selected — invokes the same
   `attachment_download` on the URL, cancelling the Electron download item) or
   **save to disk** (accept with a save dialog). No selected reference →
   straight to the save dialog. This makes "click the PDF link on the arXiv
   page you're reading" land in the library too, not just the metadata-driven
   buttons.

## 2. W1 — the real browser tab

| Piece | Change |
|---|---|
| `electron/src/webtab.ts` (new) | session setup (partition, UA, proxy, permissions), `web-contents-created`/`will-attach-webview` enforcement, window-open + will-navigate policy, `will-download` → save-dialog default. Called from `main.ts` whenReady. |
| `electron/src/main.ts` | `webviewTag: true`; drop the now-stale "iframe with allow-popups" comment on the popup-deny handler (the deny-all stance is unchanged and still correct for the app window). |
| `electron/src/ipc/platform.ts` | delete `frame_check` + `frameRefused` (the un-M4'd `frame_check downloads full body` nit dies with it). |
| `src/platform.ts` | delete `frameCheck`. |
| `src/surfaces/BrowserView.tsx` | `<iframe>` → `<webview partition="persist:webtab">`; real-history nav bar; `did-fail-load` error pane; empty-URL **start state** (autofocused address input); Ctrl/Cmd+L focuses the address bar; title/URL events up to the tab strip (`onTitle` prop). Synthetic history stack, `refused` state, preflight effect deleted. |
| `src/surfaces/ReadSurface.tsx` | web tabs re-title from `onTitle`; **"+" new-web-tab button** on the tab strip + **"Open link" header button** (both `openWebTab('')`; empty URL titles the tab "New tab" instead of `hostOf`). |
| i18n | new strings (load-failure pane, retry, new-tab/open-link labels, "New tab" placeholder) en + zh; drop the refused-panel strings. |
| E2E | serve a page from an in-test `node:http` server; assert the webview guest loads it, `document.title` propagates, the guest has **no** `__ELECTRON_BRIDGE__`, and an `app://` fetch from the guest fails (isolation pin). |

**Acceptance (W1).** arXiv abs page, a publisher landing page, GitHub, and
Google Scholar all render in the in-app tab (device verify); back/forward/
address reflect real navigation incl. in-page links; a `target=_blank` link
navigates in-tab; no popup window ever carries the bridge (E2E-pinned);
`frame_check` is gone from both processes; a configured proxy routes webtab
traffic; cookies survive an app restart (log into a site, relaunch, still
logged in). **From a fresh Read surface with no tabs open, a user can reach a
website in two clicks**: header "Open link" → type `arxiv.org` → Enter
(scheme auto-prepended); with tabs open, the strip's "+" does the same; the
tab re-titles from the page title after navigation.

## 3. W2 — PDF download as attachment

| Piece | Change |
|---|---|
| `electron/src/ipc/storage.ts` | `attachment_download` per §1.5 (uses `proxyFetch` from `ipc/net.ts`; progress via `events.emit`). |
| `src/state/library.ts` | `Attachment.srcUrl?: string` (additive). |
| `src/state/attachments.ts` | `downloadPdfAsAttachment(refId, url)` per §1.6; `onAttachmentDownloadProgress` listener helper. |
| `src/surfaces/ReadSurface.tsx` | the three affordances (§1.6) + the W2b chooser (§1.7). |
| i18n | button/busy/error/downloaded strings en + zh. |
| E2E | in-test `node:http` server serves a small `%PDF-` payload: `attachment_download` round-trips into a temp root (bytes match, `AddedAttachment` shape); serving an HTML body instead yields the typed "not a PDF" error. |

**Acceptance (W2).** An arXiv discovery result → **Add + PDF** → item in
library with a managed attachment that opens in the pdf.js reader; the same
from Info/Read-tab buttons on an existing reference with `pdfUrl`; a paywalled
`pdfUrl` (HTML response) surfaces the typed error and leaves no attachment;
re-clicking is inert ("Downloaded"); download honours the `attachments` proxy;
W2b: a PDF link clicked inside a web tab offers attach-to-selected-reference
and lands in the same layout.

## 4. Sequencing & risk

- **W1 ∥ W2** — they share no code (W2 needs neither the webview nor
  `webtab.ts`; only W2b touches the `will-download` chokepoint, so W2b lands
  after W1). Ship W2 first if reading-flow demand says so.
- **Riskiest item: webview flakiness** (Electron's own docs hedge on it).
  Mitigated by containment (§1.1): the renderer contract is one component,
  the policy one main module; the `WebContentsView` fallback is a swap inside
  that boundary, and the overlay problem it brings would then be paid
  deliberately, not by default.
- **Site hostility** (bot checks, UA sniffing): the persistent partition +
  stock-Chrome UA handles the common cases; sites that still block render
  their own error inside the tab — the open-external button remains one click
  away. No cloaking beyond UA normalization (no header spoofing).
- **Memory**: each web tab is a renderer process. Acceptable at reading-tab
  counts; if it bites, cap concurrent web tabs or reap background guests
  (open question, not a blocker).
- **Privacy surface**: `persist:webtab` accumulates cookies/history-like
  state on disk. Add a Settings "Clear web-tab browsing data"
  (`ses.clearStorageData()`) in W1 — one button, closes the loop.
- **`attachment_download` fetches arbitrary URLs from the main process** —
  same class as the existing drawio/sync fetches; the URL comes from
  discovery metadata or the user, is http(s)-validated, and writes only
  through `intoKeyDir`'s sanitized layout under the user's chosen root.

## 5. Open questions (not blockers)

1. Retire `open_browser_window` + its toolbar button once the in-app tab is
   proven, or keep as the "detach to window" affordance? (Decide after W1
   device time.)
2. Find-in-page (`webview.findInPage`) — cheap and useful for long pages;
   fold into W1 chrome or later?
3. Should W2 auto-run for **Add by ID** (arXiv/DOI paste) too — import +
   download in one, symmetric with Add + PDF? Leaning yes, behind the same
   helper; needs the discover-pane UX to show progress.
4. Web-tab session zoom / dark-mode injection parity with the reader panes —
   nice-to-have, unscoped.
5. Address-bar **search fallback**: non-URL input (plain words) currently
   passes through `normalizeUrl` unchanged and fails to load. Route it to a
   configurable search engine (`https://duckduckgo.com/?q=…` default)? Cheap,
   but adds a "default search engine" setting — decide with the director.
6. A global **Ctrl/Cmd+T** for "new web tab" (browser muscle memory) — only
   meaningful inside Read; check it doesn't collide with the app-wide
   shortcut map before claiming it.

## Related

- [reference-library-and-reading.md](../discussions/reference-library-and-reading.md)
  — the library/discovery/reading interaction patterns this serves.
- [desktop-workbench-jobs.md](desktop-workbench-jobs.md) — J1 Read.
- [desktop-electron-migration.md](desktop-electron-migration.md) — ADR-055 §7
  row 15 ("new OS affordances") is the umbrella this executes under; the
  `frame_check` deletion is workaround paydown in the M4 spirit.
