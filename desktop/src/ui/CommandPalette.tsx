import { useEffect, useMemo, useState } from 'react';
import { useT } from '../i18n';

export interface Command {
  id: string;
  label: string;
  run: () => void;
}

interface Props {
  open: boolean;
  commands: Command[];
  onClose: () => void;
}

/// Minimal ⌘K command palette — the keyboard spine (WS2 stub; grows as surfaces
/// land). Filter, arrow-navigate, Enter to run, Esc to close.
export function CommandPalette({ open, commands, onClose }: Props): JSX.Element | null {
  const t = useT();
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q === '' ? commands : commands.filter((c) => c.label.toLowerCase().includes(q));
  }, [query, commands]);

  useEffect(() => {
    if (open) {
      setQuery('');
      setActive(0);
    }
  }, [open]);

  useEffect(() => {
    if (active >= filtered.length) setActive(0);
  }, [filtered, active]);

  if (!open) return null;

  function onKeyDown(e: React.KeyboardEvent): void {
    if (e.key === 'Escape') onClose();
    else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, filtered.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === 'Enter') {
      const cmd = filtered[active];
      if (cmd) {
        onClose();
        cmd.run();
      }
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()}>
        <input
          autoFocus
          value={query}
          placeholder={t('palette.placeholder')}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <div>
          {filtered.map((c, i) => (
            <div
              key={c.id}
              className={`palette-item${i === active ? ' active' : ''}`}
              onMouseEnter={() => setActive(i)}
              onMouseDown={() => {
                onClose();
                c.run();
              }}
            >
              {c.label}
            </div>
          ))}
          {filtered.length === 0 && <div className="palette-item">{t('palette.noMatches')}</div>}
        </div>
      </div>
    </div>
  );
}
