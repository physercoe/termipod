/// URL policy for `forge_fetch` (round-3 T3 / §5.9) — pure so it is unit-testable
/// (forge.ts itself pulls in electron via platform.ts). https only, except plain
/// http to loopback UNDER THE E2E HARNESS (which launches the app with
/// TERMIPOD_E2E=1) so the suite can point the forge base URL at a local stand-in
/// server. Production stays https-only.
const LOOPBACK_HTTP = /^http:\/\/(127\.0\.0\.1|localhost|\[::1\])(:\d+)?(\/|$)/i;

export function isAllowedForgeUrl(url: string, e2eEnv: string | undefined): boolean {
  if (/^https:\/\//i.test(url)) return true;
  return e2eEnv === '1' && LOOPBACK_HTTP.test(url);
}
