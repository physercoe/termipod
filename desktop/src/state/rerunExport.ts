/// The Rerun export flow (J8 Replay W4b-1) — submit a host job, poll it, hand
/// the produced `.rrd` to the W4a Rerun manager.
///
/// PURE — no runtime imports, so `node --test` loads it directly (the #411
/// node-ESM lesson). Everything that touches the hub or the Electron bridge
/// lives in the surface; what is here is the state machine and the reading of
/// an untyped command row, which is the part that is actually easy to get wrong.
///
/// Why a poll at all: the export decodes every frame of an episode, so it runs
/// as a detached host job (ADR-058) and the hub answers with a command id. For
/// a host job `delivered` means *running* — the command lifecycle gained no new
/// status, which is why nothing else in the app had to learn one.

/// How often to ask the hub for the command row.
///
/// The host pushes progress every ~30s, so polling much faster only costs
/// requests; polling much slower makes a finished export feel stuck. 2s is the
/// same order as the rest of the app's short polls.
export const EXPORT_POLL_MS = 2_000;

/// Where the flow is. `running` covers both `pending` (the host has not picked
/// it up) and `delivered` (it is working) — the distinction matters to the hub,
/// not to someone waiting for a file. `fetching` is W4b-2's remote case: the
/// export finished on another machine and its bytes are coming over SFTP.
export type ExportPhase =
  | 'idle'
  | 'submitting'
  | 'running'
  | 'fetching'
  | 'opening'
  | 'ready'
  | 'failed';

export interface ExportProgress {
  phase: string;
  done: number;
  total: number;
}

/// The recording a finished job produced, as the host described it.
export interface Artifact {
  path: string;
  sha256: string;
  bytes: number;
}

export interface ExportState {
  phase: ExportPhase;
  /// The host job's command id, once submitted. Kept through `ready` so a
  /// caller can still cancel or re-read it.
  commandId: string | null;
  progress: ExportProgress | null;
  /// What the host reported it produced — a path on ITS filesystem, which is
  /// only this machine's when the dataset is local.
  artifact: Artifact | null;
  /// Bytes moved / total for the `fetching` phase. Separate from `progress`,
  /// which is the host's own export progress — conflating them would make a
  /// stalled transfer look like a stalled export.
  transfer: { done: number; total: number } | null;
  /// Loopback viewer URL, once the Rerun manager is serving it.
  viewerUrl: string | null;
  error: string | null;
}

export const IDLE_EXPORT: ExportState = {
  phase: 'idle',
  commandId: null,
  progress: null,
  artifact: null,
  transfer: null,
  viewerUrl: null,
  error: null,
};

/// The fields of a `host_commands` row this flow reads. Hub entities arrive
/// untyped (`Record<string, unknown>`) — there are no generated models to
/// update, and none to save us — so every field is read defensively.
export interface CommandView {
  status: string;
  progress: ExportProgress | null;
  path: string | null;
  /// The host's sha-256 of the recording. Load-bearing for the remote fetch:
  /// it names the local cache file and proves the bytes that arrived are the
  /// ones the host exported.
  sha256: string | null;
  bytes: number;
  error: string | null;
}

function str(o: Record<string, unknown>, k: string): string | null {
  const v = o[k];
  return typeof v === 'string' && v !== '' ? v : null;
}

function num(o: Record<string, unknown>, k: string): number {
  const v = o[k];
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

function obj(o: Record<string, unknown>, k: string): Record<string, unknown> | null {
  const v = o[k];
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : null;
}

/// Read a command row into the four things the flow cares about.
export function readCommand(entity: unknown): CommandView {
  const row = typeof entity === 'object' && entity !== null ? (entity as Record<string, unknown>) : {};
  const progressRaw = obj(row, 'progress');
  const resultRaw = obj(row, 'result');
  return {
    status: str(row, 'status') ?? '',
    progress:
      progressRaw === null
        ? null
        : {
            phase: str(progressRaw, 'phase') ?? '',
            done: num(progressRaw, 'done'),
            total: num(progressRaw, 'total'),
          },
    path: resultRaw === null ? null : str(resultRaw, 'path'),
    sha256: resultRaw === null ? null : str(resultRaw, 'sha256'),
    bytes: resultRaw === null ? 0 : num(resultRaw, 'bytes'),
    error: str(row, 'error'),
  };
}

/// Advance the flow on one poll result.
///
/// `done` without a path is treated as a failure rather than success: the whole
/// point of the poll is to obtain a file to open, and reporting "ready" with
/// nothing to open would send the viewer at `undefined`. The host already
/// refuses to report `done` without one, so this is the second of two guards on
/// the same mistake — deliberately, because they are on opposite sides of a
/// network.
export function advanceExport(prev: ExportState, view: CommandView): ExportState {
  switch (view.status) {
    case 'pending':
    case 'delivered':
      return { ...prev, phase: 'running', progress: view.progress ?? prev.progress };
    case 'done':
      if (view.path === null) {
        return {
          ...prev,
          phase: 'failed',
          error: 'the export finished but reported no file',
        };
      }
      return {
        ...prev,
        phase: 'opening',
        progress: null,
        artifact: { path: view.path, sha256: view.sha256 ?? '', bytes: view.bytes },
        error: null,
      };
    case 'failed':
      return {
        ...prev,
        phase: 'failed',
        progress: null,
        error: view.error ?? 'the export failed without a reason',
      };
    default:
      // An unknown status is not a reason to declare failure — a newer hub
      // could add one — so hold the current phase and keep polling.
      return prev;
  }
}

/// Whether the flow still wants to be polled.
export function isPolling(s: ExportState): boolean {
  return s.phase === 'submitting' || s.phase === 'running';
}

/// Whether a cancel is meaningful right now.
export function canCancel(s: ExportState): boolean {
  return s.phase === 'running' && s.commandId !== null;
}

/// A short human line for the status area. Returns null when there is nothing
/// worth saying, so the caller renders nothing rather than an empty row.
export function progressLabel(s: ExportState): string | null {
  switch (s.phase) {
    case 'idle':
      return null;
    case 'submitting':
      return 'submitting…';
    case 'running': {
      const p = s.progress;
      if (p === null || p.phase === '') return 'exporting on the host…';
      if (p.total > 0) {
        const pct = Math.min(100, Math.max(0, Math.round((p.done / p.total) * 100)));
        return `${p.phase} ${pct}%`;
      }
      return `${p.phase}…`;
    }
    case 'fetching': {
      const x = s.transfer;
      if (x === null || x.total <= 0) return 'fetching the recording…';
      const mb = (n: number): string => (n / (1024 * 1024)).toFixed(1);
      return `fetching the recording — ${mb(x.done)} / ${mb(x.total)} MB`;
    }
    case 'opening':
      return 'starting the viewer…';
    case 'ready':
      return null;
    case 'failed':
      return s.error;
  }
}

/// Why a finished export cannot be opened. Each maps to one sentence the
/// director can act on — which is the whole point of naming them rather than
/// returning a single "cannot open".
export type BlockedReason =
  | 'badPath' // the host reported something that is not an absolute .rrd
  | 'noConnection' // the file is elsewhere and no SSH connection is set for this dataset
  | 'noSession' // a connection is set, but no terminal tab is live on it
  | 'noDigest'; // the host reported no sha256, so the transfer cannot be verified

/// What to do with a finished export's artifact.
export type ArtifactPlan =
  | { kind: 'local'; path: string }
  | { kind: 'fetch'; sessionId: string; path: string; sha256: string; bytes: number }
  | { kind: 'blocked'; reason: BlockedReason };

/// Decide how to get the recording in front of the viewer (J8 Replay W4b-2).
///
/// **`localExists` is the signal, not the dataset's remote-video setting.** The
/// export always runs on the host that owns the dataset's bytes and returns a
/// path on *that* filesystem; whether that host is this machine has no reliable
/// answer in the hub's metadata, but the local filesystem answers it directly.
/// An earlier revision used the video-source connection as the proxy, which got
/// the common case right and silently mis-classified the rest: a remote dataset
/// browsed for its plots alone has no connection set, so it read as local and
/// the viewer failed with "rerun exited before serving".
///
/// `remoteConn` (the connection the director picked for this dataset's video,
/// from `replayRemoteStore`) is still what says *where* to fetch from, and
/// `session` is that connection's live ssh session if a terminal tab has one.
/// Reusing them means there is one notion of "the machine this dataset is on"
/// rather than two that can disagree.
export function planArtifact(
  artifact: Artifact | null,
  opts: { localExists: boolean; remoteConn: string | null; session: string | null },
): ArtifactPlan {
  if (artifact === null || !isOpenableRecording(artifact.path)) {
    return { kind: 'blocked', reason: 'badPath' };
  }
  if (opts.localExists) return { kind: 'local', path: artifact.path };
  if (opts.remoteConn === null || opts.remoteConn === '') {
    return { kind: 'blocked', reason: 'noConnection' };
  }
  if (opts.session === null || opts.session === '') {
    return { kind: 'blocked', reason: 'noSession' };
  }
  // Refused rather than fetched unverified: the digest is what names the local
  // cache file and what proves the bytes are the ones the host exported. A
  // host-runner too old to report one is a fixable state, not a reason to
  // invent a filename and trust whatever arrives.
  if (!/^[0-9a-f]{64}$/.test(artifact.sha256)) {
    return { kind: 'blocked', reason: 'noDigest' };
  }
  return {
    kind: 'fetch',
    sessionId: opts.session,
    path: artifact.path,
    sha256: artifact.sha256,
    bytes: artifact.bytes,
  };
}

/// The recording path is handed straight to the Rerun manager, which will only
/// accept an absolute `.rrd`. Checking it here too means a malformed result
/// surfaces as this flow's error rather than as an opaque rejection from the
/// bridge — and, more to the point, the desktop never asks the shell to open
/// something a host claimed was a recording without looking at it.
export function isOpenableRecording(p: string | null): boolean {
  if (p === null) return false;
  const t = p.trim();
  if (t === '') return false;
  if (!t.toLowerCase().endsWith('.rrd')) return false;
  // Absolute in either flavour: the host may be POSIX while the desktop is
  // Windows, or the reverse.
  return t.startsWith('/') || /^[A-Za-z]:[\\/]/.test(t) || t.startsWith('\\\\');
}
