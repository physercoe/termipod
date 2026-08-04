/// Build an `SshConnectReq` straight from a SAVED connection — the silent
/// half of one-click reconnect.
///
/// The Reconnect button used to reopen the connect form and wait for a human
/// to press Connect, even though everything the form would send is already
/// stored: the row itself, the password in the vault keychain slot, the key
/// in the key store. This assembles that request without the form. `null`
/// means a needed credential is NOT stored (a pasted-key connection, an
/// empty vault slot) — the caller falls back to the form rather than
/// attempting a connect that can only fail, because the form is where the
/// missing credential can be typed.
///
/// The assembly rules are the connect form's, deliberately (mobile parity
/// rules included): an empty jump password reuses the main password;
/// `jump_user` is omitted when blank (the core defaults it to `user`); a
/// disabled section is one whose host field is empty.
///
/// Secrets arrive through `SavedConnectIO` rather than imports, so this
/// module stays type-only on the stores and `node --test` can drive every
/// branch without a vault or a keychain behind it (the AuthorIO pattern).
/// `TerminalPanel` binds the real getters.
import type { SshConnectReq } from '../ssh/native';
import type { Connection } from '../state/connections';

export interface SavedConnectIO {
  getPassword: (connId: string) => Promise<string | null>;
  getJumpPassword: (connId: string) => Promise<string | null>;
  getKey: (keyId: string) => Promise<{ pem: string | null; passphrase: string | null }>;
}

/// The connect form's attempt ceiling (#319), shared so the silent attempt
/// and the form time out on the same clock: each extra hop (SOCKS5
/// handshake, jump-host auth + forward) adds its own round-trips.
export function connectTimeoutMs(c: Pick<Connection, 'jumpHost' | 'proxyHost'>): number {
  return 20_000 + ((c.jumpHost ?? '') !== '' ? 15_000 : 0) + ((c.proxyHost ?? '') !== '' ? 10_000 : 0);
}

export async function buildSavedConnectReq(
  c: Connection,
  connectId: string,
  io: SavedConnectIO,
): Promise<SshConnectReq | null> {
  const req: SshConnectReq = {
    host: c.host,
    port: c.port,
    user: c.username,
    cols: 80,
    rows: 24,
    connect_id: connectId,
  };

  let mainPassword = '';
  if (c.authMethod === 'password') {
    mainPassword = (await io.getPassword(c.id)) ?? '';
    if (mainPassword === '') return null;
    req.password = mainPassword;
  } else {
    // Only a key-store key can be replayed; a key that was PASTED into the
    // form was never saved anywhere this module may read.
    if (c.keyId === null || c.keyId === '') return null;
    const { pem, passphrase } = await io.getKey(c.keyId);
    if (pem === null) return null;
    req.private_key = pem;
    if (passphrase !== null) req.passphrase = passphrase;
  }

  if ((c.jumpHost ?? '') !== '') {
    req.jump_host = (c.jumpHost ?? '').trim();
    req.jump_port = c.jumpPort ?? 22;
    if ((c.jumpUsername ?? '').trim() !== '') req.jump_user = (c.jumpUsername ?? '').trim();
    if (c.jumpAuthMethod === 'key') {
      if ((c.jumpKeyId ?? '') === '') return null;
      const { pem, passphrase } = await io.getKey(c.jumpKeyId ?? '');
      if (pem === null) return null;
      req.jump_private_key = pem;
      if (passphrase !== null) req.jump_passphrase = passphrase;
    } else {
      // Mobile parity: an empty jump password reuses the main password.
      const jpw = (await io.getJumpPassword(c.id)) ?? '';
      const effective = jpw !== '' ? jpw : mainPassword;
      if (effective === '') return null;
      req.jump_password = effective;
    }
  }

  if ((c.proxyHost ?? '') !== '') {
    req.proxy_host = (c.proxyHost ?? '').trim();
    req.proxy_port = c.proxyPort ?? 1080;
    if ((c.proxyUsername ?? '').trim() !== '') req.proxy_username = (c.proxyUsername ?? '').trim();
    if ((c.proxyPassword ?? '') !== '') req.proxy_password = c.proxyPassword ?? '';
  }

  return req;
}
