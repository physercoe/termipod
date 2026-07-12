import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';

/// Shared HTTP for the discovery sources. Routed through the Rust core's
/// `hub_request` (reqwest) under Tauri so it's CORS-free in the sandboxed webview
/// (the same transport the hub SDK uses); the plain-browser build falls back to
/// `fetch`. Retries HTTP 429 with backoff — Semantic Scholar's keyless pool 429s
/// constantly, and a couple retries usually land. Only 429 is retried; other
/// statuses fail fast.

interface Raw {
  status: number;
  body: string;
}

export function lsGet(key: string): string {
  try {
    return localStorage.getItem(key) ?? '';
  } catch {
    return '';
  }
}
export function lsSet(key: string, value: string): void {
  try {
    if (value.trim() === '') localStorage.removeItem(key);
    else localStorage.setItem(key, value.trim());
  } catch {
    /* ignore */
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function requestOnce(url: string, headers: Record<string, string>): Promise<Raw> {
  if (isTauri()) {
    return invoke<Raw>('hub_request', { req: { method: 'GET', url, headers, body: null } });
  }
  const res = await fetch(url, { headers });
  return { status: res.status, body: await res.text() };
}

/// GET the raw text body, retrying 429 with backoff. `attempts` defaults to 4
/// (pass 2 for keyed calls that have their own quota).
export async function httpGet(url: string, headers: Record<string, string> = {}, attempts = 4): Promise<string> {
  const hdrs = { accept: 'application/json', ...headers };
  let last = 0;
  for (let i = 0; i < attempts; i += 1) {
    if (i > 0) await delay(500 * i + 300); // 800ms, 1.3s, 1.8s, 2.3s
    const res = await requestOnce(url, hdrs);
    if (res.status >= 200 && res.status < 300) return res.body;
    last = res.status;
    if (res.status !== 429) break;
  }
  throw new Error(last === 429 ? 'rate-limited' : `HTTP ${last}`);
}

export async function getJson(url: string, headers?: Record<string, string>, attempts?: number): Promise<unknown> {
  return JSON.parse(await httpGet(url, headers, attempts));
}

// Polite-pool contact for OpenAlex / Crossref / Unpaywall (they ask for a mailto/
// email; a neutral no-reply keeps us in the faster pool without a personal addr).
export const CONTACT = 'termipod-desktop@users.noreply.github.com';
