# Author workspace — agent assist + offline diagrams

> **Type:** discussion
> **Status:** Open (2026-07-12) — scopes three director asks for the **J2 Author**
> surface. **Multi-document tabs + on-disk file save/open shipped** in
> `AuthorSurface.tsx` + `state/documents.ts` + Rust `docfile.rs`. The **agent-assist
> side panel** and **offline draw.io diagrams** are designed here and deferred
> pending the two decisions below. Feeds
> [desktop-workbench-jobs.md](../plans/desktop-workbench-jobs.md) (J2 deepening);
> relates to [research-tooling-landscape.md](research-tooling-landscape.md) (embed
> vs build postures) and the POSIX host-runner in
> [../spine/agent-lifecycle.md](../spine/agent-lifecycle.md).
> **Audience:** contributors · principal
> **Last verified vs code:** desktop v0.3.25 (post-release, on main)

**TL;DR.** The Author tab now holds **multiple documents as tabs**, each a
Markdown split-editor, device-local by default and **saveable to a real file on
disk** (native dialog → `doc_save`/`doc_open`/`doc_write`). Two follow-ups need a
decision before building: **(2) an agent-assist side panel** — blocked by a real
platform fact: the **host-runner is POSIX-only**, so a Windows desktop can't host
a local agent and must drive a *remote* agent through the hub (or use an inline
LLM path the desktop doesn't have wired); **(3) draw.io diagrams offline** — the
director chose **bundle drawio locally** over the remote embed, which is feasible
(Apache-2.0, fully client-side) but costs installer size and a vendoring step.

---

## 1. Shipped — multi-document tabs + file storage (director ask #1)

"The Author tab should support multiple tabs, and where are files saved?"

- `state/documents.ts` — a `useDocuments` store: a list of `Doc { id, kind,
  title, body, filePath?, dirty?, updatedAt }` persisted to `localStorage`
  (`termipod.documents.v1`). Migrates the old single `termipod.draft.author`
  draft into the first document so nothing is lost.
- `AuthorSurface.tsx` — a **tab strip** (one tab per open document, `+ New`,
  close-with-confirm) over the existing split Markdown editor.
- **Where files live** (the explicit answer):
  - **By default: device-local.** A document's text lives in the WebView's
    `localStorage` on the user's own machine — nothing is uploaded. The editor's
    meta bar says *"Saved in-app (this device)"*.
  - **Optionally: a real file on disk.** `Save` opens a native Save dialog and
    writes the file (Rust `doc_save`); `Open file…` reads one (`doc_open`); a
    linked document re-saves in place (`doc_write`). The meta bar then shows the
    filename and a ● dirty marker. These reuse the same `tauri-plugin-dialog`
    already added for the Zotero storage link — no new dependency.

**Not yet:** promoting a document to a **hub Document/Deliverable** (with run
provenance, fleet-shared, agent-readable) — the same graduation the
[reference library](reference-library-and-reading.md) is taking. That is the
natural convergence point with ask #2.

---

## 2. Agent-assist side panel (director ask #2) — DECISION NEEDED

"There should be an agent page alongside the Author edit page, since
editing/writing is agent-assisted."

The director raised the blocking question directly: **does this support Windows,
given the host-runner seems POSIX-only?** It does not, and that shapes the design.

### 2.1 Finding: the host-runner is POSIX-only

Verified in the code — the deputy that spawns and supervises agents is Unix-bound:

- `hub/internal/hostrunner/tmux_launcher.go` — every agent is placed in a **tmux**
  window/pane (`exec.Command(ctx, "tmux", …)`); tmux has no Windows port.
- `hub/internal/hostrunner/launch_m2.go:53,63,131` — `exec.Command(ctx, "bash",
  "-c", …)`, `syscall.SysProcAttr{Setpgid: true}`, and
  `syscall.Kill(-pid, SIGKILL)` (process-group teardown). `Setpgid`/`Kill` are
  Unix-only — this file **won't even compile** on Windows.
- `runner.go:284` requires the `tmux` and `hub-mcp-bridge` binaries on PATH.

So a **Windows machine cannot run the host-runner** and cannot host a local
agent. This is not a desktop-client limitation — it's the fleet's execution
substrate.

### 2.2 What that means for agent-assisted writing

The desktop is a **hub client**, not a host-runner, so two models are possible:

- **A. Remote hub agent (architecture-consistent).** The side panel reuses the
  existing hub agent transcript + composer (`surfaces/AgentTranscript.tsx`,
  `ui/Composer.tsx`, `hub/` client) to chat with an agent running on a **remote
  POSIX host** in the fleet; the author pulls its replies (prose / diagram XML)
  into the active document. Works from a Windows desktop **iff** the fleet has at
  least one POSIX host with a running agent. Fits the data-ownership law once the
  document is a hub Document the agent can read via MCP (see §1 "not yet").
  *Cost:* requires a connected hub + a spawned agent; the "agent edits my local
  file" loop needs the document promoted to a hub entity first.
- **B. Inline LLM assistant (self-contained).** A lightweight in-pane assistant
  that calls an LLM directly with the document as context and proposes edits —
  no spawned agent, works on a lone Windows desktop with no POSIX host. *Cost:*
  the desktop has **no LLM endpoint/key path wired** today (all model traffic
  goes through the hub/host-runner). This introduces a new, ungoverned model
  channel that bypasses the fleet's provenance/telemetry — a policy question, not
  just an engineering one.

### 2.3 Open questions (for discussion, before building)

1. Is the target user always connected to a hub with a POSIX host, or must
   Author assist work **standalone on Windows** with no fleet?
2. If standalone matters, is a **direct desktop→LLM** channel acceptable given it
   sidesteps hub governance (ADR-030 propose/telemetry)? If so, does it route
   through the hub's `llm_call` (M3) for provenance, or a raw provider key?
3. Should "agent-assisted" mean **document as hub Document** (agent CRUDs it via
   MCP, like the [reference entity](../decisions/053-hub-reference-library-entity.md))
   rather than a chat side panel at all — i.e. converge ask #1's file model with
   the fleet?

Recommendation: **model A**, gated on promoting the Author document to a hub
Document, is the architecture-consistent path — but confirm the Windows/standalone
requirement first, because it may force model B (and a governance decision).

---

## 3. Offline draw.io diagrams (director ask #3) — chosen: BUNDLE

"The Author should also support diagram drawing like draw.io" — and the director
chose **bundle drawio locally** (offline) over the remote embed, referencing
[next-ai-draw-io](https://github.com/DayuanJiang/next-ai-draw-io).

### 3.1 How the reference app works

`next-ai-draw-io` uses the **`react-drawio`** npm package (a thin wrapper over an
`<iframe>` to the diagrams.net editor + its `postMessage` embed protocol) and has
an **LLM generate/modify the draw.io XML (mxGraph)**, which it loads back into the
editor. The AI's whole job is emitting valid draw.io XML; the editor renders it.
By default `react-drawio` points at the hosted `embed.diagrams.net`.

### 3.2 Bundling drawio offline is feasible

- **License:** Apache-2.0 — vendoring/redistribution is permitted.
- **Client-side:** draw.io is a pure browser JS editor; once its files load it
  needs **no network**. The official **draw.io Desktop** (Electron) is exactly
  this — the same webapp bundled offline — so the pattern is proven.
- **Artifact:** the prebuilt webapp is `src/main/webapp/` in `jgraph/drawio`
  (releases also ship `.war`/Docker). We vendor those static files.

### 3.3 Proposed integration

1. **Vendor** a pinned drawio webapp build under the desktop app (e.g.
   `desktop/vendor/drawio/` copied into the bundle resources) — *offline*.
2. **Serve locally**: load it from the app origin (place under the Vite/`public`
   output or a Tauri asset path) and point an `<iframe>` at
   `…/drawio/index.html?embed=1&proto=json&ui=min&noSaveBtn=1`.
3. **Protocol** (hand-rolled, no `react-drawio` dep — keeps `npm ci` clean, same
   choice as the J1 browser tab): on the iframe's `init` message, post
   `{action:'load', xml}`; on its `save`/`autosave` messages, store `xml` back
   into the `Doc.body`. A `diagram` document kind already exists in
   `state/documents.ts` for this.
4. **AI (later):** a diagram document is just draw.io XML in `Doc.body`, so
   ask #2's agent panel can generate/patch that XML exactly like the reference
   app — no separate mechanism.

### 3.4 Tradeoffs to accept / decide

- **Installer size.** The drawio webapp (mxGraph + full shape/stencil libraries)
  is several MB to tens of MB; draw.io Desktop installers run ~100 MB+. We can
  **trim shape libraries** to the ones we need to keep the bundle lean — decide
  the shape set.
- **Vendoring hygiene.** A pinned, checked-in third-party build is a large,
  opaque blob in-repo; note the pinned version + provenance and refresh
  deliberately. (Alternative: fetch-at-build in CI rather than commit the blob.)
- **Update cadence.** Bundled drawio is frozen at the pinned version until we
  re-vendor — fine for an editor, unlike a security-sensitive dependency.

Recommendation: vendor a **trimmed** drawio webapp, serve from the app origin,
hand-roll the `postMessage` bridge, store XML in the `diagram` document. Build
this as its own wedge (the vendor commit is heavy and worth isolating).

---

## Decisions requested

1. **Author assist (§2):** must it work **standalone on Windows** (forcing an
   inline/`llm_call` model), or is a **remote hub agent** (model A) acceptable?
   And should the Author document graduate to a **hub Document** so agents CRUD it?
2. **Diagrams (§3):** confirm **bundle** (chosen) and pick the **shape-library
   scope** (full vs trimmed) and **vendoring method** (checked-in blob vs
   fetch-in-CI).
