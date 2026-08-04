import { useEffect, useRef, useState } from 'react';
import { invoke } from '../bridge';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { useDocuments, type Doc } from '../state/documents';
import { registerLiveApply } from '../state/liveApply';
import { registerLiveRender } from '../state/liveRender';
import { svgFromDataUri } from '../state/renderDoc';
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

// Electron resolves the custom `drawio://` scheme itself on every OS (the
// main process registers it as a privileged scheme), so the iframe loads the
// offline draw.io webapp directly from it.
//
// This is a real tuple origin, not `"null"`, because the scheme is registered
// with `standard: true` (electron/src/schemes.ts:27-29) — which is what makes
// it safe to pin below. A non-standard scheme would report `"null"` and the
// pin would deadlock the embed protocol.
const DRAWIO_ORIGIN = 'drawio://localhost';

/// How long an `author_render` export may take before the caller is told the
/// editor did not answer. Generous — draw.io rasterizes a large sheet on the
/// main thread — and bounded, because the alternative is an agent's tool call
/// parked on an iframe that will never reply.
const EXPORT_TIMEOUT_MS = 20_000;

function drawioBase(): string {
  return `${DRAWIO_ORIGIN}/`;
}

export function DiagramEditor({ doc }: { doc: Doc }): JSX.Element {
  const t = useT();
  const update = useDocuments((s) => s.update);
  const [status, setStatus] = useState<DrawioStatus | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    if (!isShell()) {
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
    let unregister: (() => void) | null = null;
    let unregisterRender: (() => void) | null = null;
    // The `export` event carries no correlation id (drawio.com/doc/faq/embed-mode
    // — the reply is `{event:'export', format, data}` and nothing else), so at
    // most one export may be in flight and a second caller waits for the first
    // rather than racing it onto the same reply.
    let pendingExport: { resolve: (svg: string) => void; reject: (e: Error) => void; timer: number } | null = null;
    function settleExport(err: Error | null, svg?: string): void {
      const p = pendingExport;
      if (p === null) return;
      pendingExport = null;
      window.clearTimeout(p.timer);
      if (err !== null) p.reject(err);
      else p.resolve(svg ?? '');
    }
    function onMessage(ev: MessageEvent): void {
      const frame = iframeRef.current;
      // Both halves are required. `ev.source` proves the message came from THIS
      // iframe; `ev.origin` proves the document inside it is still the draw.io
      // app and not something it navigated to. Without the origin check a page
      // the embed reached could drive `load` — which, once B1's write path
      // exists, means rewriting the user's diagram.
      if (frame === null || ev.source !== frame.contentWindow || ev.origin !== DRAWIO_ORIGIN) return;
      let msg: { event?: string; xml?: string; data?: string };
      try {
        msg = typeof ev.data === 'string' ? JSON.parse(ev.data) : (ev.data as typeof msg);
      } catch {
        return;
      }
      if (msg.event === 'export') {
        // `author_render`'s answer for this kind. draw.io is the only thing that
        // can rasterize an mxGraph model, so the picture has to come from here;
        // PNG is NOT asked for, because the app already owns one SVG rasterizer
        // and a second path through draw.io's export pipeline would be a second
        // answer to the same question.
        if (typeof msg.data !== 'string' || msg.data === '') {
          settleExport(new Error('draw.io returned an empty export'));
          return;
        }
        const out = svgFromDataUri(msg.data, (b64) => atob(b64));
        settleExport(out.ok ? null : new Error(out.message), out.ok ? out.svg : undefined);
        return;
      }
      if (msg.event === 'init') {
        const cur = useDocuments.getState().docs.find((d) => d.id === doc.id);
        post({ action: 'load', autosave: 1, xml: cur?.body ?? '' });
        // B1: the write path opens only AFTER `init`. draw.io drops anything
        // sent before it announces itself, so registering at mount would give
        // lane A a target that silently swallows the first apply.
        unregister?.();
        unregister = registerLiveApply(doc.id, (body) => {
          if (iframeRef.current === null) return 'rejected';
          // `load` replaces the sheet wholesale — and it is the whole write
          // path, `mode:'ops'` (D1) included. Ops are resolved to a complete
          // body BEFORE they get here (`state/drawioOps.ts` edits the document
          // as text and the result goes through the same validator), so what
          // arrives is always a finished diagram.
          //
          // draw.io's own additive action, `merge`, stays unused on purpose: it
          // can only add, so the op grammar's delete-with-cascade cannot be
          // expressed with it — a batch would half-apply through the editor
          // while the store held the other half.
          post({ action: 'load', autosave: 1, xml: body });
          // draw.io answers a `load` with an autosave, which is what writes the
          // store — so the outcome is "the live editor took it", not "the
          // document now equals `body`".
          return 'applied_live';
        });
        // Same reason the write path opens only after `init`: an export posted
        // before draw.io announces itself is dropped, and the caller would then
        // wait out the whole deadline for a reply that was never coming.
        unregisterRender?.();
        unregisterRender = registerLiveRender(doc.id, async () => {
          if (iframeRef.current === null) throw new Error('the diagram editor is no longer on screen');
          if (pendingExport !== null) throw new Error('another export of this diagram is already in flight — retry in a moment');
          const svg = await new Promise<string>((resolve, reject) => {
            const timer = window.setTimeout(() => {
              settleExport(new Error('draw.io did not answer the export within 20s'));
            }, EXPORT_TIMEOUT_MS);
            pendingExport = { resolve, reject, timer };
            post({ action: 'export', format: 'svg' });
          });
          return svg;
        });
      } else if ((msg.event === 'save' || msg.event === 'autosave') && typeof msg.xml === 'string') {
        update(doc.id, { body: msg.xml });
      }
    }
    // Targeted at the draw.io origin, never `'*'`: a wildcard hands the message
    // (and the document body in it) to whatever the frame has navigated to.
    function post(payload: Record<string, unknown>): void {
      iframeRef.current?.contentWindow?.postMessage(JSON.stringify(payload), DRAWIO_ORIGIN);
    }
    window.addEventListener('message', onMessage);
    return () => {
      window.removeEventListener('message', onMessage);
      unregister?.();
      unregisterRender?.();
      // A caller parked on an export must not outlive the editor it is waiting
      // on: unmounting is a definite answer, and a rejected promise reaches the
      // agent as a refusal rather than as this call's 15s renderer deadline.
      settleExport(new Error('the diagram editor closed before the export finished'));
    };
  }, [status?.installed, doc.id, update]);

  if (status === null) return <div className="muted region-pad">{t('author.diagramChecking')}</div>;

  if (!status.installed) {
    return (
      <div className="diagram-install">
        <p className="muted">{t('author.diagramIntro')}</p>
        {isShell() ? (
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
