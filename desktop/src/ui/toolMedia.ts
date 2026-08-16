/// Tool-row media + live-output extraction (vision-parity R4). Pure and
/// import-free at runtime (the Entity import is type-only) so it runs under
/// `node --test` like streamingPartials.ts and the other src leaf modules.
///
/// Two jobs, both feeding the tool row:
///
///  1. **Live output.** E3 (hub `driver_appserver.go`) buffers codex's
///     `item/commandExecution/outputDelta` and re-posts it as a
///     `tool_call_update` carrying the ACP payload verbatim —
///     `{toolCallId, content:[{type:"content", content:{type:"text", text}}],
///     partial:true}`, where `text` is the whole buffer so far (not a delta),
///     tail-capped at 32 KiB. `streamedOutputOf` pulls that text back out.
///
///  2. **Agent-produced images.** A `tool_result`'s `content` is whatever the
///     engine sent, forwarded verbatim by the drivers, so it may be a string OR
///     an array of content blocks — and an image block arrives in one of two
///     dialects (see `imageRefsOf`).
///
/// Neither is a `streamingPartials` concern: that module folds `text`/`thought`/
/// `plan` chains keyed on `message_id`, and a `tool_call_update` carries neither
/// (its id field is `toolCallId`). Update-to-parent folding is already done by
/// `useToolMaps`' `updateById` map, latest-wins.
import type { Entity } from '../hub/types';

/// The content-addressed ref scheme the hub swaps in for oversized payload
/// leaves (`hub/internal/server/payload_externalize.go`).
export const BLOB_REF_PREFIX = 'blob:sha256/';

/// A normalized, paintable image reference.
///
/// `inline` carries base64 bytes that came down with the event. `blob` is the
/// externalized case: the hub replaces **every** JSON string leaf over 64 KiB
/// with a `blob:sha256/<hex>` ref on agent-event ingest
/// (`handlers_agent_events.go:118`), and a real screenshot's base64 is always
/// over that. So `blob` is the NORMAL path for agent-produced images, not an
/// exotic one — the ref carries no bytes and must be fetched.
export type MediaRef =
  | { source: 'inline'; mime: string; data: string }
  | { source: 'blob'; mime: string; sha: string };

function isRecord(v: unknown): v is Entity {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

function strOf(o: Entity, k: string): string | undefined {
  const v = o[k];
  return typeof v === 'string' ? v : undefined;
}

/// Normalize one `(mime, data)` pair into a `MediaRef`, routing an externalized
/// `blob:sha256/<hex>` payload to the blob branch. Returns undefined for an
/// empty payload or a non-image mime — a `<img>` that can't paint is worse than
/// no card, and the mime here is attacker-adjacent data (it rides an agent
/// event), so this is an allow-list rather than a pass-through.
export function mediaRefFrom(mime: string | undefined, data: string | undefined): MediaRef | undefined {
  if (data === undefined || data === '') return undefined;
  // A block that declares no mime is still an image block by position (the
  // caller only reaches here for type:"image"); mirror the existing
  // InputImages default rather than dropping the image.
  const m = mime === undefined || mime === '' ? 'image/png' : mime;
  if (!m.startsWith('image/')) return undefined;
  if (data.startsWith(BLOB_REF_PREFIX)) {
    const sha = data.slice(BLOB_REF_PREFIX.length);
    return sha === '' ? undefined : { source: 'blob', mime: m, sha };
  }
  return { source: 'inline', mime: m, data };
}

/// Pull paintable image refs out of a `tool_result` / `tool_call_update`
/// content value. Tolerates the three shapes our producers actually emit:
///
///  - **Anthropic** (claude M2, `driver_stdio.go:446` forwards claude's
///    stream-json `tool_result` block verbatim — measured against
///    claude-code's own output): `{type:"image", source:{type:"base64",
///    media_type, data}}`.
///  - **MCP / ACP** (`driver_acp.go:1752`, and the desktop bridge tools E4 now
///    forwards unwrapped): `{type:"image", mimeType, data}`.
///  - **ACP ToolCallContent wrapper**: `{type:"content", content:<block>}` —
///    unwrapped one level, which is also the shape E3's live output rides.
///
/// A `content` that is a plain string (the common tool_result) yields nothing.
/// `source:{type:"url"}` is deliberately NOT painted: it would make the
/// renderer fetch an arbitrary host chosen by agent-controlled data.
export function imageRefsOf(content: unknown): MediaRef[] {
  if (!Array.isArray(content)) return [];
  const out: MediaRef[] = [];
  for (const raw of content) {
    if (!isRecord(raw)) continue;
    // ACP wraps the real block one level down.
    const block = strOf(raw, 'type') === 'content' && isRecord(raw['content']) ? raw['content'] : raw;
    if (strOf(block, 'type') !== 'image') continue;
    const src = block['source'];
    const ref = isRecord(src)
      ? // Anthropic dialect. Only base64 sources carry bytes we can paint.
        strOf(src, 'type') === 'base64'
        ? mediaRefFrom(strOf(src, 'media_type'), strOf(src, 'data'))
        : undefined
      : // MCP/ACP dialect — `mimeType` + `data` sit on the block itself.
        mediaRefFrom(strOf(block, 'mimeType') ?? strOf(block, 'mime_type'), strOf(block, 'data'));
    if (ref !== undefined) out.push(ref);
  }
  return out;
}

/// The human-readable text of a tool result's content.
///
/// A plain string is the content itself. A block array yields its text blocks
/// joined by newlines — these are discrete blocks, unlike the chunked byte
/// stream `streamedOutputOf` reassembles, so they get a separator. Image blocks
/// contribute nothing: their bytes belong in an `<img>`, and dumping a
/// megabyte of base64 into the DOM is what this replaces.
export function resultTextOf(content: unknown): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  const parts: string[] = [];
  for (const raw of content) {
    if (!isRecord(raw)) continue;
    const block = strOf(raw, 'type') === 'content' && isRecord(raw['content']) ? raw['content'] : raw;
    if (strOf(block, 'type') !== 'text') continue;
    const s = strOf(block, 'text');
    if (s !== undefined && s !== '') parts.push(s);
  }
  return parts.join('\n');
}

/// The cumulative streamed output carried by a `tool_call_update`, or '' when
/// it carries none.
///
/// Text blocks are joined with no separator: the payload is a byte stream that
/// was chunked, not a list of paragraphs, so a separator would inject blank
/// lines into a command's output. In practice E3 emits exactly one block
/// holding the whole buffer, so this only decides the shape of a case no
/// current producer emits.
export function streamedOutputOf(update: Entity | undefined): string {
  if (update === undefined) return '';
  const content = update['content'];
  if (!Array.isArray(content)) return '';
  let out = '';
  for (const raw of content) {
    if (!isRecord(raw)) continue;
    const block = strOf(raw, 'type') === 'content' && isRecord(raw['content']) ? raw['content'] : raw;
    if (strOf(block, 'type') !== 'text') continue;
    out += strOf(block, 'text') ?? '';
  }
  return out;
}

/// The last [max] lines of [text], plus whether anything was dropped.
///
/// E3 already tail-caps at 32 KiB, but nothing caps an ACP engine or the
/// desktop-local driver, so the renderer keeps its own bound. The TAIL is kept
/// (matching the producer's own trim) because a running command's newest output
/// is the interesting end. `clipped` exists so the row can SAY it dropped
/// something — a silently truncated log reads as a complete one.
export function tailLines(text: string, max: number): { text: string; clipped: boolean } {
  if (text === '' || max <= 0) return { text, clipped: false };
  const lines = text.split('\n');
  if (lines.length <= max) return { text, clipped: false };
  return { text: lines.slice(lines.length - max).join('\n'), clipped: true };
}
