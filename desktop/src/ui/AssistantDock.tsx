import { useEffect, useRef } from 'react';
import { useT } from '../i18n';
import { Icon } from './Icon';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { dockHiddenForPhase, useAnnotation } from '../state/annotation';
import { useAssistant } from '../state/assistant';
import { DOCK_COMPANION_KEY } from '../state/companionBinding';
import { activeProvider, useCompanionContext } from '../state/companionContext';
import { useWorkbench } from '../state/workbench';
import { AgentCompanion } from './AgentCompanion';
import { useConfirm } from './ConfirmModal';
import { webPanelById } from './webPanels';
import { WebPanel } from '../surfaces/WebPanel';

/// The unified assistant dock — the terminal dock's shape
/// (terminal/TerminalPanel.tsx dock mode) applied to the assistant: always
/// mounted in the app shell, overlaying the active surface at the bottom or
/// right edge, CSS-hidden (never unmounted) on toggle so the SPA and its
/// backing `kimi web` server keep running like a daemon. Close is an
/// explicit, confirmed action (it stops this dock's hold on the server);
/// detach pops the SPA into its own native window (kimiwebwin.ts) and the dock
/// shows a re-attach placeholder meanwhile.
///
/// TABBED (D2.2): the header's segmented strip switches between the embedded
/// kimi web SPA and the Companion (the dock's one AgentCompanion, fed by the
/// surface context registry — state/companionContext.ts). Both render once
/// `started` and are CSS-hidden by `view`, never unmounted; the kimi
/// lifecycle (detach/attach, confirmed close) applies only to the kimi tab,
/// and the Companion tab stays usable while kimi is detached. While the
/// annotation overlay is armed the dock steps aside via the `annotating`
/// hide class — `open` is untouched, so cancel/target-pick restores it.

const H_KEY = 'termipod.assistant.dockH';
const W_KEY = 'termipod.assistant.dockW';

function loadSize(key: string, fallback: number): number {
  try {
    const n = Number(localStorage.getItem(key));
    return Number.isFinite(n) && n >= 200 ? n : fallback;
  } catch {
    return fallback;
  }
}
function saveSize(key: string, v: number): void {
  try {
    localStorage.setItem(key, String(Math.round(v)));
  } catch {
    /* preference only */
  }
}

export function AssistantDock(): JSX.Element | null {
  const t = useT();
  const shell = isShell();
  const { open, started, detached, dockSide, view, setOpen, setDetached, setDockSide, setView, close } =
    useAssistant();
  // The dock steps aside while the annotation overlay runs (any origin): out
  // of the captured pixels, and an unobstructed selection surface. Derived
  // from the annotation phase — `open` is NOT flipped, so the dock returns
  // exactly as it was on cancel / target pick.
  const annotating = useAnnotation((s) => dockHiddenForPhase(s.phase));
  // The Companion tab's context chip + insert target come from the active
  // surface-registered provider (state/companionContext.ts).
  const provider = useCompanionContext((s) => activeProvider(s));
  const setJob = useWorkbench((s) => s.setJob);
  const confirm = useConfirm();
  const panel = webPanelById('kimi');

  // Size state lives in refs + direct style writes during drag (same approach
  // as the terminal dock) — React state only for the settled value.
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<{ axis: 'x' | 'y'; start: number; startSize: number } | null>(null);
  const sizeRef = useRef({ h: loadSize(H_KEY, 380), w: loadSize(W_KEY, 480) });

  useEffect(() => {
    function onMove(e: MouseEvent): void {
      const d = dragRef.current;
      const el = wrapRef.current;
      if (d === null || el === null) return;
      const delta = d.axis === 'x' ? d.start - e.clientX : d.start - e.clientY;
      const next = Math.max(240, d.startSize + delta);
      if (d.axis === 'x') {
        sizeRef.current.w = next;
        el.style.width = `${next}px`;
      } else {
        sizeRef.current.h = next;
        el.style.height = `${next}px`;
      }
    }
    function onUp(): void {
      if (dragRef.current === null) return;
      dragRef.current = null;
      saveSize(H_KEY, sizeRef.current.h);
      saveSize(W_KEY, sizeRef.current.w);
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, []);

  // While detached, watch for the user closing the native window directly (its
  // X button) — there's no push channel, so poll the main-side status and fold
  // the dock back to its embedded panel when the window is gone.
  useEffect(() => {
    if (!detached || !shell) return;
    const id = setInterval(() => {
      invoke<{ detached: boolean }>('kimiweb_win_status')
        .then((s) => {
          if (!s.detached) setDetached(false);
        })
        .catch(() => undefined);
    }, 3000);
    return () => clearInterval(id);
  }, [detached, shell, setDetached]);

  if (!shell || panel === undefined || !started) return null;

  async function onDetach(): Promise<void> {
    try {
      await invoke('kimiweb_detach');
      setDetached(true); // dock unmounts its panel; the window holds the server
    } catch {
      /* server failed to start — the embedded panel's error state covers it */
    }
  }
  async function onAttach(): Promise<void> {
    try {
      await invoke('kimiweb_attach');
    } catch {
      /* window already gone */
    }
    setDetached(false);
  }
  async function onClose(): Promise<void> {
    const ok = await confirm.ask({
      message: t('assistant.closeConfirm'),
      confirmLabel: t('assistant.closeConfirmBtn'),
      danger: true,
    });
    if (!ok) return;
    if (detached) {
      try {
        await invoke('kimiweb_attach'); // closes the native window → releases its hold
      } catch {
        /* already gone */
      }
    }
    close(); // unmounts the WebPanel → its stop() releases the dock's hold
  }

  const style = dockSide === 'right' ? { width: sizeRef.current.w } : { height: sizeRef.current.h };
  return (
    <div
      ref={wrapRef}
      className={`assistant-dock ${dockSide}${open ? '' : ' hidden'}${annotating ? ' annotating' : ''}`}
      style={style}
    >
      <div
        className="assistant-dock-resize"
        onMouseDown={(e) =>
          (dragRef.current =
            dockSide === 'right'
              ? { axis: 'x', start: e.clientX, startSize: sizeRef.current.w }
              : { axis: 'y', start: e.clientY, startSize: sizeRef.current.h })
        }
      />
      <div className="assistant-dock-head">
        <div className="seg assistant-dock-tabs" role="tablist" aria-label={t('assistant.title')}>
          <button
            role="tab"
            aria-selected={view === 'kimi'}
            className={view === 'kimi' ? 'seg-btn active' : 'seg-btn'}
            onClick={() => setView('kimi')}
          >
            {t('assistant.tabKimi')}
          </button>
          <button
            role="tab"
            aria-selected={view === 'companion'}
            className={view === 'companion' ? 'seg-btn active' : 'seg-btn'}
            onClick={() => setView('companion')}
          >
            {t('assistant.tabCompanion')}
          </button>
        </div>
        <span className="spacer" />
        <button
          className="icon-btn sm"
          title={t('assistant.settings')}
          onClick={() => {
            localStorage.setItem('termipod.settings.cat', 'assistant');
            setJob('settings');
          }}
        >
          <Icon name="sliders" size={13} />
        </button>
        <button
          className="icon-btn sm"
          title={dockSide === 'right' ? t('assistant.dockBottom') : t('assistant.dockRight')}
          onClick={() => setDockSide(dockSide === 'right' ? 'bottom' : 'right')}
        >
          <Icon name={dockSide === 'right' ? 'dock-bottom' : 'dock-right'} size={13} />
        </button>
        {!detached ? (
          <button className="icon-btn sm" title={t('assistant.detach')} onClick={() => void onDetach()}>
            <Icon name="expand" size={13} />
          </button>
        ) : (
          <button className="icon-btn sm" title={t('assistant.attach')} onClick={() => void onAttach()}>
            <Icon name="fit-page" size={13} />
          </button>
        )}
        <button
          className="icon-btn sm"
          title={t('assistant.hide')}
          onClick={() => setOpen(false)}
        >
          <Icon name={dockSide === 'right' ? 'chevron-right' : 'chevron-down'} size={14} />
        </button>
        <button className="icon-btn sm assistant-dock-close" title={t('assistant.close')} onClick={() => void onClose()}>
          <Icon name="close" size={13} />
        </button>
      </div>
      <div className="assistant-dock-body">
        {/* Both tabs stay mounted once `started` (CSS-hidden by `view`) — the
            kimi SPA keeps its server hold, the companion keeps its SSE stream
            and its staged compose-box state. The kimi tab's detach placeholder
            is the kimi lifecycle only; the Companion tab works detached. */}
        <div className={`assistant-dock-pane${view === 'kimi' ? '' : ' hidden'}`}>
          {detached ? (
            <div className="assistant-dock-detached muted">
              <span>{t('assistant.detachedNote')}</span>
              <button className="import-btn" onClick={() => void onAttach()}>
                <Icon name="fit-page" size={13} /> {t('assistant.attach')}
              </button>
            </div>
          ) : (
            <WebPanel panel={panel} />
          )}
        </div>
        <div className={`assistant-dock-pane${view === 'companion' ? '' : ' hidden'}`}>
          <AgentCompanion
            storageKey={DOCK_COMPANION_KEY}
            context={provider ?? undefined}
            onInsert={provider?.insert}
          />
        </div>
      </div>
      {confirm.node}
    </div>
  );
}
