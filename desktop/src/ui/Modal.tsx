import { type ReactNode, useEffect, useRef } from 'react';
import { useModalA11y } from './useModalA11y';

/// The one real modal primitive (#313). Wraps the app's `palette-backdrop` idiom
/// with proper dialog semantics so every modal gets the same behaviour instead of
/// re-implementing it (inconsistently) each time:
///   • role="dialog" + aria-modal, labelled by `ariaLabel`
///   • focus trap + focus restore + background scroll lock (useModalA11y)
///   • Escape closes — and the handler stopPropagation()s so it can't ALSO reach
///     a shell-level window listener (the SessionsPanel bug: Esc cancelled a
///     rename AND closed the whole panel)
///   • backdrop mousedown closes; a mousedown inside does not
///
/// The caller mounts/unmounts the Modal (usually `{open && <Modal …/>}`), so it is
/// always active while rendered. `onClose` fires for Escape or a backdrop click.

/// Mounted modals, in mount order — see the Escape handler below for why (#313).
const modalStack: symbol[] = [];
export function Modal({
  onClose,
  className,
  ariaLabel,
  children,
  closeOnBackdrop = true,
}: {
  onClose: () => void;
  className?: string;
  ariaLabel?: string;
  children: ReactNode;
  /** Set false for editors that must confirm before discarding (dirty guards). */
  closeOnBackdrop?: boolean;
}): JSX.Element {
  const ref = useModalA11y<HTMLDivElement>(true);
  // Dialogs can nest (e.g. the document composer opens inside the Docs panel),
  // and every Modal listens at document capture — stopPropagation can't reach a
  // *sibling* listener on the same node, so without this stack one Escape would
  // close every layer at once. Only the topmost (last-mounted) Modal answers
  // (#313).
  const id = useRef(Symbol('modal')).current;
  useEffect(() => {
    modalStack.push(id);
    return () => {
      const i = modalStack.indexOf(id);
      if (i !== -1) modalStack.splice(i, 1);
    };
  }, [id]);
  // Escape closes at the capture phase and stops there, so it never also trips a
  // window-level shell listener.
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape' && modalStack[modalStack.length - 1] === id) {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [onClose, id]);

  return (
    <div className="palette-backdrop" onMouseDown={closeOnBackdrop ? onClose : undefined}>
      <div
        ref={ref}
        className={className}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
        onMouseDown={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
