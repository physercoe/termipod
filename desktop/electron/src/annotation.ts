/// Annotation overlay — electron-free core (D2 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4/§3.5, ADR-062 D-5).
///
/// The user points the agent at a screen region: drag a rect on the overlay,
/// the crop lands in the kimi-web composer's file input (main-side CDP
/// `DOM.setFileInputFiles`, NEVER a bridge tool — the kimiweb partition's
/// read-only posture binds agent-driven traffic, not the user's own gesture)
/// or in the AgentCompanion's compose box (`postAgentInput` images, the
/// existing hub path).
///
/// Everything here is dependency-free so the unit tests run under plain
/// `node --test` (the electron glue — capturePage, temp files, clipboard,
/// debugger attach — lives in annotation_host.ts):
///   - drag → rect normalization + guest-region mapping (which capturePage
///     target, translated coordinates);
///   - the sensitive-surface refusal (a rect FULLY inside a `capture: refuse`
///     region is refused, anything less is allowed — plan §3.4);
///   - the downscale fit (browser_screenshot discipline: PNG, max edge);
///   - the kimi composer injection sequence (selector candidates →
///     setFileInputFiles → change/input dispatch) against an injected CDP
///     sender, so the test drives it with a fake exactly like
///     browserbridge.test.ts's fake backend.
import { uiPolicyFor } from '../../src/state/ui_policy.ts';

// ── Rects ────────────────────────────────────────────────────────────────────

/// All rects are CSS px (DIPs): the selection rect and the guest element rects
/// arrive from the renderer in window coordinates; a guest capture rect is
/// translated into the guest's own coordinate space (same scale — capturePage
/// takes DIPs and returns physical pixels at the page's scale factor).
export interface AnnotRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/// A `<webview>` guest region the renderer knows about: the element's bounds
/// (window CSS px) paired with the guest's webContents id
/// (`webview.getWebContentsId()`), which is how main finds the webContents to
/// capture from. Main does NOT know guest layout — the renderer is the only
/// side that does (ADR-062 D-1: no shell scraping), so it reports these.
export interface GuestRegion {
  id: number;
  rect: AnnotRect;
}

/// A shell region showing a surface (e.g. the Settings pane): the renderer
/// reports the geometry, main applies the policy table's `capture` column to
/// the surface id — the split keeps the privacy decision in ONE file
/// (ui_policy.ts) while only the renderer knows layout.
export interface SurfaceRegion {
  surface: string;
  rect: AnnotRect;
}

/// Two drag corners → a normalized rect (the user may drag any direction).
export function normalizeDrag(x1: number, y1: number, x2: number, y2: number): AnnotRect {
  return {
    x: Math.min(x1, x2),
    y: Math.min(y1, y2),
    width: Math.abs(x2 - x1),
    height: Math.abs(y2 - y1),
  };
}

/// The drag must cover a few px to count as a selection — a bare click cancels
/// (plan §3.8: cancel attaches nothing, records nothing).
export const MIN_SELECTION_EDGE = 4;

export function rectArea(r: AnnotRect): number {
  return r.width * r.height;
}

export function rectIntersection(a: AnnotRect, b: AnnotRect): AnnotRect | null {
  const x = Math.max(a.x, b.x);
  const y = Math.max(a.y, b.y);
  const right = Math.min(a.x + a.width, b.x + b.width);
  const bottom = Math.min(a.y + a.height, b.y + b.height);
  if (right <= x || bottom <= y) return null;
  return { x, y, width: right - x, height: bottom - y };
}

/// `inner` is FULLY inside `outer` (inclusive edges) — the plan's refusal rule
/// is "entirely over a sensitive surface"; a rect that merely overlaps a
/// refuse region is allowed (§3.4: "refusal only when ENTIRELY inside").
export function rectFullyInside(inner: AnnotRect, outer: AnnotRect): boolean {
  return (
    inner.width > 0 &&
    inner.height > 0 &&
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  );
}

// ── Capture target mapping ───────────────────────────────────────────────────

/// Where a selection rect is captured from (plan §3.4: "a rect over a guest
/// captures from THAT guest's capturePage region; over the shell, from the
/// shell's").
export type CaptureTarget =
  /// The rect intersects a guest: capture the INTERSECTION (clipped to the
  /// guest's bounds, translated into guest coordinates) from that guest's
  /// webContents. A rect straddling a guest edge crops to the guest part —
  /// the target-row preview shows exactly what was captured, so the user can
  /// re-select if that isn't what they meant.
  | { kind: 'guest'; id: number; rect: AnnotRect }
  /// No guest under the rect: capture it from the shell window.
  | { kind: 'shell'; rect: AnnotRect };

/// Pick the capture target: the guest with the largest intersection wins
/// (split panes + the assistant dock can overlap), clipped + translated into
/// guest coordinates; no intersection → the shell window, rect unchanged.
export function pickCaptureTarget(rect: AnnotRect, guests: readonly GuestRegion[]): CaptureTarget {
  let best: { id: number; origin: { x: number; y: number }; clip: AnnotRect } | null = null;
  for (const g of guests) {
    const clip = rectIntersection(rect, g.rect);
    if (clip === null) continue;
    if (best === null || rectArea(clip) > rectArea(best.clip)) best = { id: g.id, origin: g.rect, clip };
  }
  if (best === null) return { kind: 'shell', rect };
  return {
    kind: 'guest',
    id: best.id,
    // Element bounds == guest viewport (the <webview> renders edge-to-edge).
    rect: { x: best.clip.x - best.origin.x, y: best.clip.y - best.origin.y, width: best.clip.width, height: best.clip.height },
  };
}

// ── Sensitive-surface refusal ────────────────────────────────────────────────

/// capturePage takes an integer rect; the drag and element rects are floats.
/// Floor the origin and ceil the far edge so the integer rect always COVERS
/// the user's selection (never crops a pixel of it).
export function toIntRect(r: AnnotRect): AnnotRect {
  const x = Math.floor(r.x);
  const y = Math.floor(r.y);
  return { x, y, width: Math.ceil(r.x + r.width) - x, height: Math.ceil(r.y + r.height) - y };
}

/// The surface id whose refuse region fully contains the rect, or null when
/// the capture is allowed. Regions whose surface has no policy row or a
/// `capture: allow` row never refuse — the table (ui_policy.ts), not the
/// renderer, decides which surfaces are sensitive (ADR-062 D-3).
export function refusedSurface(rect: AnnotRect, regions: readonly SurfaceRegion[]): string | null {
  for (const r of regions) {
    if (uiPolicyFor(r.surface)?.capture !== 'refuse') continue;
    if (rectFullyInside(rect, r.rect)) return r.surface;
  }
  return null;
}

// ── Downscale ────────────────────────────────────────────────────────────────

/// Same discipline as browser_screenshot: PNG, max edge capped so the crop is
/// a usable multimodal input, not a multi-MB screenshot.
export const MAX_IMAGE_EDGE = 1568;

/// Fit `w × h` within `max` on the longest edge, aspect preserved, never
/// upscaling. Input dims are the captured image's PHYSICAL pixels.
export function fitWithin(w: number, h: number, max: number = MAX_IMAGE_EDGE): { width: number; height: number } {
  if (w <= 0 || h <= 0) return { width: Math.max(1, w), height: Math.max(1, h) };
  const longest = Math.max(w, h);
  if (longest <= max) return { width: w, height: h };
  const scale = max / longest;
  return { width: Math.max(1, Math.round(w * scale)), height: Math.max(1, Math.round(h * scale)) };
}

// ── Temp crop files ──────────────────────────────────────────────────────────

/// `os.tmpdir()/termipod-annot-<stamp>.png` (written 0o600 by the host). The
/// kimi injection needs a real path for `DOM.setFileInputFiles`; the companion
/// path only uses the base64 payload.
export function annotationTempPath(tmpdir: string, stamp: string): string {
  const safe = stamp.replace(/[^0-9A-Za-z-]/g, '');
  return `${tmpdir}/termipod-annot-${safe}.png`;
}

/// A path the discard/attach handlers may touch: under the given tmpdir with
/// exactly our prefix + .png (defense in depth — the renderer names the file
/// it wants discarded, and it must never name anything else).
export function isAnnotationTempFile(tmpdir: string, p: string): boolean {
  const prefix = `${tmpdir}/termipod-annot-`;
  return p.startsWith(prefix) && p.endsWith('.png') && !p.slice(prefix.length).includes('/');
}

// ── kimi web composer injection ──────────────────────────────────────────────
// The live kimi web 0.28.1 bundle keeps a real (hidden) file input behind the
// composer's attach button — the same path its paste handler feeds (plan
// §3.5). The input's exact attributes are the SPA's internals, so resolution
// tries an ordered candidate list and the caller falls back to clipboard +
// focus + a paste hint when nothing matches (or the debugger is unavailable).
// This runs main-side on the USER's gesture and is NEVER registered as a
// bridge tool (plan §7 OQ-5 — the kimiweb partition stays read-only for
// agents).

/// Minimal CDP sender (webContents.debugger.sendCommand in the host; a fake in
/// tests — the same seam as browserbridge.ts's BridgeBackend).
export type CdpSend = (method: string, params?: Record<string, unknown>) => Promise<unknown>;

/// Ordered candidates, most-specific first. The attach input typically
/// restricts `accept` to images; the bare fallback catches a bundle that
/// doesn't. querySelectorAll returns document order; the first non-empty
/// result wins.
export const KIMI_FILE_INPUT_SELECTORS: readonly string[] = ['input[type="file"][accept]', 'input[type="file"]'];

export type KimiInjectResult = 'attached' | 'no-input';

/// Resolve the composer file input, set the crop on it, and dispatch
/// input/change so the SPA's attach pipeline picks the file up (CDP
/// `DOM.setFileInputFiles` does NOT fire DOM events — React listens for
/// `change`, so without the dispatch the attachment never registers). The user
/// reviews and hits send in kimi's own UI — this NEVER sends.
///
/// Returns 'no-input' (the caller's cue for the clipboard fallback) when no
/// candidate selector matches; CDP failures throw (the host maps them to the
/// same fallback — a lost crop is worse than a paste gesture).
export async function injectImageIntoComposer(send: CdpSend, filePath: string): Promise<KimiInjectResult> {
  const doc = (await send('DOM.getDocument', { depth: 1 })) as { root?: { nodeId?: number } };
  const rootId = doc.root?.nodeId;
  if (rootId === undefined) return 'no-input';
  let inputNode: number | null = null;
  for (const selector of KIMI_FILE_INPUT_SELECTORS) {
    const res = (await send('DOM.querySelectorAll', { nodeId: rootId, selector })) as { nodeIds?: number[] };
    if (Array.isArray(res.nodeIds) && res.nodeIds.length > 0) {
      inputNode = res.nodeIds[0] ?? null;
      break;
    }
  }
  if (inputNode === null) return 'no-input';
  const resolved = (await send('DOM.resolveNode', { nodeId: inputNode })) as { object?: { objectId?: string } };
  const objectId = resolved.object?.objectId;
  if (objectId === undefined) return 'no-input';
  try {
    await send('DOM.setFileInputFiles', { files: [filePath], objectId });
    await send('Runtime.callFunctionOn', {
      objectId,
      functionDeclaration:
        'function () { this.dispatchEvent(new Event("input", { bubbles: true })); this.dispatchEvent(new Event("change", { bubbles: true })); }',
    });
  } finally {
    await send('Runtime.releaseObject', { objectId }).catch(() => undefined);
  }
  return 'attached';
}

/// Where the composer's TEXT lands. Chat composers are either a textarea or a
/// contenteditable; same ordered-candidates posture as the file input above.
export const KIMI_TEXT_INPUT_SELECTORS: readonly string[] = ['textarea', '[contenteditable="true"]'];

export type KimiNoteResult = 'injected' | 'no-input';

/// Put the user's note (their words + the D4 pointer line) into the kimi
/// composer's text box, beside the crop the file injection just attached —
/// without this, the kimi path delivers pixels only and the pre-send pointer
/// chip promises something the agent never receives (§3.4 step 5).
///
/// `DOM.focus` + `Input.insertText`: the insertion rides the browser's own
/// input pipeline (React's value tracker included), exactly as typing would —
/// never a `.value=` assignment the SPA can't see. The user still reviews and
/// hits send in kimi's own UI; this NEVER sends.
export async function injectNoteIntoComposer(send: CdpSend, note: string): Promise<KimiNoteResult> {
  const doc = (await send('DOM.getDocument', { depth: 1 })) as { root?: { nodeId?: number } };
  const rootId = doc.root?.nodeId;
  if (rootId === undefined) return 'no-input';
  let inputNode: number | null = null;
  for (const selector of KIMI_TEXT_INPUT_SELECTORS) {
    const res = (await send('DOM.querySelectorAll', { nodeId: rootId, selector })) as { nodeIds?: number[] };
    if (Array.isArray(res.nodeIds) && res.nodeIds.length > 0) {
      inputNode = res.nodeIds[0] ?? null;
      break;
    }
  }
  if (inputNode === null) return 'no-input';
  await send('DOM.focus', { nodeId: inputNode });
  await send('Input.insertText', { text: note });
  return 'injected';
}
