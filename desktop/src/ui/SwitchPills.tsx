import { useEffect, useRef, useState } from 'react';
import { useT } from '../i18n';
import type { SwitchPill } from '../state/runtimeSwitch';
import { anyPillVisible } from '../state/runtimeSwitch';
import { useConfirm } from './ConfirmModal';

/// R6 — the model and permission-mode pills, in the composer's own row.
///
/// They sit beside the context ring for the same reason it does: these are
/// questions asked while composing ("which model is about to answer this?"),
/// not facts read once in a details panel.
///
/// Three renderings, decided by `switchPills()` and never by this component:
///
///   - **readonly** — the value is known and the hub would refuse a change.
///     Shown greyed with the reason on hover. Mobile's picker hides in this
///     case, which leaves a claude session with no model indicator at all;
///     showing what is in effect is honest, and offering a button that can
///     only 422 is not.
///   - **pick** — the agent advertised a list, so a menu.
///   - **type** — switchable with no advertised vocabulary (claude's `--model`
///     takes an alias or a full name and enumerates neither), so a text entry.
///
/// Under IAA a switch on the `respawn` route is previewed before it fires: the
/// hub terminates the agent and spawns a replacement on the same session row,
/// and "my model pill restarted the agent" is not something to discover after
/// the fact. The rpc route changes it in place and needs no such step.

export function SwitchPills({
  pills,
  onPick,
}: {
  pills: readonly SwitchPill[];
  /// Commit a switch. Rejections surface as the pill's error text — the hub
  /// refuses with a sentence, and swallowing it would leave a pill that
  /// silently does nothing.
  onPick: (field: 'mode' | 'model', value: string) => Promise<void>;
}): JSX.Element | null {
  const t = useT();
  const { ask, node: confirmNode } = useConfirm();
  const [open, setOpen] = useState<'mode' | 'model' | null>(null);
  const [typed, setTyped] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const rowRef = useRef<HTMLDivElement>(null);

  // Escape closes the menu, and a click anywhere else does too — a popover
  // that only closes on re-click strands itself over the transcript.
  useEffect(() => {
    if (open === null) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') setOpen(null);
    };
    const onDown = (e: MouseEvent): void => {
      if (rowRef.current !== null && !rowRef.current.contains(e.target as Node)) setOpen(null);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('mousedown', onDown);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('mousedown', onDown);
    };
  }, [open]);

  if (!anyPillVisible(pills)) return null;

  const fieldLabel = (field: 'mode' | 'model'): string =>
    field === 'model' ? t('pills.model') : t('pills.mode');

  async function commit(pill: SwitchPill, value: string): Promise<void> {
    if (value === '' || value === pill.current) {
      setOpen(null);
      return;
    }
    if (pill.respawns) {
      const okToRestart = await ask({
        message: t('pills.respawnWarn')
          .replace('{field}', fieldLabel(pill.field).toLowerCase())
          .replace('{value}', value),
        confirmLabel: t('pills.restartAndSwitch'),
      });
      if (!okToRestart) return;
    }
    setBusy(true);
    setErr(null);
    try {
      await onPick(pill.field, value);
      setOpen(null);
      setTyped('');
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="switch-pills" ref={rowRef}>
      {pills.map((pill) => {
        if (pill.kind === 'hidden') return null;
        const label = pill.currentLabel ?? t('pills.unset');
        const name = fieldLabel(pill.field);
        if (pill.kind === 'readonly') {
          return (
            <span
              key={pill.field}
              className="switch-pill locked"
              title={t('pills.locked').replace('{field}', name.toLowerCase())}
            >
              <span className="switch-pill-key">{name}</span>
              <span className="switch-pill-val">{label}</span>
            </span>
          );
        }
        const isOpen = open === pill.field;
        return (
          <span key={pill.field} className="switch-pill-wrap">
            <button
              className={isOpen ? 'switch-pill open' : 'switch-pill'}
              disabled={busy}
              aria-haspopup={pill.kind === 'pick' ? 'listbox' : 'dialog'}
              aria-expanded={isOpen}
              title={pill.respawns ? t('pills.respawnHint').replace('{field}', name.toLowerCase()) : name}
              onClick={() => {
                setErr(null);
                setTyped(pill.kind === 'type' ? (pill.current ?? '') : '');
                setOpen(isOpen ? null : pill.field);
              }}
            >
              <span className="switch-pill-key">{name}</span>
              <span className="switch-pill-val">{label}</span>
            </button>
            {isOpen && pill.kind === 'pick' && (
              <div className="switch-pop" role="listbox" aria-label={name}>
                {pill.options.map((o) => (
                  <button
                    key={o.id}
                    className={o.id === pill.current ? 'switch-opt current' : 'switch-opt'}
                    role="option"
                    aria-selected={o.id === pill.current}
                    title={o.description}
                    onClick={() => void commit(pill, o.id)}
                  >
                    <span className="switch-opt-name">{o.label}</span>
                    {o.description !== undefined && <span className="switch-opt-desc">{o.description}</span>}
                  </button>
                ))}
              </div>
            )}
            {isOpen && pill.kind === 'type' && (
              <form
                className="switch-pop typed"
                onSubmit={(e) => {
                  e.preventDefault();
                  void commit(pill, typed.trim());
                }}
              >
                <input
                  className="switch-input"
                  value={typed}
                  autoFocus
                  spellCheck={false}
                  placeholder={t('pills.typeHint')}
                  aria-label={name}
                  onChange={(e) => setTyped(e.target.value)}
                />
                <button className="btn small" type="submit" disabled={busy || typed.trim() === ''}>
                  {t('pills.apply')}
                </button>
              </form>
            )}
          </span>
        );
      })}
      {err !== null && <span className="switch-pill-err">{t('pills.failed').replace('{err}', err)}</span>}
      {confirmNode}
    </div>
  );
}
