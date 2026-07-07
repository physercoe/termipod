import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { useSession } from '../state/session';
import { Markdown } from '../ui/Markdown';
import { chartFromJson, ChartView, type ChartData } from '../ui/ChartView';

/// Artifact / blob preview overlay (parity — mobile blobs_section.dart `_preview`).
/// Fetches the content-addressed blob once (`GET /v1/blobs/{sha}` → `{mime,
/// base64}`) and dispatches on the mime (with a filename-extension fallback,
/// since the hub stores `application/octet-stream` when sniffing is
/// inconclusive) to an image / pdf / html / json / text renderer, falling back
/// to a download link. Desktop had no way to open artifacts before this — the
/// list rows were inert; all the fetch plumbing already existed (RunImageTile).

type Kind = 'image' | 'pdf' | 'html' | 'json' | 'text' | 'other';

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

/** Resolve a renderer kind from the mime, falling back to the file extension
 * when the mime is missing or the generic octet-stream. */
function kindOf(mime: string, name: string): Kind {
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

export function ArtifactViewer({
  sha,
  name,
  mime,
  onClose,
}: {
  sha: string;
  name: string;
  mime?: string;
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

  const resolvedMime = blobQ.data?.mime || mime || 'application/octet-stream';
  const kind = kindOf(resolvedMime, name);
  const dataUrl = blobQ.data !== undefined ? `data:${resolvedMime};base64,${blobQ.data.base64}` : '';

  // Text/json bodies are decoded from the base64 once loaded.
  const text = useMemo(() => {
    if (blobQ.data === undefined || (kind !== 'text' && kind !== 'json' && kind !== 'html')) return '';
    try {
      return b64ToUtf8(blobQ.data.base64);
    } catch {
      return '';
    }
  }, [blobQ.data, kind]);

  // Parse JSON once: derive both the pretty form and a chart (when the data is
  // chart-shaped — director feedback: chart-data JSON should render as a chart).
  const parsedJson = useMemo<{ pretty: string; chart: ChartData | null }>(() => {
    if (kind !== 'json' || text === '') return { pretty: text, chart: null };
    try {
      const value = JSON.parse(text);
      return { pretty: JSON.stringify(value, null, 2), chart: chartFromJson(value) };
    } catch {
      return { pretty: text, chart: null }; // not valid JSON — show as-is
    }
  }, [kind, text]);

  function body(): JSX.Element {
    if (blobQ.isLoading) return <div className="muted">{t('common.loading')}</div>;
    if (blobQ.isError) return <div className="error">{(blobQ.error as Error).message}</div>;
    if (blobQ.data === undefined) return <div className="muted">{t('artifact.empty')}</div>;
    switch (kind) {
      case 'image':
        return <img className="artifact-img" src={dataUrl} alt={name} />;
      case 'pdf':
        return <iframe className="artifact-frame" src={dataUrl} title={name} />;
      case 'html':
        // `allow-scripts` (but deliberately NOT `allow-same-origin`) lets a
        // self-contained HTML artifact — an agent-produced chart or report —
        // actually run its inline JS/CSS while staying fully isolated from the
        // app origin (it can't reach cookies, storage, or the parent DOM).
        // Director feedback: HTML artifacts were showing as raw markup.
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
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="artifact-view" onMouseDown={(e) => e.stopPropagation()}>
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
      </div>
    </div>
  );
}
