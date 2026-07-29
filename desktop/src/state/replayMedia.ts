import type { EpisodeVideo } from './replayDigest';

/// Building a playable URL for one episode's video (J8 Replay W2d).
///
/// The bytes are served by the Electron main process over a privileged scheme
/// with range support (`electron/src/mediascheme.ts`), because `<video>` needs
/// a URL and needs to seek. This module is the renderer's half: joining the
/// dataset root to the host-resolved relative path, and spelling the URL.
///
/// **Mirror of `electron/src/media_policy.ts`** — the two packages cannot
/// import from each other, so the scheme name and URL shape are duplicated
/// here, the same way `InspectTree`'s HEAVY_DIRS mirrors the main-process skip
/// list. That file is the authority; the tests on both sides pin the same
/// shape, so a drift shows up as a failure rather than as a video that never
/// loads.

const MEDIA_SCHEME = 'termipod-media';

/// Join a dataset root to a POSIX-relative path from the host.
///
/// The root can be either flavour — a Windows dataset root arrives as
/// `C:\data\ds` — while the host always resolves the relative part with
/// forward slashes. Keeping the root's own separator at the seam is the same
/// rule `InspectTree.joinRel` uses; Windows accepts mixed separators, so the
/// interior slashes are left alone.
export function joinDatasetPath(root: string, rel: string): string {
  const trimmedRoot = root.replace(/[\\/]+$/, '');
  const trimmedRel = rel.replace(/^[\\/]+/, '');
  if (trimmedRoot === '') return trimmedRel;
  if (trimmedRel === '') return trimmedRoot;
  const sep = trimmedRoot.includes('\\') && !trimmedRoot.includes('/') ? '\\' : '/';
  return `${trimmedRoot}${sep}${trimmedRel}`;
}

/// The URL that plays one episode video, or null when there is nothing to play.
///
/// Null rather than a broken URL: the caller renders an honest "no video"
/// rather than a `<video>` that fails to load, which looks like a bug in the
/// player instead of an absence in the dataset.
export function episodeVideoUrl(rootPath: string, video: EpisodeVideo): string | null {
  if (rootPath.trim() === '' || video.path.trim() === '') return null;
  return `${MEDIA_SCHEME}://file/?p=${encodeURIComponent(joinDatasetPath(rootPath, video.path))}`;
}

/// Where the playhead should sit for a given moment in the EPISODE.
///
/// The episode clock starts at 0; the file clock starts at `fromTS`, which is
/// 0 for v2.1 (one file per episode) and the episode's offset for v3.0 (many
/// episodes share a file). Every caller works in episode time, so this is the
/// one place the two clocks meet.
export function fileTimeOf(video: EpisodeVideo, episodeTime: number): number {
  const t = Number.isFinite(episodeTime) ? Math.max(0, episodeTime) : 0;
  const span = video.toTS - video.fromTS;
  // A zero or negative span means the host could not derive a duration (v2.1
  // with no fps). Clamping to it would pin the playhead at the episode start;
  // leaving it unclamped lets the file's own duration bound playback instead.
  if (span <= 0) return video.fromTS + t;
  return video.fromTS + Math.min(t, span);
}

/// The episode-relative moment a file playhead is at — the inverse, for
/// driving the plot cursor from playback.
export function episodeTimeOf(video: EpisodeVideo, fileTime: number): number {
  if (!Number.isFinite(fileTime)) return 0;
  return Math.max(0, fileTime - video.fromTS);
}

/// Whether the playhead has run past the end of this episode's slice.
///
/// The reason a shared v3.0 file needs a stop at all: left alone, playback
/// would roll straight into the next episode, which looks like the robot
/// suddenly teleporting rather than like a player that did not stop.
export function isPastEnd(video: EpisodeVideo, fileTime: number): boolean {
  if (video.toTS <= video.fromTS) return false; // no known end to run past
  return fileTime >= video.toTS;
}
