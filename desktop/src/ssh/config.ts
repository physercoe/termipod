import { upsertConnection, type Connection } from '../state/connections';

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

/// Import parsed hosts as saved connections; returns how many were written.
/// A host whose name matches an existing connection is updated in place (so a
/// re-import refreshes rather than duplicates). The IdentityFile is only a path —
/// the key itself still has to be added under Settings → SSH Keys — so auth is set
/// to `key` when an IdentityFile is present (the user then picks the saved key),
/// else `password`.
export function importSshConfig(text: string, existing: Connection[]): number {
  const parsed = parseSshConfig(text);
  for (const h of parsed) {
    const match = existing.find((c) => c.name === h.name);
    upsertConnection({
      id: match?.id,
      name: h.name,
      host: h.host,
      port: h.port,
      username: h.user,
      authMethod: h.identityFile !== null ? 'key' : 'password',
      keyId: match?.keyId ?? null,
    });
  }
  return parsed.length;
}
