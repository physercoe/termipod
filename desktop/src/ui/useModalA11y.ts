import { useEffect, useRef, type RefObject } from 'react';

/// Shared modal accessibility (#313): while `active`, lock background scroll, trap
/// Tab focus inside the modal, and restore focus to the previously-focused
/// element when it closes. Attach the returned ref to the modal's content element
/// (the one inside the backdrop). Pair with role="dialog" aria-modal="true".
export function useModalA11y<T extends HTMLElement>(active: boolean): RefObject<T> {
  const ref = useRef<T>(null);
  useEffect(() => {
    if (!active) return;
    const node = ref.current;
    const prevFocus = document.activeElement as HTMLElement | null;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const focusables = (): HTMLElement[] =>
      node === null
        ? []
        : Array.from(
            node.querySelectorAll<HTMLElement>(
              'a[href],button:not([disabled]),input:not([disabled]),textarea:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex="-1"])',
            ),
          ).filter((el) => el.offsetParent !== null);

    // Move focus into the modal if it isn't already there (autoFocus usually
    // handles this, but the container is the fallback).
    if (node !== null && !node.contains(document.activeElement)) {
      (focusables()[0] ?? node).focus();
    }

    function onKey(e: KeyboardEvent): void {
      if (e.key !== 'Tab' || node === null) return;
      const list = focusables();
      if (list.length === 0) {
        e.preventDefault();
        node.focus();
        return;
      }
      const first = list[0];
      const last = list[list.length - 1];
      const activeEl = document.activeElement;
      if (e.shiftKey && (activeEl === first || activeEl === node)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && activeEl === last) {
        e.preventDefault();
        first.focus();
      }
    }

    document.addEventListener('keydown', onKey, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.body.style.overflow = prevOverflow;
      prevFocus?.focus?.();
    };
  }, [active]);
  return ref;
}
