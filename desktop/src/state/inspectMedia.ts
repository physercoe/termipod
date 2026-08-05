// Type-only, and therefore erased — `inspect.ts` imports `mediaKindFor` from
// here, so a value import would close a cycle.
import type { InspectSource } from './inspect.ts';

/// Media previews for the Inspect surface — which files are pictures rather
/// than text, and the URL that shows one.
///
/// Inspect can be pointed at any file on any of its sources, but its only read
/// path was a strict-UTF-8 text slurp (`doc_read`). Opening an mp4 therefore
/// surfaced the decoder's own `TypeError: The encoded data was not valid for
/// encoding utf-8` — a true statement about bytes, and an unhelpful one about a
/// video. Read has rendered images/video/audio/PDF since it shipped; this is
/// the same treatment for Inspect.
///
/// **Mirror of `electron/src/media_policy.ts`** — the two packages cannot
/// import from each other, so the scheme name, the served-extension set and the
/// not-text marker are duplicated here, exactly as `replayMedia.ts` already
/// duplicates the URL shape. That file is the authority; tests on both sides
/// pin the same values, so a drift fails a test rather than silently producing
/// a preview that never loads.

const MEDIA_SCHEME = 'termipod-media';

export type MediaKind = 'image' | 'video' | 'audio' | 'pdf';

/// Extension → preview kind. Every entry must also be in the main process's
/// `MEDIA_TYPES`, or the scheme answers 415 and the viewer shows an empty box.
///
/// `svg` is deliberately NOT here. It is markup, the main process refuses to
/// serve it for that reason, and Inspect showing its source is the more useful
/// view of a file you are inspecting.
const EXT_MEDIA: Record<string, MediaKind> = {
  mp4: 'video', m4v: 'video', mov: 'video', webm: 'video', mkv: 'video', avi: 'video', ogv: 'video',
  png: 'image', jpg: 'image', jpeg: 'image', gif: 'image', webp: 'image', bmp: 'image', avif: 'image',
  ico: 'image', tif: 'image', tiff: 'image',
  mp3: 'audio', m4a: 'audio', wav: 'audio', oga: 'audio', ogg: 'audio', flac: 'audio', aac: 'audio', opus: 'audio',
  pdf: 'pdf',
};

/// The preview kind for a file extension, or null when the file is not media
/// (and should be read as text, as before).
export function mediaKindFor(ext: string): MediaKind | null {
  return EXT_MEDIA[ext.trim().toLowerCase().replace(/^\./, '')] ?? null;
}

/// Sources whose bytes the media scheme can stream.
///
/// `local` and `workspace` are absolute paths on this machine; `remote` rides a
/// live SFTP channel. `hub`, `github` and `hf` reach their bytes over HTTP
/// transports that decode to text before the renderer sees them — a preview
/// there is a transport change, not a viewer change, so those sources say so
/// rather than showing a broken player.
export function canStreamMedia(source: InspectSource): boolean {
  return source === 'local' || source === 'workspace' || source === 'remote';
}

/// The streaming URL for a local (or workspace) absolute path.
///
/// Null rather than a malformed URL when there is no path: the caller renders
/// an honest placard, where a dead `<video>` would read as a broken player.
export function mediaFileUrl(absPath: string | undefined): string | null {
  if (absPath === undefined || absPath.trim() === '') return null;
  return `${MEDIA_SCHEME}://file/?p=${encodeURIComponent(absPath)}`;
}

/// The streaming URL for a path on a remote host, over an open SSH session.
/// `sessionId` is the ephemeral `ssh_connect` id — the same one `sftpRead`
/// uses, so a tab that can read a remote file can also stream one.
export function mediaSftpUrl(sessionId: string, remotePath: string): string | null {
  if (sessionId.trim() === '' || remotePath.trim() === '') return null;
  return `${MEDIA_SCHEME}://sftp/?s=${encodeURIComponent(sessionId)}&p=${encodeURIComponent(remotePath)}`;
}

/// Largest PDF Inspect will pull into memory for the pdf.js viewer.
///
/// Only the PDF path slurps: image / video / audio stream from the media scheme
/// and cost the renderer nothing. PDF cannot, because pdf.js parses a whole
/// document, and Chromium's own viewer is not an option — `plugins` is false on
/// this window, so an `<iframe src="…​.pdf">` renders nothing at all.
export const PDF_PREVIEW_CAP = 64 * 1024 * 1024;

/// Mirror of `NOT_TEXT_PREFIX` in `electron/src/ipc/fsutil.ts`.
const NOT_TEXT_PREFIX = 'not a UTF-8 text file:';

/// Whether a failed read failed *because the file is not text*, as opposed to
/// being missing or unreadable. The two want different words on screen: one is
/// a fact about the file that no retry changes, the other is a problem the user
/// may be able to fix.
///
/// Matches as a substring because the error crosses the IPC boundary wrapped
/// ("Error invoking remote method 'bridge:invoke': Error: …").
export function isNotTextError(message: string): boolean {
  return message.includes(NOT_TEXT_PREFIX);
}
