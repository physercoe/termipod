/// Remote-media routing for the Replay player (J8 remote datasets).
///
/// A dataset registered on a remote hub host already gets its digest,
/// episodes and series through the hub tunnel — only the VIDEO bytes are
/// local-only, because `termipod-media://file/` reads this machine's disk.
/// The remote answer: the director opens an SSH terminal to the dataset's
/// machine (they have one saved — it's a terminal app), picks that
/// connection once for the dataset, and video streams over that session's
/// SFTP channel (`termipod-media://sftp/`).
///
/// What persists is the CONNECTION id (stable, vault-synced); ssh session
/// ids are ephemeral per connect, so they are resolved live from the
/// terminal dock's open tabs on every render.
///
/// PURE — no runtime imports, so `node --test` loads it directly (the #411
/// node-ESM lesson). The persisted dataset→connection map lives in
/// `replayRemoteStore.ts`, which wraps this around `persist.loadJson`.

/// The minimal tab shape this module needs from the terminal dock store —
/// structural, so the pure resolution below is testable without the store.
export interface SshTabLike {
  kind: string;
  sessionId: string;
  connId?: string;
}

/// The live ssh session id for a saved connection, or null when no open
/// terminal tab uses it. First match wins (duplicated tabs share a session's
/// connection anyway).
export function liveSessionFor(connId: string, tabs: SshTabLike[]): string | null {
  if (connId === '') return null;
  const tab = tabs.find((t) => t.kind === 'ssh' && t.connId === connId && t.sessionId !== '');
  return tab?.sessionId ?? null;
}

/// Connections that currently have a live ssh tab — the options the picker
/// offers (a dead connection can't stream anything).
export function liveConnIds(tabs: SshTabLike[]): string[] {
  const out: string[] = [];
  for (const t of tabs) {
    if (t.kind !== 'ssh' || t.connId === undefined || t.connId === '') continue;
    if (!out.includes(t.connId)) out.push(t.connId);
  }
  return out;
}
