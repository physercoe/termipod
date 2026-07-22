import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';
import { Modal } from '../ui/Modal';
import { PdfCanvas } from '../ui/PdfCanvas';
import { chartFromJson, ChartView, type ChartData } from '../ui/ChartView';
import {
  CANVAS_MIME,
  CODE_MIME,
  inlineCanvasBundle,
  parseArtifactFileManifest,
  type ArtifactFileManifest,
} from '../ui/canvasBundle';

/// Artifact / blob preview overlay (parity — mobile artifacts_screen.dart
/// `_routeForArtifact` + canvas_viewer.dart). Fetches the content-addressed blob
/// once (`GET /v1/blobs/{sha}` → `{mime, base64}`) and picks a renderer.
///
/// Dispatch order matters: the artifact **`kind`** is authoritative (mobile
/// switches on it), because a `canvas-app` / `code-bundle` artifact's blob is a
/// JSON AFM-V1 manifest carried under a vendor mime
/// (`application/vnd.termipod.canvas+json`), NOT `text/html` — so a mime-only
/// classifier renders the manifest as raw JSON (the director's "html canvas
/// shows raw content" bug). We resolve `kind` first, then fall back to mime /
/// extension, and finally content-sniff for HTML that the hub mis-typed as
/// text/octet-stream (agent uploads don't always set `Content-Type: text/html`).

type View = 'canvas' | 'code' | 'image' | 'pdf' | 'html' | 'json' | 'text' | 'other';

/** Decode a base64 payload to a UTF-8 string (for the text renderers). */
function b64ToUtf8(b64: string): string {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder('utf-8').decode(bytes);
}

function extOf(name: string): string {
  const i = name.lastIndexOf('.');
  return i >= 0 ? name.slice(i + 1).toLowerCase() : '';
}

/** A specific blob mime wins; a generic/empty one yields to the artifact-row
 * mime (the hub often serves `application/octet-stream` for typed content). */
function pickMime(blobMime: string | undefined, rowMime: string | undefined): string {
  const b = (blobMime ?? '').toLowerCase();
  if (b !== '' && b !== 'application/octet-stream') return blobMime as string;
  if (rowMime !== undefined && rowMime !== '') return rowMime;
  return blobMime ?? 'application/octet-stream';
}

/** Mime/extension renderer kind (no artifact-kind / content signal). */
function mimeKind(mime: string, name: string): View {
  const m = mime.toLowerCase();
  const ext = extOf(name);
  if (m.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg'].includes(ext)) return 'image';
  if (m === 'application/pdf' || ext === 'pdf') return 'pdf';
  if (m === 'text/html' || ext === 'html' || ext === 'htm') return 'html';
  if (m === 'application/json' || ext === 'json') return 'json';
  if (
    m.startsWith('text/') ||
    m === 'application/yaml' ||
    m === 'application/x-yaml' ||
    ['txt', 'md', 'markdown', 'yaml', 'yml', 'csv', 'log', 'xml', 'toml', 'ini', 'sh'].includes(ext)
  ) {
    return 'text';
  }
  return 'other';
}

/** Does a decoded body look like a standalone HTML document? Used to catch HTML
 * the hub stored as text/plain or octet-stream (no reliable mime). */
function looksLikeHtml(text: string): boolean {
  return /^\s*(<!doctype\s+html|<html[\s>]|<svg[\s>])/i.test(text);
}

/** A highlight.js-friendly fence language token from a file path. */
function langOf(path: string): string {
  const ext = extOf(path);
  const map: Record<string, string> = { mjs: 'js', cjs: 'js', jsx: 'jsx', tsx: 'tsx', yml: 'yaml', htm: 'html' };
  return map[ext] ?? ext;
}

export function ArtifactViewer({
  sha,
  name,
  mime,
  kind,
  onClose,
}: {
  sha: string;
  name: string;
  mime?: string;
  /** The artifact-row `kind` (e.g. `canvas-app`, `code-bundle`) — authoritative. */
  kind?: string;
  onClose: () => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [rawJson, setRawJson] = useState(false);

  const blobQ = useQuery({
    queryKey: ['blob', sha],
    enabled: client !== null && sha !== '',
    staleTime: 5 * 60_000,
    queryFn: () => client!.getBlobBytes(sha),
  });

  const resolvedMime = pickMime(blobQ.data?.mime, mime);
  const base = mimeKind(resolvedMime, name);
  const dataUrl = blobQ.data !== undefined ? `data:${resolvedMime};base64,${blobQ.data.base64}` : '';

  // Decode the body for anything that isn't a binary image/pdf (needed for the
  // text renderers, the HTML content-sniff, and the canvas/code manifest parse).
  const text = useMemo(() => {
    if (blobQ.data === undefined || base === 'image' || base === 'pdf') return '';
    try {
      return b64ToUtf8(blobQ.data.base64);
    } catch {
      return '';
    }
  }, [blobQ.data, base]);

  // Decode PDF bytes for the canvas-based renderer (below). We render PDFs with
  // pdf.js (PdfCanvas) rather than handing a `data:`/`blob:` URL to the browser's
  // native PDF viewer: the canvas pipeline gives a real text layer, reflow zoom,
  // and consistent chrome across platforms (§7 row 2 — the canvas pipeline is kept).
  const pdfData = useMemo<ArrayBuffer | null>(() => {
    if (base !== 'pdf' || blobQ.data === undefined) return null;
    try {
      const bin = atob(blobQ.data.base64);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
      return bytes.buffer;
    } catch {
      return null;
    }
  }, [base, blobQ.data]);

  // Final renderer: artifact `kind` and vendor mimes win, then mime/ext, then a
  // content sniff for mis-typed HTML.
  const view: View = useMemo(() => {
    if (kind === 'canvas-app' || resolvedMime === CANVAS_MIME) return 'canvas';
    if (kind === 'code-bundle' || resolvedMime === CODE_MIME) return 'code';
    if ((base === 'text' || base === 'other') && looksLikeHtml(text)) return 'html';
    return base;
  }, [kind, resolvedMime, base, text]);

  // Canvas / code bundles carry a JSON AFM-V1 manifest as their body.
  const manifest = useMemo<ArtifactFileManifest | null>(() => {
    if ((view !== 'canvas' && view !== 'code') || text === '') return null;
    try {
      return parseArtifactFileManifest(JSON.parse(text));
    } catch {
      return null;
    }
  }, [view, text]);

  // The inlined, self-contained HTML document for a canvas (or an error string).
  const canvasHtml = useMemo<{ html: string } | { err: string } | null>(() => {
    if (view !== 'canvas') return null;
    if (manifest === null) return { err: t('artifact.canvasError') };
    try {
      return { html: inlineCanvasBundle(manifest) };
    } catch (e) {
      return { err: e instanceof Error ? e.message : String(e) };
    }
  }, [view, manifest, t]);

  // Parse JSON once: derive both the pretty form and a chart (director feedback:
  // chart-data JSON should render as a chart).
  const parsedJson = useMemo<{ pretty: string; chart: ChartData | null }>(() => {
    if (view !== 'json' || text === '') return { pretty: text, chart: null };
    try {
      const value = JSON.parse(text);
      return { pretty: JSON.stringify(value, null, 2), chart: chartFromJson(value) };
    } catch {
      return { pretty: text, chart: null }; // not valid JSON — show as-is
    }
  }, [view, text]);

  function body(): JSX.Element {
    if (blobQ.isLoading) return <div className="muted">{t('common.loading')}</div>;
    if (blobQ.isError) return <div className="error">{(blobQ.error as Error).message}</div>;
    if (blobQ.data === undefined) return <div className="muted">{t('artifact.empty')}</div>;
    switch (view) {
      case 'image':
        return <img className="artifact-img" src={dataUrl} alt={name} />;
      case 'pdf':
        return pdfData !== null ? (
          <div className="artifact-pdf">
            <PdfCanvas data={pdfData} fileName={name} />
          </div>
        ) : (
          <div className="muted">{t('common.loading')}</div>
        );
      case 'canvas':
        // Inlined single-doc HTML in an isolated iframe (`allow-scripts`, NOT
        // allow-same-origin) — the canvas app runs but can't reach the app origin.
        if (canvasHtml !== null && 'html' in canvasHtml) {
          return <iframe className="artifact-frame" sandbox="allow-scripts" srcDoc={canvasHtml.html} title={name} />;
        }
        return (
          <div className="artifact-download">
            <p className="error small">{canvasHtml !== null ? canvasHtml.err : t('artifact.canvasError')}</p>
            <a className="btn-like" href={dataUrl} download={name || sha}>
              {t('artifact.download')}
            </a>
          </div>
        );
      case 'code':
        if (manifest === null) return <pre className="ev-mono artifact-text">{text}</pre>;
        return (
          <div className="artifact-code-bundle">
            {manifest.files.map((f) => (
              <div key={f.path} className="code-file">
                <div className="code-file-path mono">{f.path}</div>
                <Markdown text={`\`\`\`\`${langOf(f.path)}\n${f.content}\n\`\`\`\``} />
              </div>
            ))}
          </div>
        );
      case 'html':
        // `allow-scripts` (deliberately NOT `allow-same-origin`) lets a
        // self-contained HTML artifact run its inline JS/CSS while staying
        // isolated from the app origin. Director feedback: HTML showed as markup.
        return <iframe className="artifact-frame" sandbox="allow-scripts" srcDoc={text} title={name} />;
      case 'json':
        if (parsedJson.chart !== null && !rawJson) {
          return (
            <div className="artifact-chart">
              <div className="chart-toolbar">
                <span className="spacer" />
                <button className="link-btn" onClick={() => setRawJson(true)}>
                  {t('artifact.showJson')}
                </button>
              </div>
              <ChartView chart={parsedJson.chart} />
            </div>
          );
        }
        return (
          <div className="artifact-json">
            {parsedJson.chart !== null && (
              <div className="chart-toolbar">
                <span className="spacer" />
                <button className="link-btn" onClick={() => setRawJson(false)}>
                  {t('artifact.showChart')}
                </button>
              </div>
            )}
            <pre className="ev-mono artifact-text">{parsedJson.pretty}</pre>
          </div>
        );
      case 'text':
        return extOf(name) === 'md' || extOf(name) === 'markdown' ? (
          <Markdown text={text} />
        ) : (
          <pre className="ev-mono artifact-text">{text}</pre>
        );
      default:
        return (
          <div className="artifact-download">
            <p className="muted small">{t('artifact.noPreview')}</p>
            <a className="btn-like" href={dataUrl} download={name || sha}>
              {t('artifact.download')}
            </a>
          </div>
        );
    }
  }

  return (
    <Modal onClose={onClose} className="artifact-view" ariaLabel={name || sha.slice(0, 12)}>
      <div className="admin-tabs">
        <strong className="mono">{name || sha.slice(0, 12)}</strong>
        <span className="muted small mono">{resolvedMime}</span>
        <span className="spacer" />
        {blobQ.data !== undefined && (
          <a className="btn-like" href={dataUrl} download={name || sha}>
            {t('artifact.download')}
          </a>
        )}
        <button onClick={onClose}>{t('admin.close')}</button>
      </div>
      <div className="artifact-body">{body()}</div>
    </Modal>
  );
}
