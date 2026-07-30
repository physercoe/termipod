import { useCallback, useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useSession } from '../state/session';
import { useT } from '../i18n';
import { remoteMediaConn } from '../state/replayRemoteStore';
import { liveSessionFor } from '../state/replayRemote';
import { localExists } from '../state/localfs';
import { onSftpProgress } from '../ssh/native';
import { useTerminals } from '../terminal/store';
import {
  EXPORT_POLL_MS,
  IDLE_EXPORT,
  advanceExport,
  canCancel,
  isPolling,
  planArtifact,
  progressLabel,
  readCommand,
  type BlockedReason,
  type ExportState,
} from '../state/rerunExport';

/// "Export to Rerun" (J8 Replay W4b-1 + W4b-2) — the first caller of W4a's
/// Rerun manager, which until now had none.
///
/// The chain: submit a `dataset_export_rrd` host job → poll the command row →
/// get the recording in front of a local `rerun --serve-web` → show that
/// viewer in a `rerunweb` webview.
///
/// "Get it in front of" is W4b-2's part: the export runs on whichever host owns
/// the dataset's bytes, so when that host is not this machine the file is
/// pulled down over the director's own live SSH session first (`rerun_fetch`,
/// zero bytes through the hub) and the viewer opens the local copy.
///
/// The decision logic lives in `state/rerunExport.ts` and is tested there. What
/// is here is the effect: a poll that stops itself, and bridge calls that only
/// happen for a path that survived inspection.

/// One message per way a finished export can fail to open — each says what the
/// director can do about it, which a single "could not open" cannot.
const BLOCKED_KEY: Record<BlockedReason, string> = {
  badPath: 'replay.export.badPath',
  noConnection: 'replay.export.remoteNoConnection',
  noSession: 'replay.export.remoteNoSession',
  noDigest: 'replay.export.noDigest',
};

export function RerunExportButton({
  datasetId,
  episode,
  onViewer,
}: {
  datasetId: string;
  episode: number;
  /// Called with the loopback viewer URL once rerun is serving, and with null
  /// when this flow no longer owns a viewer.
  onViewer: (url: string | null) => void;
}): JSX.Element | null {
  const t = useT();
  const client = useSession((s) => s.client);
  const [state, setState] = useState<ExportState>(IDLE_EXPORT);

  // Live terminal tabs, read through a ref: they are what resolves a saved
  // connection to an ssh session, and a tab opening mid-transfer must not
  // re-run the open effect below.
  const tabs = useTerminals((s) => s.tabs);
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;

  // Which artifact the open sequence has already been started for. Without it a
  // late poll tick — `advanceExport` builds a fresh artifact object each time it
  // sees `done` — would start a second fetch of the same file.
  const openedRef = useRef<string | null>(null);
  // Bumped whenever an in-flight open sequence is abandoned. It is a generation
  // rather than a boolean because the open effect must survive its own re-runs:
  // the phase changes while it works, and a cleanup tied to that would cancel
  // the very transfer it started.
  const genRef = useRef(0);

  // The poll and the viewer belong to one episode. Switching episodes must not
  // leave a poll running against the previous one, or land its result in the new
  // one's panel.
  useEffect(() => {
    genRef.current += 1;
    openedRef.current = null;
    setState(IDLE_EXPORT);
    onViewer(null);
  }, [datasetId, episode, onViewer]);

  // Unmounting abandons whatever is in flight for the same reason.
  useEffect(
    () => () => {
      genRef.current += 1;
    },
    [],
  );

  // A ref, not state: the interval callback closes over its first render's
  // value, and re-creating the interval on every progress tick would reset it.
  const stateRef = useRef(state);
  stateRef.current = state;

  const submit = useCallback(async () => {
    if (client === null) return;
    // "Export again" on the same episode returns the same path; without this the
    // open effect would see an artifact it has already handled and do nothing.
    openedRef.current = null;
    setState({ ...IDLE_EXPORT, phase: 'submitting' });
    try {
      const out = await client.exportEpisodeToRerun(datasetId, episode);
      const id = typeof out.command_id === 'string' ? out.command_id : '';
      if (id === '') throw new Error('the hub accepted the export but returned no command id');
      setState((s) => ({ ...s, phase: 'running', commandId: id }));
    } catch (e) {
      setState({
        ...IDLE_EXPORT,
        phase: 'failed',
        error: e instanceof Error ? e.message : String(e),
      });
    }
  }, [client, datasetId, episode]);

  /// Re-run the open sequence against the artifact the host already produced.
  ///
  /// The blocked reasons are all things the director fixes elsewhere and comes
  /// back from — open a terminal, pick a connection — and the recording is
  /// still sitting in the host's jobcache. Without this the only way back is a
  /// full re-export, which decodes every frame of the episode again to arrive
  /// at the same file.
  const retryOpen = useCallback(() => {
    openedRef.current = null;
    setState((s) =>
      s.artifact === null
        ? s
        : // A new artifact object, deliberately: the open effect keys on its
          // identity, so a same-valued one would not re-run.
          { ...s, artifact: { ...s.artifact }, phase: 'opening', error: null },
    );
  }, []);

  const cancel = useCallback(async () => {
    const id = stateRef.current.commandId;
    if (client === null || id === null) return;
    // Deliberately not optimistic: the host is what stops the subprocess, and
    // the row will report `cancelled` when it has. Claiming otherwise here
    // would hide an export that kept running.
    try {
      await client.cancelHostCommand(id);
    } catch {
      /* the poll surfaces whatever actually happened */
    }
  }, [client]);

  // Poll while the job is in flight. One interval for the life of the flow;
  // isPolling is what ends it.
  useEffect(() => {
    if (client === null) return;
    if (!isPolling(state)) return;
    const id = state.commandId;
    if (id === null) return;
    let stopped = false;
    const timer = setInterval(() => {
      void (async () => {
        try {
          const row = await client.getHostCommand(id);
          if (stopped) return;
          setState((prev) => advanceExport(prev, readCommand(row)));
        } catch {
          // A transient hub blip must not fail an export that is still running
          // on the host; the next tick asks again.
        }
      })();
    }, EXPORT_POLL_MS);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
    // state.phase + commandId are the only inputs that should re-arm the poll;
    // progress churn must not.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, state.phase, state.commandId]);

  // Get the finished recording in front of the Rerun manager — fetching it from
  // the dataset's host first when it is not already on this machine.
  useEffect(() => {
    const artifact = state.artifact;
    if (artifact === null || openedRef.current === artifact.path) return;
    openedRef.current = artifact.path;

    const gen = genRef.current;
    const alive = (): boolean => genRef.current === gen;
    let unlisten: (() => void) | null = null;

    const fail = (message: string): void => {
      if (alive()) setState((s) => ({ ...s, phase: 'failed', transfer: null, error: message }));
    };

    void (async () => {
      try {
        // Whether the host's path is a file HERE is the only trustworthy answer
        // to "did this export land on this machine" — see planArtifact.
        const here = await localExists(artifact.path);
        if (!alive()) return;
        const conn = remoteMediaConn(datasetId);
        const plan = planArtifact(artifact, {
          localExists: here,
          remoteConn: conn,
          session: conn === null ? null : liveSessionFor(conn, tabsRef.current),
        });
        if (plan.kind === 'blocked') {
          fail(t(BLOCKED_KEY[plan.reason]));
          return;
        }

        let recording = plan.kind === 'local' ? plan.path : '';
        if (plan.kind === 'fetch') {
          const transferId = `rerun-${plan.sha256.slice(0, 12)}`;
          setState((s) => ({ ...s, phase: 'fetching', transfer: { done: 0, total: plan.bytes } }));
          // Subscribed before the fetch starts: a cache hit answers in one tick,
          // and a listener attached afterwards would show a bar that never moved.
          unlisten = await onSftpProgress(transferId, (done) => {
            if (alive()) setState((s) => ({ ...s, transfer: { done, total: plan.bytes } }));
          });
          if (!alive()) return;
          const got = await invoke<{ path: string }>('rerun_fetch', {
            sessionId: plan.sessionId,
            path: plan.path,
            sha256: plan.sha256,
            transferId,
          });
          if (!alive()) return;
          recording = got.path;
          setState((s) => ({ ...s, phase: 'opening', transfer: null }));
        }

        const out = await invoke<{ url: string }>('rerun_start', { recording });
        if (!alive()) return;
        setState((s) => ({ ...s, phase: 'ready', viewerUrl: out.url, transfer: null }));
        onViewer(out.url);
      } catch (e) {
        fail(e instanceof Error ? e.message : String(e));
      } finally {
        unlisten?.();
      }
    })();
    // No cleanup: this sequence outlives its own re-renders deliberately — it
    // changes `phase` as it works, and cancelling on that would abort the
    // transfer it just started. Abandonment goes through `genRef` instead.
  }, [state.artifact, datasetId, onViewer, t]);

  // Electron-only: the export produces a host-local file and rerun is a local
  // process, neither of which a browser build can reach. Saying so is better
  // than a button that always fails.
  if (!isShell()) {
    return <span className="muted small">{t('replay.export.desktopOnly')}</span>;
  }

  const label = progressLabel(state);
  const busy = isPolling(state) || state.phase === 'fetching' || state.phase === 'opening';

  return (
    <>
      <button
        type="button"
        className="replay-toggle"
        disabled={busy || client === null}
        onClick={() => void submit()}
        title={t('replay.export.hint')}
      >
        {state.phase === 'ready' ? t('replay.export.again') : t('replay.export.action')}
      </button>
      {canCancel(state) && (
        <button type="button" className="link-btn small" onClick={() => void cancel()}>
          {t('replay.export.cancel')}
        </button>
      )}
      {state.phase === 'failed' && state.artifact !== null && (
        <button type="button" className="link-btn small" onClick={retryOpen}>
          {t('replay.export.retryOpen')}
        </button>
      )}
      {label !== null && (
        <span className={state.phase === 'failed' ? 'replay-error small' : 'muted small'}>{label}</span>
      )}
    </>
  );
}

// `<webview>` is a host custom element; cast the tag so TS accepts src/partition
// without a global JSX.IntrinsicElements shim — same pattern as
// surfaces/WebPanel.tsx and surfaces/BrowserView.tsx.
const Webview = 'webview' as unknown as React.FC<
  React.HTMLAttributes<HTMLElement> & { src?: string; partition?: string }
>;

/// The viewer panel — a `rerunweb` guest pointed at the loopback server rerun
/// started. Non-persistent partition, loopback-pinned, popups denied: the policy
/// is enforced main-side in `webtab.ts`, not here (a renderer-side check would
/// be advice, not a rule).
export function RerunViewerPanel({
  url,
  onClose,
}: {
  url: string;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  return (
    <div className="replay-rerun-panel">
      <div className="replay-player-head">
        <div className="replay-player-title">{t('replay.export.viewerTitle')}</div>
        <span className="spacer" />
        <button
          type="button"
          className="link-btn small"
          onClick={() => {
            void invoke('rerun_stop').catch(() => undefined);
            onClose();
          }}
        >
          {t('replay.export.viewerClose')}
        </button>
      </div>
      <Webview className="replay-rerun-guest" partition="rerunweb" src={url} />
    </div>
  );
}
