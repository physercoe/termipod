import { useCallback, useState } from 'react';
import { useT } from '../i18n';
import { useModalA11y } from './useModalA11y';

/// In-app confirmation dialog — a replacement for `window.confirm`, which (like
/// `window.prompt`) renders an unreliable native `tauri.localhost` dialog in the
/// webview. Use where a two-step `ConfirmButton` doesn't fit: context-menu items,
/// `<select>` changes, tab-close buttons.
///
/// `useConfirm()` returns an `ask(opts)` that resolves to `true` (confirmed) or
/// `false` (cancelled), plus a `node` to render once in the component tree. Enter
/// confirms, Escape / backdrop cancels.

interface ConfirmOpts {
  message: string;
  confirmLabel?: string;
  danger?: boolean;
}
interface ConfirmState extends ConfirmOpts {
  resolve: (v: boolean) => void;
}

export function useConfirm(): {
  ask: (opts: ConfirmOpts) => Promise<boolean>;
  node: JSX.Element | null;
} {
  const t = useT();
  const [st, setSt] = useState<ConfirmState | null>(null);
  const modalRef = useModalA11y<HTMLDivElement>(st !== null);

  const ask = useCallback(
    (opts: ConfirmOpts): Promise<boolean> =>
      new Promise<boolean>((resolve) => setSt({ ...opts, resolve })),
    [],
  );

  const close = (result: boolean): void => {
    if (st !== null) st.resolve(result);
    setSt(null);
  };

  const node =
    st === null ? null : (
      <div className="palette-backdrop" onMouseDown={() => close(false)}>
        <div
          ref={modalRef}
          className="prompt-modal"
          role="dialog"
          aria-modal="true"
          aria-label={st.message}
          onMouseDown={(e) => e.stopPropagation()}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.stopPropagation();
              close(true);
            } else if (e.key === 'Escape') {
              e.stopPropagation();
              close(false);
            }
          }}
        >
          <div className="prompt-label">{st.message}</div>
          <div className="prompt-actions">
            <button autoFocus onClick={() => close(false)}>
              {t('common.cancel')}
            </button>
            <span className="spacer" />
            <button className={st.danger === true ? 'danger' : 'primary'} onClick={() => close(true)}>
              {st.confirmLabel ?? t('confirm.confirm')}
            </button>
          </div>
        </div>
      </div>
    );

  return { ask, node };
}
