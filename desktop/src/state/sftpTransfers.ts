export type SftpTransferDirection = 'up' | 'down';
export type SftpTransferStatus = 'queued' | 'active' | 'done' | 'error';

export interface SftpTransfer {
  id: string;
  sessionId: string;
  name: string;
  dir: SftpTransferDirection;
  done: number;
  total: number;
  status: SftpTransferStatus;
  note?: string;
  error?: string;
}

export interface SftpTransferRunContext {
  id: string;
  update: (patch: Partial<Pick<SftpTransfer, 'done' | 'total' | 'note'>>) => void;
}

export interface SftpTransferRequest {
  sessionId: string;
  name: string;
  dir: SftpTransferDirection;
  total: number;
  run: (context: SftpTransferRunContext) => Promise<void>;
}

export interface SftpTransferHandle {
  id: string;
  completion: Promise<SftpTransfer>;
}

interface InternalTransfer extends SftpTransfer {
  run: SftpTransferRequest['run'];
  finish: (transfer: SftpTransfer) => void;
}

interface QueueOptions {
  makeId?: () => string;
  retentionMs?: number;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * A renderer-lifetime queue, keyed by SSH session. It deliberately lives
 * outside React: switching the session sub-view unmounts FileTransferPanel,
 * but must not cancel the job or discard its progress.
 */
export class SftpTransferQueue {
  private readonly jobs = new Map<string, InternalTransfer[]>();
  private readonly snapshots = new Map<string, readonly SftpTransfer[]>();
  private readonly listeners = new Map<string, Set<() => void>>();
  private readonly running = new Set<string>();
  private readonly makeId: () => string;
  private readonly retentionMs: number;

  constructor(options: QueueOptions = {}) {
    this.makeId = options.makeId ?? (() => `tx${crypto.randomUUID()}`);
    this.retentionMs = options.retentionMs ?? 8_000;
  }

  list(sessionId: string): readonly SftpTransfer[] {
    return this.snapshots.get(sessionId) ?? EMPTY_TRANSFERS;
  }

  subscribe(sessionId: string, listener: () => void): () => void {
    const set = this.listeners.get(sessionId) ?? new Set<() => void>();
    set.add(listener);
    this.listeners.set(sessionId, set);
    return () => {
      set.delete(listener);
      if (set.size === 0) this.listeners.delete(sessionId);
    };
  }

  enqueue(request: SftpTransferRequest): SftpTransferHandle {
    const id = this.makeId();
    let finish: (transfer: SftpTransfer) => void = () => {};
    const completion = new Promise<SftpTransfer>((resolve) => {
      finish = resolve;
    });
    const job: InternalTransfer = {
      id,
      sessionId: request.sessionId,
      name: request.name,
      dir: request.dir,
      done: 0,
      total: request.total,
      status: 'queued',
      run: request.run,
      finish,
    };
    this.jobs.set(request.sessionId, [...(this.jobs.get(request.sessionId) ?? []), job]);
    this.publish(request.sessionId);
    void this.pump(request.sessionId);
    return { id, completion };
  }

  private update(sessionId: string, id: string, patch: Partial<SftpTransfer>): void {
    const current = this.jobs.get(sessionId) ?? [];
    this.jobs.set(
      sessionId,
      current.map((job) => (job.id === id ? { ...job, ...patch } : job)),
    );
    this.publish(sessionId);
  }

  private publish(sessionId: string): void {
    const snapshot = (this.jobs.get(sessionId) ?? []).map(({ run: _run, finish: _finish, ...job }) => job);
    this.snapshots.set(sessionId, snapshot);
    for (const listener of this.listeners.get(sessionId) ?? []) listener();
  }

  private async pump(sessionId: string): Promise<void> {
    if (this.running.has(sessionId)) return;
    this.running.add(sessionId);
    try {
      while (true) {
        const job = (this.jobs.get(sessionId) ?? []).find((candidate) => candidate.status === 'queued');
        if (job === undefined) break;
        this.update(sessionId, job.id, { status: 'active' });
        try {
          await job.run({
            id: job.id,
            update: (patch) => this.update(sessionId, job.id, patch),
          });
          const latest = (this.jobs.get(sessionId) ?? []).find((candidate) => candidate.id === job.id);
          const done = latest?.total !== undefined && latest.total > 0 ? latest.total : (latest?.done ?? 0);
          this.update(sessionId, job.id, { status: 'done', done, note: undefined });
        } catch (error) {
          this.update(sessionId, job.id, { status: 'error', error: message(error) });
        }
        const terminal = this.list(sessionId).find((candidate) => candidate.id === job.id);
        if (terminal !== undefined) job.finish(terminal);
        if (this.retentionMs >= 0) {
          setTimeout(() => {
            const remaining = (this.jobs.get(sessionId) ?? []).filter((candidate) => candidate.id !== job.id);
            this.jobs.set(sessionId, remaining);
            this.publish(sessionId);
          }, this.retentionMs);
        }
      }
    } finally {
      this.running.delete(sessionId);
      // A job may have arrived between the final lookup and clearing `running`.
      if ((this.jobs.get(sessionId) ?? []).some((candidate) => candidate.status === 'queued')) void this.pump(sessionId);
    }
  }
}

const EMPTY_TRANSFERS: readonly SftpTransfer[] = [];
const queue = new SftpTransferQueue();

export function enqueueSftpTransfer(request: SftpTransferRequest): SftpTransferHandle {
  return queue.enqueue(request);
}

export function listSftpTransfers(sessionId: string): readonly SftpTransfer[] {
  return queue.list(sessionId);
}

export function subscribeSftpTransfers(sessionId: string, listener: () => void): () => void {
  return queue.subscribe(sessionId, listener);
}
