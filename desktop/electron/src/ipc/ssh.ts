/// Direct-SSH transport + SFTP (ADR-055 M2.2a) — the Electron port of the
/// connection/terminal/transfer core of `src-tauri/src/ssh.rs`. Same command
/// names (`ssh_connect` / `ssh_duplicate` / `ssh_exec` / `ssh_write` /
/// `ssh_resize` / `ssh_close` / `sftp_list` / `sftp_read` / `sftp_write`) and the
/// same `ssh-data` / `ssh-exit` / `ssh-connect-progress` / `sftp-progress`
/// contract, so `src/ssh/tauri.ts` drives it unchanged. Engine: `ssh2` in place
/// of russh/russh-sftp.
///
/// The key-store crypto commands (`ssh_parse_key` / `ssh_generate_key`) land in
/// a follow-up slice (M2.2b) — the ed25519 OpenSSH-PEM keygen needs its own
/// library vetting.
///
/// SHARED CONNECTION (multiplexing): one `ssh2.Client` (one TCP+auth handshake)
/// backs many shell channels — `ssh_duplicate` opens a second shell on the same
/// Client, and `ssh_exec` / SFTP open their own channels on it — so duplicating
/// never re-prompts for credentials (ssh.rs `ssh_duplicate`). A ref-count of the
/// shell sessions on each Client ends it only when the last one closes.
///
/// TOFU HOST-KEY PINNING: the first key seen for a `host:port` is pinned; every
/// later connection must present the same key or is rejected. The pin store is
/// the safeStorage keychain (`keychain.pinGet`/`pinSet`), NOT migrated from the
/// Tauri build — russh and ssh2 serialize keys differently, so a Tauri-era pin
/// can't be compared against an ssh2-presented key without spuriously rejecting a
/// known host. Electron re-TOFUs each host once at cutover (documented in
/// keychain.ts `pinGet`). The pinned string is the OpenSSH authorized-keys line
/// (`<type> <base64>`), which needs only to be self-consistent within Electron.
///
/// No subscribe-gate: like ssh.rs, the shell's first bytes race is masked by
/// network latency (the frontend attaches its `ssh-data` listener after
/// `ssh_connect` resolves, before the round-trip banner arrives). `ssh-data`
/// carries the raw channel Buffer — no re-encoding, unlike the PTY string path.
import type { WebContents } from 'electron';
import type { Client as Ssh2Client, ClientChannel, ConnectConfig, SFTPWrapper } from 'ssh2';
import { emit } from '../events';
import { assertSafeRemoteDelete } from './fsutil';
import { pinGet, pinSet } from './keychain';
import { loadSsh2 } from './ssh2mod';
import type { Ssh2Module } from './ssh2mod';
import type { Handler } from './dispatch';

/// The chunk size for streamed SFTP transfers — big enough to keep the pipe busy,
/// small enough that a per-chunk progress tick feels live (ssh.rs SFTP_CHUNK).
const SFTP_CHUNK = 256 * 1024;

interface SshConnectReq {
  host: string;
  port: number;
  user: string;
  password?: string;
  private_key?: string;
  passphrase?: string;
  cols: number;
  rows: number;
  connect_id?: string;
}

/// A live SSH connection: the ssh2 Client plus the ids of the shell sessions
/// sharing it, so the Client is ended only when the last shell closes.
interface Conn {
  client: Ssh2Client;
  refs: Set<string>;
}

interface Session {
  conn: Conn;
  stream: ClientChannel;
  sender: WebContents;
}

let nextId = 1;
const sessions = new Map<string, Session>();

function emitPhase(sender: WebContents, connectId: string | undefined, phase: string): void {
  if (connectId !== undefined) emit(sender, 'ssh-connect-progress', { connect_id: connectId, phase });
}

/// TOFU: accept if unpinned (and pin it) or if the presented key matches the pin.
/// A pin-store failure (safeStorage unavailable, corrupt entry) must never block
/// the connection: an unreadable pin is treated as "no pin" and a failed pin
/// write is ignored — the connection proceeds, the pin just doesn't persist.
/// Mirrors keychain.rs `pin_get` (None when unset or unreadable) / the ignored
/// `pin_set` error in ssh.rs.
async function verifyHostKey(host: string, port: number, keyBuf: Buffer, ssh2: Ssh2Module): Promise<boolean> {
  const parsed = ssh2.utils.parseKey(keyBuf);
  const line =
    parsed instanceof Error
      ? keyBuf.toString('base64')
      : `${(Array.isArray(parsed) ? parsed[0] : parsed).type} ${(Array.isArray(parsed) ? parsed[0] : parsed)
          .getPublicSSH()
          .toString('base64')}`;
  const pinKey = `sshhostkey_${host}_${port}`;
  const pinned = await pinGet(pinKey).catch(() => null);
  if (pinned === null) {
    await pinSet(pinKey, line).catch(() => undefined); // first contact — pin and accept
    return true;
  }
  return pinned === line;
}

/// Register a freshly-opened interactive shell channel as a session and wire its
/// events to `ssh-data` / `ssh-exit`. Shared by connect and duplicate.
function registerShell(conn: Conn, stream: ClientChannel, sender: WebContents): string {
  const id = `s${nextId}`;
  nextId += 1;
  conn.refs.add(id);
  sessions.set(id, { conn, stream, sender });

  let exitCode: number | null = null;
  const cleanup = (): void => {
    if (!sessions.has(id)) return; // already torn down (e.g. by ssh_close)
    sessions.delete(id);
    conn.refs.delete(id);
    if (conn.refs.size === 0) {
      try {
        conn.client.end();
      } catch {
        /* already ended */
      }
    }
  };

  stream.on('data', (d: Buffer) => emit(sender, 'ssh-data', { id, bytes: d }));
  // Fold stderr (ExtendedData) into ssh-data, as ssh.rs does.
  stream.stderr.on('data', (d: Buffer) => emit(sender, 'ssh-data', { id, bytes: d }));
  stream.on('exit', (code: number | null) => {
    exitCode = typeof code === 'number' ? code : null;
  });
  stream.on('close', () => {
    cleanup();
    emit(sender, 'ssh-exit', { id, code: exitCode });
  });
  return id;
}

function openShell(conn: Conn, cols: number, rows: number, sender: WebContents): Promise<string> {
  return new Promise((resolve, reject) => {
    conn.client.shell({ term: 'xterm-256color', cols, rows }, (err, stream) => {
      if (err) {
        reject(new Error(`open channel: ${err.message}`));
        return;
      }
      resolve(registerShell(conn, stream, sender));
    });
  });
}

function openSftp(id: string): Promise<SFTPWrapper> {
  const s = sessions.get(id);
  if (s === undefined) return Promise.reject(new Error('no such session'));
  return new Promise((resolve, reject) => {
    s.conn.client.sftp((err, sftp) => {
      if (err) reject(new Error(`sftp subsystem: ${err.message}`));
      else resolve(sftp);
    });
  });
}

interface SftpEntry {
  name: string;
  is_dir: boolean;
  size: number;
}

/// Promisified SFTP primitives used by the directory-transfer / file-op
/// handlers below (the transfer panel's New Folder / Rename / Delete and the
/// recursive directory upload/download).
function sftpMkdirOnce(sftp: SFTPWrapper, path: string): Promise<void> {
  return new Promise((resolve, reject) => {
    sftp.mkdir(path, (err) => (err ? reject(new Error(`mkdir: ${err.message}`)) : resolve()));
  });
}
function sftpStat(sftp: SFTPWrapper, path: string): Promise<{ isDir: boolean } | null> {
  return new Promise((resolve) => {
    sftp.stat(path, (err, st) => {
      if (err) resolve(null); // missing / unreadable — caller decides
      else resolve({ isDir: st.isDirectory() });
    });
  });
}
/// mkdir -p: walk the path components, creating each missing segment. A segment
/// that already exists as a directory is fine; anything else that collides
/// throws. Handles both absolute ('/a/b') and home-relative ('./a', 'a') paths.
async function sftpMkdirp(sftp: SFTPWrapper, dirPath: string): Promise<void> {
  const absolute = dirPath.startsWith('/');
  const parts = dirPath.split('/').filter((p) => p !== '' && p !== '.');
  let cur = absolute ? '/' : '.';
  for (const part of parts) {
    cur = cur === '/' ? `/${part}` : `${cur}/${part}`;
    try {
      await sftpMkdirOnce(sftp, cur);
    } catch (e) {
      const st = await sftpStat(sftp, cur);
      if (st === null || !st.isDir) throw e;
    }
  }
}
/// rm -rf: recursive delete. Readdir returns full names; '.'/'..' never appear
/// in ssh2's readdir output, but skip defensively.
async function sftpRmrf(sftp: SFTPWrapper, target: string): Promise<void> {
  const st = await sftpStat(sftp, target);
  if (st === null) throw new Error(`no such file: ${target}`);
  if (!st.isDir) {
    await new Promise<void>((resolve, reject) => {
      sftp.unlink(target, (err) => (err ? reject(new Error(`delete: ${err.message}`)) : resolve()));
    });
    return;
  }
  const names = await new Promise<string[]>((resolve, reject) => {
    sftp.readdir(target, (err, list) => {
      if (err) reject(new Error(`read_dir: ${err.message}`));
      else resolve(list.map((e) => e.filename));
    });
  });
  for (const name of names) {
    if (name === '.' || name === '..') continue;
    await sftpRmrf(sftp, target === '/' ? `/${name}` : `${target.replace(/\/$/, '')}/${name}`);
  }
  await new Promise<void>((resolve, reject) => {
    sftp.rmdir(target, (err) => (err ? reject(new Error(`rmdir: ${err.message}`)) : resolve()));
  });
}

export const sshHandlers: Record<string, Handler> = {
  ssh_connect: async (args, ctx): Promise<string> => {
    const req = (args.req !== null && typeof args.req === 'object' ? args.req : {}) as SshConnectReq;
    const host = String(req.host ?? '');
    const port = Math.trunc(Number(req.port)) || 22;
    const user = String(req.user ?? '');
    const cols = Math.max(1, Math.trunc(Number(req.cols)) || 80);
    const rows = Math.max(1, Math.trunc(Number(req.rows)) || 24);
    const connectId = typeof req.connect_id === 'string' ? req.connect_id : undefined;
    const privateKey = typeof req.private_key === 'string' && req.private_key.trim() !== '' ? req.private_key : undefined;
    const password = typeof req.password === 'string' ? req.password : undefined;
    const passphrase = typeof req.passphrase === 'string' && req.passphrase !== '' ? req.passphrase : undefined;
    const sender = ctx.sender;

    const ssh2 = await loadSsh2();
    const client = new ssh2.Client();

    emitPhase(sender, connectId, 'tcp');
    return new Promise<string>((resolve, reject) => {
      let settled = false;
      const fail = (e: Error): void => {
        if (settled) return;
        settled = true;
        try {
          client.end();
        } catch {
          /* not connected */
        }
        reject(e);
      };

      client.on('handshake', () => emitPhase(sender, connectId, 'auth'));
      client.on('ready', () => {
        emitPhase(sender, connectId, 'channel');
        const conn: Conn = { client, refs: new Set() };
        openShell(conn, cols, rows, sender).then(
          (id) => {
            if (settled) return;
            settled = true;
            resolve(id);
          },
          (e) => fail(e as Error),
        );
      });
      client.on('error', (err: Error) => fail(new Error(`connect: ${err.message}`)));
      // A close before 'ready' means the handshake failed with no 'error'; after
      // resolve this is a no-op (settled guard), so it can't reject a live session.
      client.on('close', () => fail(new Error('connect: connection closed')));

      const cfg: ConnectConfig = {
        host,
        port,
        username: user,
        readyTimeout: 30_000,
        hostVerifier: (keyBuf: Buffer, verify: (valid: boolean) => void) => {
          verifyHostKey(host, port, keyBuf, ssh2).then(verify, () => verify(false));
        },
      };
      // A private key wins over a password when both are supplied (ssh.rs).
      if (privateKey !== undefined) {
        cfg.privateKey = privateKey;
        if (passphrase !== undefined) cfg.passphrase = passphrase;
      } else if (password !== undefined) {
        cfg.password = password;
      } else {
        fail(new Error('no credentials supplied'));
        return;
      }
      client.connect(cfg);
    });
  },

  ssh_duplicate: async (args, ctx): Promise<string> => {
    const id = String(args.id ?? '');
    const cols = Math.max(1, Math.trunc(Number(args.cols)) || 80);
    const rows = Math.max(1, Math.trunc(Number(args.rows)) || 24);
    const src = sessions.get(id);
    if (src === undefined) throw new Error('no such session');
    return openShell(src.conn, cols, rows, ctx.sender);
  },

  ssh_exec: async (args): Promise<string> => {
    const id = String(args.id ?? '');
    const command = String(args.command ?? '');
    const s = sessions.get(id);
    if (s === undefined) throw new Error('no such session');
    return new Promise<string>((resolve, reject) => {
      s.conn.client.exec(command, (err, stream) => {
        if (err) {
          reject(new Error(`exec: ${err.message}`));
          return;
        }
        const chunks: Buffer[] = [];
        stream.on('data', (d: Buffer) => chunks.push(d));
        stream.stderr.on('data', (d: Buffer) => chunks.push(d)); // stderr folded in (ssh.rs)
        stream.on('close', () => resolve(Buffer.concat(chunks).toString('utf8')));
      });
    });
  },

  ssh_write: async (args): Promise<void> => {
    const s = sessions.get(String(args.id ?? ''));
    if (s === undefined) throw new Error('session closed');
    s.stream.write(String(args.data ?? ''));
  },

  ssh_resize: async (args): Promise<void> => {
    const s = sessions.get(String(args.id ?? ''));
    if (s === undefined) throw new Error('session closed');
    const cols = Math.max(1, Math.trunc(Number(args.cols)) || 1);
    const rows = Math.max(1, Math.trunc(Number(args.rows)) || 1);
    s.stream.setWindow(rows, cols, 0, 0);
  },

  ssh_close: async (args): Promise<void> => {
    const s = sessions.get(String(args.id ?? ''));
    if (s === undefined) return; // already gone
    // Initiate close; the stream's own 'close' handler does the bookkeeping and
    // emits ssh-exit (and ends the Client if this was its last shell).
    try {
      s.stream.end();
    } catch {
      /* already closing */
    }
  },

  sftp_list: async (args): Promise<SftpEntry[]> => {
    const id = String(args.id ?? '');
    const dirPath = String(args.path ?? '');
    const sftp = await openSftp(id);
    try {
      return await new Promise<SftpEntry[]>((resolve, reject) => {
        sftp.readdir(dirPath, (err, list) => {
          if (err) {
            reject(new Error(`read_dir: ${err.message}`));
            return;
          }
          const out: SftpEntry[] = list.map((e) => ({
            name: e.filename,
            is_dir: e.attrs.isDirectory(),
            size: e.attrs.size ?? 0,
          }));
          // Dirs first, then name ascending (code-unit compare, matching ssh.rs's
          // byte ordering for ASCII paths).
          out.sort((a, b) => (a.is_dir !== b.is_dir ? (a.is_dir ? -1 : 1) : a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
          resolve(out);
        });
      });
    } finally {
      sftp.end();
    }
  },

  sftp_read: async (args, ctx): Promise<Uint8Array> => {
    const id = String(args.id ?? '');
    const filePath = String(args.path ?? '');
    const transferId = String(args.transferId ?? '');
    const sender = ctx.sender;
    const sftp = await openSftp(id);
    return new Promise<Uint8Array>((resolve, reject) => {
      const rs = sftp.createReadStream(filePath);
      const chunks: Buffer[] = [];
      let done = 0;
      let lastEmit = 0;
      rs.on('data', (d: Buffer) => {
        chunks.push(d);
        done += d.length;
        if (done - lastEmit >= SFTP_CHUNK) {
          lastEmit = done;
          emit(sender, 'sftp-progress', { transfer_id: transferId, done });
        }
      });
      rs.on('error', (err: Error) => {
        sftp.end();
        reject(new Error(`read: ${err.message}`));
      });
      rs.on('end', () => {
        const buf = Buffer.concat(chunks);
        emit(sender, 'sftp-progress', { transfer_id: transferId, done: buf.length }); // final exact tick
        sftp.end();
        resolve(buf); // raw bytes over IPC (§7 row 4), no base64
      });
    });
  },

  sftp_write: async (args, ctx): Promise<void> => {
    const id = String(args.id ?? '');
    const filePath = String(args.path ?? '');
    const transferId = String(args.transferId ?? '');
    const sender = ctx.sender;
    const data = Buffer.from((args.bytes ?? new Uint8Array()) as Uint8Array); // raw bytes over IPC (§7 row 4)
    const total = data.length;
    const sftp = await openSftp(id);
    return new Promise<void>((resolve, reject) => {
      const ws = sftp.createWriteStream(filePath);
      ws.on('error', (err: Error) => {
        sftp.end();
        reject(new Error(`write: ${err.message}`));
      });
      ws.on('close', () => {
        sftp.end();
        resolve();
      });
      let done = 0;
      let lastEmit = 0;
      for (let off = 0; off < total; off += SFTP_CHUNK) {
        const chunk = data.subarray(off, Math.min(off + SFTP_CHUNK, total));
        ws.write(chunk);
        done += chunk.length;
        if (done - lastEmit >= SFTP_CHUNK || done === total) {
          lastEmit = done;
          emit(sender, 'sftp-progress', { transfer_id: transferId, done });
        }
      }
      ws.end();
    });
  },

  /// mkdir -p on the remote (recursive — New Folder + directory upload).
  sftp_mkdir: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const dirPath = String(args.path ?? '');
    const sftp = await openSftp(id);
    try {
      await sftpMkdirp(sftp, dirPath);
    } finally {
      sftp.end();
    }
  },

  /// Recursive delete (rm -rf) — files unlink, dirs walk then rmdir. The guard
  /// refuses the remote root / working dir / '~' before any traversal begins.
  sftp_delete: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const target = String(args.path ?? '');
    assertSafeRemoteDelete(target);
    const sftp = await openSftp(id);
    try {
      await sftpRmrf(sftp, target);
    } finally {
      sftp.end();
    }
  },

  sftp_rename: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const from = String(args.from ?? '');
    const to = String(args.to ?? '');
    const sftp = await openSftp(id);
    try {
      await new Promise<void>((resolve, reject) => {
        sftp.rename(from, to, (err) => (err ? reject(new Error(`rename: ${err.message}`)) : resolve()));
      });
    } finally {
      sftp.end();
    }
  },
};

/// End every live SSH connection — wired to `before-quit` so quitting never
/// leaves a dangling socket. Best-effort.
export function disposeAllSsh(): void {
  const clients = new Set<Ssh2Client>();
  for (const s of sessions.values()) clients.add(s.conn.client);
  sessions.clear();
  for (const c of clients) {
    try {
      c.end();
    } catch {
      /* already ended */
    }
  }
}
