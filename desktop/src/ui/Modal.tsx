import { type ReactNode, useEffect } from 'react';
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
  // Escape closes at the capture phase and stops there, so it never also trips a
  // window-level shell listener.
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [onClose]);

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
