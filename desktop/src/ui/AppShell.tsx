import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useFocus } from '../state/focus';
import { useSession } from '../state/session';
import { AgentTranscript } from '../surfaces/AgentTranscript';
import { AuditConsole } from '../surfaces/AuditConsole';
import { Navigator } from '../surfaces/Navigator';
import { CommandPalette, type Command } from './CommandPalette';
import { StatusBar } from './StatusBar';

/// The three-region mission-control frame (plan §4): titlebar · Navigator |
/// Focus | Attention dock · status bar. WS3 wires the Navigator (fleet tree) and
/// status counters; WS4 the Focus transcript. The Attention dock is WS5.
export function AppShell(): JSX.Element {
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
  const selectedAgentId = useFocus((s) => s.selectedAgentId);
  const select = useFocus((s) => s.select);
  const qc = useQueryClient();
  const [paletteOpen, setPaletteOpen] = useState(false);

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
    { id: 'audit', label: 'Show activity / audit console', run: () => select(null) },
    {
      id: 'refresh-fleet',
      label: 'Refresh fleet',
      run: () => void qc.invalidateQueries({ queryKey: ['agents'] }),
    },
    { id: 'disconnect', label: 'Disconnect from hub', run: disconnect },
  ];

  return (
    <div className="shell">
      <div className="titlebar">
        <strong>TermiPod</strong>
        <span className="pill">{teamId}</span>
        <span className="spacer" />
        <button onClick={() => setPaletteOpen(true)}>⌘K</button>
      </div>

      <div className="shell-body">
        <div className="region navigator">
          <div className="region-header">Fleet</div>
          <Navigator />
        </div>

        <div className="region focus">
          <div className="region-header">
            {selectedAgentId !== null ? `Agent · ${selectedAgentId}` : 'Activity · Audit console'}
          </div>
          {selectedAgentId !== null ? <AgentTranscript agentId={selectedAgentId} /> : <AuditConsole />}
        </div>

        <div className="region dock">
          <div className="region-header">Attention</div>
          <div className="region-pad muted">Approvals dock — WS5.</div>
        </div>
      </div>

      <StatusBar />

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
