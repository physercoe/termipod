export type WebForwardScheme = 'http' | 'https';

/** Parse a user-entered TCP port without accepting floats or numeric suffixes. */
export function parseRemotePort(value: string): number | null {
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  const port = Number(trimmed);
  return Number.isSafeInteger(port) && port >= 1 && port <= 65535 ? port : null;
}

/** A browser target is always rooted at the forwarded loopback origin. */
export function normalizeWebPath(value: string): string {
  const trimmed = value.trim();
  if (trimmed === '') return '/';
  return trimmed.startsWith('/') ? trimmed : `/${trimmed}`;
}

export function forwardedWebUrl(scheme: WebForwardScheme, localPort: number, path: string): string {
  return `${scheme}://127.0.0.1:${localPort}${normalizeWebPath(path)}`;
}
