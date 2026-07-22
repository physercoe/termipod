/// Pure download core for `attachment_download` (plan §1.5) — split out of
/// storage.ts so it can be unit-tested without the Electron runtime (storage.ts
/// imports `electron`, so it can't load under `node --test`). No electron
/// imports here: fetch is injected, bytes + filename come back, the caller does
/// the managed-attachment placement + progress emit.
import path from 'node:path';

// Hard cap for a downloaded PDF: 2× the sync cap (scanned-book PDFs exist).
export const DOWNLOAD_CAP = 200 * 1024 * 1024;

/// Resolve a download filename: `Content-Disposition` → URL path basename →
/// the caller-supplied fallback (arxivId / doi-slug / title-slug). Always a bare,
/// `.pdf`-suffixed basename so nothing escapes the key folder.
export function downloadFilename(res: Response, url: string, fallback: string): string {
  const cd = res.headers.get('content-disposition') ?? '';
  const m = /filename\*?=(?:UTF-8'')?["']?([^"';]+)/i.exec(cd);
  let name = '';
  if (m !== null) {
    try {
      name = decodeURIComponent(m[1]);
    } catch {
      name = m[1];
    }
  }
  if (name.trim() === '') {
    try {
      name = path.basename(new URL(url).pathname);
    } catch {
      name = '';
    }
  }
  if (name.trim() === '' || name === '/') name = fallback.trim();
  name = path.basename(name.trim());
  if (name === '') name = 'download.pdf';
  // Default a name with NO extension to `.pdf` (the metadata path is always a
  // PDF); a name that already carries an extension keeps it — a W2b browser-tab
  // download can be any file type, so don't force `.pdf` onto `report.zip`.
  if (!/\.[a-z0-9]{1,8}$/i.test(name)) name += '.pdf';
  return name;
}

export interface DownloadedFile {
  bytes: Buffer;
  file: string;
}

/// Fetch `url` (via the injected fetch), reject an HTML landing page (the common
/// paywall failure) with a typed error, enforce the 200 MB cap while streaming,
/// and return the bytes + resolved filename. `onProgress(done,total)` ticks as
/// the body streams (total is 0 until known, then the Content-Length).
export async function downloadPdfBytes(
  url: string,
  opts: {
    fetchImpl: (u: string, init: RequestInit) => Promise<Response>;
    fallback?: string;
    onProgress?: (done: number, total: number) => void;
  },
): Promise<DownloadedFile> {
  if (!/^https?:\/\//i.test(url)) throw new Error('invalid download URL');
  const res = await opts.fetchImpl(url, { redirect: 'follow' });
  if (!res.ok) throw new Error(`download failed: HTTP ${res.status}`);
  const ctype = (res.headers.get('content-type') ?? '').toLowerCase();
  // A paywalled `pdfUrl` typically returns the landing page (HTML) — surface it
  // typed so the UI can say why. Accept pdf + octet-stream (some OA hosts
  // mislabel); an empty type is allowed (streamed, sniffed by extension).
  if (ctype.includes('text/html') || ctype.includes('application/xhtml')) {
    throw new Error('not a PDF (landing page, not a file)');
  }
  const lenHeader = Number(res.headers.get('content-length') ?? '');
  const total = Number.isFinite(lenHeader) && lenHeader > 0 ? lenHeader : 0;
  if (total > DOWNLOAD_CAP) throw new Error('download exceeds the 200 MB cap');

  const file = downloadFilename(res, url, opts.fallback ?? 'download.pdf');
  const chunks: Buffer[] = [];
  let done = 0;
  const reader = res.body?.getReader();
  if (reader !== undefined && reader !== null) {
    opts.onProgress?.(0, total);
    for (;;) {
      const { value, done: fin } = await reader.read();
      if (fin) break;
      if (value !== undefined) {
        done += value.byteLength;
        if (done > DOWNLOAD_CAP) {
          await reader.cancel().catch(() => undefined);
          throw new Error('download exceeds the 200 MB cap');
        }
        chunks.push(Buffer.from(value));
        opts.onProgress?.(done, total > 0 ? total : done);
      }
    }
  } else {
    const buf = Buffer.from(await res.arrayBuffer());
    if (buf.byteLength > DOWNLOAD_CAP) throw new Error('download exceeds the 200 MB cap');
    chunks.push(buf);
    done = buf.byteLength;
    opts.onProgress?.(done, done);
  }
  const bytes = Buffer.concat(chunks);
  if (bytes.byteLength === 0) throw new Error('download was empty');
  return { bytes, file };
}
