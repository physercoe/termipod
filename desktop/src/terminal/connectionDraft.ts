import type { Connection } from '../state/connections';

export type ConnectionAuth = 'password' | 'key';

export interface ConnectionDraft {
  id: string | null;
  name: string;
  group: string;
  host: string;
  port: string;
  user: string;
  auth: ConnectionAuth;
  password: string;
  keyId: string;
  useJump: boolean;
  jumpHost: string;
  jumpPort: string;
  jumpUser: string;
  jumpAuth: ConnectionAuth;
  jumpKeyId: string;
  jumpPassword: string;
  useProxy: boolean;
  proxyHost: string;
  proxyPort: string;
  proxyUser: string;
  proxyPassword: string;
}

type ConnectionInput = Partial<Connection> & { name: string; host: string; username: string };

export interface ConnectionDraftIO {
  defaultGroup: string;
  upsert: (input: ConnectionInput) => Connection;
  setPassword: (id: string, password: string) => Promise<void>;
  setJumpPassword: (id: string, password: string) => Promise<void>;
}

/**
 * Persist the connection form through the same path for both Save and Connect.
 * Connection metadata lives in the saved-connections store; password secrets
 * go through the OS-keychain-backed setters supplied by the caller.
 */
export async function rememberConnection(draft: ConnectionDraft, io: ConnectionDraftIO): Promise<Connection> {
  const jumpOn = draft.useJump && draft.jumpHost.trim() !== '';
  const proxyOn = draft.useProxy && draft.proxyHost.trim() !== '';
  const conn = io.upsert({
    id: draft.id ?? undefined,
    name: draft.name.trim() || draft.host.trim(),
    group: draft.group.trim() || io.defaultGroup,
    host: draft.host.trim(),
    port: Number(draft.port) || 22,
    username: draft.user.trim(),
    authMethod: draft.auth,
    keyId: draft.auth === 'key' && draft.keyId !== '' ? draft.keyId : null,
    // Explicit nulls clear a disabled section (presence-keyed carry-over in
    // upsertConnection) — the same shape the mobile form writes.
    jumpHost: jumpOn ? draft.jumpHost.trim() : null,
    jumpPort: jumpOn ? Number(draft.jumpPort) || 22 : null,
    jumpUsername: jumpOn && draft.jumpUser.trim() !== '' ? draft.jumpUser.trim() : null,
    jumpAuthMethod: jumpOn ? draft.jumpAuth : null,
    jumpKeyId: jumpOn && draft.jumpAuth === 'key' && draft.jumpKeyId !== '' ? draft.jumpKeyId : null,
    proxyHost: proxyOn ? draft.proxyHost.trim() : null,
    proxyPort: proxyOn ? Number(draft.proxyPort) || 1080 : null,
    proxyUsername: proxyOn && draft.proxyUser.trim() !== '' ? draft.proxyUser.trim() : null,
    proxyPassword: proxyOn && draft.proxyPassword !== '' ? draft.proxyPassword : null,
  });
  if (draft.auth === 'password' && draft.password !== '') await io.setPassword(conn.id, draft.password);
  // Empty (or disabled jump / key-auth jump) deletes the jump keychain slot.
  await io.setJumpPassword(conn.id, jumpOn && draft.jumpAuth === 'password' ? draft.jumpPassword : '');
  return conn;
}
