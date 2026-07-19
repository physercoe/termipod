import { create } from 'zustand';

/// Minimal transient-notification channel (#315). The app swallowed most
/// data-layer failures into `console.error` (invisible to the user) and gave no
/// acknowledgement for background successes (sync finished, vault saved). A toast
/// is the lightweight surface for both: an error that isn't worth a modal, and a
/// success that shouldn't need one.
///
/// Deliberately tiny — no queueing library, no portals. `push` adds a toast and
/// schedules its own removal; the `<ToastHost/>` mounted once at the app root
/// renders the stack. Call the `toast.*` helpers from anywhere (they don't need
/// a hook), read the list with `useToasts()` inside the host.

export type ToastKind = 'error' | 'success' | 'info';

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastState {
  toasts: Toast[];
  remove: (id: number) => void;
  push: (kind: ToastKind, message: string, timeoutMs?: number) => number;
}

let seq = 0;

// Errors linger (the user may have looked away); successes/info auto-clear fast.
const DEFAULT_TIMEOUT: Record<ToastKind, number> = {
  error: 8000,
  success: 3500,
  info: 4500,
};

export const useToasts = create<ToastState>((set, get) => ({
  toasts: [],
  remove: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
  push: (kind, message, timeoutMs) => {
    seq += 1;
    const id = seq;
    const text = message.trim() === '' ? kind : message;
    set((s) => ({ toasts: [...s.toasts, { id, kind, message: text }] }));
    const ms = timeoutMs ?? DEFAULT_TIMEOUT[kind];
    if (ms > 0) {
      setTimeout(() => get().remove(id), ms);
    }
    return id;
  },
}));

/// Fire-and-forget helpers usable outside React (catch blocks, store actions).
export const toast = {
  error: (message: string): number => useToasts.getState().push('error', message),
  success: (message: string): number => useToasts.getState().push('success', message),
  info: (message: string): number => useToasts.getState().push('info', message),
  dismiss: (id: number): void => useToasts.getState().remove(id),
};
