import { useCallback, useState } from 'react';
import { useT } from '../i18n';
import { Modal } from './Modal';

/// In-app text prompt — a replacement for `window.prompt`, which in the Tauri
/// WebView2 renders as a native "tauri.localhost says…" dialog (it stamps the
/// webview origin on the chrome) and is unreliable / can be blocked outright.
/// The director hit exactly this: a tmux "new session" prompt showed
/// `tauri.localhost` and the op silently failed.
///
/// `useTextPrompt()` returns an `ask(label, initial?)` that resolves to the
/// entered string (or null if cancelled), plus a `node` to render once in the
/// component tree. Enter submits, Escape / backdrop cancels.

interface PromptState {
  label: string;
  value: string;
  resolve: (v: string | null) => void;
}

export function useTextPrompt(): {
  ask: (label: string, initial?: string) => Promise<string | null>;
  node: JSX.Element | null;
} {
  const t = useT();
  const [st, setSt] = useState<PromptState | null>(null);

  const ask = useCallback(
    (label: string, initial = ''): Promise<string | null> =>
      new Promise<string | null>((resolve) => setSt({ label, value: initial, resolve })),
    [],
  );

  const close = (result: string | null): void => {
    if (st !== null) st.resolve(result);
    setSt(null);
  };

  const node =
    st === null ? null : (
      <Modal onClose={() => close(null)} className="prompt-modal" ariaLabel={st.label}>
        <label className="prompt-label">{st.label}</label>
        <input
          autoFocus
          value={st.value}
          spellCheck={false}
          onChange={(e) => setSt({ ...st, value: e.target.value })}
          onKeyDown={(e) => {
            // Stop Enter from bubbling to a global shell listener that would also
            // act on it (e.g. AppShell's window-level handlers). Escape is the
            // Modal's job — it stopPropagates at the capture phase.
            if (e.key === 'Enter') {
              e.stopPropagation();
              close(st.value);
            }
          }}
        />
        <div className="prompt-actions">
          <button onClick={() => close(null)}>{t('common.cancel')}</button>
          <span className="spacer" />
          <button className="primary" onClick={() => close(st.value)}>
            {t('common.ok')}
          </button>
        </div>
      </Modal>
    );

  return { ask, node };
}
