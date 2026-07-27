import { upsertConnection, type Connection } from '../state/connections';
import { importKey, listKeys } from '../state/keys';
import { localHome, localRead } from '../state/localfs';
import { parseSshConfig } from './format';

/// OpenSSH client config (`~/.ssh/config`) import flow → saved connections, so
/// the director can pull in hosts they already use elsewhere. The pure text
/// conversions (parser + the export inverse) live in `./format.ts` — this file
/// holds the side-effectful upsert/key-loading around them.

export { exportSshConfig, parseSshConfig, type ParsedSshHost } from './format';

// Comment marker stamped on keys auto-loaded from a config's IdentityFile, so a
// re-import reuses the same saved key instead of importing a duplicate.
const CFG_KEY_TAG = 'ssh-config:';

// Resolve an IdentityFile directive to an absolute path: `~` → home, and a bare
// name (no directory) is taken relative to ~/.ssh, matching ssh(1).
function resolveIdentityPath(idf: string, home: string | null): string {
  let p = idf.trim();
  if (p.startsWith('~')) p = (home ?? '') + p.slice(1);
  const absolute = p.startsWith('/') || /^[A-Za-z]:[\\/]/.test(p);
  if (!absolute && home !== null) p = `${home}/.ssh/${p}`;
  return p;
}

/// Ensure the private key an IdentityFile points at is loaded into the key store,
/// returning its id so the connection can link it. Reads the file via the local
/// core and validates it through `ssh_parse_key` (inside importKey). Returns a
/// null id — leaving the connection on `key` auth with nothing linked — when the
/// file is missing or passphrase-protected (`ssh_parse_key` rejects it without
/// the passphrase); the director then links a key manually under Settings → Vault.
async function ensureIdentityKey(
  idf: string,
  connName: string,
  home: string | null,
): Promise<{ keyId: string | null; added: boolean }> {
  const path = resolveIdentityPath(idf, home);
  const tag = `${CFG_KEY_TAG}${path}`;
  const existing = listKeys().find((k) => k.comment === tag);
  if (existing !== undefined) return { keyId: existing.id, added: false };
  try {
    // PEM key files are UTF-8 text; localRead now returns raw bytes (§7 row 4).
    const pem = new TextDecoder().decode(await localRead(path));
    if (pem.trim() === '') return { keyId: null, added: false };
    const base = path.replace(/\\/g, '/').split('/').pop() ?? 'key';
    const meta = await importKey({ name: connName || base, pem, comment: tag });
    return { keyId: meta.id, added: true };
  } catch {
    return { keyId: null, added: false };
  }
}

/// Import parsed hosts as saved connections; returns the count written plus how
/// many key files were newly loaded. A host whose name matches an existing
/// connection is updated in place (a re-import refreshes rather than duplicates).
/// When a host has an IdentityFile we try to load that key too and link it — ssh
/// configs carry no passwords, so `password`-auth hosts are imported without a
/// secret (the director enters it on connect).
export async function importSshConfig(
  text: string,
  existing: Connection[],
): Promise<{ count: number; keysAdded: number }> {
  const parsed = parseSshConfig(text);
  const home = await localHome().catch(() => null);
  let keysAdded = 0;
  for (const h of parsed) {
    const match = existing.find((c) => c.name === h.name);
    let keyId = match?.keyId ?? null;
    if (h.identityFile !== null) {
      const res = await ensureIdentityKey(h.identityFile, h.name, home);
      if (res.keyId !== null) keyId = res.keyId;
      if (res.added) keysAdded += 1;
    }
    upsertConnection({
      id: match?.id,
      name: h.name,
      host: h.host,
      port: h.port,
      username: h.user,
      authMethod: h.identityFile !== null ? 'key' : 'password',
      keyId,
    });
  }
  return { count: parsed.length, keysAdded };
}
