import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { AdminCockpit } from '../surfaces/AdminCockpit';
import { AgentTranscript } from '../surfaces/AgentTranscript';
import { AttentionDock } from '../surfaces/AttentionDock';
import { AuditConsole } from '../surfaces/AuditConsole';
import { Navigator } from '../surfaces/Navigator';
import { ProjectBoard } from '../surfaces/ProjectBoard';
import { CommandPalette, type Command } from './CommandPalette';
import { StatusBar } from './StatusBar';

/// The three-region mission-control frame (plan §4): titlebar · Navigator |
/// Focus | Attention dock · status bar. WS3 wires the Navigator (fleet tree) and
/// status counters; WS4 the Focus transcript. The Attention dock is WS5.
export function AppShell(): JSX.Element {
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
  const selection = useFocus((s) => s.selection);
  const clear = useFocus((s) => s.clear);
  const qc = useQueryClient();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const commands: Command[] = [
    { id: 'audit', label: 'Show activity / audit console', run: () => clear() },
    {
      id: 'refresh-fleet',
      label: 'Refresh fleet',
      run: () => void qc.invalidateQueries({ queryKey: ['agents'] }),
    },
    {
      id: 'refresh-approvals',
      label: 'Refresh approvals',
      run: () => void qc.invalidateQueries({ queryKey: ['attention'] }),
    },
    { id: 'admin', label: 'Open admin & governance', run: () => setAdminOpen(true) },
    { id: 'disconnect', label: 'Disconnect from hub', run: disconnect },
  ];

  return (
    <div className="shell">
      <div className="titlebar">
        <strong>TermiPod</strong>
        <span className="pill">{teamId}</span>
        <span className="spacer" />
        <button onClick={() => setAdminOpen(true)}>Admin</button>
        <button onClick={() => setPaletteOpen(true)}>⌘K</button>
      </div>

      <div className="shell-body">
        <div className="region navigator">
          <div className="region-header">Fleet</div>
          <Navigator />
        </div>

        <div className="region focus">
          <div className="region-header">
            {selection?.type === 'agent'
              ? `Agent · ${selection.id}`
              : selection?.type === 'project'
                ? `Project · ${selection.id}`
                : 'Activity · Audit console'}
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
          <div className="region-header">Attention</div>
          <AttentionDock />
        </div>
      </div>

      <StatusBar />

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
      {adminOpen && <AdminCockpit onClose={() => setAdminOpen(false)} />}
    </div>
  );
}
