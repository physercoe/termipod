import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useFocus } from '../state/focus';
import { useOnline } from '../state/online';
import type { HubProfile } from '../state/profiles';
import { useSession } from '../state/session';
import { AdminCockpit } from '../surfaces/AdminCockpit';
import { AgentTranscript } from '../surfaces/AgentTranscript';
import { AttentionDock } from '../surfaces/AttentionDock';
import { AuditConsole } from '../surfaces/AuditConsole';
import { ChannelsPanel } from '../surfaces/ChannelsPanel';
import { DocsPanel } from '../surfaces/DocsPanel';
import { InsightsPanel } from '../surfaces/InsightsPanel';
import { MePanel } from '../surfaces/MePanel';
import { Navigator } from '../surfaces/Navigator';
import { ProjectBoard } from '../surfaces/ProjectBoard';
import { SearchPanel } from '../surfaces/SearchPanel';
import { SessionsPanel } from '../surfaces/SessionsPanel';
import { Settings } from '../surfaces/Settings';
import { Terminal } from '../surfaces/Terminal';
import { CommandPalette, type Command } from './CommandPalette';
import { ConnectPanel } from './ConnectPanel';
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
  const online = useOnline();
  const qc = useQueryClient();
  const t = useT();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [terminalOpen, setTerminalOpen] = useState(false);
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

  // Close whichever overlay panels are open (Phase 5 polish). The command
  // palette and connect overlay manage their own Escape; this covers the
  // read-panel overlays that otherwise only dismiss on a backdrop click.
  function closeOverlays(): void {
    setAdminOpen(false);
    setSettingsOpen(false);
    setTerminalOpen(false);
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
    { id: 'terminal', label: t('cmd.terminal'), run: () => setTerminalOpen(true) },
    { id: 'settings', label: t('cmd.settings'), run: () => setSettingsOpen(true) },
    client === null
      ? { id: 'connect', label: t('shell.connect'), run: () => openConnect() }
      : { id: 'disconnect', label: t('cmd.disconnect'), run: disconnect },
  ];

  function openConnect(edit?: HubProfile): void {
    setEditProfile(edit);
    setConnectOpen(true);
  }

  return (
    <div className="shell">
      <div className="titlebar">
        <strong>TermiPod</strong>
        {client === null ? (
          <>
            <span className="pill offline">{t('shell.offline')}</span>
            <button className="primary" onClick={() => openConnect()}>
              {t('shell.connect')}
            </button>
          </>
        ) : (
          <ProfileSwitcher onAdd={() => openConnect()} onEdit={(p) => openConnect(p)} />
        )}
        <span className="spacer" />
        <button onClick={() => setInsightsOpen(true)}>{t('shell.insights')}</button>
        <button onClick={() => setSearchOpen(true)}>{t('shell.search')}</button>
        <button onClick={() => setMeOpen(true)}>{t('shell.me')}</button>
        <button onClick={() => setAdminOpen(true)}>{t('shell.admin')}</button>
        <button onClick={() => setSessionsOpen(true)}>{t('shell.sessions')}</button>
        <button onClick={() => setChannelsOpen(true)}>{t('shell.channels')}</button>
        <button onClick={() => setTerminalOpen(true)}>{t('shell.terminal')}</button>
        <button onClick={() => setSettingsOpen(true)}>{t('shell.settings')}</button>
        <button onClick={() => setPaletteOpen(true)}>⌘K</button>
      </div>

      {client !== null && !online && <div className="offline-banner">{t('shell.offlineBanner')}</div>}

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
                : t('region.activity')}
          </div>
          {selection?.type === 'agent' ? (
            <AgentTranscript agentId={selection.id} />
          ) : selection?.type === 'project' ? (
            <ProjectBoard projectId={selection.id} />
          ) : (
            <AuditConsole />
          )}
        </div>

        <div className="region dock">
          <div className="region-header">{t('region.attention')}</div>
          <AttentionDock />
        </div>
      </div>

      <StatusBar />

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
      {adminOpen && <AdminCockpit onClose={() => setAdminOpen(false)} />}
      {terminalOpen && <Terminal onClose={() => setTerminalOpen(false)} />}
      {sessionsOpen && <SessionsPanel onClose={() => setSessionsOpen(false)} />}
      {channelsOpen && <ChannelsPanel onClose={() => setChannelsOpen(false)} />}
      {insightsOpen && <InsightsPanel onClose={() => setInsightsOpen(false)} />}
      {docsOpen && <DocsPanel onClose={() => setDocsOpen(false)} />}
      {meOpen && <MePanel onClose={() => setMeOpen(false)} />}
      {searchOpen && <SearchPanel onClose={() => setSearchOpen(false)} />}
      {settingsOpen && <Settings onClose={() => setSettingsOpen(false)} />}
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
