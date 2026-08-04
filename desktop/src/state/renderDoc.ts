/// `author_render` — the decisions, without a DOM (coworking W2, deferred out
/// of lane A1; ADR-064).
///
/// **What the verb is for.** An agent that just wrote a diagram cannot tell
/// whether it looks right by re-reading the XML it sent. `author_render` closes
/// that loop: render the document the user has, hand back the picture. It is
/// the difference between an agent that edits drawings and one that edits text
/// that happens to be a drawing.
///
/// **Why it needs no approval card, even though it returns pixels.** The
/// resemblance to `ui_screenshot` is superficial and worth naming, because the
/// wrong reading here would either bury the verb under a card it does not need
/// or — much worse — let a screenshot ride a read-class gate:
///
///   - `ui_screenshot` captures the SCREEN. Its frame contains whatever else is
///     on it, which is why ADR-062 D-3 refuses by surface table and D-4 cards
///     every call with no session grant.
///   - `author_render` renders ONE DOCUMENT from its own body. No screen is
///     involved for `figure` and `excalidraw` at all, and the `diagram` path
///     asks the editor to export the document rather than to photograph itself.
///     It therefore discloses strictly LESS than `author_read`, which returns
///     the same document as source — and that rides the sharing toggle plus an
///     audit row, with no card.
///
/// So: same gate as `author_read`, same audit, `readOnlyHint: true`, no card.
/// The document is still the user's work, so the toggle is what consent means
/// here, and the toggle's own copy already enumerates it.
///
/// Everything below is decidable without a renderer. The rendering itself
/// reaches lazily-loaded vendor code, `Image`, and a canvas, so it is injected
/// (`AuthorIO.renderDocument`) exactly as B5's dry run is.
import type { DocKind } from './documents.ts';

export type RenderFormat = 'svg' | 'png';

/// How a kind is rendered, or `null` when it cannot be.
///
///   - `'body'` — from `doc.body` alone, whether or not the document is open.
///     `figure` goes through the same `renderFigure` registry the figure pane
///     uses; `excalidraw` through the same vendor exporters its own export
///     buttons use. Reusing the app's own paths is the point: a second renderer
///     would agree with the user's screen until the day it did not.
///   - `'live'` — only through the mounted editor. `diagram` is draw.io inside
///     an iframe, and the only thing that can rasterize an mxGraph model is
///     draw.io itself (`{action:'export'}` over the embed protocol). A closed
///     diagram therefore cannot be rendered, and the refusal says so rather
///     than returning a blank page.
///   - `null` — `markdown` and `table` have no picture that is not a screenshot
///     of a text layout, and `canvas` (React Flow) has no exporter today. D2's
///     canvas work is where that changes.
export function renderPathFor(kind: DocKind): 'body' | 'live' | null {
  switch (kind) {
    case 'figure':
    case 'excalidraw':
      return 'body';
    case 'diagram':
      return 'live';
    case 'markdown':
    case 'table':
    case 'canvas':
      return null;
  }
}

export const RENDERABLE_KINDS = 'figure, excalidraw and diagram';

export function renderKindRefusal(kind: DocKind): string {
  if (kind === 'canvas') {
    return `a canvas document has no renderer yet — ${RENDERABLE_KINDS} are the kinds author_render can draw. Use author_read for its JSON Canvas source`;
  }
  return `a ${kind} document is text, not a drawing — ${RENDERABLE_KINDS} are the kinds author_render can draw. Use author_read for its source`;
}

/// The refusal for a diagram nobody has open. Names the recovery, because "not
/// open" is a state the agent can do something about (ask the user, or point
/// them at it once H1–H3 land `desktop_open`).
export function renderNotOpenRefusal(title: string): string {
  return (
    `“${title}” is a diagram, and only the draw.io editor can render one — the document is not open in Author, ` +
    `so there is nothing to ask. Ask the user to open it, then call author_render again`
  );
}

/// The largest image `author_render` will hand back, as BASE64 characters —
/// the units the agent's context actually pays in, not decoded bytes.
///
/// Not a limit of the transport (the bridge would carry more) but of the
/// conversation: an image lands inline in the agent's context and stays there
/// for the rest of the session. 1 MiB of base64 is a generous drawing and a
/// painful transcript; past it the honest answer is "ask for svg", which for
/// every kind here is both smaller and the format an agent can actually read.
export const RENDER_BASE64_MAX = 1024 * 1024;

export function renderTooLargeRefusal(chars: number, format: RenderFormat): string {
  const advice =
    format === 'png'
      ? "ask for format 'svg' — it is smaller for a drawing and you can read it"
      : 'the document is very large; render a smaller page or read the source instead';
  return `the rendered ${format} is ${String(chars)} base64 characters and the cap is ${String(RENDER_BASE64_MAX)} — ${advice}`;
}

export function narrowRenderFormat(value: unknown): RenderFormat | null {
  return value === 'svg' || value === 'png' ? value : null;
}

export function mimeForFormat(format: RenderFormat): string {
  return format === 'svg' ? 'image/svg+xml' : 'image/png';
}

/// Pull the payload out of a `data:` URI.
///
/// draw.io's embed protocol answers an export with `data` as a data URI, and
/// the encoding is not fixed: SVG comes back base64 in some builds and
/// percent-encoded in others. Getting this wrong does not throw — it hands back
/// a *slightly* wrong image, or a base64 string that decodes to the literal
/// text `data:image/svg+xml;base64,…`. So it is parsed here, where a test can
/// drive both encodings, rather than by a `split(',')[1]` at the call site.
///
/// Returns the decoded TEXT for a textual type and raw base64 for a binary one,
/// which is what each caller needs next.
export function decodeDataUri(uri: string): { ok: true; mime: string; base64: boolean; payload: string } | { ok: false; message: string } {
  if (!uri.startsWith('data:')) {
    return { ok: false, message: 'the editor answered with something that is not a data URI' };
  }
  const comma = uri.indexOf(',');
  if (comma === -1) return { ok: false, message: 'the editor answered with a truncated data URI' };
  const head = uri.slice(5, comma);
  const isB64 = /;base64$/i.test(head);
  const mime = (isB64 ? head.slice(0, head.length - 7) : head).split(';')[0].trim();
  return { ok: true, mime, base64: isB64, payload: uri.slice(comma + 1) };
}

/// The SVG text out of whatever draw.io answered with.
export function svgFromDataUri(uri: string, decodeBase64: (b64: string) => string): { ok: true; svg: string } | { ok: false; message: string } {
  const parsed = decodeDataUri(uri);
  if (!parsed.ok) return parsed;
  if (!parsed.mime.includes('svg')) {
    return { ok: false, message: `the editor exported ${parsed.mime} when svg was asked for` };
  }
  const svg = parsed.base64 ? decodeBase64(parsed.payload) : decodeURIComponent(parsed.payload);
  if (!svg.includes('<svg')) return { ok: false, message: 'the editor exported something that is not an SVG document' };
  return { ok: true, svg };
}

/// One rendered image, on its way back to the agent.
export interface RenderedImage {
  /// Base64 of the image bytes — for `svg` that is base64 of the SVG source,
  /// so both formats ride the same MCP image block rather than one being an
  /// image and the other a wall of markup in a text block.
  base64: string;
  mimeType: string;
}

/// The sentence that goes WITH the image. An MCP image block carries no
/// caption, and "here is a picture" without saying which document it is of, at
/// what size, invites an agent to attribute it to the wrong one when it has
/// rendered two.
export function renderResultText(title: string, kind: string, format: RenderFormat, chars: number): string {
  return `rendered ${kind} document “${title}” as ${format} (${String(chars)} base64 characters). This is the document as the user sees it, not a screenshot of the app.`;
}
