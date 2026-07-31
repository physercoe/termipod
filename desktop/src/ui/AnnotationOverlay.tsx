import { useCallback, useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { useAnnotation, resolveTargets, type AnnotationCapture } from '../state/annotation';
import { kimiAttachable, useAssistant } from '../state/assistant';
import { toast } from '../state/toast';
import { useUiContext } from '../state/uiContext';
import { appendPointerNote, formatPointerLabel, type UiPointer } from '../state/uiPointer';
import { uiPolicyFor } from '../state/ui_policy';
import { isSplitVisible, useWorkbench } from '../state/workbench';
import { Icon } from './Icon';

/// The annotation overlay (D2 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4): the user→agent pointing gesture, mounted once at the app shell.
///
/// Guest coverage. `<webview>` guests are OOPIF-composited INSIDE the shell's
/// frame tree, so this fixed shell-DOM layer paints over them AND wins the
/// hit-test — a drag over a guest still delivers every pointer event here
/// (verified on Electron 43 with trusted CDP input). The overlay therefore
/// captures the selection in window CSS px for the whole window, and reports
/// the guest element rects (paired with `getWebContentsId()`) so main can
/// capture the rect from the right webContents. No guest-side preload or
/// per-guest iframe is needed — and none would be allowed (webtab.ts strips
/// preloads).
///
/// Flow (plan §3.4 steps 2-4): armed by the dock AgentCompanion's "Ask agent"
/// button, or GLOBALLY by the status-bar chip / palette entry (D2.1 — no
/// companion origin) → drag a rect (Esc cancels: nothing attached, no event,
/// no audit) → `annotation_capture` (a rect fully inside a capture:refuse
/// region — the Settings pane, which also holds the vault — is refused with a
/// hint and the user re-selects) → the target row: "Attach to kimi web" first
/// when the kimi guest is mounted + embedded (started && !detached — the dock
/// may be hidden), then "Send to <agent>" (the arming companion when bound;
/// for a global arm the first registered bound companion), an optional
/// one-line note, Cancel (deletes the temp crop). While armed, the assistant
/// dock steps aside (the `annotating` CSS hide) without flipping its `open`
/// state; a kimi attach reveals the dock on the kimi tab, a companion handoff
/// on the Companion tab (D2.2).

interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface CaptureResponse {
  ok: boolean;
  refused?: boolean;
  surface?: string;
  error?: string;
  file?: string;
  width?: number;
  height?: number;
  preview?: string;
  data_b64?: string;
  target?: 'shell' | 'guest';
  /// D4: the element resolved under the crop's centre, when the rect landed
  /// on a bridgeable guest. Absent over the shell, or when the page had
  /// nothing to name.
  pointer?: UiPointer;
}

interface AttachResponse {
  ok: boolean;
  injected?: boolean;
  /// Whether the note (user words + pointer line) landed in the composer's
  /// text box. Absent when no note was sent; false = the crop attached but
  /// the user must type the note themselves.
  note_injected?: boolean;
  fallback?: string;
  error?: string;
}

/// A drag under this edge counts as a click — it cancels the gesture (the
/// mirror of annotation.ts's MIN_SELECTION_EDGE).
const MIN_DRAG = 4;

interface WebviewLike extends HTMLElement {
  getWebContentsId?: () => number;
}

/// The `<webview>` guests the renderer knows, paired with their webContents
/// ids. `visibility` is checked, not just geometry: the hidden assistant dock
/// keeps its guest mounted (visibility:hidden, never display:none — the
/// white-screen hazard), and an invisible guest must not claim the capture of
/// the content painted beneath it.
function collectGuests(): Array<{ id: number; rect: Rect }> {
  const out: Array<{ id: number; rect: Rect }> = [];
  for (const el of Array.from(document.querySelectorAll('webview'))) {
    const wv = el as WebviewLike;
    const id = wv.getWebContentsId?.();
    if (typeof id !== 'number') continue;
    if (window.getComputedStyle(el).visibility === 'hidden') continue;
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) continue;
    out.push({ id, rect: { x: r.left, y: r.top, width: r.width, height: r.height } });
  }
  return out;
}

/// The visible panes whose surface row says `capture: refuse` (the geometry
/// half of the refusal; main re-applies the policy table to the surface ids).
/// Today that is the Settings surface (which also hosts the vault) — and it
/// is never split-eligible, so only the primary pane can carry it.
function collectSurfaceRegions(): Array<{ surface: string; rect: Rect }> {
  const out: Array<{ surface: string; rect: Rect }> = [];
  const wb = useWorkbench.getState();
  const panes: Array<[string, string | null]> = [
    ['primary', wb.job],
    ['secondary', isSplitVisible(wb) ? wb.secondary : null],
  ];
  for (const [pane, job] of panes) {
    if (job === null || uiPolicyFor(job)?.capture !== 'refuse') continue;
    const el = document.querySelector(`.shell-pane[data-pane="${pane}"]`);
    if (el === null) continue;
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0) out.push({ surface: job, rect: { x: r.left, y: r.top, width: r.width, height: r.height } });
  }
  return out;
}

/// The kimi-web dock's guest, when the panel is mounted and embedded (not
/// detached to its own window). The dock need not be OPEN — its kimi tab
/// hides (visibility) but never unmounts, so the guest keeps its webContents
/// id and main can inject into it; a successful attach then reveals the dock
/// on the kimi tab. The target row offers "Attach to kimi web" first —
/// plan §3.4 step 4.
function kimiGuestId(): number | null {
  const el = document.querySelector('.assistant-dock webview');
  if (el === null) return null;
  const id = (el as WebviewLike).getWebContentsId?.();
  return typeof id === 'number' ? id : null;
}

export function AnnotationOverlay(): JSX.Element | null {
  const t = useT();
  const sharing = useUiContext((s) => s.enabled);
  const phase = useAnnotation((s) => s.phase);
  const origin = useAnnotation((s) => s.origin);
  const capture = useAnnotation((s) => s.capture);
  const refused = useAnnotation((s) => s.refused);
  const cancel = useAnnotation((s) => s.cancel);
  const discard = useAnnotation((s) => s.discard);
  const setRefused = useAnnotation((s) => s.setRefused);
  const captured = useAnnotation((s) => s.captured);
  const handOffToCompanion = useAnnotation((s) => s.handOffToCompanion);
  const companions = useAnnotation((s) => s.companions);
  // Kimi attach availability (D2.2 amendment): guest mounted (started) and
  // embedded — the dock may be hidden or showing the Companion tab; a
  // successful attach reveals it on the kimi tab.
  const kimiReady = useAssistant(kimiAttachable);
  const revealAssistant = useAssistant((s) => s.reveal);

  const [drag, setDrag] = useState<{ x1: number; y1: number; x2: number; y2: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState('');

  // Per-arm reset: a new gesture starts with no drag and no leftover note.
  useEffect(() => {
    if (phase === 'selecting') {
      setDrag(null);
      setBusy(false);
    }
    if (phase === 'target') setNote('');
  }, [phase]);

  // Toggle off mid-gesture: disarm rather than leave the store armed behind an
  // unmounted overlay (main refuses captures anyway — this keeps the renderer
  // state honest and reaps a target-phase temp crop via discard).
  useEffect(() => {
    if (sharing || phase === 'idle') return;
    if (phase === 'target') discard();
    else cancel();
  }, [sharing, phase, discard, cancel]);

  const finish = useCallback(
    (rect: Rect): void => {
      setDrag(null);
      if (rect.width < MIN_DRAG || rect.height < MIN_DRAG || busy) {
        if (!busy) cancel();
        return;
      }
      setBusy(true);
      invoke<CaptureResponse>('annotation_capture', {
        rect,
        guests: collectGuests(),
        surface_regions: collectSurfaceRegions(),
      })
        .then((r) => {
          if (r.ok === true && r.refused !== true && typeof r.file === 'string') {
            captured({
              file: r.file,
              preview: r.preview ?? '',
              dataB64: r.data_b64 ?? '',
              width: r.width ?? 0,
              height: r.height ?? 0,
              target: r.target ?? 'shell',
              ...(r.pointer !== undefined ? { pointer: r.pointer } : {}),
            } satisfies AnnotationCapture);
            return;
          }
          if (r.refused === true) {
            // Entirely inside a refuse region (plan §3.4): hint + re-select.
            setRefused(r.surface ?? 'settings');
            return;
          }
          toast.error(t('annotate.failed'));
          cancel();
        })
        .catch(() => {
          toast.error(t('annotate.failed'));
          cancel();
        })
        .finally(() => setBusy(false));
    },
    [busy, cancel, captured, setRefused, t],
  );

  // Drag tracking on window (the pointer may leave the layer mid-drag) + Esc.
  // The drag lives in state for the selection paint; a ref mirror lets the
  // mouseup handler read it without side-effects inside a state updater.
  const dragRef = useRef(drag);
  dragRef.current = drag;
  useEffect(() => {
    if (phase === 'idle') return;
    function onKey(e: KeyboardEvent): void {
      if (e.key !== 'Escape') return;
      e.preventDefault();
      e.stopPropagation();
      if (useAnnotation.getState().phase === 'target') discard();
      else cancel();
    }
    window.addEventListener('keydown', onKey, true);
    if (phase !== 'selecting') return () => window.removeEventListener('keydown', onKey, true);
    function onMove(e: MouseEvent): void {
      setDrag((d) => (d === null ? null : { ...d, x2: e.clientX, y2: e.clientY }));
    }
    function onUp(e: MouseEvent): void {
      const d = dragRef.current;
      setDrag(null);
      if (d === null) return;
      finish({
        x: Math.min(d.x1, e.clientX),
        y: Math.min(d.y1, e.clientY),
        width: Math.abs(e.clientX - d.x1),
        height: Math.abs(e.clientY - d.y1),
      });
    }
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
  }, [phase, finish, cancel, discard]);

  if (!sharing || phase === 'idle') return null;

  const sel =
    drag === null
      ? null
      : {
          left: Math.min(drag.x1, drag.x2),
          top: Math.min(drag.y1, drag.y2),
          width: Math.abs(drag.x2 - drag.x1),
          height: Math.abs(drag.y2 - drag.y1),
        };

  if (phase === 'selecting') {
    return (
      <div
        className="annot-overlay"
        role="presentation"
        onMouseDown={(e) => {
          if (e.button !== 0) return;
          setRefused(null);
          setDrag({ x1: e.clientX, y1: e.clientY, x2: e.clientX, y2: e.clientY });
        }}
      >
        <div className={`annot-hint${refused !== null ? ' refused' : ''}`}>
          {refused !== null ? t('annotate.refused') : t('annotate.hint')}
        </div>
        {sel !== null && sel.width >= MIN_DRAG && sel.height >= MIN_DRAG && <div className="annot-selection" style={sel} />}
      </div>
    );
  }

  // phase === 'target': the crop is captured; pick where it goes.
  const kimiId = kimiReady ? kimiGuestId() : null;
  const targets = resolveTargets({ kimiOpen: kimiId !== null, origin, companions });

  async function attachKimi(): Promise<void> {
    if (capture === null || kimiId === null || busy) return;
    setBusy(true);
    try {
      // The kimi path delivers the SAME message the companion path would:
      // the user's note with the D4 pointer line folded in, injected into
      // the composer beside the crop (main-side, same user-gesture posture).
      const composed = appendPointerNote(note.trim(), capture.pointer);
      const r = await invoke<AttachResponse>('annotation_attach_kimi', { file: capture.file, guest_id: kimiId, note: composed });
      if (r.ok === true && r.injected === true) {
        // The crop landed; if the note did not, say so — a silent drop would
        // leave the pointer chip promising what the agent never received.
        if (composed !== '' && r.note_injected === false) toast.info(t('annotate.attachedNoNote'));
        else toast.success(t('annotate.attached'));
        cancel(); // the temp file stays (the SPA may read lazily; the LRU reaps)
        // Land the user in kimi's composer to review and send (D2.2).
        revealAssistant('kimi');
      } else if (r.ok === true && r.fallback === 'clipboard') {
        // The paste key by platform — ⌘V is a lie on Linux/Windows.
        const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
        toast.info(t('annotate.clipboard').replace('{key}', mac ? '⌘V' : 'Ctrl+V'));
        cancel();
        revealAssistant('kimi'); // the clipboard hold counts as attached — reveal too
      } else {
        // Includes fallback === 'clipboard-failed': nothing was attached AND
        // nothing was copied — an info toast here would claim a clipboard
        // that holds nothing.
        toast.error(t('annotate.failed'));
        cancel();
      }
    } catch {
      toast.error(t('annotate.failed'));
      cancel();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="annot-overlay annot-target-wrap" role="presentation">
      <div className="annot-target" role="dialog" aria-label={t('annotate.title')}>
        {capture !== null && capture.preview !== '' && (
          <img className="annot-thumb" src={capture.preview} alt={t('annotate.title')} />
        )}
        {/* D4: what the agent will be told it is looking at. Shown BEFORE the
            send so a mis-resolved element is the user's to correct — they
            re-select rather than discovering it in the agent's reply. */}
        {capture?.pointer !== undefined && (
          <span className="annot-pointer" title={t('annotate.pointing')}>
            {formatPointerLabel(capture.pointer, t('annotate.pointerTab').replace('{id}', String(capture.pointer.tab_id)))}
          </span>
        )}
        <input
          className="annot-note"
          type="text"
          value={note}
          placeholder={t('annotate.notePlaceholder')}
          onChange={(e) => setNote(e.target.value)}
          autoFocus
        />
        {targets.kimi && (
          <button className="primary" disabled={busy} onClick={() => void attachKimi()}>
            <Icon name="globe" size={13} /> {t('annotate.attachKimi')}
          </button>
        )}
        {targets.companion !== null && (
          <button disabled={busy} onClick={() => handOffToCompanion(note.trim(), targets.companion?.storageKey)}>
            <Icon name="send" size={13} /> {t('annotate.sendTo').replace('{agent}', targets.companion.agentLabel)}
          </button>
        )}
        {!targets.kimi && targets.companion === null && <span className="muted small">{t('annotate.noTarget')}</span>}
        <button className="link-btn" disabled={busy} onClick={discard}>
          {t('common.cancel')}
        </button>
      </div>
    </div>
  );
}
