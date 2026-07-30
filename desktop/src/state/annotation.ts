import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useAssistant } from './assistant';
import {
  handoffKey,
  handoffRevealsDock,
  removeCompanion,
  upsertCompanion,
  type AnnotationOrigin,
  type AnnotationPhase,
  type CompanionTarget,
} from './annotationTargets';

// The origin / companion-target types + the target-row resolution + the pure
// decisions (dock-hide flag, handoff routing/reveal) live in
// annotationTargets.ts (bridge-free, so node --test covers the contract);
// re-exported here so existing importers keep one entry point.
export { GLOBAL_ORIGIN, dockHiddenForPhase, resolveTargets } from './annotationTargets';
export type { AnnotationOrigin, CompanionTarget } from './annotationTargets';

/// Annotation overlay state (D2 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4): the user→agent pointing gesture. The trigger ARMS the overlay (only
/// while the UI-context-sharing toggle is on — the same toggle as D1, no new
/// one): an AgentCompanion's "Ask agent" compose button, or — D2.1 — the
/// status-bar crosshair chip / the command-palette entry, which arm GLOBALLY
/// (no companion origin) so the gesture is reachable from anywhere, including
/// the embedded kimi-web loop. The app-shell overlay runs the drag, main
/// captures the crop (annotation_host.ts), and the target row hands it to the
/// kimi-web composer (main-side injection) or to a companion as a staged
/// image chip (the handoff below — the arming companion, or for a global arm
/// the first bound companion in the registry).
///
/// Esc / cancel attaches nothing and records nothing (plan §3.8); a refused
/// selection (rect fully inside a capture:refuse region) keeps the overlay
/// armed with a hint so the user re-selects.
///
/// Dock interplay (the unified assistant dock, D2.2): while the overlay is
/// armed (`phase !== 'idle'`) the dock hides WITHOUT flipping its `open`
/// state (ui/AssistantDock.tsx reads the phase into a CSS hide class) so it
/// stays out of the captured pixels; cancel/discard/target-pick restore it.
/// A handoff to the dock companion REVEALS the dock on the companion tab so
/// the user sees the crop chip arrive (`handOffToCompanion` below).

export interface AnnotationCapture {
  /// Main-side 0o600 temp path (the kimi injection + discard).
  file: string;
  /// Small data-URL thumbnail for the target row + the companion chip.
  preview: string;
  /// The full PNG as raw base64 — the companion path's postAgentInput payload.
  dataB64: string;
  width: number;
  height: number;
  target: 'shell' | 'guest';
}

/// The companion-path handoff: the crop becomes a delete-able chip in the
/// arming companion's compose box and the note lands in its draft — the user
/// reviews and hits send (the send is always the user's). `id` bumps per
/// handoff so a second annotation re-injects into the same companion.
export interface AnnotationHandoff {
  storageKey: string;
  note: string;
  name: string;
  image: { mime_type: string; data: string };
  preview: string;
  id: number;
}

interface AnnotationState {
  phase: AnnotationPhase;
  origin: AnnotationOrigin | null;
  capture: AnnotationCapture | null;
  /// The surface id whose refuse region swallowed the last selection (a hint
  /// shows while the overlay stays armed); null = no active refusal.
  refused: string | null;
  handoff: AnnotationHandoff | null;
  /// The mounted AgentCompanions that can take a handoff (D2.1), self-
  /// reported by each companion while it has a bound agent — the target row
  /// of a GLOBAL arm offers the first bound one. Mount order.
  companions: CompanionTarget[];
  arm: (origin: AnnotationOrigin) => void;
  /// Esc / tiny-drag: the gesture never happened — no capture, no event.
  cancel: () => void;
  setRefused: (surface: string | null) => void;
  captured: (c: AnnotationCapture) => void;
  /// Target-row cancel: drop the crop AND its temp file.
  discard: () => void;
  /// Target-row "Send to <agent>": hand the crop + note to a companion and
  /// drop the temp file (the companion path sends base64). Companion arms
  /// route to the arming mount; a global arm (D2.1) passes the resolved
  /// companion's storageKey explicitly.
  handOffToCompanion: (note: string, storageKey?: string) => void;
  clearHandoff: () => void;
  registerCompanion: (c: CompanionTarget) => void;
  unregisterCompanion: (storageKey: string) => void;
}

let handoffSeq = 0;

export const useAnnotation = create<AnnotationState>((set, get) => ({
  phase: 'idle',
  origin: null,
  capture: null,
  refused: null,
  handoff: null,
  companions: [],
  /// Re-arming over a staged crop (a bound chord / the palette entry fires
  /// through the window keydown handler even in the target phase) must reap
  /// the temp file like discard — dropping the reference alone leaks it until
  /// the LRU / quit sweep.
  arm: (origin) => {
    const c = get().capture;
    if (c !== null && isShell()) void invoke('annotation_discard', { file: c.file }).catch(() => undefined);
    set({ phase: 'selecting', origin, capture: null, refused: null });
  },
  cancel: () => set({ phase: 'idle', origin: null, capture: null, refused: null }),
  setRefused: (surface) => set({ refused: surface }),
  captured: (c) => set({ phase: 'target', capture: c, refused: null }),
  discard: () => {
    const c = get().capture;
    if (c !== null && isShell()) void invoke('annotation_discard', { file: c.file }).catch(() => undefined);
    set({ phase: 'idle', origin: null, capture: null, refused: null });
  },
  handOffToCompanion: (note, storageKey) => {
    const { origin, capture } = get();
    const key = handoffKey(origin, storageKey);
    if (key === null || capture === null) return;
    handoffSeq += 1;
    set({
      phase: 'idle',
      refused: null,
      handoff: {
        storageKey: key,
        note,
        name: `annotation-${String(capture.width)}x${String(capture.height)}.png`,
        image: { mime_type: 'image/png', data: capture.dataB64 },
        preview: capture.preview,
        id: handoffSeq,
      },
    });
    // The companion path sends the base64 payload; the temp file was only
    // ever for the kimi injection — drop it.
    if (isShell()) void invoke('annotation_discard', { file: capture.file }).catch(() => undefined);
    set({ origin: null, capture: null });
    // D2.2: a handoff to the dock companion (the only registered one now —
    // the per-surface mounts are retired) reveals the dock on the companion
    // tab so the user watches the crop chip land in its compose box.
    if (handoffRevealsDock(key)) useAssistant.getState().reveal('companion');
  },
  clearHandoff: () => set({ handoff: null }),
  registerCompanion: (c) => set((s) => ({ companions: upsertCompanion(s.companions, c) })),
  unregisterCompanion: (storageKey) => set((s) => ({ companions: removeCompanion(s.companions, storageKey) })),
}));
