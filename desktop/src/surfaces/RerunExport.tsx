import { useCallback, useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useSession } from '../state/session';
import { useT } from '../i18n';
import {
  EXPORT_POLL_MS,
  IDLE_EXPORT,
  advanceExport,
  canCancel,
  isOpenableRecording,
  isPolling,
  progressLabel,
  readCommand,
  type ExportState,
} from '../state/rerunExport';

/// "Export to Rerun" (J8 Replay W4b-1) — the first caller of W4a's Rerun
/// manager, which until now had none.
///
/// The chain: submit a `dataset_export_rrd` host job → poll the command row →
/// hand `result.path` to `rerun_start`, which serves it over a loopback web
/// viewer → show that viewer in a `rerunweb` webview.
///
/// The decision logic lives in `state/rerunExport.ts` and is tested there. What
/// is here is the effect: a poll that stops itself, and a bridge call that only
/// happens for a path that survived inspection.

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

  // The poll and the viewer belong to one episode. Switching episodes must not
  // leave a poll running against the previous one, or land its result in the new
  // one's panel.
  useEffect(() => {
    setState(IDLE_EXPORT);
    onViewer(null);
  }, [datasetId, episode, onViewer]);

  // A ref, not state: the interval callback closes over its first render's
  // value, and re-creating the interval on every progress tick would reset it.
  const stateRef = useRef(state);
  stateRef.current = state;

  const submit = useCallback(async () => {
    if (client === null) return;
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

  // Hand the finished recording to the Rerun manager.
  useEffect(() => {
    if (state.phase !== 'opening') return;
    const path = state.path;
    if (!isOpenableRecording(path)) {
      setState((s) => ({ ...s, phase: 'failed', error: t('replay.export.badPath') }));
      return;
    }
    let stopped = false;
    void (async () => {
      try {
        const out = await invoke<{ url: string }>('rerun_start', { recording: path });
        if (stopped) return;
        setState((s) => ({ ...s, phase: 'ready', viewerUrl: out.url }));
        onViewer(out.url);
      } catch (e) {
        if (stopped) return;
        setState((s) => ({
          ...s,
          phase: 'failed',
          error: e instanceof Error ? e.message : String(e),
        }));
      }
    })();
    return () => {
      stopped = true;
    };
  }, [state.phase, state.path, onViewer, t]);

  // Electron-only: the export produces a host-local file and rerun is a local
  // process, neither of which a browser build can reach. Saying so is better
  // than a button that always fails.
  if (!isShell()) {
    return <span className="muted small">{t('replay.export.desktopOnly')}</span>;
  }

  const label = progressLabel(state);
  const busy = isPolling(state) || state.phase === 'opening';

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
