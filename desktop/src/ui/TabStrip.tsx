import type { ReactNode, KeyboardEvent } from 'react';

/// A keyboard-accessible tab strip (WAI-ARIA tabs pattern, #316). The container
/// is a `role="tablist"`; each button is a `role="tab"` with `aria-selected` and
/// a roving tabIndex (only the active tab is in the tab order); Arrow/Home/End
/// move the selection. Callers keep their own CSS class on the container and get
/// the `active` class on the selected button, so styling is unchanged.
export interface TabDef {
  id: string;
  label: ReactNode;
  /** Optional per-button extra class (kept alongside `active`). */
  className?: string;
}

export function TabStrip({
  tabs,
  active,
  onSelect,
  className,
  ariaLabel,
  vertical = false,
}: {
  tabs: TabDef[];
  active: string;
  onSelect: (id: string) => void;
  className?: string;
  ariaLabel?: string;
  /** Vertical strips use Up/Down as the primary movement keys. */
  vertical?: boolean;
}): JSX.Element {
  const idx = tabs.findIndex((tb) => tb.id === active);
  const move = (delta: number): void => {
    if (tabs.length === 0) return;
    const next = ((idx < 0 ? 0 : idx) + delta + tabs.length) % tabs.length;
    onSelect(tabs[next].id);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>): void => {
    const fwd = vertical ? 'ArrowDown' : 'ArrowRight';
    const back = vertical ? 'ArrowUp' : 'ArrowLeft';
    if (e.key === fwd) {
      e.preventDefault();
      move(1);
    } else if (e.key === back) {
      e.preventDefault();
      move(-1);
    } else if (e.key === 'Home') {
      e.preventDefault();
      onSelect(tabs[0]?.id ?? active);
    } else if (e.key === 'End') {
      e.preventDefault();
      onSelect(tabs[tabs.length - 1]?.id ?? active);
    }
  };
  return (
    <div
      className={className}
      role="tablist"
      aria-label={ariaLabel}
      aria-orientation={vertical ? 'vertical' : undefined}
      onKeyDown={onKeyDown}
    >
      {tabs.map((tb) => {
        const isActive = tb.id === active;
        return (
          <button
            key={tb.id}
            role="tab"
            aria-selected={isActive}
            tabIndex={isActive ? 0 : -1}
            className={`${isActive ? 'active' : ''}${tb.className !== undefined ? ` ${tb.className}` : ''}`.trim() || undefined}
            onClick={() => onSelect(tb.id)}
          >
            {tb.label}
          </button>
        );
      })}
    </div>
  );
}
