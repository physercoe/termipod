/// Transcript media + live-output views (vision-parity R4). The extraction is
/// pure (toolMedia.ts); this file only paints, and owns the one piece that
/// cannot be pure — fetching an externalized blob.
import { useQuery } from '@tanstack/react-query';
import { useSession } from '../state/session';
import { useT } from '../i18n';
import { tailLines, type MediaRef } from './toolMedia';

/// How many trailing lines of a running command's output we keep in the DOM.
/// E3 tail-caps the wire payload at 32 KiB, but an ACP engine or the
/// desktop-local driver is uncapped, so the row bounds itself too.
const MAX_OUTPUT_LINES = 400;

/// Decode a blob body that holds base64 TEXT back into that text.
///
/// This is the subtlety of an externalized payload leaf. `payload_externalize.go`
/// replaces an oversized string leaf with a blob ref and stores `[]byte(leaf)` —
/// so for an image the blob's bytes ARE the base64 characters, not the decoded
/// picture. `getBytes` then base64-encodes those bytes for transport, leaving us
/// with base64(base64(image)). One `atob` unwraps the transport layer and hands
/// back the original base64 the `<img>` wants.
///
/// This is why the blob path cannot reuse `getBlobDataUrl`: that helper is built
/// for artifact blobs (raw bytes, real mime) and would both double-encode this
/// body and label it `application/octet-stream`.
function unwrapBase64Text(transportB64: string): string | undefined {
  try {
    const s = atob(transportB64);
    return s === '' ? undefined : s;
  } catch {
    return undefined;
  }
}

/// One agent-produced image. An inline ref paints immediately; a blob ref is
/// fetched by sha. The MIME always comes from the event block, never from the
/// blob record — the hub stores externalized leaves as application/octet-stream,
/// which no browser will paint as an image.
export function EventImage({ media, alt }: { media: MediaRef; alt: string }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const sha = media.source === 'blob' ? media.sha : '';
  const blobQ = useQuery({
    queryKey: ['event-media', sha],
    enabled: sha !== '' && client !== null,
    staleTime: 5 * 60_000,
    queryFn: () => client!.getBlobBytes(sha),
  });

  if (media.source === 'inline') {
    return <img className="ev-image" src={`data:${media.mime};base64,${media.data}`} alt={alt} />;
  }
  // A hub-less (desktop-local) session has no client to fetch with. Local
  // drivers never externalize, so this is unreachable in practice — but say so
  // rather than render a broken image icon.
  if (client === null || blobQ.isError) {
    return <span className="ev-image-missing">{t('tx.imageUnavailable')}</span>;
  }
  const body = blobQ.data === undefined ? undefined : unwrapBase64Text(blobQ.data.base64);
  if (body === undefined) {
    return <span className="ev-image-loading" aria-label={alt} />;
  }
  return <img className="ev-image" src={`data:${media.mime};base64,${body}`} alt={alt} />;
}

/// A row of agent-produced images. Renders nothing when there are none, so
/// callers can drop it in unconditionally.
export function EventImages({ media, alt }: { media: MediaRef[]; alt: string }): JSX.Element | null {
  if (media.length === 0) return null;
  return (
    <div className="ev-images">
      {media.map((m, i) => (
        <EventImage key={m.source === 'blob' ? `b${m.sha}` : `i${String(i)}-${m.data.length}`} media={m} alt={alt} />
      ))}
    </div>
  );
}

/// A running command's output, streamed by E3 and folded latest-wins into the
/// parent tool row. Scroll-capped rather than clamped: a running build's output
/// is something you watch, so the block is always open and pinned to its tail
/// (kimi-web's output block), not hidden behind a "show more".
export function StreamedOutput({ text }: { text: string }): JSX.Element | null {
  const t = useT();
  if (text === '') return null;
  const { text: shown, clipped } = tailLines(text, MAX_OUTPUT_LINES);
  return (
    <div className="ev-stream">
      <div className="ev-stream-head">
        <span className="dot running" aria-hidden="true" />
        <span className="ev-stream-label">{t('tx.liveOutput')}</span>
        {clipped && <span className="ev-stream-clip">{t('tx.outputClipped')}</span>}
      </div>
      <div className="ev-stream-body">
        <pre className="ev-mono ev-stream-text">{shown}</pre>
      </div>
    </div>
  );
}
