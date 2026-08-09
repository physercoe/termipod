import { type CSSProperties, type ReactNode, type RefObject, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

type Placement = {
  left: number;
  top: number;
  maxHeight: number;
};

/// A header-safe popup menu. It portals to `document.body` so a surface's
/// overflow/stacking context cannot clip it, and owns outside-click + Escape
/// dismissal so every workbench dropdown behaves the same way.
export function PopoverMenu({
  anchorRef,
  open,
  onClose,
  children,
  className = '',
  ariaLabel,
  align = 'end',
}: {
  anchorRef: RefObject<HTMLElement | null>;
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  className?: string;
  ariaLabel?: string;
  align?: 'start' | 'end';
}): JSX.Element | null {
  const menuRef = useRef<HTMLDivElement>(null);
  const [placement, setPlacement] = useState<Placement | null>(null);

  const place = useCallback((): void => {
    const anchor = anchorRef.current;
    const menu = menuRef.current;
    if (anchor === null || menu === null) return;

    const edge = 8;
    const gap = 6;
    const anchorRect = anchor.getBoundingClientRect();
    const menuWidth = menu.offsetWidth;
    const menuHeight = menu.offsetHeight;
    const below = window.innerHeight - anchorRect.bottom - gap - edge;
    const above = anchorRect.top - gap - edge;
    const openAbove = menuHeight > below && above > below;
    const top = openAbove
      ? Math.max(edge, anchorRect.top - gap - menuHeight)
      : Math.min(window.innerHeight - edge, anchorRect.bottom + gap);
    const desiredLeft = align === 'end' ? anchorRect.right - menuWidth : anchorRect.left;
    const left = Math.min(Math.max(edge, desiredLeft), Math.max(edge, window.innerWidth - menuWidth - edge));

    setPlacement({
      left,
      top,
      maxHeight: Math.max(96, openAbove ? above : below),
    });
  }, [align, anchorRef]);

  useLayoutEffect(() => {
    if (!open) {
      setPlacement(null);
      return;
    }
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open, place]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        onClose();
        anchorRef.current?.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [anchorRef, onClose, open]);

  if (!open) return null;

  const style: CSSProperties = {
    position: 'fixed',
    left: placement?.left ?? 0,
    top: placement?.top ?? 0,
    right: 'auto',
    bottom: 'auto',
    maxHeight: placement?.maxHeight,
    visibility: placement === null ? 'hidden' : 'visible',
  };

  return createPortal(
    <>
      <div className="popover-menu-backdrop" onPointerDown={onClose} />
      <div
        ref={menuRef}
        className={`popover-menu${className !== '' ? ` ${className}` : ''}`}
        role="menu"
        aria-label={ariaLabel}
        style={style}
        onPointerDown={(event) => event.stopPropagation()}
      >
        {children}
      </div>
    </>,
    document.body,
  );
}
