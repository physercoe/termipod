import { loadJson, saveJson } from './persist';

/// The persisted half of `replayRemote.ts` (split so that module stays pure
/// and node-testable): which SSH connection each dataset's video streams
/// through. Keyed by dataset id; absence = play from this machine's disk.

const KEY = 'replay_remote_media'; // dataset id → ssh connection id

export function remoteMediaConn(datasetId: string): string | null {
  const map = loadJson<Record<string, string>>(KEY, {});
  const c = map[datasetId];
  return c === undefined || c === '' ? null : c;
}

/// Set or clear (null) the connection a dataset's video streams through.
export function setRemoteMediaConn(datasetId: string, connId: string | null): void {
  const map = loadJson<Record<string, string>>(KEY, {});
  if (connId === null || connId === '') delete map[datasetId];
  else map[datasetId] = connId;
  saveJson(KEY, map);
}
