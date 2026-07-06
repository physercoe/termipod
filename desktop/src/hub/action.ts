import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

/// Foundation F3 — a uniform write-path helper. Every create/edit surface runs
/// its hub call through `run()`, which tracks busy/error and invalidates the
/// affected query keys on success (optimistic-refresh). This keeps the growing
/// set of write surfaces consistent instead of each re-implementing the
/// try/catch/invalidate dance.
///
/// Governance note (ADR-030): from the desktop the actor is the *principal*
/// (director), the top authorization tier — its creates apply directly (e.g.
/// `handleCreateProject` admits a principal, only forcing agents through
/// `propose`). The director *approves* agent-proposed governed actions via the
/// AttentionDock. So these are direct calls, not propose→approve round-trips.
export function useHubAction(): {
  run: <T>(fn: () => Promise<T>, opts?: { invalidate?: unknown[][] }) => Promise<T | undefined>;
  busy: boolean;
  error: string | null;
  setError: (e: string | null) => void;
} {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run<T>(fn: () => Promise<T>, opts: { invalidate?: unknown[][] } = {}): Promise<T | undefined> {
    setBusy(true);
    setError(null);
    try {
      const result = await fn();
      for (const key of opts.invalidate ?? []) {
        await qc.invalidateQueries({ queryKey: key });
      }
      return result;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return undefined;
    } finally {
      setBusy(false);
    }
  }

  return { run, busy, error, setError };
}
