import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { panesForFocus, useWorkbench } from './workbench';
import { useFocus } from './focus';
import { useDocuments } from './documents';
import { useInspect } from './inspect';
import { useReplay } from './replay';
import { useTerminals } from '../terminal/store';
import { assembleRawFocus, createFocusSender, projectFocus, type FocusSources } from './ui_policy';
import { useAgentHighlight } from './agentHighlight';

/// UI context sharing (D1 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.2/§3.7). Whether agents ON THIS HOST (the embedded kimi web panel and
/// the user's local kimi-code sessions) may ask what the user is looking at —
/// served as the `ui_get_focus` MCP tool / `ui://focus` resource on the
/// browser bridge's loopback server. OFF BY DEFAULT: no publisher, no tool in
/// any catalog, no mcp.json entry.
///
/// Same plumbing pattern as the browser bridge toggle (browserBridge.ts): the
/// flag persists renderer-side in localStorage and is PUSHED to main at boot
/// (`syncUiContextToMain`, main.tsx) and on every change; main owns the focus
/// cache, the tool's catalog presence, and the user-level
/// `~/.kimi-code/mcp.json` entry lifecycle.
///
/// The publisher below subscribes to the stores that hold focus state,
/// projects through the ui_policy table (secret-free by construction), and
/// pushes over the `desktopui_focus` invoke — throttled ≥500 ms with
/// coalescing, and only while the toggle is on. It reads only what stores
/// already expose: Read-surface tabs, the Inspect text selection, the compare
/// refs and the record target live in surface-local useState today and are
/// deliberately NOT published in D1 (their policy rows already reserve the
/// fields).

const LS_KEY = 'termipod.uiContext.enabled';

function loadEnabled(): boolean {
  try {
    return localStorage.getItem(LS_KEY) === '1';
  } catch {
    return false;
  }
}

function persistEnabled(v: boolean): void {
  try {
    localStorage.setItem(LS_KEY, v ? '1' : '0');
  } catch {
    /* private mode — the toggle still works for the session */
  }
}

interface UiContextState {
  /// The user's setting (persisted). Main gates the tool catalog + mcp.json
  /// entry on the same flag via `desktopui_set_enabled`.
  enabled: boolean;
  setEnabled: (v: boolean) => void;
}

export const useUiContext = create<UiContextState>((set) => ({
  enabled: loadEnabled(),
  setEnabled: (v) => {
    persistEnabled(v);
    set({ enabled: v });
    setPublishing(v);
    // Turning sharing OFF revokes the agents' presence on screen — any live
    // highlight markers (D6) go with it rather than glowing out their TTL
    // after the user withdrew consent.
    if (!v) useAgentHighlight.getState().clear();
    if (!isShell()) return;
    void invoke('desktopui_set_enabled', { enabled: v }).catch(() => undefined);
  },
}));

// ── Focus snapshot publisher ─────────────────────────────────────────────────

/// The push half: coalesces store bursts to one snapshot per 500 ms window
/// and dedupes identical projections (most store ticks — e.g. Inspect typing —
/// change nothing the policy reads).
const sender = createFocusSender(500, (snapshot) => {
  if (!isShell()) return;
  void invoke('desktopui_focus', { snapshot }).catch(() => undefined);
});

/// Publishing mirrors the persisted toggle from module load — main learns the
/// flag (and gets the first snapshot) via the boot sync / the toggle itself.
let publishing = loadEnabled();

function setPublishing(v: boolean): void {
  publishing = v;
  if (!v) {
    sender.cancel();
    return;
  }
  tick(); // an immediate first snapshot, so a just-enabled tool never sees null
}

/// Read the current focus slices out of the stores. Kept trivially cheap:
/// object reads only — the projection, not the assembly, is where fields are
/// decided.
function sourcesNow(): FocusSources {
  // Both panes (split-pane S3). The top level is the PRIMARY pane and
  // `activePane` names where the user is, so a split snapshot describes what is
  // on screen rather than only the focused half. A parked pin is not reported.
  const { job, secondaryJob, activePane } = panesForFocus(useWorkbench.getState());
  const focus = useFocus.getState();
  const docs = useDocuments.getState();
  const inspect = useInspect.getState();
  const replay = useReplay.getState();
  const term = useTerminals.getState();
  const activeDoc = docs.activeId !== null ? (docs.docs.find((d) => d.id === docs.activeId) ?? null) : null;
  const activeInspect = inspect.activeId !== null ? (inspect.tabs.find((t) => t.id === inspect.activeId) ?? null) : null;
  return {
    job,
    secondaryJob,
    activePane,
    fleetSelection: focus.fleet.selection,
    projectSelection: focus.projects.selection,
    activeDocument: activeDoc !== null ? { id: activeDoc.id, title: activeDoc.title } : null,
    inspectTabs: inspect.tabs.map((t) => (t.path !== undefined ? { kind: t.kind, path: t.path } : { kind: t.kind })),
    inspectActive: activeInspect !== null && activeInspect.path !== undefined ? { path: activeInspect.path } : null,
    replayDatasetId: replay.target?.datasetId ?? (replay.selectedId !== '' ? replay.selectedId : null),
    // A pane is "focused" only when the terminal is actually visible — the
    // dock stays mounted (and its activeId alive) while hidden.
    terminalPaneId: term.activeId !== null && (term.open || job === 'terminal') ? term.activeId : null,
    capturedAt: new Date().toISOString(),
  };
}

function tick(): void {
  if (!publishing) return;
  sender.push(projectFocus(assembleRawFocus(sourcesNow())));
}

// Any store change may move the projection; the sender dedupes the ones that
// don't. Fine-grained selector subscriptions would save a cheap recompute per
// tick at the cost of seven hand-maintained selector pairs — not worth it.
useWorkbench.subscribe(tick);
useFocus.subscribe(tick);
useDocuments.subscribe(tick);
useInspect.subscribe(tick);
useReplay.subscribe(tick);
useTerminals.subscribe(tick);

/// Boot push (called from main.tsx): hand the persisted toggle to main (which
/// reconciles the ~/.kimi-code/mcp.json entry — this doubles as the per-start
/// refresh of the stable relay copy) and send a first snapshot. Only when ON
/// — off is the default main-side and boot work stays at zero.
export function syncUiContextToMain(): void {
  if (!isShell() || !loadEnabled()) return;
  void invoke('desktopui_set_enabled', { enabled: true }).catch(() => undefined);
  tick();
}
