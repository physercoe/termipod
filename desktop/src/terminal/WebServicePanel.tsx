import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { useT } from '../i18n';
import {
  onSshForwardClosed,
  sshForwardList,
  sshForwardStart,
  sshForwardStop,
  type SshForwardInfo,
} from '../ssh/native';
import { forwardedWebUrl, parseRemotePort, type WebForwardScheme } from '../ssh/webForward';
import { useReadTabs } from '../state/readTabs';
import { useWorkbench } from '../state/workbench';

interface WebForwardView extends SshForwardInfo {
  scheme: WebForwardScheme;
  path: string;
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** Configure web-service tunnels over one already-authenticated SSH session. */
export function WebServicePanel({ sessionId, sshHost }: { sessionId: string; sshHost: string }): JSX.Element {
  const t = useT();
  const [remoteHost, setRemoteHost] = useState('127.0.0.1');
  const [remotePort, setRemotePort] = useState('8080');
  const [scheme, setScheme] = useState<WebForwardScheme>('http');
  const [path, setPath] = useState('');
  const [forwards, setForwards] = useState<WebForwardView[]>([]);
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState(false);
  const [stopping, setStopping] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const port = useMemo(() => parseRemotePort(remotePort), [remotePort]);
  const canStart = !loading && !starting && remoteHost.trim() !== '' && port !== null;

  useEffect(() => {
    let alive = true;
    let unlisten: (() => void) | undefined;
    setForwards([]);
    setLoading(true);
    setError(null);

    void sshForwardList(sessionId).then(
      (rows) => {
        if (!alive) return;
        setForwards((current) =>
          rows.map((row) => {
            const existing = current.find((item) => item.forward_id === row.forward_id);
            return { ...row, scheme: existing?.scheme ?? 'http', path: existing?.path ?? '' };
          }),
        );
        setLoading(false);
      },
      (cause: unknown) => {
        if (!alive) return;
        setError(message(cause));
        setLoading(false);
      },
    );

    void onSshForwardClosed((forwardId) => {
      if (alive) setForwards((rows) => rows.filter((row) => row.forward_id !== forwardId));
    }).then(
      (dispose) => {
        if (alive) unlisten = dispose;
        else dispose();
      },
      (cause: unknown) => {
        if (alive) setError(message(cause));
      },
    );

    return () => {
      alive = false;
      unlisten?.();
    };
  }, [sessionId]);

  async function start(event: FormEvent): Promise<void> {
    event.preventDefault();
    if (!canStart || port === null) return;
    setStarting(true);
    setError(null);
    try {
      const info = await sshForwardStart(sessionId, remoteHost.trim(), port);
      setForwards((rows) => [...rows, { ...info, scheme, path }]);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setStarting(false);
    }
  }

  async function stop(forwardId: string): Promise<void> {
    setStopping(forwardId);
    setError(null);
    try {
      await sshForwardStop(forwardId);
      setForwards((rows) => rows.filter((row) => row.forward_id !== forwardId));
    } catch (cause) {
      setError(message(cause));
    } finally {
      setStopping(null);
    }
  }

  function openInTermipod(forward: WebForwardView): void {
    const url = forwardedWebUrl(forward.scheme, forward.local_port, forward.path);
    useReadTabs.getState().open({
      kind: 'web',
      url,
      title: `${sshHost} · ${forward.remote_port}`,
    });
    useWorkbench.getState().setJob('read');
  }

  return (
    <div className="web-service-panel scroll">
      <div className="web-service-intro">
        <div>
          <strong>{t('term.webServiceTitle')}</strong>
          <p className="muted small">{t('term.webServiceHint')}</p>
        </div>
        <div className="web-service-host">
          <span className="muted small">{t('term.sshHost')}</span>
          <span className="mono">{sshHost}</span>
        </div>
      </div>

      <form className="web-service-form" onSubmit={(event) => void start(event)}>
        <label>
          {t('term.webScheme')}
          <select value={scheme} onChange={(event) => setScheme(event.target.value as WebForwardScheme)}>
            <option value="http">HTTP</option>
            <option value="https">HTTPS</option>
          </select>
        </label>
        <label className="web-service-remote-host">
          {t('term.remoteServiceHost')}
          <input value={remoteHost} onChange={(event) => setRemoteHost(event.target.value)} placeholder="127.0.0.1" />
        </label>
        <label>
          {t('term.remoteServicePort')}
          <input
            value={remotePort}
            onChange={(event) => setRemotePort(event.target.value)}
            inputMode="numeric"
            aria-invalid={remotePort !== '' && port === null}
          />
        </label>
        <label className="web-service-path">
          {t('term.webPath')}
          <input value={path} onChange={(event) => setPath(event.target.value)} placeholder="/" />
        </label>
        <button type="submit" className="primary web-service-start" disabled={!canStart}>
          {starting ? t('term.startingForward') : t('term.startForward')}
        </button>
      </form>

      {error !== null && <div className="error web-service-error">{error}</div>}

      <section className="web-service-active">
        <div className="web-service-section-head">
          <strong>{t('term.activeWebServices')}</strong>
          {loading && <span className="muted small">{t('common.loading')}</span>}
        </div>
        {!loading && forwards.length === 0 && <p className="muted small">{t('term.noActiveWebServices')}</p>}
        {forwards.map((forward) => {
          const url = forwardedWebUrl(forward.scheme, forward.local_port, forward.path);
          return (
            <article className="web-service-row" key={forward.forward_id}>
              <div className="web-service-route">
                <span className="mono">{forward.remote_host}:{forward.remote_port}</span>
                <span className="muted" aria-hidden="true">→</span>
                <span className="mono web-service-url">{url}</span>
              </div>
              <div className="web-service-actions">
                <button type="button" className="primary" onClick={() => openInTermipod(forward)}>
                  {t('term.openInTermipod')}
                </button>
                <button type="button" disabled={stopping === forward.forward_id} onClick={() => void stop(forward.forward_id)}>
                  {stopping === forward.forward_id ? t('term.stoppingForward') : t('term.stopForward')}
                </button>
              </div>
            </article>
          );
        })}
      </section>
    </div>
  );
}
