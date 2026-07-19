import type { KeyboardEvent } from 'react';

/// Keyboard activation for elements that behave like buttons but are rendered as
/// a `<div>` (tree rows, cards, list items where a nested real `<button>` would
/// clash with the row's own click). Pair with `role="button"` and `tabIndex={0}`
/// so the row is focusable and Enter/Space fire it — matching a native button
/// (#312/#316).
export function activateOnKey(fn: () => void): (e: KeyboardEvent) => void {
  return (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      fn();
    }
  };
}
