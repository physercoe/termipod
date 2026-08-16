import { memo, useState, type ReactNode } from 'react';
import { ApprovalRequestBody, AttentionRequestBody } from './ApprovalCards';
import { parseApprovalRequest } from './approvalRequest';
import { Icon, type IconName } from './Icon';
import { Markdown } from './Markdown';
import { useT, type TLookup } from '../i18n';
import { arr, bool, num, obj, str, type Entity } from '../hub/types';
import { callToolId } from './toolGroups';
import { contextDivider, fmtCost, fmtDuration, turnFooter, type ContextDivider } from './turnMarkers.ts';
import { EventImage, EventImages, StreamedOutput } from './EventMedia';
import { imageRefsOf, mediaRefFrom, resultTextOf, streamedOutputOf } from './toolMedia';

// Re-exported from its new home in toolGroups.ts (the P1 tool-lineage
// substrate) so existing importers keep working.
export { callToolId };

/// Rich transcript rendering (parity Phase 1a; visual redesign #332). The hub
/// emits FLAT events, one row per content block:
/// `{id, seq, ts, kind, producer, payload}` where the real content lives in the
/// nested `payload` keyed by `kind`
/// (hub/internal/server/handlers_agent_events.go, claude-code
/// drivers/local_log_tail/claude_code/mapper.go). This dispatches on `kind` and
/// reads the per-kind payload fields, mirroring the mobile
/// lib/widgets/transcript/event_card.dart.
///
/// #332 discipline: assistant prose and telemetry lines render *borderless*
/// (structure from typography + whitespace); a card box appears only where
/// action lives — tools, diffs, plans, errors, and the director's own input.
/// Chrome colour collapses to three semantics (accent = user, red = error,
/// neutral = everything else) instead of a per-kind rainbow. Long assistant text
/// clamps with a "show more" fade; text/input rows carry a relative timestamp and
/// hover actions (copy / quote).

export interface FeedEvent {
  id: string;
  seq: number;
  /// The dense, session-unique position (`session_ordinal`, ADR-042). 0 for
  /// per-agent / pre-migration rows. Unlike `seq` (per-agent, so it collides
  /// across a resumed session's agents) this is the correct navigation anchor
  /// for a session-scoped feed.
  ord: number;
  kind: string;
  producer: string;
  ts?: string;
  payload: Entity;
}

export function toFeedEvent(e: Entity, fallbackIdx: number): FeedEvent {
  return {
    id: str(e, 'id') ?? String(num(e, 'seq') ?? fallbackIdx),
    seq: num(e, 'seq') ?? 0,
    ord: num(e, 'session_ordinal') ?? 0,
    kind: str(e, 'kind') ?? str(e, 'type') ?? 'event',
    producer: str(e, 'producer') ?? '',
    ts: str(e, 'ts'),
    payload: obj(e, 'payload') ?? {},
  };
}

/// Visual tone — the redesign (#332) collapses the old 9-colour per-kind spine
/// palette to three semantics. Only boxed cards paint a spine; bare prose has
/// none.
type Tone = 'user' | 'error' | 'neutral';
function toneFor(kind: string, producer: string): Tone {
  if (kind === 'input.text' || producer === 'user') return 'user';
  if (kind === 'error') return 'error';
  return 'neutral';
}

/// Kinds that render borderless — assistant prose, thinking, and telemetry
/// lines. Everything else (tools, diffs, plans, unmapped payloads) keeps a card
/// box, as do the user bubble and error card (by tone).
const BARE_KINDS = new Set([
  'text',
  'thought',
  'thinking',
  'reasoning',
  'turn.result',
  'usage',
  'session.init',
  'lifecycle',
  'completion',
  'system',
]);
function isBoxed(kind: string, tone: Tone): boolean {
  if (tone !== 'neutral') return true; // user bubble + error card always boxed
  return !BARE_KINDS.has(kind);
}

/// The plain-text body of a message row (assistant text / director input) — used
/// by the copy + quote hover actions.
function messageText(ev: FeedEvent): string | undefined {
  if (ev.kind === 'text') return str(ev.payload, 'text');
  if (ev.kind === 'input.text') return str(ev.payload, 'text') ?? str(ev.payload, 'body');
  return undefined;
}

function firstLine(s: string, max = 80): string {
  const line = s.split('\n', 1)[0] ?? '';
  return line.length > max ? `${line.slice(0, max)}…` : line || '(empty)';
}

function truncate(s: string, max = 96): string {
  const one = s.replace(/\s+/g, ' ').trim();
  return one.length > max ? `${one.slice(0, max)}…` : one;
}

function jsonText(v: unknown): string {
  if (typeof v === 'string') return v;
  try {
    // JSON.stringify(undefined) is itself `undefined`, so guard the result.
    return JSON.stringify(v, null, 2) ?? String(v);
  } catch {
    return String(v);
  }
}

function relTime(ts: string, t: TLookup): string {
  const then = Date.parse(ts);
  if (Number.isNaN(then)) return '';
  const s = Math.round((Date.now() - then) / 1000);
  if (s < 45) return t('tx.now');
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}
function absTime(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
}

/// Relative timestamp with the absolute time on hover (#332 rec 5) — the row
/// timestamps the hub already emits, finally rendered.
function TimeStamp({ ts }: { ts: string }): JSX.Element {
  const t = useT();
  return (
    <time className="ev-time" dateTime={ts} title={absTime(ts)}>
      {relTime(ts, t)}
    </time>
  );
}

function Collapsible({ label, children, open }: { label: string; children: ReactNode; open?: boolean }): JSX.Element {
  return (
    <details className="ev-collapse" open={open}>
      <summary>{label}</summary>
      {children}
    </details>
  );
}

/// A foldable thought/reasoning block — collapsed by default (it's context you
/// expand on demand). Short blocks render inline with no disclosure.
function FoldBlock({
  preview,
  className,
  children,
}: {
  preview: string;
  className?: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <details className={`ev-fold${className !== undefined ? ` ${className}` : ''}`}>
      <summary className="ev-fold-sum">
        <span className="ev-fold-preview">{preview}</span>
      </summary>
      <div className="ev-fold-body">{children}</div>
    </details>
  );
}

/// Long assistant text clamps to a few lines behind a gradient fade with a
/// "Show more" toggle (#332 rec 11) — the chat idiom, replacing the old
/// `<details>` with a redundant dimmed preview line.
function ClampText({ children }: { children: ReactNode }): JSX.Element {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  return (
    <div className={expanded ? 'ev-clamp expanded' : 'ev-clamp'}>
      <div className="ev-clamp-body">{children}</div>
      <button type="button" className="ev-clamp-toggle" onClick={() => setExpanded((e) => !e)}>
        {expanded ? t('tx.showLess') : t('tx.showMore')}
      </button>
    </div>
  );
}

/// A text block is worth clamping once it spans several lines / is long enough
/// that collapsing it meaningfully shortens the transcript.
function isFoldable(s: string): boolean {
  return s.length > 240 || s.split('\n').length > 4;
}

/// A tool call's one-line, natural-language summary (#332): an icon, a verb, and
/// the *key argument* inline (bash → the command, read/write/edit → the path,
/// search → the pattern). The full JSON hides behind a single disclosure — this
/// one change removes most of the feed's visual noise. `verbKey` is a t-key for
/// known tools; unknown tools fall back to the raw tool name + first argument.
/// Exported for the P1 tool-group rows (ToolGroupCard.tsx), which render the
/// same icon + verb + arg one-liner per call.
export function toolMeta(name: string, input: unknown): { icon: IconName; verbKey?: string; verb: string; arg?: string } {
  const p = (input !== null && typeof input === 'object' ? input : {}) as Entity;
  const pick = (...keys: string[]): string | undefined => {
    for (const k of keys) {
      const v = str(p, k);
      if (v !== undefined && v !== '') return v;
    }
    return undefined;
  };
  const n = name.toLowerCase();
  if (/\b(bash|shell|exec|run|command|terminal)\b/.test(n)) {
    return { icon: 'terminal', verbKey: 'tx.verb.ran', verb: name, arg: pick('command', 'cmd', 'script') };
  }
  if (/(multi.?edit|edit|update|replace|patch)/.test(n)) {
    return { icon: 'pen', verbKey: 'tx.verb.edited', verb: name, arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(write|create|save)/.test(n)) {
    return { icon: 'pen', verbKey: 'tx.verb.wrote', verb: name, arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(read|cat|view|open)/.test(n)) {
    return { icon: 'file-text', verbKey: 'tx.verb.read', verb: name, arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(grep|search|glob|find|ripgrep)/.test(n)) {
    return { icon: 'search', verbKey: 'tx.verb.searched', verb: name, arg: pick('pattern', 'query', 'glob', 'q', 'regex') };
  }
  if (/(ls|list|dir|tree)/.test(n)) {
    return { icon: 'folder', verbKey: 'tx.verb.listed', verb: name, arg: pick('path', 'dir', 'directory') };
  }
  if (/(web|fetch|http|url|browser|curl)/.test(n)) {
    return { icon: 'globe', verbKey: 'tx.verb.fetched', verb: name, arg: pick('url', 'uri', 'query') };
  }
  // Fallback: the tool name as the verb, first string-valued argument inline.
  const firstStr = Object.values(p).find((v): v is string => typeof v === 'string' && v !== '');
  return { icon: 'wrench', verb: name, arg: firstStr };
}

function ToolResultBody({ result }: { result: Entity }): JSX.Element {
  const t = useT();
  const isErr = bool(result, 'is_error') === true;
  const denied = bool(result, 'denied') === true;
  // A tool that returns a picture (claude reading a PNG, or any bridge tool
  // behind E4's relay passthrough) sends image BLOCKS, and the drivers forward
  // the content verbatim. Painting them beats the old behaviour, which
  // jsonText'd the array and printed a screen of base64 at the director.
  const media = imageRefsOf(result['content']);
  const label = denied ? t('tx.denied') : isErr ? t('tx.errorLabel') : t('tx.result');
  if (media.length > 0) {
    // The image IS the result. Any text blocks that came with it still show;
    // the raw content deliberately does NOT, because "raw" here is megabytes of
    // base64 and <details> keeps its children in the DOM even when closed.
    const sidecar = resultTextOf(result['content']);
    return (
      <div className={`ev-result${isErr ? ' err' : ''}`}>
        <EventImages media={media} alt={t('tx.result')} />
        {sidecar !== '' && (
          <Collapsible label={`${label} · ${firstLine(sidecar)}`} open={isErr}>
            <pre className="ev-mono">{sidecar}</pre>
          </Collapsible>
        )}
      </div>
    );
  }
  const content = str(result, 'content') ?? jsonText(result['content']);
  return (
    <div className={`ev-result${isErr ? ' err' : ''}`}>
      <Collapsible label={`${label} · ${firstLine(content)}`} open={isErr}>
        <pre className="ev-mono">{content}</pre>
      </Collapsible>
    </div>
  );
}

/// The standalone tool_call card body — icon + verb + key-arg head, the
/// Arguments disclosure, and the folded-in tool_result. Exported so the P1
/// tool-group card (ToolGroupCard.tsx) reuses it verbatim as a row's lazy
/// detail.
export function ToolCallBody({ p, result, update }: { p: Entity; result?: Entity; update?: Entity }): JSX.Element {
  const t = useT();
  const name = str(p, 'name') ?? 'tool';
  const input = p['input'];
  const hasInput = input !== undefined && input !== null && input !== '';
  const meta = toolMeta(name, input);
  const errored = result !== undefined && bool(result, 'is_error') === true;
  // E3 streams a running command's output as tool_call_update partials, each
  // carrying the whole buffer so far; useToolMaps has already folded them
  // latest-wins onto this call. Once the tool_result lands it carries the same
  // bytes (codex's `aggregatedOutput`, which the E3 probe measured as identical
  // to the reassembled deltas), so the live block stands down rather than
  // printing the output twice.
  const streamed = result === undefined ? streamedOutputOf(update) : '';
  return (
    <div className="ev-tool">
      <div className="ev-tool-head">
        <Icon name={meta.icon} size={15} className="ev-tool-ico" />
        <span className="ev-tool-verb">{meta.verbKey !== undefined ? t(meta.verbKey) : meta.verb}</span>
        {meta.arg !== undefined && <code className="ev-tool-arg">{truncate(meta.arg)}</code>}
        {result !== undefined && (
          <span className={`ev-chip${errored ? ' err' : ' ok'}`} aria-hidden="true">
            <Icon name={errored ? 'close' : 'check'} size={12} />
          </span>
        )}
      </div>
      {hasInput && (
        <Collapsible label={t('tx.arguments')}>
          <pre className="ev-mono">{jsonText(input)}</pre>
        </Collapsible>
      )}
      <StreamedOutput text={streamed} />
      {result !== undefined && <ToolResultBody result={result} />}
    </div>
  );
}

function DiffBody({ p }: { p: Entity }): JSX.Element {
  const path = str(p, 'path') ?? '';
  const oldText = str(p, 'oldText') ?? str(p, 'old_text') ?? '';
  const newText = str(p, 'newText') ?? str(p, 'new_text') ?? '';
  const removed = oldText ? oldText.split('\n') : [];
  const added = newText ? newText.split('\n') : [];
  return (
    <div className="ev-diff">
      {path && (
        <div className="ev-diff-path">
          <Icon name="pen" size={13} /> {path}
        </div>
      )}
      <pre className="ev-mono ev-diff-body">
        {removed.map((l, i) => (
          <div key={`r${i}`} className="diff-del">
            <span className="diff-gutter">-</span>
            {l}
          </div>
        ))}
        {added.map((l, i) => (
          <div key={`a${i}`} className="diff-add">
            <span className="diff-gutter">+</span>
            {l}
          </div>
        ))}
      </pre>
    </div>
  );
}

/// A plan's status mark — icon, not a unicode glyph (#332): a check for done, a
/// half-disc for in-progress, an empty square for todo.
function planMark(status: string): IconName {
  if (status === 'completed' || status === 'done') return 'check';
  if (status === 'in_progress') return 'circle-half';
  return 'square';
}

function PlanBody({ p }: { p: Entity }): JSX.Element {
  const entries = arr(p, 'entries');
  return (
    <ul className="ev-plan">
      {entries.map((raw, i) => {
        const e = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
        const status = str(e, 'status') ?? 'todo';
        return (
          <li key={i} className={`plan-${status}`}>
            <Icon name={planMark(status)} size={14} className="plan-mark" /> {str(e, 'content') ?? ''}
          </li>
        );
      })}
    </ul>
  );
}

/// Extended-thinking / reasoning block. claude-code M4 emits a marker-only
/// `thought` (`{text:"Thinking…", marker_only:true}`); other frames may carry
/// the real reasoning text under text/thinking/reasoning. Render it AS thinking
/// (a muted italic block), never as a raw payload dump (director feedback).
function ThoughtBody({ p }: { p: Entity }): JSX.Element {
  const t = useT();
  const raw = (str(p, 'text') ?? str(p, 'thinking') ?? str(p, 'reasoning') ?? '').trim();
  const isMarker = bool(p, 'marker_only') === true || raw === '' || raw === 'Thinking…' || raw === 'Thinking';
  // Marker-only frames have nothing to expand — render the label inline. Real
  // reasoning folds, collapsed by default (it's context, not the answer).
  if (isMarker) {
    return (
      <div className="ev-thought">
        <span className="ev-think-lead">{t('tx.thinking')}</span>
      </div>
    );
  }
  return (
    <div className="ev-thought">
      <FoldBlock preview={`${t('tx.thinking')} ${firstLine(raw, 72)}`} className="ev-fold-thought">
        <div className="ev-thought-text">{raw}</div>
      </FoldBlock>
    </div>
  );
}

function InputTextBody({ p }: { p: Entity }): JSX.Element {
  const from = obj(p, 'from');
  const label = str(p, 'from_label') ?? (from ? str(from, 'handle') ?? str(from, 'role') : undefined) ?? 'director';
  const text = str(p, 'text') ?? str(p, 'body') ?? '';
  return (
    <div className="ev-input">
      <span className="ev-from">{label}</span>
      <InputImages p={p} />
      <Markdown text={text} uiRefs />
    </div>
  );
}

/// Image attachments on a user input (D2 annotation crops ride
/// `payload.images` verbatim — handlers_agent_input.go stamps them inline on
/// the input.text event, so the data is raw base64 here). The image card sits
/// above the note text (plan §3.5: "an image card with the note beneath").
function InputImages({ p }: { p: Entity }): JSX.Element | null {
  const images = arr(p, 'images');
  if (images.length === 0) return null;
  // A `blob:sha256/` ref here is not hypothetical and not rare: the hub
  // externalizes every payload string leaf over 64 KiB on ingest
  // (payload_externalize.go), and any real screenshot's base64 clears that. It
  // used to be skipped, so pasted images simply vanished from the transcript at
  // the size where they matter most; EventImage resolves them by sha.
  const media = images.flatMap((img) => {
    const e = (img !== null && typeof img === 'object' ? img : {}) as Entity;
    const ref = mediaRefFrom(str(e, 'mime_type'), str(e, 'data'));
    // Keep the per-image filename as alt text — it is the only label a
    // screen reader (or a broken image) has for a director's attachment.
    return ref === undefined ? [] : [{ ref, alt: str(e, 'filename') ?? 'attachment' }];
  });
  if (media.length === 0) return null;
  return (
    <div className="ev-images">
      {media.map((m, i) => (
        <EventImage key={m.ref.source === 'blob' ? `b${m.ref.sha}` : `i${String(i)}-${m.ref.data.length}`} media={m.ref} alt={m.alt} />
      ))}
    </div>
  );
}

function bodyFor(
  ev: FeedEvent,
  t: TLookup,
  result?: Entity,
  callName?: string,
  agentId?: string,
  update?: Entity,
): ReactNode {
  const p = ev.payload;
  switch (ev.kind) {
    case 'text': {
      // `text` is the agent's own prose — the PRIMARY carrier of D6
      // ref-chips ("an agent-emitted UIRef in a reply renders as a
      // clickable chip"). uiRefs must ride both branches of the fold.
      const body = str(p, 'text') ?? '';
      if (!isFoldable(body)) return <Markdown text={body} uiRefs />;
      return (
        <ClampText>
          <Markdown text={body} uiRefs />
        </ClampText>
      );
    }
    case 'thought':
    case 'thinking':
    case 'reasoning':
      return <ThoughtBody p={p} />;
    case 'approval_request':
      // The two full interactive cards (D-3). Classification is a pure call,
      // so it can gate the JSX without calling a hook-bearing component
      // conditionally: a payload none of the three producers' shapes matched
      // falls through to the generic dump, because showing the bytes beats
      // showing a card that misreads them.
      if (parseApprovalRequest(p).form !== 'unknown') {
        return <ApprovalRequestBody p={p} agentId={agentId} />;
      }
      return <GenericBody ev={ev} />;
    case 'attention_request':
      return <AttentionRequestBody p={p} />;
    case 'tool_call':
      return <ToolCallBody p={p} result={result} update={update} />;
    case 'tool_call_update': {
      // Standalone update — only reachable when the parent is gated or missing
      // (feedLens folds the rest into the parent card). Mobile parity
      // (_toolCallUpdateBody): tool + status kv line and the first text block
      // of the ACP content array as a preview.
      const title = str(p, 'title') ?? str(p, 'name') ?? 'tool';
      const status = str(p, 'status');
      // This card is the ONLY place a parentless update's content is visible,
      // so it shows the whole streamed output rather than mobile's one-line
      // preview — for a command whose parent tool_call never arrived, the first
      // line is rarely the one you need. Images render as images (an ACP update
      // can carry an image block just as a tool_result can).
      const streamed = streamedOutputOf(p);
      const media = imageRefsOf(p['content']);
      return (
        <div className="ev-tool">
          <div className="ev-tool-head">
            <Icon name="wrench" size={15} className="ev-tool-ico" />
            <span className="ev-tool-verb">{title}</span>
            {status !== undefined && <span className="muted small">· {status}</span>}
          </div>
          <EventImages media={media} alt={title} />
          <StreamedOutput text={streamed} />
        </div>
      );
    }
    case 'tool_result':
      return (
        <div className="ev-tool">
          <div className="ev-tool-head">
            <Icon name="wrench" size={15} className="ev-tool-ico" />
            <span className="ev-tool-verb">{callName ?? t('tx.result')}</span>
          </div>
          <ToolResultBody result={p} />
        </div>
      );
    case 'input.text':
      return <InputTextBody p={p} />;
    case 'error':
      return <div className="ev-err-body">{str(p, 'error') ?? str(p, 'message') ?? jsonText(p)}</div>;
    case 'diff':
      return <DiffBody p={p} />;
    case 'plan':
      return <PlanBody p={p} />;
    case 'turn.result':
      return <TurnFooterBody p={p} t={t} />;
    case 'usage': {
      const model = str(p, 'model');
      const inTok = num(p, 'input_tokens') ?? 0;
      const outTok = num(p, 'output_tokens') ?? 0;
      const cacheR = num(p, 'cache_read') ?? 0;
      return (
        <div className="ev-usage">
          {model ? `${model} · ` : ''}
          {t('tx.tokIn')} {inTok} · {t('tx.tokOut')} {outTok}
          {cacheR > 0 && ` · cache ${cacheR}`}
        </div>
      );
    }
    case 'session.init':
      return (
        <div className="ev-kv">
          {t('tx.session')}
          {str(p, 'model') && <span> · {str(p, 'model')}</span>}
          {arr(p, 'tools').length > 0 && <span className="muted"> · {arr(p, 'tools').length} tools</span>}
        </div>
      );
    case 'lifecycle':
      return (
        <div className="ev-line">
          {str(p, 'phase') ?? ''} {str(p, 'mode') ?? ''}
        </div>
      );
    case 'completion':
      return (
        <div className="ev-line">
          {t('tx.done')} {str(p, 'subtype') ?? ''}
          {num(p, 'duration_ms') !== undefined && ` · ${num(p, 'duration_ms')} ms`}
        </div>
      );
    case 'system': {
      // A compaction boundary is structure, not a notice — it draws a rule.
      const boundary = contextDivider(ev.kind, p);
      if (boundary !== undefined) return <ContextDividerBody d={boundary} t={t} />;
      // Prefer the frame's human one-liner when the hub ships one (compacted
      // task_progress mirrors `description` into `text`, #374); fall back to
      // the bare subtype tag.
      return <div className="ev-line muted">{str(p, 'text') ?? str(p, 'subtype') ?? 'event'}</div>;
    }
    case 'context.compacted':
    case 'context.cleared':
    case 'context.rewound': {
      // The hub's input-route markers (ADR-014 OQ-4). Until R3 these fell to
      // the generic payload dump — the one row in the transcript that explains
      // why the agent stopped remembering, rendered as raw JSON.
      const d = contextDivider(ev.kind, p);
      return d !== undefined ? <ContextDividerBody d={d} t={t} /> : <GenericBody ev={ev} />;
    }
    default: {
      // Any unmapped frame that is thinking-ish (a thinking/reasoning payload
      // field, or a kind like `thinking`) renders as a thought, not a raw
      // payload dump (director feedback — thinking should read as thinking).
      if (str(p, 'thinking') !== undefined || str(p, 'reasoning') !== undefined || /think|reason/i.test(ev.kind)) {
        return <ThoughtBody p={p} />;
      }
      const text = str(p, 'text');
      if (text !== undefined) return <Markdown text={text} uiRefs />;
      return <GenericBody ev={ev} />;
    }
  }
}

/// The quiet line under a closed turn (R3). `turn.result` was in the feed's
/// always-hidden set until now, which is where a whole class of "what did that
/// turn actually cost me" went: mobile has never hidden it. Duration · msgs ·
/// cost, each shown only when the engine reported it, and a failed turn says
/// so — the transcript above it may look identical to a successful one.
function TurnFooterBody({ p, t }: { p: Entity; t: TLookup }): JSX.Element {
  const f = turnFooter(p);
  const parts: string[] = [];
  if (f.durationMs !== undefined) parts.push(fmtDuration(f.durationMs));
  if (f.messages !== undefined) parts.push(`${f.messages} ${t('tx.msgs')}`);
  if (f.costUsd !== undefined) parts.push(fmtCost(f.costUsd));
  return (
    <div className={f.failed ? 'ev-turn-foot failed' : 'ev-turn-foot'}>
      <span className="tf-rule" />
      <span className="tf-text">
        {f.failed ? `${t('tx.turnEndedBadly')} ${f.status ?? ''}`.trim() : t('tx.turnEnded')}
        {parts.length > 0 && ` · ${parts.join(' · ')}`}
      </span>
      <span className="tf-rule" />
    </div>
  );
}

/// The compaction / clear / rewind divider (R3): a hairline with a centred
/// label, because what it marks is a discontinuity in the AGENT's view of the
/// conversation while the transcript above and below runs continuously. Token
/// before→after appears only where a producer reports it (kimi's M4 tap does;
/// claude's `compact_boundary` and the hub's input-route markers do not) —
/// absent stays absent rather than becoming a zero.
function ContextDividerBody({ d, t }: { d: ContextDivider; t: TLookup }): JSX.Element {
  const label = t(`tx.ctx.${d.verb}`);
  const delta =
    d.tokensBefore !== undefined && d.tokensAfter !== undefined
      ? `${d.tokensBefore} → ${d.tokensAfter}`
      : undefined;
  // The engine's own word, when it differs from the vocabulary's — gemini
  // calls it `compress`. On hover, so one label reads the same everywhere.
  const title = [t('tx.ctx.title'), d.engineVerb !== undefined ? `(${d.engineVerb})` : '', d.summary ?? '']
    .filter((x) => x !== '')
    .join(' ');
  return (
    <div className="ev-ctx-divider" title={title}>
      <span className="cd-rule" />
      <span className="cd-label">
        {label}
        {delta !== undefined && <span className="cd-delta"> {delta}</span>}
      </span>
      <span className="cd-rule" />
    </div>
  );
}

/// The unmapped-frame fallback: the kind plus a collapsed payload dump. Named
/// (rather than inline in `default:`) because a typed case can also decide its
/// payload is not the shape it expected and hand back here — an approval whose
/// producer shipped something none of the known rules match.
function GenericBody({ ev }: { ev: FeedEvent }): JSX.Element {
  return (
    <div className="ev-generic">
      <span className="ev-kind-label">{ev.kind}</span>
      <Collapsible label="payload">
        <pre className="ev-mono">{jsonText(ev.payload)}</pre>
      </Collapsible>
    </div>
  );
}

/// A copy button that flips to a check for a beat after copying.
function CopyBtn({ text }: { text: string }): JSX.Element {
  const t = useT();
  const [copied, setCopied] = useState(false);
  function copy(): void {
    void navigator.clipboard?.writeText(text).then(
      () => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      },
      () => undefined,
    );
  }
  return (
    <button type="button" className="ev-act" onClick={copy} title={t('tx.copy')} aria-label={t('tx.copy')}>
      <Icon name={copied ? 'check' : 'copy'} size={13} />
    </button>
  );
}

export const EventCard = memo(function EventCard({
  ev,
  result,
  callName,
  agentId,
  onQuote,
  update,
}: {
  ev: FeedEvent;
  result?: Entity;
  callName?: string;
  /// The agent this feed belongs to. Required for the R1 cards to ANSWER an
  /// approval or question — without it they still render what was asked, but
  /// with no buttons (a replayed transcript has nothing to unblock).
  agentId?: string;
  /// Quote this message into the composer (assistant text only). Omitted where
  /// there is no composer to quote into.
  onQuote?: (text: string) => void;
  /// The latest `tool_call_update` folded onto this `tool_call` (useToolMaps'
  /// updateById). Carries E3's streamed command output while the tool runs.
  update?: Entity;
}): JSX.Element {
  const t = useT();
  // #332: three tones (user / error / neutral); boxed only where action lives,
  // bare (borderless) for prose + telemetry. Director/user input reads as a
  // distinct right-aligned bubble.
  const tone = toneFor(ev.kind, ev.producer);
  const boxed = isBoxed(ev.kind, tone);
  const cls = [
    'ev',
    boxed ? 'ev--boxed' : 'ev--bare',
    tone === 'user' ? 'ev--user' : tone === 'error' ? 'ev--error' : '',
  ]
    .filter(Boolean)
    .join(' ');
  // Message rows (assistant text / director input) carry a timestamp + hover
  // actions; telemetry and tool cards don't.
  const text = messageText(ev);
  return (
    <div className={cls} data-seq={ev.seq}>
      <div className="ev-body">{bodyFor(ev, t, result, callName, agentId, update)}</div>
      {text !== undefined && (ev.ts !== undefined || text.trim() !== '') && (
        <div className="ev-meta">
          {ev.ts !== undefined && <TimeStamp ts={ev.ts} />}
          <span className="ev-actions">
            {ev.kind === 'text' && onQuote !== undefined && (
              <button
                type="button"
                className="ev-act"
                onClick={() => onQuote(text)}
                title={t('tx.quote')}
                aria-label={t('tx.quote')}
              >
                <Icon name="quote" size={13} />
              </button>
            )}
            <CopyBtn text={text} />
          </span>
        </div>
      )}
    </div>
  );
});
