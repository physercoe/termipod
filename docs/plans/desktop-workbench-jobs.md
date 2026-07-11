# Desktop workbench jobs — J1–J6 as sidebar tabs

> **Type:** plan
> **Status:** In flight (2026-07-10) — the workbench half of
> [ADR-050](../decisions/050-desktop-workbench-delivery-model.md). The control
> plane (J7) shipped as the [desktop control plane](desktop-control-plane.md)
> shell; this plan mounts the **research-workbench jobs J1–J6** into that shell
> as distinct **activity-bar tabs**. Per-job build/embed/integrate posture comes
> from [research-tooling-landscape.md](../discussions/research-tooling-landscape.md)
> §4; the job derivation is [desktop-research-surface.md](../discussions/desktop-research-surface.md)
> §3. **Round 1 shipped** (commit `28b6e578`); rounds 2+ deepen each surface.
> **Audience:** principal · contributors
> **Last verified vs code:** desktop v0.3.15

**TL;DR.** The desktop app was a single control-fleet screen. This plan turns it
into a **left activity-bar** of seven jobs — **Fleet** (J7, the existing
three-region mission-control) plus **J1 Read · J2 Author · J3 Debug · J4 Canvas ·
J5 Compare · J6 Record** — each its own centre-stage surface. The rail is the
single source of truth (`state/workbench.ts`); switching is instant and the
active job persists. Round 1 shipped the shell plus a functional first cut of
every job that existing dependencies allow (J1/J2/J3/J5/J6); J4 followed as a
native BUILD (pan/zoom card canvas) rather than the heavier tldraw embed. The headline is
**J5 — the multi-run comparison wall** (the landscape doc's biggest BUILD),
functional today on the hub's existing `listRuns` / `getRunMetrics` with no schema
change.

---

## 1. Why a sidebar of jobs (not one screen)

[desktop-research-surface.md](../discussions/desktop-research-surface.md) §3
derives the desktop app from the **work a phone can't do** — seven jobs, J1–J7 —
and §6 sketches a workbench shell rather than the phone's bottom tabs. The
control-plane half (J7) shipped first as the unified web-tech client
([desktop-control-plane.md](desktop-control-plane.md), WS0–WS8). The remaining
six jobs are **document-, editor-, canvas-, and chart-shaped** surfaces that mount
*into* that shell. Exposing them as distinct activity-bar tabs (the VS Code
idiom) is the director's explicit ask — "the jobs show as distinct sidebar tabs"
— and matches §6's "left rail + central Bench" shape, with the rail promoted from
Bench *modes* to first-class tabs so each job is one click from anywhere.

## 2. The jobs, their posture, and the round-1 cut

Posture column is from [research-tooling-landscape.md](../discussions/research-tooling-landscape.md)
§4. "Round 1" is what shipped in `28b6e578`; "Target" is the mature surface.

| Job | Posture | Round-1 cut (shipped) | Target |
|---|---|---|---|
| **J1 Read** | EMBED | dual-pane: hub `Document` (or pasted Markdown) rendered ↔ device-local notes keyed by source | EMBED Semantic Reader / PaperCraft (real PDF/HTML paper reading + annotation) |
| **J2 Author** | EMBED | split Markdown editor + live preview (KaTeX + highlight.js, offline) | EMBED BlockNote (block editor + Yjs collab) + INTEGRATE Quarto/Typst export |
| **J3 Debug** | EMBED | paste code/logs → syntax-highlighted view + line count | EMBED Monaco (+ MonacoDiffEditor, `file:line` jumps, fast huge-log scroll) |
| **J4 Canvas** | BUILD | **native** pan/zoom canvas: note & reference cards (wired to the J1 library), typed edges, inspector with backlinks | richer selection/routing, board persistence to the hub; optional tldraw embed if the native surface hits a ceiling |
| **J5 Compare** | **BUILD** | project → multi-select runs → per-metric overlay charts + final-value table, live-polled | + config-diff panel; EMBED optuna-dashboard sweep panel |
| **J6 Record** | BUILD | ADR-shaped capture (title/context/decision/consequences) → Markdown, device-local log | link records to the runs that justify them (provenance on hub `Deliverable`); share J2 editor |

**Why J5 is the headline.** No open tool exports a reusable run-comparison
component, and the data already lives in the hub (`listRuns` →
`client.ts:216`, `getRunMetrics` → `client.ts:293`). The round-1 wall projects
each run's `/metrics` rows onto the existing dependency-free `ChartView`
(`ui/ChartView.tsx`) as one overlaid series per run — no charting library, no
schema surgery. This is the surface that is most valuable **and** most
mobile-hostile, exactly the §5 sequencing call.

## 3. Architecture (round 1, shipped)

- **`state/workbench.ts`** — the `JOBS` registry (id · J-tag · icon · i18n keys)
  and a persisted `job` selector. Adding a job = one entry here + one surface
  component; the rail and the shell switch both read the registry.
- **`ui/ActivityBar.tsx`** — the left rail, one button per registry entry,
  active-highlighted.
- **`ui/WorkbenchSurface.tsx`** — shared job chrome (tagged header + hint +
  actions slot + scrolling body) and an honest `SurfacePlaceholder` for unshipped
  EMBEDs.
- **`ui/AppShell.tsx`** — `.shell` is now a flex column; `workbench-row` =
  ActivityBar + the active surface. The **Fleet** tab renders the original
  three-region body verbatim, so J7 is unchanged.
- **`state/draft.ts`** — device-local scratch (`useDraft` / `useJsonDraft`) for
  J1 notes, J2 drafts, J6 records. Deliberately `localStorage`, not the hub:
  these are private, in-progress artifacts; promoting them to hub Documents /
  Deliverables is a later round.
- **Styling** — all in `styles/app.css` semantic-token layer; `tokens.json`
  untouched, so mobile is unaffected (the load-bearing ADR-050 constraint).
- **i18n** — en + zh strings for every job and surface.

## 4. Sequencing — rounds 2+

Ordered by value × mobile-hostility (§5 of the landscape doc), each its own
shippable wedge and install-feedback round:

1. **J5 depth** — config-diff panel (getRunConfig, `client.ts:264`) + EMBED
   optuna-dashboard for sweeps; swatch↔line colour parity per metric.
2. **J1/J2 pair** — EMBED Semantic Reader (J1) and BlockNote (J2); graduate
   notes/drafts from localStorage to hub-backed incubation notes
   ([research-reading-and-ideation-ui.md](../discussions/research-reading-and-ideation-ui.md)).
3. **J4 Canvas** — ✅ **shipped as a native BUILD** (not the tldraw embed): an
   infinite pan/zoom surface of note & reference cards (bound to the J1 library)
   joined by typed edges, with a backlinks inspector — a Zettelkasten, not a
   whiteboard. Chosen over embedding tldraw because the cards must *be* library
   references and the dependency is heavy; the native surface is ~10 kB and
   frontend-only. A tldraw embed stays a fallback only if the native canvas hits
   a ceiling (freehand ink, complex routing).
4. **J3 Debug** — EMBED Monaco + MonacoDiffEditor.
5. **J6 provenance** — link records to runs; share the J2 editor.

Each dep-adding round is isolated so a single `cargo`/`vite` regression can't
stall the shell.

## 5. Open questions

1. **Bundle size.** Monaco + tldraw + BlockNote are each large; do they load
   lazily per-tab (dynamic `import()`) so the shell stays light? (The round-1
   bundle already trips Vite's 500 kB warning.)
2. **Draft persistence.** When do J1/J2/J6 drafts graduate from localStorage to
   hub Documents — on explicit "save to project", or continuously?
3. **Inspector rail.** §6 sketches a right-hand contextual Inspector; is it a
   per-job pane or a shared shell region? (Deferred until two jobs want it.)
4. **Command palette.** Should ⌘K jump to a job tab, not just fire commands?

## Related

- [ADR-050](../decisions/050-desktop-workbench-delivery-model.md) — the delivery
  model this plan's workbench half serves.
- [desktop-control-plane.md](desktop-control-plane.md) — the shell (J7) these
  jobs mount into.
- [research-tooling-landscape.md](../discussions/research-tooling-landscape.md) —
  the per-capability build/embed/integrate register (§4) driving the posture
  column.
- [reference-library-and-reading.md](../discussions/reference-library-and-reading.md)
  — J1's deepening design: the Zotero-shaped library + Semantic Scholar discovery
  and its mapping onto the hub data-ownership law.
- [desktop-research-surface.md](../discussions/desktop-research-surface.md) — the
  J1–J7 derivation and the §6 workbench shape.
