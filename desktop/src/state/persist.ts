import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';

/// Foundation F2 — the local persistence + secret layer. Secrets (SSH
/// passwords, private keys, passphrases, later vault material) go to the OS
/// keychain via the Rust core; non-secret metadata (connection names, hosts,
/// key metadata, profiles) is plain JSON in the webview's localStorage. The
/// browser build has no native core, so secrets fall back to localStorage under
/// a `sec:` prefix — insecure, but the SSH surfaces that need them are
/// desktop-only, so this only matters for dev.

export async function secretSet(key: string, value: string): Promise<void> {
  if (isTauri()) await invoke('keychain_set', { key, value });
  else localStorage.setItem(`sec:${key}`, value);
}

export async function secretGet(key: string): Promise<string | null> {
  if (isTauri()) return (await invoke<string | null>('keychain_get', { key })) ?? null;
  return localStorage.getItem(`sec:${key}`);
}

export async function secretDelete(key: string): Promise<void> {
  if (isTauri()) await invoke('keychain_delete', { key });
  else localStorage.removeItem(`sec:${key}`);
}

export function loadJson<T>(key: string, fallback: T): T {
  try {
    const s = localStorage.getItem(key);
    return s !== null ? (JSON.parse(s) as T) : fallback;
  } catch {
    return fallback;
  }
}

export function saveJson(key: string, value: unknown): void {
  localStorage.setItem(key, JSON.stringify(value));
}

/// A short opaque id for a new connection/key. Mirrors the mobile `<millis>`
/// convention loosely; uniqueness is all that matters (Date.now is unavailable
/// in some sandboxes but fine in the webview).
export function newId(): string {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}
