/// Tests for the attachment-download core (plan §3 W2 acceptance): a `%PDF-`
/// body round-trips into bytes + a resolved filename; an HTML landing page yields
/// the typed "not a PDF" error; the 200 MB cap and filename resolution hold. A
/// real `node:http` server + the platform `fetch` exercise the streaming path.
/// Run with `node --test` (Node 22 strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { downloadPdfBytes, downloadFilename, DOWNLOAD_CAP } from './download.ts';

// Spin an http server for the duration of `fn`, handing it the base URL.
async function withServer(
  handler: (req: http.IncomingMessage, res: http.ServerResponse) => void,
  fn: (base: string) => Promise<void>,
): Promise<void> {
  const server = http.createServer(handler);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  try {
    await fn(`http://127.0.0.1:${port}`);
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
}

test('downloadPdfBytes: a %PDF- body round-trips into bytes + filename', async () => {
  const body = Buffer.from('%PDF-1.7\n... pretend pdf ...\n%%EOF');
  await withServer(
    (_req, res) => {
      res.setHeader('content-type', 'application/pdf');
      res.setHeader('content-disposition', 'attachment; filename="paper.pdf"');
      res.end(body);
    },
    async (base) => {
      const ticks: Array<[number, number]> = [];
      const out = await downloadPdfBytes(`${base}/x`, {
        fetchImpl: (u, init) => fetch(u, init as RequestInit),
        onProgress: (done, total) => ticks.push([done, total]),
      });
      assert.equal(out.file, 'paper.pdf');
      assert.ok(out.bytes.equals(body), 'bytes match the served body');
      assert.ok(ticks.length >= 1, 'emitted at least one progress tick');
      assert.equal(ticks.at(-1)?.[0], body.byteLength, 'final tick is the full size');
    },
  );
});

test('downloadPdfBytes: an HTML landing page is the typed "not a PDF" error', async () => {
  await withServer(
    (_req, res) => {
      res.setHeader('content-type', 'text/html; charset=utf-8');
      res.end('<html><body>Sign in to read this article</body></html>');
    },
    async (base) => {
      await assert.rejects(
        () => downloadPdfBytes(`${base}/paywall`, { fetchImpl: (u, init) => fetch(u, init as RequestInit) }),
        /not a PDF/,
      );
    },
  );
});

test('downloadPdfBytes: a non-2xx response rejects', async () => {
  await withServer(
    (_req, res) => {
      res.statusCode = 404;
      res.end('nope');
    },
    async (base) => {
      await assert.rejects(
        () => downloadPdfBytes(`${base}/missing`, { fetchImpl: (u, init) => fetch(u, init as RequestInit) }),
        /HTTP 404/,
      );
    },
  );
});

test('downloadPdfBytes: octet-stream is accepted (some OA hosts mislabel)', async () => {
  const body = Buffer.from('%PDF-1.4 mislabeled');
  await withServer(
    (_req, res) => {
      res.setHeader('content-type', 'application/octet-stream');
      res.end(body);
    },
    async (base) => {
      const out = await downloadPdfBytes(`${base}/a/b/report`, {
        fetchImpl: (u, init) => fetch(u, init as RequestInit),
      });
      // No Content-Disposition, path basename has no extension → .pdf appended.
      assert.equal(out.file, 'report.pdf');
      assert.ok(out.bytes.equals(body));
    },
  );
});

test('downloadPdfBytes: a non-http(s) URL is rejected before any fetch', async () => {
  let called = false;
  await assert.rejects(
    () =>
      downloadPdfBytes('file:///etc/passwd', {
        fetchImpl: async () => {
          called = true;
          return new Response('');
        },
      }),
    /invalid download URL/,
  );
  assert.equal(called, false, 'fetch was never called for a bad scheme');
});

test('downloadPdfBytes: a too-large Content-Length is refused up front', async () => {
  const res = new Response('x', { headers: { 'content-length': String(DOWNLOAD_CAP + 1), 'content-type': 'application/pdf' } });
  await assert.rejects(
    () => downloadPdfBytes('https://example.com/huge.pdf', { fetchImpl: async () => res }),
    /200 MB cap/,
  );
});

test('downloadFilename: Content-Disposition wins, then URL basename, then fallback', () => {
  const withCd = new Response('', { headers: { 'content-disposition': "attachment; filename=real.pdf" } });
  assert.equal(downloadFilename(withCd, 'https://h/ignored.pdf', 'fb.pdf'), 'real.pdf');

  const noCd = new Response('', {});
  assert.equal(downloadFilename(noCd, 'https://h/dir/from-url.pdf', 'fb.pdf'), 'from-url.pdf');

  const noneUsable = new Response('', {});
  assert.equal(downloadFilename(noneUsable, 'https://h/', 'fallback-slug.pdf'), 'fallback-slug.pdf');

  // A basename without a .pdf extension gets one appended.
  const bare = new Response('', {});
  assert.equal(downloadFilename(bare, 'https://h/dir/paper', 'fb.pdf'), 'paper.pdf');

  // A W2b browser-tab download that already has an extension keeps it (not
  // forced to .pdf) — Content-Disposition wins and .zip is preserved.
  const zip = new Response('', { headers: { 'content-disposition': 'attachment; filename="dataset.zip"' } });
  assert.equal(downloadFilename(zip, 'https://h/dl', 'fb.pdf'), 'dataset.zip');
});
