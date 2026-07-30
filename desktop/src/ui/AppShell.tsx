import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { useFocus } from '../state/focus';
import { useOnline } from '../state/online';
import { useProxy } from '../state/proxy';
import { vaultStatus, vaultStatusKey } from '../vault/service';
import type { HubProfile } from '../state/profiles';
import { useSession } from '../state/session';
import { formatCombo, matchCombo, useKeybindings } from '../state/keybindings';
import {
  activeJob,
  clampSplitRatio,
  isSplitEligible,
  isSplitVisible,
  JOBS,
  SETTINGS_JOB,
  useWorkbench,
  type JobId,
  type Pane,
} from '../state/workbench';
import { AdminCockpit } from '../surfaces/AdminCockpit';
import { AgentSpawn } from '../surfaces/AgentSpawn';
import { ChannelsPanel } from '../surfaces/ChannelsPanel';
import { DocsPanel } from '../surfaces/DocsPanel';
import { MePanel } from '../surfaces/MePanel';
import { SearchPanel } from '../surfaces/SearchPanel';
import { SessionsPanel } from '../surfaces/SessionsPanel';
import { TerminalPanel } from '../terminal/TerminalPanel';
import { AssistantDock } from './AssistantDock';
import { useAssistant } from '../state/assistant';
import { useTerminals } from '../terminal/store';
import { ActivityBar } from './ActivityBar';
import { AnnotationOverlay } from './AnnotationOverlay';
import { CommandPalette, type Command } from './CommandPalette';
import { ConnectPanel } from './ConnectPanel';
import { ErrorBoundary } from './ErrorBoundary';
import { ProfileSwitcher } from './ProfileSwitcher';
import { ResizeHandle } from './ResizeHandle';
import { StatusBar } from './StatusBar';
import { SurfaceView } from './SurfaceView';
import { ToastHost } from './ToastHost';

/// The three-region mission-control frame (plan §4): titlebar · Navigator |
/// Focus | Attention dock · status bar. WS3 wires the Navigator (fleet tree) and
/// status counters; WS4 the Focus transcript. The Attention dock is WS5.
export function AppShell(): JSX.Element {
  const client = useSession((s) => s.client);
  const disconnect = useSession((s) => s.disconnect);
  const init = useSession((s) => s.init);
  const clear = useFocus((s) => s.clear);
  const job = useWorkbench((s) => s.job);
  const setJob = useWorkbench((s) => s.setJob);
  const secondary = useWorkbench((s) => s.secondary);
  const activePane = useWorkbench((s) => s.activePane);
  const setSecondary = useWorkbench((s) => s.setSecondary);
  const focusPane = useWorkbench((s) => s.focusPane);
  const ratio = useWorkbench((s) => s.ratio);
  const setRatio = useWorkbench((s) => s.setRatio);
  // The pane row, measured to turn the divider's pixel deltas into a ratio.
  const panesRef = useRef<HTMLDivElement>(null);
  const online = useOnline();
  const qc = useQueryClient();
  const t = useT();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [channelsOpen, setChannelsOpen] = useState(false);
  const [docsOpen, setDocsOpen] = useState(false);
  const [meOpen, setMeOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [spawnOpen, setSpawnOpen] = useState(false);
  const [connectOpen, setConnectOpen] = useState(false);
  const [editProfile, setEditProfile] = useState<HubProfile | undefined>(undefined);

  // Auto-bind the active profile on launch; raise the connect overlay only if
  // that leaves us disconnected (no profile / no stored token). Resolve the
  // system/env proxy first (seeded synchronously from cache; this refreshes it)
  // so proxy-routed connections have it before the first hub call.
  useEffect(() => {
    void useProxy.getState().resolveDetected();
    void init().finally(() => {
      if (useSession.getState().client === null) setConnectOpen(true);
    });
  }, [init]);

  // Prime the vault status while the shell is idle so Settings shows it
  // immediately on open (the underlying keychain check is slow, and popping it
  // in a beat late reads as a "splash"). Tauri-only; the query is shared with
  // VaultPanel by key.
  useEffect(() => {
    if (client === null || !isShell()) return;
    void qc.prefetchQuery({
      queryKey: vaultStatusKey(client),
      queryFn: () => vaultStatus(client),
      staleTime: 60_000,
    });
  }, [client, qc]);

  // Close whichever overlay panels are open (Phase 5 polish). The command
  // palette and connect overlay manage their own Escape; this covers the
  // read-panel overlays that otherwise only dismiss on a backdrop click.
  function closeOverlays(): void {
    setAdminOpen(false);
    setSessionsOpen(false);
    setChannelsOpen(false);
    setDocsOpen(false);
    setMeOpen(false);
    setSearchOpen(false);
  }

  useEffect(() => {
    // The rail order (1-based) for Cmd/Ctrl+<n> job switching: the working jobs
    // then the pinned Settings tab, matching the activity bar top-to-bottom.
    const ordered: JobId[] = [...JOBS.map((j) => j.id), SETTINGS_JOB.id];
    function onKey(e: KeyboardEvent): void {
      // The app-level chords come from the rebindable keybindings store (#460) —
      // read live via getState so a rebind takes effect without a remount.
      // Exact-match semantics: extra modifiers break a match.
      const kb = useKeybindings.getState().bindings;
      if (matchCombo(e, kb.palette)) {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      } else if (matchCombo(e, kb.splitToggle)) {
        // Close a live split, or reopen the last pinned job (S2). Inert while the
        // primary is a chrome job — the store owns that rule.
        e.preventDefault();
        useWorkbench.getState().toggleSplit();
      } else if (matchCombo(e, kb.splitSwap)) {
        e.preventDefault();
        useWorkbench.getState().swapPanes();
      } else if (matchCombo(e, kb.terminal)) {
        // VS Code's integrated-terminal toggle. The dock is persistent, so this
        // only shows/hides it — sessions keep running underneath.
        e.preventDefault();
        useTerminals.getState().toggle();
      } else if (matchCombo(e, kb.assistant)) {
        // The app-level assistant dock — same persistent-dock semantics as the
        // terminal: toggling only shows/hides it, the SPA keeps running.
        e.preventDefault();
        useAssistant.getState().toggle();
      } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key >= '1' && e.key <= '9') {
        // VS Code's Cmd/Ctrl+<n> tab jump — switch the active job by rail index.
        const target = ordered[Number(e.key) - 1];
        if (target !== undefined) {
          e.preventDefault();
          useWorkbench.getState().setJob(target);
        }
      } else if (e.key === 'Escape') {
        closeOverlays();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Platform-aware modifier glyph for the palette shortcut hints.
  const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
  const modKey = mac ? '⌘' : 'Ctrl+';
  const bindings = useKeybindings((s) => s.bindings);

  // A Go-to command per job — the palette is the keyboard-first entry to every
  // surface, not just a few overlays (#460). The ⌘<n> hint is COMPUTED from the
  // rail order: the old hardcoded `⌘8`/`⌘9` hints drifted as jobs were added
  // (terminal is actually ⌘9; settings has no digit — it's the 10th slot).
  const goToCommands: Command[] = [...JOBS, SETTINGS_JOB].map((j, i) => ({
    id: `goto-${j.id}`,
    label: `${t('cmd.goto')} ${t(j.labelKey)}`,
    hint: i < 9 ? `${modKey}${i + 1}` : undefined,
    run: () => setJob(j.id),
  }));

  // Split-pane commands (`plans/desktop-shell-split-pane.md` S1): pin an eligible
  // job beside the current one, or close the split. Offered only when they can
  // apply — nothing pairs with a chrome job (terminal/settings are full-surface
  // switches), an already-pinned job can't be pinned twice, and "close" needs a
  // split. A palette entry that silently no-ops is worse than an absent one.
  // S2 adds the rail's Alt-click, the swap command and the shortcuts.
  const panes = { job, secondary, activePane };
  const split = isSplitVisible(panes);
  const splitCommands: Command[] = isSplitEligible(job)
    ? [
        ...JOBS.filter((j) => isSplitEligible(j.id) && j.id !== job && j.id !== secondary).map((j) => ({
          id: `split-open-${j.id}`,
          label: t('cmd.splitOpen').replace('{job}', t(j.labelKey)),
          run: () => setSecondary(j.id),
        })),
        ...(secondary !== null
          ? [
              {
                id: 'split-close',
                label: t('cmd.splitClose'),
                hint: formatCombo(bindings.splitToggle, mac),
                run: () => setSecondary(null),
              },
              {
                id: 'split-swap',
                label: t('cmd.splitSwap'),
                hint: formatCombo(bindings.splitSwap, mac),
                run: () => useWorkbench.getState().swapPanes(),
              },
            ]
          : []),
      ]
    : [];

  const commands: Command[] = [
    ...goToCommands,
    ...splitCommands,
    // "Audit" drops the current tab's focus back to its activity console. Only the
    // Fleet and Projects tabs own a FocusRegion scope; elsewhere it's a no-op. It
    // follows the ACTIVE pane: with a split open, "audit" means the surface the
    // user is working in, not whatever sits on the left.
    {
      id: 'audit',
      label: t('cmd.audit'),
      run: () => {
        const target = activeJob(panes);
        if (target === 'fleet' || target === 'projects') clear(target);
      },
    },
    {
      id: 'refresh-fleet',
      label: t('cmd.refreshFleet'),
      run: () => void qc.invalidateQueries({ queryKey: ['agents'] }),
    },
    {
      id: 'refresh-approvals',
      label: t('cmd.refreshApprovals'),
      run: () => void qc.invalidateQueries({ queryKey: ['attention'] }),
    },
    { id: 'admin', label: t('cmd.admin'), run: () => setAdminOpen(true) },
    { id: 'sessions', label: t('cmd.sessions'), run: () => setSessionsOpen(true) },
    { id: 'channels', label: t('cmd.channels'), run: () => setChannelsOpen(true) },
    { id: 'docs', label: t('cmd.docs'), run: () => setDocsOpen(true) },
    { id: 'me', label: t('cmd.history'), run: () => setMeOpen(true) },
    { id: 'spawn', label: t('spawn.title'), run: () => setSpawnOpen(true) },
    { id: 'search', label: t('cmd.search'), run: () => setSearchOpen(true) },
    { id: 'assistant', label: t('cmd.assistant'), hint: formatCombo(bindings.assistant, mac), run: () => useAssistant.getState().toggle() },
    client === null
      ? { id: 'connect', label: t('shell.connect'), run: () => openConnect() }
      : { id: 'disconnect', label: t('cmd.disconnect'), run: disconnect },
  ];

  function openConnect(edit?: HubProfile): void {
    setEditProfile(edit);
    setConnectOpen(true);
  }

  // The fleet's own action bar. Built here (not in SurfaceView) because every
  // button opens an AppShell-owned overlay; only one fleet pane can exist, so one
  // toolbar node serves both panes.
  const fleetToolbar = (
    <>
      <span className="fleet-toolbar-label">{t('nav.fleet')}</span>
      <span className="fleet-toolbar-sep" />
      <button className="primary" disabled={client === null} onClick={() => setSpawnOpen(true)}>
        {t('spawn.title')}
      </button>
      <button onClick={() => setSessionsOpen(true)}>{t('shell.sessions')}</button>
      <button onClick={() => setChannelsOpen(true)}>{t('shell.channels')}</button>
      <button onClick={() => setSearchOpen(true)}>{t('shell.search')}</button>
      <span className="spacer" />
      <button onClick={() => setMeOpen(true)}>{t('shell.history')}</button>
      <button onClick={() => setAdminOpen(true)}>{t('shell.admin')}</button>
    </>
  );

  // Divider drag → ratio. The handle reports pixel deltas (it is the same
  // window-listener gesture the nav/dock dividers use), so the row's own width
  // converts them; the pixel min-pane clamp needs that width too, which is why it
  // is applied here rather than in the store's plain guard.
  function onSplitResize(dx: number): void {
    const width = panesRef.current?.clientWidth ?? 0;
    if (width <= 0) return;
    // Read the ratio LIVE rather than from this render's closure: pointermove
    // fires faster than React re-renders, and a one-frame-stale base would drop
    // deltas and make the divider lag the cursor. (`usePanelWidth` avoids the
    // same trap with a functional setState.)
    setRatio(clampSplitRatio(useWorkbench.getState().ratio + dx / width, width));
  }

  // One pane. The ErrorBoundary is INSIDE each pane (keyed by job, so switching
  // surfaces resets a caught error) — a crash in the pinned pane leaves the other
  // one working, which the single shell-wide boundary could not do. Focus
  // attribution is capture-phase so it fires before any surface handler; a
  // <webview> guest's own clicks land main-side and never reach here, so a pane
  // whose surface is all webview only becomes active via its chrome.
  function renderPane(pane: Pane, paneJob: JobId): JSX.Element {
    return (
      <section
        className={`shell-pane${split && activePane === pane ? ' active' : ''}`}
        // The ratio is the PRIMARY pane's share; the secondary takes the rest, so
        // one number describes the row (S2). Inline rather than a CSS custom
        // property: a runtime-injected `var()` reads as a phantom token to the
        // design-token ratchet.
        style={split && pane === 'primary' ? { flex: `0 0 ${(ratio * 100).toFixed(3)}%` } : undefined}
        aria-label={t(pane === 'primary' ? 'shell.panePrimary' : 'shell.paneSecondary')}
        data-pane={pane}
        onFocusCapture={() => focusPane(pane)}
        onMouseDownCapture={() => focusPane(pane)}
      >
        <ErrorBoundary key={paneJob} label={paneJob}>
          <SurfaceView job={paneJob} fleetToolbar={fleetToolbar} onConnect={openConnect} />
        </ErrorBoundary>
      </section>
    );
  }

  // Hub identity / connection state — lives at the top of the activity bar (the
  // brand slot) so the hub you're driving is the first thing in the top-left.
  const hubChrome =
    client === null ? (
      <>
        <span className="pill offline">{t('shell.offline')}</span>
        <button className="primary" onClick={() => openConnect()}>
          {t('shell.connect')}
        </button>
      </>
    ) : (
      <ProfileSwitcher onAdd={() => openConnect()} onEdit={(p) => openConnect(p)} />
    );

  // The command palette shortcut stays in the status bar's right end, showing
  // the CURRENT binding (rebindable in Settings → Keyboard, #460).
  const statusChrome = (
    <button className="statusbar-palette" onClick={() => setPaletteOpen(true)} title={t('cmd.palette')}>
      {formatCombo(bindings.palette, mac)}
    </button>
  );

  return (
    <div className="shell">
      {client !== null && !online && (
        <div className="offline-banner" role="status" aria-live="polite">
          {t('shell.offlineBanner')}
        </div>
      )}

      <div className="workbench-row">
        <ActivityBar chrome={hubChrome} />
        <main className="workbench-main">
          {/* The terminal lives in an always-mounted panel (its <Screen>s die if
              unmounted); every other job renders in this stack, which the panel
              overlays in dock mode and replaces in surface mode. */}
          <div className={`surface-stack${job === 'terminal' ? ' hidden' : ''}`}>
            <div ref={panesRef} className={`shell-panes${split ? ' split' : ''}`}>
              {renderPane('primary', job)}
              {split && secondary !== null && (
                <>
                  <ResizeHandle onResize={onSplitResize} />
                  {renderPane('secondary', secondary)}
                </>
              )}
            </div>
          </div>
          <TerminalPanel />
          <AssistantDock />
        </main>
      </div>

      <StatusBar right={statusChrome} />

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
      {adminOpen && <AdminCockpit onClose={() => setAdminOpen(false)} />}
      {sessionsOpen && <SessionsPanel onClose={() => setSessionsOpen(false)} />}
      {channelsOpen && <ChannelsPanel onClose={() => setChannelsOpen(false)} />}
      {docsOpen && <DocsPanel onClose={() => setDocsOpen(false)} />}
      {meOpen && <MePanel onClose={() => setMeOpen(false)} />}
      {searchOpen && <SearchPanel onClose={() => setSearchOpen(false)} />}
      {spawnOpen && <AgentSpawn onClose={() => setSpawnOpen(false)} />}
      {connectOpen && (
        <ConnectPanel
          edit={editProfile}
          onClose={() => {
            setConnectOpen(false);
            setEditProfile(undefined);
          }}
        />
      )}
      <ToastHost />
      {/* D2 annotation overlay — armed by an AgentCompanion's "Ask agent";
          renders only while the UI-context-sharing toggle is on. */}
      <AnnotationOverlay />
    </div>
  );
}
