# Desktop shell split pane — minimal simultaneity

> **Type:** plan
> **Status:** Done (2026-07-30) — all three wedges shipped: **S1** store +
> render + toggle (§4.1), **S2** ergonomics (§4.2), **S3** focus projection
> (§4.3). Deliberately **not** a docking framework: exactly one pinned secondary
> surface, per `desktop-design-review.md` §4.2's minimal cut. Open questions in
> §7 stay open — they were scoped out, not answered.
> **Audience:** principal · contributors
> **Last verified vs code:** 2026.727.938 (`desktop/src/state/workbench.ts`,
> `src/state/ui_policy.ts`)

**TL;DR.** ADR-050 argues the desktop's entire reason to exist is
*simultaneity*, yet the shell is still a modal one-job-at-a-time switch
(`desktop/src/ui/AppShell.tsx` renders exactly one surface from
`useWorkbench().job`). Paper-beside-draft, transcript-beside-compare-wall,
replay-beside-decisions are all impossible today — the design review called
this "the one place the thinking is ahead of the implementation," and both
follow-on plans (compare wall, citation bridge) assume their surfaces can be
kept visible while the user works elsewhere. The fix is small because the
review predicted it would be: the `JOBS` registry + `workbench` store are the
clean place to model it. Add `secondary: JobId | null` to the workbench store,
factor the shell's job→surface ternary into one `SurfaceView` component, render
primary | secondary with a divider, and extend the (planned) `ui_get_focus`
snapshot so agents see both panes. One pane, pinned right — nothing more.

## 1. Context and grounding

- The shell switch is a single ternary chain in `AppShell.tsx` (`job ===
  'projects' ? <ProjectsSurface/> : …`, one branch per `JobId`), wrapped in
  **one** `ErrorBoundary`, with `TerminalPanel` and `AssistantDock` mounted
  beside it unconditionally. The terminal therefore already has simultaneity
  (bottom panel), as does the assistant dock — the *surfaces* are what can't
  pair.
- `desktop/src/state/workbench.ts` is a 99-line store: `JOBS` registry +
  `{ job, setJob }` persisted to `localStorage`. Adding a job is "one entry
  plus a surface component"; adding a *pane* should be equally contained.
- The UI-context plan (`desktop-ui-context-and-pointing.md`, ADR-062) defines
  the focus snapshot agents will read. Its D1 wedge is not yet implemented at
  `de201ca9` — this plan specifies the shape extension now so D1 can land
  split-aware rather than retrofitting.
- Pairings this unblocks, from the design review: J1↔J2 (paper beside draft —
  the citation bridge's working posture), J5↔J8 (curves beside episodes),
  fleet/transcript↔J5 (what the agent says beside what the runs show),
  J3↔J2.

## 2. Goals / non-goals

**Goals**

1. Pin any one job as a secondary surface in the right half; close/swap it.
2. Both panes live: independent scroll, state, and error containment.
3. Focus attribution: exactly one pane is *active* (drives the status bar,
   palette context, and the ADR-062 focus snapshot).
4. Persistence: the split (and which pane is active) restores on relaunch,
   same as `job` does today.

**Non-goals (v1)**

- No N-pane layouts, no drag-to-dock, no tab groups — one secondary, right
  half. The review: "not a full docking framework."
- No same-job-in-both-panes: surfaces are singletons over singleton stores
  (one `ReadSurface`, one library selection). `secondary === job` is refused.
- No per-pane terminal/settings: `terminal` stays the always-mounted bottom
  panel; `settings` stays a full-surface switch (it is chrome, not work).
- No pane-size persistence beyond a single ratio; no vertical split.

## 3. Design

### 3.1 Store

`workbench.ts` grows three fields, same persistence pattern as `job`:

```ts
interface WorkbenchState {
  job: JobId;                       // primary pane (unchanged meaning)
  secondary: JobId | null;          // pinned right pane, null = no split
  activePane: 'primary' | 'secondary';
  setJob(job: JobId): void;         // targets the ACTIVE pane (see 3.3)
  setSecondary(job: JobId | null): void;
  focusPane(p: 'primary' | 'secondary'): void;
  swapPanes(): void;
}
```

As shipped, each rule is a **pure reducer over `PaneState`** (`applySetJob`,
`applySetSecondary`, `applyFocusPane`, `applySwapPanes`, `healPanes`) and the
store actions are one line each: `set(persist(applySetJob(get(), job)))`. Every
rule below is therefore asserted in `workbench.test.ts` without React, and the
shell's own derived questions are functions too — `activeJob(s)` and
`isSplitVisible(s)`.

Rules enforced in the store (single source of truth, testable without React):

- `setSecondary(j)` with `j === job` is a no-op (singleton rule above);
  `setSecondary(null)` closes the split and forces `activePane: 'primary'`.
- `setJob(j)` when `j === secondary` swaps instead of duplicating — clicking
  the rail icon of the already-pinned job brings it to the primary pane.
- `'terminal'`/`'settings'` are refused as `secondary` (registry-driven: a
  `splitEligible` predicate over `JOBS`, not a scattered check).
- Restore validates both ids against `KNOWN_JOBS` exactly as `initialJob()`
  does, and re-applies the rules (a persisted `secondary === job` heals to
  `null`).

### 3.2 Shell rendering

Factor the existing ternary chain into `SurfaceView({ job }: { job: JobId })`
— a pure mapping component, no behavior change — then:

```tsx
<div className={`shell-panes${secondary ? ' split' : ''}`}>
  <section className={pane('primary')} onFocusCapture={() => focusPane('primary')}>
    <ErrorBoundary><SurfaceView job={job} /></ErrorBoundary>
  </section>
  {secondary && (
    <>
      <ResizeHandle onResize={onSplitResize} /> {/* S2; a hairline div in S1 */}
      <section className={pane('secondary')} onFocusCapture={() => focusPane('secondary')}>
        <ErrorBoundary><SurfaceView job={secondary} /></ErrorBoundary>
      </section>
    </>
  )}
</div>
```

- **Per-pane ErrorBoundary** — this also pays down the review's §3.4 finding
  (one boundary for the whole switch): a crash in the pinned pane leaves the
  primary working, and vice versa.
- The active pane gets a subtle border/title highlight (`pane()` adds
  `active`); `onFocusCapture` + click both move focus. `TerminalPanel` /
  `AssistantDock` stay outside the pane container, unchanged.
- 50/50 by default with a min-width guard (~480px per pane; below the
  threshold the divider clamps). Surfaces already render inside
  `WorkbenchSurface` and are width-fluid; S1 accepts that some (ReadSurface's
  three-column library view) are cramped at half width — that is the user's
  trade to make, not a blocker.

### 3.3 Ergonomics (S2)

- **ActivityBar**: modifier-click (Alt) or context-menu "Open beside" pins a
  job as secondary; the pinned job's rail icon gets a corner dot. Plain click
  keeps today's meaning (it switches a surface, it does not pin one), except the
  swap-on-click rule from 3.1.

  > **Reconciled in S1.** This bullet originally read "switch *primary*", which
  > contradicts §3.1's `setJob` → *active pane*. Shipped behaviour is §3.1's:
  > `setJob` targets the active pane, so a rail click changes the surface the
  > user is looking at (VS Code's "open in the active group"). With no split the
  > two readings are identical, which is why they could both be written down.
  > The chrome jobs are the exception — `terminal`/`settings` always take the
  > primary and pull focus with them, since neither is a pane.
- **Palette**: `Split: open <job> beside` (one command per eligible job,
  contributed from `JOBS`), `Split: close`, `Split: swap panes`.
- **Shortcuts** via the rebindable-shortcut registry (PR #464): default
  `Mod+\` = toggle split (reopens the last secondary), `Mod+Shift+\` = swap.

  > **S2 found this default unreachable.** `comboFromEvent` derived the combo from
  > `KeyboardEvent.key`, which carries the *shifted* character — Shift+Backslash
  > is `'|'` on a US layout and something else elsewhere — so `mod+shift+\` could
  > never match, and hardcoding `mod+shift+|` would have been layout-specific.
  > Punctuation now resolves by `e.code` (the physical key); letters and digits
  > deliberately still resolve by `key`, since remapping them would invalidate
  > combos users had already captured. Any future hand-written default with
  > punctuation in it depends on this.
- **Divider drag** with a stored ratio (single number, clamped 0.25–0.75).

### 3.4 Focus projection (S3, with ui-context D1)

The ADR-062 focus snapshot gains two fields — this is additive to the
UI-context plan's §3.2 shape, and the per-surface `ui_policy` table is
untouched (split is layout, not a new surface):

```json
{ "surface": "author", "pane": "primary", "…": "…",
  "secondary": { "surface": "compare", "…": "…" },
  "active_pane": "primary" }
```

Both panes' UIRefs are served under their own surface's policy row; `ui_highlight`
targets resolve in whichever pane hosts the ref's surface. If D1 lands before
S3, the snapshot builder reads `secondary`/`activePane` from the same store —
no IPC change, one more field in the throttled push.

> **As shipped, `pane` is not emitted.** With `surface` positional (always the
> primary) and `active_pane` naming the focused one, a top-level `pane` field
> could only ever say `"primary"` — it carries no information a reader doesn't
> already have. The sketch above shows a case where the two happen to agree,
> which is what hid the redundancy.

`ui_highlight` is D6 and unbuilt (no such tool exists in the tree at S3), so the
resolve-in-the-hosting-pane rule is a constraint on D6, not something S3 could
implement. What S3 owes it is the shape: the snapshot now says which surface is
in which pane, which is exactly what a highlight target needs to resolve.

## 4. Wedges

- **S1 — store + render + toggle** (the working core): 3.1 + 3.2, palette
  `Split: open/close`, persistence, per-pane boundaries. Ship with 50/50 fixed.
  **Shipped 2026-07-30** — see §4.1.
- **S2 — ergonomics**: rail modifier-click + dot, swap command + shortcut
  registry entries, divider drag + ratio persistence, min-width clamps, i18n.
  **Shipped 2026-07-30** — see §4.2.
- **S3 — focus projection**: snapshot fields + tests, coordinated with
  ui-context D1 (whichever lands second carries the integration test).
  **Shipped 2026-07-30** — see §4.3. D1 landed first, so S3 carried the
  integration test.

### 4.1 S1 as shipped (2026-07-30)

`workbench.ts` (+ `workbench.test.ts`), `ui/SurfaceView.tsx` (new),
`ui/AppShell.tsx`, `partials/01-base-shell.css`, `i18n/index.ts`,
`electron/e2e/app.spec.ts`.

Three judgement calls the plan left open, and how they resolved:

1. **`activePane` names a position, not a piece of content.** So `swapPanes`
   moves the two surfaces and leaves the active *side* alone. That is what makes
   the §3.1 swap-on-click rule land the clicked job in the pane the user is
   looking at — "clicking the rail icon of the already-pinned job brings it to
   the primary pane" is exactly this rule seen from the primary pane.
2. **A pinned pane survives a trip to a chrome job.** `settings` is a
   full-surface switch and `terminal` is the bottom panel, so neither can share
   the row; rather than dropping the pin, `secondary` is *kept* and
   `isSplitVisible()` reports false while the primary is a chrome job. Come back
   to a work surface and the split is still there. This is also why the palette
   offers no split commands while you are on Settings — a command that silently
   no-ops is worse than an absent one. The paired invariant: **`activePane` may
   never name a pane that is off screen**, or `activeJob()` would report a
   surface the user cannot see (the goal-3 focus attribution, and later the
   ADR-062 snapshot, both read it). Review follow-up: the *parked* pin obeys
   the same rules at every reducer, not just the healer — pinning under a
   chrome primary parks the pin unfocused, clicking the parked pin's rail icon
   promotes it to the primary (the swap branch would have seated the chrome
   job in a pane it is banned from), and a parked split refuses `swapPanes`.
3. **Persistence is two keys, not a migrated blob.** The primary keeps
   `termipod.workbench.job` with its original shape (a bare job id) and the split
   rides in `termipod.workbench.split.v1`, absent when there is none. No
   migration, and an older build simply ignores the split. `healPanes()` repairs
   the pair on restore, because two independently-parsed keys can disagree.

Deliberately **not** in S1: the divider is a hairline, not a drag handle (S2 owns
the ratio and the min-width clamp together — the clamp is meaningless without the
drag); no rail Alt-click, no `Split: swap` command, no `Mod+\` binding.

Not verified locally: the Electron E2E spec (`split S1`) — this host has no
Electron binary and no X display, so CI is its only gate, as the Playwright
config already states. The state tests and both typechecks were run locally.
(CI then ran it green, including the swap-without-duplication assertion.)

### 4.2 S2 as shipped (2026-07-30)

`workbench.ts`, `keybindings.ts` (+ both test files), `ui/ActivityBar.tsx`,
`ui/AppShell.tsx`, `surfaces/Settings.tsx`, `partials/01-base-shell.css`,
`partials/02-job-surfaces.css`, `i18n/index.ts`, `electron/e2e/app.spec.ts`.

Four entry points now write the pane state — rail Alt-click, the rail's
right-click menu, two chords, and the palette. That is precisely the shape the S1
review found a bug in (a rule enforced at the dedicated setter but not the other
writers), so each one is gated by the *same* `isSplitEligible` predicate rather
than its own check, and the e2e spec drives all of them.

- **Ratio.** One number, `clampSplitRatio`'s 0.25–0.75 band, applied as the
  primary pane's inline `flex-basis` (the secondary takes the rest). Inline
  rather than a CSS custom property: a runtime-injected `var()` reads as a
  *phantom token* to the design-token ratchet. The pixel min-pane clamp lives in
  the same pure function but needs a measured width, so the shell passes one; on
  a window too narrow for two minimum panes the pixel rule is unsatisfiable and
  the plain band stands, rather than freezing the divider mid-row.
- **Divider.** The shared `ResizeHandle` — already keyboard-operable
  (`role="separator"`, arrow nudges) and already using window listeners instead
  of `setPointerCapture`, for a documented WebView2 reason. Writing a second
  divider would have re-earned that bug. `.shell-pane-divider` is gone with it.
- **Persistence.** The split key gains `ratio` and `lastSecondary`. Neither is
  part of `PaneState`: no pane reducer needs them, and keeping them out leaves
  the reducers a 3-field algebra. `parseSplit` degrades each field
  independently, so an S1-era blob still restores.
- **Toggle memory.** `Mod+\` closes a live split and reopens the last pinned
  job; with no memory it pins the first eligible job in rail order, so the chord
  always does something visible on a fresh install. Inert under a chrome
  primary, matching the palette.
- **Naming.** The rail's split marker is `.activity-tab.beside`, NOT `.pinned` —
  `.activity-tab-pinned` already means "pinned to the bottom of the rail" (the
  Settings gear). Two senses of *pinned* in one stylesheet would be a trap.

Deliberately not in S2: no per-pane navigation history (§7 Q2 — still creep), and
the ratio is not per-job (one row shape, not a remembered layout per pairing).

Not verified locally, again: the e2e spec (`split S2`), including the divider
drag. CI is the gate.

### 4.3 S3 as shipped (2026-07-30)

`workbench.ts` (`panesForFocus`), `state/ui_policy.ts`, `state/uiContext.ts`,
`electron/src/ui_policy.test.ts`, `state/workbench.test.ts`; the UI-context
plan's §3.2 shape updated to match.

- **The top level became positional.** D1 published the *active* pane as
  `surface` — the best answer available when there was no way to name the other
  one. S3 makes `surface` the primary pane and adds `active_pane`, so a snapshot
  describes what is **on screen** rather than only what has focus. With no split
  the two readings are identical, so nothing changed for the single-pane case.
- **The gate follows focus, not position.** `projectFocus` degrades the whole
  answer when the row of the surface the user is *in* has an empty allowlist —
  so a vault pane suppresses the other pane's blocks too. Gating on the
  top-level (primary) surface instead would have leaked them.
- **A pinned pane is served under its own row**, so pinning a surface can never
  publish more than opening it would.
- **A parked pin is never reported** (`panesForFocus` keys on `isSplitVisible`):
  an agent is not told about a pane the user cannot see.
- No main-side change: `desktopui_focus` caches the projection verbatim
  (shape + 16 KiB size check only), so both fields ride through untouched.

Left to D6: `ui_highlight` resolving a target into the pane that hosts its
surface. No such tool exists yet; S3 supplies the shape it will need.

## 5. Testing

State tests run manually (`node --test src/state/*.test.ts` — CI does not run
desktop state tests):

- Store rules: `secondary === job` refused; swap-on-click; close forces
  primary focus; terminal/settings refused; corrupt/legacy persisted values
  heal (`secondary === job`, unknown ids).
- Restore: relaunch state = (job, secondary, activePane, ratio).
- Boundary containment: a surface that throws in the secondary pane leaves the
  primary interactive (component test at the `SurfaceView` seam).
- S3 (done): snapshot includes `secondary` + `active_pane`; single-pane snapshot
  has neither (no `"secondary": null` noise — absent means no split); the pinned
  pane carries only its own row's fields; an undeclared pinned surface is not
  published; the gate follows focus across the split. These live in
  `electron/src/ui_policy.test.ts`, which **CI does run**.
- S3 integration (the plan's second-lander obligation): the real workbench store
  driven through the real projection — pin, focus, swap, park on Settings, come
  back — in `src/state/workbench.test.ts`.

## 6. Risks

- **Memory/CPU of two mounted surfaces** — ReadSurface (pdf.js) beside
  ReplaySurface (Rerun WASM, ~2 GiB viewer cap) is the worst pair. v1 accepts
  it (the user chose the pair); if it bites, the mitigation is per-surface
  suspend-on-hide, not fewer panes.
- **Width assumptions in surface CSS** — audit the worst offenders at half
  width during S1 and fix only what's broken-broken (overlap/clipping), not
  what's merely cramped.
- **Interaction with always-mounted chrome** — TerminalPanel height + split
  panes on small displays; the min-width clamp plus the existing terminal
  collapse handle should be enough.

## 7. Open questions

1. Should `fleet` be split-eligible from S1, or wait until its three-region
   layout (Navigator | Focus | AttentionDock) is audited at half width? Lean:
   eligible from S1 — transcript-beside-wall is a headline pairing.
2. Does the pinned pane deserve its own navigation history (back/forward per
   pane), or is that docking-framework creep? Lean: creep; revisit only with
   evidence.
