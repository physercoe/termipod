/**
 * Tracks cancellable SFTP streams by renderer + transfer id. Keeping this
 * registry independent of ssh2 makes cancellation semantics unit-testable.
 */
export class SftpCancelRegistry {
  private readonly cancels = new Map<string, () => void>();
  private readonly pending = new Set<string>();

  private key(senderId: number, transferId: string): string {
    return `${senderId}:${transferId}`;
  }

  register(senderId: number, transferId: string, cancel: () => void): () => void {
    if (transferId === '') return () => {};
    const key = this.key(senderId, transferId);
    if (this.pending.delete(key)) {
      queueMicrotask(cancel);
      return () => {};
    }
    this.cancels.set(key, cancel);
    return () => {
      if (this.cancels.get(key) === cancel) this.cancels.delete(key);
    };
  }

  cancel(senderId: number, transferId: string): boolean {
    if (transferId === '') return false;
    const key = this.key(senderId, transferId);
    const cancel = this.cancels.get(key);
    if (cancel === undefined) {
      // Close the small IPC race where Cancel arrives after the renderer has
      // invoked sftp_read/write but before that handler registers its stream.
      this.pending.add(key);
      const expiry = setTimeout(() => this.pending.delete(key), 30_000);
      expiry.unref();
      return false;
    }
    this.cancels.delete(key);
    cancel();
    return true;
  }
}
