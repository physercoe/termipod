import { getConnectionPassword, listConnections, type Connection } from './connections';
import { getKeyMaterial } from './keys';
import { sftpList, sftpRead, sshClose, sshConnect, type SftpEntry } from '../ssh/native';
import { readWorkspaceFile } from './workspaceFiles';
import { useSession } from './session';
import { isShell } from '../platform';
import { invoke } from '../bridge';
import { readForgeBlob } from './forge';
import type { ForgeRepo, InspectRef, InspectSource, InspectTab } from './inspect';

/// Source-reading for the Inspect (J3) surface — the W1 follow-on that adds the
/// `workspace`, `remote` (SFTP) and `hub` sources on top of W1's `paste` +
/// `local`. Kept out of the surface component so the async / connection-cache
/// logic is testable and the surface stays a view.

// Build a `SshConnectReq` from a saved connection + its vaulted credentials and
// open a session. Extracted from the connect logic in terminal/ConnectForm.tsx
// (which is interactive) so a headless "connect to read a file" path can reuse
// exactly the same credential resolution. A passphrase-protected key works when
// its passphrase is stored in the vault; otherwise the connect fails cleanly.
export async function connectSaved(conn: Connection): Promise<string> {
  const base = { host: conn.host, port: conn.port, user: conn.username, cols: 80, rows: 24 };
  if (conn.authMethod === 'password') {
    const pw = (await getConnectionPassword(conn.id)) ?? '';
    return sshConnect({ ...base, password: pw });
  }
  if (conn.keyId !== null && conn.keyId !== '') {
    const { pem, passphrase } = await getKeyMaterial(conn.keyId);
    if (pem === null) throw new Error('key material is missing for this connection');
    return sshConnect({ ...base, private_key: pem, passphrase: passphrase ?? undefined });
  }
  throw new Error('this connection has no stored credentials to connect with');
}

// One live SFTP session per connection, cached so browsing + reading + a tab's
// later re-reads reuse a single remote shell instead of reconnecting each time.
// A failed connect is not cached (so the next attempt retries).
const sessions = new Map<string, Promise<string>>();

export async function sftpSessionFor(connId: string): Promise<string> {
  const conn = listConnections().find((c) => c.id === connId);
  if (conn === undefined) throw new Error('connection not found');
  let p = sessions.get(connId);
  if (p === undefined) {
    p = connectSaved(conn);
    sessions.set(connId, p);
  }
  try {
    return await p;
  } catch (e) {
    sessions.delete(connId);
    throw e;
  }
}

/// List a remote directory over a saved connection (opening the session lazily).
export async function sftpBrowse(connId: string, path: string): Promise<SftpEntry[]> {
  return sftpList(await sftpSessionFor(connId), path);
}

/// Tear down a cached SFTP session (e.g. the picker closed) — best-effort.
export async function closeSftpSession(connId: string): Promise<void> {
  const p = sessions.get(connId);
  sessions.delete(connId);
  if (p === undefined) return;
  try {
    await sshClose(await p);
  } catch {
    /* already gone */
  }
}

// The shared read core for a file-backed source. `transferId` scopes the SFTP
// byte transfer (unique per reader so concurrent reads don't collide).
async function readFrom(loc: {
  source: InspectSource;
  path?: string;
  hostId?: string;
  projectId?: string;
  repo?: ForgeRepo;
  transferId: string;
}): Promise<string> {
  const native = loc.source === 'local' || loc.source === 'workspace' || loc.source === 'remote';
  if (native && !isShell()) throw new Error('opening files requires the desktop app');
  switch (loc.source) {
    case 'local':
    case 'workspace':
      // Both are local absolute paths → the strict-UTF-8 `doc_read` bridge.
      return readWorkspaceFile(loc.path ?? '');
    case 'remote': {
      const sid = await sftpSessionFor(loc.hostId ?? '');
      const bytes = await sftpRead(sid, loc.path ?? '', loc.transferId);
      return new TextDecoder('utf-8', { fatal: false }).decode(bytes);
    }
    case 'hub': {
      const client = useSession.getState().client;
      if (client === null) throw new Error('not connected to a hub');
      return client.getProjectDocText(loc.projectId ?? '', loc.path ?? '');
    }
    case 'github':
    case 'hf': {
      if (loc.repo === undefined) throw new Error('inspect: forge tab is missing its repo snapshot');
      return readForgeBlob(loc.repo, loc.source, loc.path ?? '');
    }
    default:
      throw new Error(`inspect: source '${loc.source}' is unsupported`);
  }
}

/// Read a file-backed source's raw BYTES.
///
/// The text core above decodes; this one does not, because some of what Inspect
/// opens is not text. Only the sources that can actually produce bytes are here
/// — `hub`, `github` and `hf` decode to a string inside their transports before
/// the renderer ever sees a buffer, so offering them would mean handing back
/// re-encoded mojibake. The caller checks `canStreamMedia` first and says so.
///
/// Used for the PDF preview only. Images, video and audio go through the media
/// scheme instead, which streams and seeks rather than slurping.
export async function readSourceBytes(tab: InspectTab, cap: number): Promise<Uint8Array> {
  if (!isShell()) throw new Error('opening files requires the desktop app');
  if (tab.source === 'local' || tab.source === 'workspace') {
    const bytes = await invoke<Uint8Array>('localfs_read', { path: tab.path ?? '' });
    if (bytes.byteLength > cap) throw new Error(tooLargeForPreview(bytes.byteLength, cap));
    return bytes;
  }
  if (tab.source === 'remote') {
    const sid = await sftpSessionFor(tab.hostId ?? '');
    const bytes = await sftpRead(sid, tab.path ?? '', `insp-bin-${tab.id}`);
    if (bytes.byteLength > cap) throw new Error(tooLargeForPreview(bytes.byteLength, cap));
    return bytes;
  }
  throw new Error(`inspect: source '${tab.source}' cannot supply raw bytes`);
}

function tooLargeForPreview(size: number, cap: number): string {
  const mb = (n: number): string => `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `file too large to preview (${mb(size)} — cap is ${mb(cap)})`;
}

/// Read a tab's current content from its source. `paste` tabs never reach here
/// (their body is authoritative in the store); the four file-backed sources do.
export async function readSource(tab: InspectTab): Promise<string> {
  return readFrom({ source: tab.source, path: tab.path, hostId: tab.hostId, projectId: tab.projectId, repo: tab.repo, transferId: `insp-${tab.id}` });
}

/// Read one side of a two-blob compare. A `paste`/scratch side carries its body
/// inline (`ref.body`); the file-backed sides go through the same read core.
export async function readRef(ref: InspectRef, transferId: string): Promise<string> {
  if (ref.body !== undefined) return ref.body;
  return readFrom({ source: ref.source, path: ref.path, hostId: ref.hostId, projectId: ref.projectId, repo: ref.repo, transferId });
}
