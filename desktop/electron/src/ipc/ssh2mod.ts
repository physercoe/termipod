/// ssh2 module loader (ADR-055 M2.2a) — isolated from ssh.ts (no Electron
/// imports) so the connect path's one external dependency is unit-testable
/// under plain node.
///
/// esbuild keeps `import('ssh2')` a REAL dynamic import (ssh2 is external —
/// see esbuild.mjs), so in the packaged app the result is node's
/// cjs-module-lexer namespace, NOT the raw module.exports. The lexer only
/// partially picks up ssh2's exports object literal (`utils: { parseKey,
/// ...require('./keygen.js'), ... }` — the spread defeats it): `Client`
/// survives, `utils` is undefined. ssh.ts's `ssh2.utils.parseKey(keyBuf)`
/// then throws a TypeError inside the hostVerifier, which the verifier maps
/// to verify(false) — every first-time host was rejected with "Host denied
/// (verification failed)" and no TOFU pin was ever written. `default` is the
/// full module.exports under the interop, so normalize through it; the
/// `?? m` fallback keeps environments that return the plain exports object
/// (e.g. future bundler setting changes) working.
export type Ssh2Module = typeof import('ssh2');

let ssh2P: Promise<Ssh2Module> | null = null;
export function loadSsh2(): Promise<Ssh2Module> {
  if (ssh2P === null) {
    ssh2P = import('ssh2').then((m) => (m as { default?: Ssh2Module }).default ?? m);
  }
  return ssh2P;
}
