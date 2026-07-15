import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { useFocus } from '../state/focus';
import { useOnline } from '../state/online';
import { vaultStatus, vaultStatusKey } from '../vault/service';
import type { HubProfile } from '../state/profiles';
import { useSession } from '../state/session';
import { useWorkbench } from '../state/workbench';
import { AdminCockpit } from '../surfaces/AdminCockpit';
import { AgentTranscript } from '../surfaces/AgentTranscript';
import { AttentionDock } from '../surfaces/AttentionDock';
import { AuditConsole } from '../surfaces/AuditConsole';
import { AuthorSurface } from '../surfaces/AuthorSurface';
import { CanvasSurface } from '../surfaces/CanvasSurface';
import { ChannelsPanel } from '../surfaces/ChannelsPanel';
import { CompareSurface } from '../surfaces/CompareSurface';
import { DebugSurface } from '../surfaces/DebugSurface';
import { DocsPanel } from '../surfaces/DocsPanel';
import { HostBoard } from '../surfaces/HostBoard';
import { InsightsPanel } from '../surfaces/InsightsPanel';
import { MePanel } from '../surfaces/MePanel';
import { Navigator } from '../surfaces/Navigator';
import { ProjectBoard } from '../surfaces/ProjectBoard';
import { ReadSurface } from '../surfaces/ReadSurface';
import { RecordSurface } from '../surfaces/RecordSurface';
import { SearchPanel } from '../surfaces/SearchPanel';
import { SessionsPanel } from '../surfaces/SessionsPanel';
import { SettingsSurface } from '../surfaces/Settings';
import { TerminalPanel } from '../terminal/TerminalPanel';
import { useTerminals } from '../terminal/store';
import { ActivityBar } from './ActivityBar';
import { CommandPalette, type Command } from './CommandPalette';
import { ConnectPanel } from './ConnectPanel';
import { ErrorBoundary } from './ErrorBoundary';
import { ProfileSwitcher } from './ProfileSwitcher';
import { StatusBar } from './StatusBar';

/// The three-region mission-control frame (plan §4): titlebar · Navigator |
/// Focus | Attention dock · status bar. WS3 wires the Navigator (fleet tree) and
/// status counters; WS4 the Focus transcript. The Attention dock is WS5.
export function AppShell(): JSX.Element {
  const client = useSession((s) => s.client);
  const disconnect = useSession((s) => s.disconnect);
  const init = useSession((s) => s.init);
  const selection = useFocus((s) => s.selection);
  const clear = useFocus((s) => s.clear);
  const job = useWorkbench((s) => s.job);
  const setJob = useWorkbench((s) => s.setJob);
  const online = useOnline();
  const qc = useQueryClient();
  const t = useT();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [channelsOpen, setChannelsOpen] = useState(false);
  const [insightsOpen, setInsightsOpen] = useState(false);
  const [docsOpen, setDocsOpen] = useState(false);
  const [meOpen, setMeOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [connectOpen, setConnectOpen] = useState(false);
  const [editProfile, setEditProfile] = useState<HubProfile | undefined>(undefined);

  // Auto-bind the active profile on launch; raise the connect overlay only if
  // that leaves us disconnected (no profile / no stored token).
  useEffect(() => {
    void init().finally(() => {
      if (useSession.getState().client === null) setConnectOpen(true);
    });
  }, [init]);

  // Prime the vault status while the shell is idle so Settings shows it
  // immediately on open (the underlying keychain check is slow, and popping it
  // in a beat late reads as a "splash"). Tauri-only; the query is shared with
  // VaultPanel by key.
  useEffect(() => {
    if (client === null || !isTauri()) return;
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
    setInsightsOpen(false);
    setDocsOpen(false);
    setMeOpen(false);
    setSearchOpen(false);
  }

  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      } else if ((e.metaKey || e.ctrlKey) && e.key === '`') {
        // VS Code's integrated-terminal toggle. The dock is persistent, so this
        // only shows/hides it — sessions keep running underneath.
        e.preventDefault();
        useTerminals.getState().toggle();
      } else if (e.key === 'Escape') {
        closeOverlays();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const commands: Command[] = [
    { id: 'audit', label: t('cmd.audit'), run: () => clear() },
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
    { id: 'insights', label: t('cmd.insights'), run: () => setInsightsOpen(true) },
    { id: 'docs', label: t('cmd.docs'), run: () => setDocsOpen(true) },
    { id: 'me', label: t('cmd.me'), run: () => setMeOpen(true) },
    { id: 'search', label: t('cmd.search'), run: () => setSearchOpen(true) },
    { id: 'terminal', label: t('cmd.terminal'), run: () => setJob('terminal') },
    { id: 'settings', label: t('cmd.settings'), run: () => setJob('settings') },
    client === null
      ? { id: 'connect', label: t('shell.connect'), run: () => openConnect() }
      : { id: 'disconnect', label: t('cmd.disconnect'), run: disconnect },
  ];

  function openConnect(edit?: HubProfile): void {
    setEditProfile(edit);
    setConnectOpen(true);
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

  // The command palette shortcut stays in the status bar's right end.
  const statusChrome = (
    <button className="statusbar-palette" onClick={() => setPaletteOpen(true)} title={t('cmd.palette')}>
      ⌘K
    </button>
  );

  return (
    <div className="shell">
      {client !== null && !online && <div className="offline-banner">{t('shell.offlineBanner')}</div>}

      <div className="workbench-row">
        <ActivityBar chrome={hubChrome} />
        <main className="workbench-main">
          {/* The terminal lives in an always-mounted panel (its <Screen>s die if
              unmounted); every other job renders in this stack, which the panel
              overlays in dock mode and replaces in surface mode. */}
          <div className={`surface-stack${job === 'terminal' ? ' hidden' : ''}`}>
          <ErrorBoundary key={job} label={job}>
          {job === 'fleet' ? (
            <>
              <div className="fleet-toolbar">
                <span className="fleet-toolbar-label">{t('nav.fleet')}</span>
                <span className="fleet-toolbar-sep" />
                <button onClick={() => setSessionsOpen(true)}>{t('shell.sessions')}</button>
                <button onClick={() => setChannelsOpen(true)}>{t('shell.channels')}</button>
                <button onClick={() => setInsightsOpen(true)}>{t('shell.insights')}</button>
                <button onClick={() => setSearchOpen(true)}>{t('shell.search')}</button>
                <span className="spacer" />
                <button onClick={() => setMeOpen(true)}>{t('shell.me')}</button>
                <button onClick={() => setAdminOpen(true)}>{t('shell.admin')}</button>
              </div>
              <div className="shell-body">
                <div className="region navigator">
                  <div className="region-header">{t('nav.fleet')}</div>
                  <Navigator />
                </div>

              <div className="region focus">
                <div className="region-header">
                  {selection?.type === 'agent'
                    ? `${t('region.agent')} · ${selection.id}`
                    : selection?.type === 'project'
                      ? `${t('region.project')} · ${selection.id}`
                      : selection?.type === 'host'
                        ? `${t('region.host')} · ${selection.id}`
                        : t('region.activity')}
                </div>
                {selection?.type === 'agent' ? (
                  <AgentTranscript agentId={selection.id} />
                ) : selection?.type === 'project' ? (
                  <ProjectBoard projectId={selection.id} />
                ) : selection?.type === 'host' ? (
                  <HostBoard hostId={selection.id} />
                ) : (
                  <AuditConsole />
                )}
              </div>

                <div className="region dock">
                  <div className="region-header">{t('region.attention')}</div>
                  <AttentionDock />
                </div>
              </div>
            </>
          ) : job === 'read' ? (
            <ReadSurface />
          ) : job === 'author' ? (
            <AuthorSurface />
          ) : job === 'debug' ? (
            <DebugSurface />
          ) : job === 'canvas' ? (
            <CanvasSurface />
          ) : job === 'compare' ? (
            <CompareSurface />
          ) : job === 'record' ? (
            <RecordSurface />
          ) : job === 'settings' ? (
            <SettingsSurface onConnect={openConnect} />
          ) : null /* terminal → the always-mounted TerminalPanel below */}
          </ErrorBoundary>
          </div>
          <TerminalPanel />
        </main>
      </div>

      <StatusBar right={statusChrome} />

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
      {adminOpen && <AdminCockpit onClose={() => setAdminOpen(false)} />}
      {sessionsOpen && <SessionsPanel onClose={() => setSessionsOpen(false)} />}
      {channelsOpen && <ChannelsPanel onClose={() => setChannelsOpen(false)} />}
      {insightsOpen && <InsightsPanel onClose={() => setInsightsOpen(false)} />}
      {docsOpen && <DocsPanel onClose={() => setDocsOpen(false)} />}
      {meOpen && <MePanel onClose={() => setMeOpen(false)} />}
      {searchOpen && <SearchPanel onClose={() => setSearchOpen(false)} />}
      {connectOpen && (
        <ConnectPanel
          edit={editProfile}
          onClose={() => {
            setConnectOpen(false);
            setEditProfile(undefined);
          }}
        />
      )}
    </div>
  );
}
