import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useSession } from '../state/session';
import { AuditConsole } from '../surfaces/AuditConsole';
import { CommandPalette, type Command } from './CommandPalette';

/// The three-region mission-control frame (plan §4): titlebar · Navigator |
/// Focus | Attention dock · statusbar. WS2 wires the Focus region to the audit
/// console; Navigator (WS3) and Attention dock (WS5) are placeholders.
export function AppShell(): JSX.Element {
  const disconnect = useSession((s) => s.disconnect);
  const teamId = useSession((s) => s.config.teamId);
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
    {
      id: 'refresh',
      label: 'Refresh audit feed',
      run: () => void qc.invalidateQueries({ queryKey: ['audit'] }),
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
          <div className="region-header">Navigator</div>
          <div className="region-pad" style={{ color: 'var(--color-text-muted)' }}>
            Fleet &amp; projects tree — WS3.
          </div>
        </div>

        <div className="region focus">
          <div className="region-header">Activity · Audit console</div>
          <AuditConsole />
        </div>

        <div className="region dock">
          <div className="region-header">Attention</div>
          <div className="region-pad" style={{ color: 'var(--color-text-muted)' }}>
            Approvals dock — WS5.
          </div>
        </div>
      </div>

      <div className="statusbar">
        <span>hub · {teamId}</span>
        <span className="spacer" />
        <span>WS2 · read-only shell</span>
      </div>

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
