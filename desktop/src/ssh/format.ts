import type { Connection } from '../state/connections';

/// Pure OpenSSH `ssh_config(5)` text ⇄ data conversions — the parser used by the
/// import flow and its inverse used by export. Kept free of runtime imports
/// (type-only above) so `node --test` can load it directly; the side-effectful
/// import/upsert flow stays in `./config.ts`.

export interface ParsedSshHost {
  name: string; // the Host alias
  host: string; // HostName, defaulting to the alias
  user: string;
  port: number;
  identityFile: string | null;
}

/// Parse an OpenSSH client config (`~/.ssh/config`). Handles the common
/// directives (Host / HostName / User / Port / IdentityFile); Match blocks and
/// wildcard-only Host patterns are skipped (they aren't concrete endpoints).
/// Keywords are case-insensitive per ssh_config(5).
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

/// Render saved connections as an OpenSSH client config — the inverse of
/// `parseSshConfig`, so hosts managed here can be used from a plain `ssh` too.
/// Secrets never leave the vault: passwords aren't representable in an ssh
/// config at all, and private keys live in the OS keychain (not files), so a
/// key-auth host gets a comment naming its vault key instead of an
/// `IdentityFile`. Jump hosts render as `ProxyJump`; a SOCKS proxy has no
/// portable client directive and is noted as a comment. Pure (takes an optional
/// keyId→name map); the caller resolves names via `listKeys`.
export function exportSshConfig(conns: Connection[], keyNames: Record<string, string> = {}): string {
  const lines: string[] = [
    '# SSH connections exported from TermiPod.',
    '# Passwords and private keys stay in the TermiPod vault (OS keychain) and are',
    '# NOT included — link an IdentityFile yourself where one is needed.',
    '',
  ];
  const seen = new Set<string>();
  for (const c of conns) {
    // Host aliases can't contain whitespace; keep them unique after sanitizing.
    let alias = c.name.trim().replace(/\s+/g, '-');
    if (alias === '') alias = c.host;
    if (seen.has(alias)) {
      let i = 2;
      while (seen.has(`${alias}-${i}`)) i += 1;
      alias = `${alias}-${i}`;
    }
    seen.add(alias);
    lines.push(`Host ${alias}`);
    lines.push(`  HostName ${c.host}`);
    if (c.username !== '') lines.push(`  User ${c.username}`);
    if (c.port !== 22) lines.push(`  Port ${c.port}`);
    if (c.jumpHost !== undefined && c.jumpHost !== null && c.jumpHost !== '') {
      const ju = c.jumpUsername !== undefined && c.jumpUsername !== null && c.jumpUsername !== '' ? `${c.jumpUsername}@` : '';
      const jp = c.jumpPort !== undefined && c.jumpPort !== null && c.jumpPort !== 22 ? `:${c.jumpPort}` : '';
      lines.push(`  ProxyJump ${ju}${c.jumpHost}${jp}`);
    }
    if (c.authMethod === 'key') {
      const keyName = c.keyId !== null ? keyNames[c.keyId] : undefined;
      lines.push(`  # key auth via TermiPod vault${keyName !== undefined ? `: ${keyName}` : ''} — set IdentityFile manually`);
    }
    if (c.proxyHost !== undefined && c.proxyHost !== null && c.proxyHost !== '') {
      lines.push(`  # SOCKS5 proxy ${c.proxyHost}:${c.proxyPort ?? 1080} — no portable directive; use ProxyCommand if needed`);
    }
    lines.push('');
  }
  return lines.join('\n');
}
