/// Minimal SOCKS5 CONNECT client (RFC 1928, + RFC 1929 username/password) — the
/// Electron port of the mobile `lib/services/ssh/socks5_socket.dart`, so a
/// desktop connection reaches the same proxied hosts a phone can. The proxy is
/// the OUTERMOST hop: it tunnels the TCP stream the SSH handshake then rides
/// (`ConnectConfig.sock`), targeting the jump host when one is configured.
///
/// The target is always sent as ATYP=domain (0x03) — the proxy resolves it, so
/// DNS for internal names happens on the proxy's side of the tunnel, exactly as
/// on mobile. Handshake reads go through `socket.read(n)`, which consumes
/// exactly n bytes and leaves anything beyond them (e.g. an SSH banner the
/// server coalesced into the same packet as the CONNECT reply) buffered in the
/// stream for ssh2 to read — a plain 'data' listener would swallow those bytes.
import { Socket } from 'node:net';

export interface Socks5Options {
  proxyHost: string;
  proxyPort: number;
  targetHost: string;
  targetPort: number;
  username?: string;
  password?: string;
  timeoutMs?: number;
}

const REPLY_MESSAGES: Record<number, string> = {
  0x01: 'general failure',
  0x02: 'connection not allowed by ruleset',
  0x03: 'network unreachable',
  0x04: 'host unreachable',
  0x05: 'connection refused',
  0x06: 'TTL expired',
  0x07: 'command not supported',
  0x08: 'address type not supported',
};

function replyMessage(code: number): string {
  return REPLY_MESSAGES[code] ?? `unknown error (${code})`;
}

/// Read exactly n bytes without consuming what follows them. `read(n)` returns
/// null until n bytes are buffered; the remainder stays in the stream.
function readExact(socket: Socket, n: number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const tryRead = (): void => {
      const buf = socket.read(n) as Buffer | null;
      if (buf !== null) {
        cleanup();
        resolve(buf);
      }
    };
    const onError = (err: Error): void => {
      cleanup();
      reject(err);
    };
    const onEnd = (): void => {
      cleanup();
      reject(new Error('connection closed during SOCKS5 handshake'));
    };
    const cleanup = (): void => {
      socket.off('readable', tryRead);
      socket.off('error', onError);
      socket.off('end', onEnd);
    };
    socket.on('readable', tryRead);
    socket.on('error', onError);
    socket.on('end', onEnd);
    tryRead();
  });
}

/// Run the SOCKS5 greeting + (optional) auth + CONNECT exchange on an
/// already-connected socket. Exported separately from `socks5Connect` so tests
/// can drive it against an in-process server. On resolve the socket is a raw
/// tunnel to the target.
export async function socks5Handshake(socket: Socket, o: Socks5Options): Promise<void> {
  const hasAuth = o.username !== undefined && o.username !== '' && o.password !== undefined;
  const user = Buffer.from(o.username ?? '', 'utf8');
  const pass = Buffer.from(o.password ?? '', 'utf8');
  const target = Buffer.from(o.targetHost, 'utf8');
  if (target.length === 0 || target.length > 255) throw new Error(`SOCKS5: target host too long (${target.length} bytes)`);
  if (hasAuth && (user.length > 255 || pass.length > 255)) throw new Error('SOCKS5: username/password over 255 bytes');

  // Greeting: no-auth always offered; username/password added when configured.
  socket.write(hasAuth ? Buffer.from([0x05, 0x02, 0x00, 0x02]) : Buffer.from([0x05, 0x01, 0x00]));
  const method = await readExact(socket, 2);
  if (method[0] !== 0x05) throw new Error(`SOCKS5: bad version ${method[0]}`);
  if (method[1] === 0xff) throw new Error('SOCKS5: no acceptable auth methods');
  if (method[1] === 0x02) {
    if (!hasAuth) throw new Error('SOCKS5: proxy requires authentication');
    socket.write(Buffer.concat([Buffer.from([0x01, user.length]), user, Buffer.from([pass.length]), pass]));
    const auth = await readExact(socket, 2);
    if (auth[1] !== 0x00) throw new Error('SOCKS5: proxy authentication failed');
  } else if (method[1] !== 0x00) {
    throw new Error(`SOCKS5: unsupported auth method ${method[1]}`);
  }

  // CONNECT: VER CMD RSV ATYP=domain LEN ADDR PORT.
  socket.write(
    Buffer.concat([
      Buffer.from([0x05, 0x01, 0x00, 0x03, target.length]),
      target,
      Buffer.from([(o.targetPort >> 8) & 0xff, o.targetPort & 0xff]),
    ]),
  );
  const head = await readExact(socket, 4);
  if (head[0] !== 0x05) throw new Error(`SOCKS5: bad version in reply ${head[0]}`);
  if (head[1] !== 0x00) throw new Error(`SOCKS5 connect failed: ${replyMessage(head[1])}`);
  // Drain the bound address the reply carries; its shape depends on ATYP.
  switch (head[3]) {
    case 0x01:
      await readExact(socket, 4 + 2);
      break;
    case 0x03: {
      const len = await readExact(socket, 1);
      await readExact(socket, len[0] + 2);
      break;
    }
    case 0x04:
      await readExact(socket, 16 + 2);
      break;
    default:
      throw new Error(`SOCKS5: unknown address type ${head[3]}`);
  }
}

/// Open a TCP connection to the proxy and tunnel it to the target. The timeout
/// covers connect + handshake together; on any failure the socket is destroyed.
export function socks5Connect(o: Socks5Options): Promise<Socket> {
  return new Promise((resolve, reject) => {
    const socket = new Socket();
    let settled = false;
    const timer = setTimeout(() => fail(new Error('SOCKS5: proxy connection timed out')), o.timeoutMs ?? 20_000);
    const fail = (err: Error): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      socket.destroy();
      reject(err);
    };
    socket.once('error', fail);
    socket.connect(o.proxyPort, o.proxyHost, () => {
      socks5Handshake(socket, o).then(() => {
        if (settled) {
          socket.destroy(); // timed out mid-handshake — don't leak the tunnel
          return;
        }
        settled = true;
        clearTimeout(timer);
        socket.off('error', fail);
        resolve(socket);
      }, fail);
    });
  });
}
