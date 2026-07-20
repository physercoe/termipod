import { useEffect, useMemo, useRef, useState } from 'react';
import { useT } from '../i18n';
import { useModalA11y } from './useModalA11y';

export interface Command {
  id: string;
  label: string;
  run: () => void;
  /** Optional keyboard-shortcut hint shown right-aligned (e.g. "⌘K"). */
  hint?: string;
}

interface Props {
  open: boolean;
  commands: Command[];
  onClose: () => void;
}

/// Minimal ⌘K command palette — the keyboard spine (WS2 stub; grows as surfaces
/// land). Filter, arrow-navigate, Enter to run, Esc to close. The list is a
/// listbox (role/aria-selected) and keeps the active row scrolled into view
/// (#312/#316). The input follows the WAI-ARIA combobox pattern: focus stays in
/// the textbox and `aria-activedescendant` points at the active option's stable
/// id (#313).
export function CommandPalette({ open, commands, onClose }: Props): JSX.Element | null {
  const t = useT();
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  const modalRef = useModalA11y<HTMLDivElement>(open);

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

  // Keep the highlighted row visible as arrow-navigation moves past the fold.
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('.palette-item.active');
    el?.scrollIntoView({ block: 'nearest' });
  }, [active, filtered]);

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
      <div ref={modalRef} className="palette" role="dialog" aria-modal="true" aria-label={t('cmd.palette')} onMouseDown={(e) => e.stopPropagation()}>
        <input
          autoFocus
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={filtered[active] !== undefined ? `palette-opt-${filtered[active].id}` : undefined}
          value={query}
          placeholder={t('palette.placeholder')}
          aria-label={t('palette.placeholder')}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <div ref={listRef} id="palette-list" role="listbox" aria-label={t('cmd.palette')}>
          {filtered.map((c, i) => (
            <div
              key={c.id}
              id={`palette-opt-${c.id}`}
              role="option"
              aria-selected={i === active}
              className={`palette-item${i === active ? ' active' : ''}`}
              onMouseEnter={() => setActive(i)}
              onMouseDown={() => {
                onClose();
                c.run();
              }}
            >
              <span className="palette-item-label">{c.label}</span>
              {c.hint !== undefined && <span className="palette-item-hint">{c.hint}</span>}
            </div>
          ))}
          {filtered.length === 0 && <div className="palette-item">{t('palette.noMatches')}</div>}
        </div>
      </div>
    </div>
  );
}
