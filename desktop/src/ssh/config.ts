import { upsertConnection, type Connection } from '../state/connections';
import { importKey, listKeys } from '../state/keys';
import { localHome, localRead } from '../state/localfs';

/// Parser for an OpenSSH client config (`~/.ssh/config`) → saved connections, so
/// the director can pull in hosts they already use elsewhere. Handles the common
/// directives (Host / HostName / User / Port / IdentityFile); Match blocks and
/// wildcard-only Host patterns are skipped (they aren't concrete endpoints).
/// Keywords are case-insensitive per ssh_config(5).

export interface ParsedSshHost {
  name: string; // the Host alias
  host: string; // HostName, defaulting to the alias
  user: string;
  port: number;
  identityFile: string | null;
}

export function parseSshConfig(text: string): ParsedSshHost[] {
  const hosts: ParsedSshHost[] = [];
  let cur: ParsedSshHost | null = null;
  const flush = (): void => {
    if (cur !== null && cur.name !== '') hosts.push(cur);
  };
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (line === '' || line.startsWith('#')) continue;
    // `Key value` or `Key=value`.
    const m = line.match(/^(\S+?)[\s=]+(.+)$/);
    if (m === null) continue;
    const key = m[1].toLowerCase();
    const val = m[2].trim().replace(/^["']|["']$/g, '');
    if (key === 'host') {
      flush();
      // A Host line can list several patterns; take the first concrete one.
      const alias = val.split(/\s+/).find((p) => !p.includes('*') && !p.includes('?'));
      cur = alias !== undefined ? { name: alias, host: alias, user: '', port: 22, identityFile: null } : null;
    } else if (key === 'match') {
      flush();
      cur = null; // Match blocks aren't concrete hosts.
    } else if (cur !== null) {
      if (key === 'hostname') cur.host = val;
      else if (key === 'user') cur.user = val;
      else if (key === 'port') {
        const p = Number(val);
        if (Number.isFinite(p) && p > 0) cur.port = p;
      } else if (key === 'identityfile') cur.identityFile = val;
    }
  }
  flush();
  return hosts;
}

// Comment marker stamped on keys auto-loaded from a config's IdentityFile, so a
// re-import reuses the same saved key instead of importing a duplicate.
const CFG_KEY_TAG = 'ssh-config:';

// PEM key files are ASCII, so atob() yields the exact key text. localfs returns
// bytes base64-encoded; guard for a missing atob (never happens in the webview).
function b64ToText(b64: string): string {
  return typeof atob === 'function' ? atob(b64) : '';
}

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
    const pem = b64ToText(await localRead(path));
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
