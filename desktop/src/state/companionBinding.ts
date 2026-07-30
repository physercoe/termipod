/// The dock companion's agent-binding storage key + the one-time migration
/// from the retired per-surface mounts (Read's aside, Author's aside). The
/// unified assistant dock (ui/AssistantDock.tsx) hosts the ONE AgentCompanion
/// now; users who bound an agent on a retired mount keep that binding through
/// the fallback below. Import-free so `node --test` covers the contract.

export const DOCK_COMPANION_KEY = 'termipod.dock.agent';

/// The retired mounts' binding keys, in fallback priority order — Read's
/// aside wins over Author's when both exist ("whichever exists", first hit).
/// The reader tab's own key (`termipod.read.reader.agent`) is deliberately
/// not migrated: it was a second, reader-scoped binding, not the primary one.
const LEGACY_COMPANION_KEYS = ['termipod.read.agent', 'termipod.author.agent'] as const;

/// Load the bound agent id for a companion mount. For the dock companion an
/// unset key falls back to the retired mounts' keys so an existing binding
/// survives the move (read-only: the next explicit pick writes the new key).
export function loadCompanionBinding(get: (key: string) => string | null, storageKey: string): string {
  const cur = get(storageKey);
  if (cur !== null && cur !== '') return cur;
  if (storageKey !== DOCK_COMPANION_KEY) return '';
  for (const key of LEGACY_COMPANION_KEYS) {
    const legacy = get(key);
    if (legacy !== null && legacy !== '') return legacy;
  }
  return '';
}
