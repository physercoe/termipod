/// Pure WebDAV URL/XML helpers (ADR-055 M2.5b), split out of `webdav.ts` so they
/// carry no electron/fs import and can be unit-tested under `node:test`. The
/// percent-encoding + trailing-slash rules here are an interop-correctness risk
/// (they must address the same object the server's PROPFIND href resolves to), so
/// they are pinned by `webdav_url.test.ts`. Mirrors foldersync.rs `base_url` /
/// `child_url` / `has_local_tag`.

export function baseUrl(url: string): URL {
  let s = url.trim();
  if (!s.endsWith('/')) s += '/';
  try {
    return new URL(s);
  } catch (e) {
    throw new Error(`invalid WebDAV URL: ${(e as Error).message}`);
  }
}

/// The URL string for a relative POSIX path under the base, each segment
/// percent-encoded; `dir` appends a trailing slash (collections need one for
/// MKCOL/PROPFIND). An empty `rel` is the base itself.
export function childUrl(base: URL, rel: string, dir: boolean): string {
  const segs = rel.split('/').filter((p) => p !== '').map(encodeURIComponent);
  let p = base.pathname; // already ends with '/'
  if (segs.length > 0) p += segs.join('/') + (dir ? '/' : '');
  return base.origin + p;
}

/// Whether the block declares a collection resourcetype (`<D:collection/>`),
/// namespace-prefix agnostic. The foldersync.rs `has_local_tag` equivalent for
/// the one tag the folder sync cares about.
export function hasCollectionTag(block: string): boolean {
  return /<(?:[A-Za-z0-9]+:)?collection(?:[\s/>])/i.test(block);
}

/// Basic-auth header value for the WebDAV requests (all of PROPFIND/GET/PUT/MKCOL
/// need it).
export function authHeader(user: string, pass: string): string {
  return `Basic ${Buffer.from(`${user}:${pass}`).toString('base64')}`;
}
