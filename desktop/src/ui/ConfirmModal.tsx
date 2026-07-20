import { useCallback, useState } from 'react';
import { useT } from '../i18n';
import { Modal } from './Modal';

/// In-app confirmation dialog — a replacement for `window.confirm`, which (like
/// `window.prompt`) renders an unreliable native `tauri.localhost` dialog in the
/// webview. Use where a two-step `ConfirmButton` doesn't fit: context-menu items,
/// `<select>` changes, tab-close buttons.
///
/// `useConfirm()` returns an `ask(opts)` that resolves to `true` (confirmed) or
/// `false` (cancelled), plus a `node` to render once in the component tree. Enter
/// confirms, Escape / backdrop cancels. Built on Modal so it joins the modal
/// stack — shown over another Modal (e.g. an editor's dirty-close guard), Escape
/// dismisses the confirm instead of re-arming the parent's close (#313).

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

  const ask = useCallback(
    (opts: ConfirmOpts): Promise<boolean> =>
      new Promise<boolean>((resolve) => setSt({ ...opts, resolve })),
    [],
  );

  const close = useCallback(
    (result: boolean): void => {
      if (st !== null) st.resolve(result);
      setSt(null);
    },
    [st],
  );
  // Modal's Escape/backdrop path — a stable identity so its keydown effect
  // doesn't tear down and re-add the document listener every render (#313).
  const dismiss = useCallback((): void => close(false), [close]);

  const node =
    st === null ? null : (
      <Modal
        onClose={dismiss}
        className="prompt-modal"
        ariaLabel={st.message}
        onKeyDown={(e) => {
          // Enter confirms from anywhere in the dialog — a button's native
          // Enter would instead activate the *focused* one (Cancel autofocuses).
          // Escape is the Modal's job (it stopPropagates at the capture phase).
          if (e.key === 'Enter') {
            e.stopPropagation();
            close(true);
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
      </Modal>
    );

  return { ask, node };
}
