/// Tests for the SOCKS5 CONNECT client against an in-process scripted server:
/// the no-auth and username/password paths, refusal replies, and — the one that
/// matters most for SSH — a CONNECT reply coalesced with the first tunnel bytes
/// in a single packet, which must leave those bytes readable after the
/// handshake (an SSH server's banner arrives exactly like that). Run with
/// `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server, type Socket } from 'node:net';
import { socks5Connect } from './socks5.ts';

/// A scripted SOCKS5 server: consumes the greeting (and auth when `expectAuth`),
/// then answers CONNECT with `reply` (0x00 = granted), optionally appending
/// `trailing` to the same write. Echoes tunnel bytes back prefixed with 'echo:'.
function scriptedServer(opts: { expectAuth?: boolean; reply?: number; trailing?: Buffer }): Promise<{ server: Server; port: number; seen: Buffer[] }> {
  const seen: Buffer[] = [];
  const server = createServer((sock: Socket) => {
    let stage = 0;
    let buf = Buffer.alloc(0);
    sock.on('data', (d: Buffer) => {
      buf = Buffer.concat([buf, d]);
      if (stage === 0 && buf.length >= 2 && buf.length >= 2 + buf[1]) {
        const methods = [...buf.subarray(2, 2 + buf[1])];
        buf = buf.subarray(2 + methods.length);
        if (opts.expectAuth === true) {
          // A real auth-only proxy answers 0xFF when user/pass wasn't offered.
          sock.write(Buffer.from([0x05, methods.includes(0x02) ? 0x02 : 0xff]));
          stage = 1;
        } else {
          sock.write(Buffer.from([0x05, 0x00]));
          stage = 2;
        }
      }
      if (stage === 1 && buf.length >= 2) {
        const ulen = buf[1];
        if (buf.length >= 2 + ulen + 1 && buf.length >= 2 + ulen + 1 + buf[2 + ulen]) {
          const plen = buf[2 + ulen];
          seen.push(buf.subarray(2, 2 + ulen), buf.subarray(3 + ulen, 3 + ulen + plen));
          buf = buf.subarray(3 + ulen + plen);
          sock.write(Buffer.from([0x01, 0x00]));
          stage = 2;
        }
      }
      if (stage === 2 && buf.length >= 5 && buf.length >= 5 + buf[4] + 2) {
        const alen = buf[4];
        seen.push(buf.subarray(5, 5 + alen)); // target host the client asked for
        buf = buf.subarray(5 + alen + 2);
        const granted = Buffer.from([0x05, opts.reply ?? 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0]);
        sock.write(opts.trailing !== undefined ? Buffer.concat([granted, opts.trailing]) : granted);
        stage = 3;
        return;
      }
      if (stage === 3 && buf.length > 0) {
        sock.write(Buffer.concat([Buffer.from('echo:'), buf]));
        buf = Buffer.alloc(0);
      }
    });
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      resolve({ server, port: (server.address() as { port: number }).port, seen });
    });
  });
}

function readOnce(sock: Socket): Promise<Buffer> {
  return new Promise((resolve) => sock.once('data', resolve));
}

test('no-auth CONNECT tunnels and carries the domain target', async () => {
  const { server, port, seen } = await scriptedServer({});
  try {
    const sock = await socks5Connect({ proxyHost: '127.0.0.1', proxyPort: port, targetHost: 'internal.example', targetPort: 2222 });
    sock.write('ping');
    const back = await readOnce(sock);
    assert.equal(back.toString(), 'echo:ping');
    assert.equal(seen[0].toString(), 'internal.example');
    sock.destroy();
  } finally {
    server.close();
  }
});

test('username/password sub-negotiation (RFC 1929)', async () => {
  const { server, port, seen } = await scriptedServer({ expectAuth: true });
  try {
    const sock = await socks5Connect({
      proxyHost: '127.0.0.1',
      proxyPort: port,
      targetHost: 'h',
      targetPort: 22,
      username: 'alice',
      password: 's3cret',
    });
    assert.equal(seen[0].toString(), 'alice');
    assert.equal(seen[1].toString(), 's3cret');
    sock.destroy();
  } finally {
    server.close();
  }
});

test('auth-required proxy without credentials is refused client-side', async () => {
  const { server, port } = await scriptedServer({ expectAuth: true });
  try {
    await assert.rejects(
      socks5Connect({ proxyHost: '127.0.0.1', proxyPort: port, targetHost: 'h', targetPort: 22 }),
      /no acceptable auth methods/,
    );
  } finally {
    server.close();
  }
});

test('non-zero CONNECT reply surfaces the reason', async () => {
  const { server, port } = await scriptedServer({ reply: 0x05 });
  try {
    await assert.rejects(
      socks5Connect({ proxyHost: '127.0.0.1', proxyPort: port, targetHost: 'h', targetPort: 22 }),
      /connection refused/,
    );
  } finally {
    server.close();
  }
});

test('bytes coalesced with the CONNECT reply are not swallowed', async () => {
  // An SSH server's banner can arrive in the SAME packet as the proxy's granted
  // reply; the handshake must consume exactly its own bytes and leave the
  // banner readable — this is why reads use socket.read(n), not 'data'.
  const banner = Buffer.from('SSH-2.0-test\r\n');
  const { server, port } = await scriptedServer({ trailing: banner });
  try {
    const sock = await socks5Connect({ proxyHost: '127.0.0.1', proxyPort: port, targetHost: 'h', targetPort: 22 });
    const first = await new Promise<Buffer>((resolve) => {
      const tryRead = (): void => {
        const b = sock.read(banner.length) as Buffer | null;
        if (b !== null) {
          sock.off('readable', tryRead);
          resolve(b);
        }
      };
      sock.on('readable', tryRead);
      tryRead();
    });
    assert.equal(first.toString(), 'SSH-2.0-test\r\n');
    sock.destroy();
  } finally {
    server.close();
  }
});
