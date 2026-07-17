import { useEffect, useRef, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { useDocuments, type Doc } from '../state/documents';
import { proxyForConnection } from '../state/proxy';

/// The J2 Author **diagram** editor — an offline draw.io embed. draw.io is
/// Apache-2.0 and fully client-side but ~50 MB, so it is NOT bundled: the user
/// downloads it once (drawio.rs extracts the `draw.war` webapp into app-data),
/// and it's served to this iframe via the custom `drawio://` scheme so it works
/// offline. We speak draw.io's JSON embed protocol over postMessage: on `init`
/// we `load` the document's XML; on `save`/`autosave` we persist it back into the
/// `diagram` doc's `body`.

interface DrawioStatus {
  installed: boolean;
  version: string;
}

// A custom URI scheme resolves differently per platform: `scheme://localhost/` on
// macOS/Linux, `http://scheme.localhost/` on Windows (Tauri v2).
function drawioBase(): string {
  return /Windows/i.test(navigator.userAgent) ? 'http://drawio.localhost/' : 'drawio://localhost/';
}

export function DiagramEditor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const update = useDocuments((s) => s.update);
  const [status, setStatus] = useState<DrawioStatus | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    if (!isTauri()) {
      setStatus({ installed: false, version: '' });
      return;
    }
    void invoke<DrawioStatus>('drawio_status')
      .then(setStatus)
      .catch(() => setStatus({ installed: false, version: '' }));
  }, []);

  async function download(): Promise<void> {
    setDownloading(true);
    setErr(null);
    try {
      setStatus(await invoke<DrawioStatus>('drawio_download', { proxy: proxyForConnection('drawio') ?? null }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setDownloading(false);
    }
  }

  // Offline fallback when the GitHub download is blocked: the user picks a
  // draw.war they downloaded manually and we extract it locally (no network).
  async function installFromFile(): Promise<void> {
    setDownloading(true);
    setErr(null);
    try {
      const res = await invoke<DrawioStatus | null>('drawio_install_file');
      if (res !== null) setStatus(res); // null = user cancelled the picker
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setDownloading(false);
    }
  }

  // draw.io embed protocol (proto=json). `doc.id` in deps so switching diagrams
  // re-binds; `doc.body` intentionally NOT a dep — we load it once on `init`,
  // then draw.io owns the live state and streams changes back via autosave.
  useEffect(() => {
    if (status?.installed !== true) return;
    function onMessage(ev: MessageEvent): void {
      const frame = iframeRef.current;
      if (frame === null || ev.source !== frame.contentWindow) return;
      let msg: { event?: string; xml?: string };
      try {
        msg = typeof ev.data === 'string' ? JSON.parse(ev.data) : (ev.data as typeof msg);
      } catch {
        return;
      }
      if (msg.event === 'init') {
        const cur = useDocuments.getState().docs.find((d) => d.id === doc.id);
        frame.contentWindow?.postMessage(
          JSON.stringify({ action: 'load', autosave: 1, xml: cur?.body ?? '' }),
          '*',
        );
      } else if ((msg.event === 'save' || msg.event === 'autosave') && typeof msg.xml === 'string') {
        update(doc.id, { body: msg.xml });
      }
    }
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, [status?.installed, doc.id, update]);

  if (status === null) return <div className="muted region-pad">{t('author.diagramChecking')}</div>;

  if (!status.installed) {
    return (
      <div className="diagram-install">
        <p className="muted">{t('author.diagramIntro')}</p>
        {isTauri() ? (
          <>
            <div className="diagram-install-actions">
              <button className="primary" disabled={downloading} onClick={() => void download()}>
                {downloading ? t('author.diagramDownloading') : t('author.diagramDownload')}
              </button>
              <button disabled={downloading} onClick={() => void installFromFile()}>
                {t('author.diagramInstallFile')}
              </button>
            </div>
            <div className="muted small">{t('author.diagramInstallFileHint')}</div>
            {err !== null && <div className="error small diagram-err">{err}</div>}
          </>
        ) : (
          <div className="muted small">{t('author.diagramDesktopOnly')}</div>
        )}
      </div>
    );
  }

  const src = `${drawioBase()}index.html?embed=1&proto=json&spin=1&stealth=1`;
  return <iframe ref={iframeRef} className="diagram-frame" title={doc.title !== '' ? doc.title : 'diagram'} src={src} />;
}
