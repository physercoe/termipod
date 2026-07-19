import { useEffect } from 'react';
import { create } from 'zustand';
import { clearSecrets } from './persist';

/// Vault session lock (#320). The desktop vault decrypts every secret into memory
/// once per launch (see persist.ts) — there was no way to re-secure it short of
/// quitting. A lock purges the decrypted in-memory cache (clearSecrets) and hides
/// secret values in the UI; unlocking just drops the veil — the next secret read
/// transparently re-reads the OS keychain (one consolidated read). This is an
/// honest lock for a device-key model: it stops secrets sitting in webview memory
/// and on-screen while the workbench is left unattended, without pretending there
/// is a master password to re-challenge.

const AUTOLOCK_KEY = 'termipod.vault.autolockMin';
const DEFAULT_MIN = 5; // 0 = never

export function loadAutolockMin(): number {
  const n = Number(localStorage.getItem(AUTOLOCK_KEY));
  return Number.isFinite(n) && n >= 0 ? n : DEFAULT_MIN;
}
function saveAutolockMin(n: number): void {
  try {
    localStorage.setItem(AUTOLOCK_KEY, String(n));
  } catch {
    /* ignore */
  }
}

interface VaultLockState {
  locked: boolean;
  autolockMin: number;
  lock: () => void;
  unlock: () => void;
  setAutolockMin: (n: number) => void;
}

export const useVaultLock = create<VaultLockState>((set) => ({
  locked: false,
  autolockMin: loadAutolockMin(),
  lock: () => {
    clearSecrets();
    set({ locked: true });
  },
  unlock: () => set({ locked: false }),
  setAutolockMin: (n) => {
    saveAutolockMin(n);
    set({ autolockMin: n });
  },
}));

/// Idle auto-lock: while the vault surface is mounted and unlocked, lock after
/// `autolockMin` minutes of no user activity. Any pointer/keyboard/scroll resets
/// the timer. A timeout of 0 disables it.
export function useAutolock(): void {
  const locked = useVaultLock((s) => s.locked);
  const autolockMin = useVaultLock((s) => s.autolockMin);
  const lock = useVaultLock((s) => s.lock);
  useEffect(() => {
    if (locked || autolockMin <= 0) return;
    let timer = 0;
    const reset = (): void => {
      window.clearTimeout(timer);
      timer = window.setTimeout(lock, autolockMin * 60_000);
    };
    const events: (keyof WindowEventMap)[] = ['mousemove', 'keydown', 'mousedown', 'scroll', 'touchstart'];
    events.forEach((e) => window.addEventListener(e, reset, { passive: true }));
    reset();
    return () => {
      window.clearTimeout(timer);
      events.forEach((e) => window.removeEventListener(e, reset));
    };
  }, [locked, autolockMin, lock]);
}
