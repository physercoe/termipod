/// SSH local port forwarding (`ssh -L` semantics) — the "SSH-forward wedge"
/// from `docs/plans/agent-transcript-redesign.md` §7 decision 1, shared with
/// the Replay plan's remote-dataset follow-up. A forward is a loopback
/// listener on an ephemeral port; every TCP connection it accepts is piped
/// over a direct-tcpip channel opened on an EXISTING authenticated ssh2
/// Client (the same `forwardOut` primitive the jump-host chain uses), so a
/// remote service becomes reachable at `127.0.0.1:<localPort>` with no new
/// credentials and no shell command.
///
/// Forwards are parasitic on their session's connection by design: they are
/// torn down when the underlying Client ends (last shell closed, disconnect,
/// quit) rather than keeping it alive — a forward must never hold a TCP
/// connection open with no visible UI owning it. Consumers listen for
/// `ssh-forward-closed` to react.
///
/// This module is electron- and ssh2-free (the channel factory is injected)
/// so `node --test` exercises the full listener/pipe/teardown lifecycle.
import { createServer, type Server, type Socket } from 'node:net';
import type { Duplex } from 'node:stream';

/// Opens one direct-tcpip channel to the forward's remote target. Rejection
/// refuses the single incoming connection; the listener stays up.
export type ChannelFactory = () => Promise<Duplex>;

interface Forward {
  id: string;
  server: Server;
  localPort: number;
  remoteHost: string;
  remotePort: number;
  /// Live local-socket → channel pairs, so stop can sever mid-flight streams.
  /// A pair is tracked from the moment the socket is ACCEPTED — its channel is
  /// null while still opening, or a stop racing the channel-open would miss
  /// the socket and leak it past teardown.
  pairs: Set<{ sock: Socket; ch: Duplex | null }>;
  onAutoClose: ((forwardId: string) => void) | null;
}

let nextForwardId = 1;
const forwards = new Map<string, Forward>();

export interface ForwardInfo {
  forward_id: string;
  local_port: number;
  remote_host: string;
  remote_port: number;
}

function infoOf(f: Forward): ForwardInfo {
  return { forward_id: f.id, local_port: f.localPort, remote_host: f.remoteHost, remote_port: f.remotePort };
}

/// Start a forward: listen on 127.0.0.1:0 and pipe each accepted connection
/// over a freshly-opened channel. `onAutoClose` fires when the forward dies
/// for any reason other than an explicit `stopForward` (session teardown).
export function startForward(
  openChannel: ChannelFactory,
  remoteHost: string,
  remotePort: number,
  onAutoClose: ((forwardId: string) => void) | null = null,
): Promise<ForwardInfo> {
  return new Promise((resolve, reject) => {
    const server = createServer((sock: Socket) => {
      const pair: { sock: Socket; ch: Duplex | null } = { sock, ch: null };
      fwd.pairs.add(pair);
      const drop = (): void => {
        fwd.pairs.delete(pair);
        sock.destroy();
        pair.ch?.destroy();
      };
      sock.on('error', drop);
      sock.on('close', drop);
      openChannel().then(
        (ch) => {
          // The forward (or this socket) may have been torn down while the
          // channel was opening — never wire a stream onto a dead forward.
          if (!forwards.has(fwd.id) || !fwd.pairs.has(pair)) {
            ch.destroy();
            drop();
            return;
          }
          pair.ch = ch;
          ch.on('error', drop);
          ch.on('close', drop);
          sock.pipe(ch);
          ch.pipe(sock);
        },
        drop, // channel refused — this connection only
      );
    });
    const fwd: Forward = { id: `f${nextForwardId}`, server, localPort: 0, remoteHost, remotePort, pairs: new Set(), onAutoClose };
    nextForwardId += 1;
    server.on('error', (err: Error) => reject(new Error(`forward listen: ${err.message}`)));
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (addr === null || typeof addr === 'string') {
        server.close();
        reject(new Error('forward listen: no address'));
        return;
      }
      fwd.localPort = addr.port;
      forwards.set(fwd.id, fwd);
      resolve(infoOf(fwd));
    });
  });
}

function teardown(f: Forward): void {
  forwards.delete(f.id);
  try {
    f.server.close();
  } catch {
    /* already closed */
  }
  for (const p of f.pairs) {
    p.sock.destroy();
    p.ch?.destroy();
  }
  f.pairs.clear();
}

/// Explicit stop (renderer-requested). Idempotent; never fires onAutoClose.
export function stopForward(forwardId: string): void {
  const f = forwards.get(forwardId);
  if (f === undefined) return;
  teardown(f);
}

/// Stop a set of forwards because their session died. Fires each onAutoClose.
export function autoCloseForwards(ids: Iterable<string>): void {
  for (const id of ids) {
    const f = forwards.get(id);
    if (f === undefined) continue;
    teardown(f);
    f.onAutoClose?.(f.id);
  }
}

export function listForwards(ids: Iterable<string>): ForwardInfo[] {
  const out: ForwardInfo[] = [];
  for (const id of ids) {
    const f = forwards.get(id);
    if (f !== undefined) out.push(infoOf(f));
  }
  return out;
}

/// End-of-world sweep (before-quit): every forward, no callbacks.
export function disposeAllForwards(): void {
  for (const f of [...forwards.values()]) teardown(f);
}
