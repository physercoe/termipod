import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';

/// Annotation overlay state (D2 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4): the user→agent pointing gesture. An AgentCompanion's "Ask agent"
/// button ARMS the overlay (only while the UI-context-sharing toggle is on —
/// the same toggle as D1, no new one); the app-shell overlay runs the drag,
/// main captures the crop (annotation_host.ts), and the target row hands it
/// to the kimi-web composer (main-side injection) or back to the arming
/// companion as a staged image chip (the handoff below).
///
/// Esc / cancel attaches nothing and records nothing (plan §3.8); a refused
/// selection (rect fully inside a capture:refuse region) keeps the overlay
/// armed with a hint so the user re-selects.

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

/// Who armed the overlay. `storageKey` scopes the handoff to THAT companion
/// mount; `agentId` '' means the companion is unbound — the kimi-web path is
/// still reachable (plan §3.4 step 2), the companion target just isn't.
export interface AnnotationOrigin {
  storageKey: string;
  agentId: string;
  agentLabel: string;
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
  phase: 'idle' | 'selecting' | 'target';
  origin: AnnotationOrigin | null;
  capture: AnnotationCapture | null;
  /// The surface id whose refuse region swallowed the last selection (a hint
  /// shows while the overlay stays armed); null = no active refusal.
  refused: string | null;
  handoff: AnnotationHandoff | null;
  arm: (origin: AnnotationOrigin) => void;
  /// Esc / tiny-drag: the gesture never happened — no capture, no event.
  cancel: () => void;
  setRefused: (surface: string | null) => void;
  captured: (c: AnnotationCapture) => void;
  /// Target-row cancel: drop the crop AND its temp file.
  discard: () => void;
  /// Target-row "Send to <agent>": hand the crop + note to the arming
  /// companion and drop the temp file (the companion path sends base64).
  handOffToCompanion: (note: string) => void;
  clearHandoff: () => void;
}

let handoffSeq = 0;

export const useAnnotation = create<AnnotationState>((set, get) => ({
  phase: 'idle',
  origin: null,
  capture: null,
  refused: null,
  handoff: null,
  arm: (origin) => set({ phase: 'selecting', origin, capture: null, refused: null }),
  cancel: () => set({ phase: 'idle', origin: null, capture: null, refused: null }),
  setRefused: (surface) => set({ refused: surface }),
  captured: (c) => set({ phase: 'target', capture: c, refused: null }),
  discard: () => {
    const c = get().capture;
    if (c !== null && isShell()) void invoke('annotation_discard', { file: c.file }).catch(() => undefined);
    set({ phase: 'idle', origin: null, capture: null, refused: null });
  },
  handOffToCompanion: (note) => {
    const { origin, capture } = get();
    if (origin === null || capture === null) return;
    handoffSeq += 1;
    set({
      phase: 'idle',
      refused: null,
      handoff: {
        storageKey: origin.storageKey,
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
  },
  clearHandoff: () => set({ handoff: null }),
}));
