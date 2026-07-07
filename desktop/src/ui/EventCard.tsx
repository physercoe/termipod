import { memo, type CSSProperties, type ReactNode } from 'react';
import { Markdown } from './Markdown';
import { arr, bool, num, obj, str, type Entity } from '../hub/types';

/// Rich transcript rendering (parity Phase 1a). The hub emits FLAT events, one
/// row per content block: `{id, seq, ts, kind, producer, payload}` where the
/// real content lives in the nested `payload` keyed by `kind`
/// (hub/internal/server/handlers_agent_events.go, claude-code
/// drivers/local_log_tail/claude_code/mapper.go). This dispatches on `kind` and
/// reads the per-kind payload fields, mirroring the mobile
/// lib/widgets/transcript/event_card.dart. Accents by kind mirror
/// feed_reducer.dart:1096-1124.

export interface FeedEvent {
  id: string;
  seq: number;
  kind: string;
  producer: string;
  ts?: string;
  payload: Entity;
}

export function toFeedEvent(e: Entity, fallbackIdx: number): FeedEvent {
  return {
    id: str(e, 'id') ?? String(num(e, 'seq') ?? fallbackIdx),
    seq: num(e, 'seq') ?? 0,
    kind: str(e, 'kind') ?? str(e, 'type') ?? 'event',
    producer: str(e, 'producer') ?? '',
    ts: str(e, 'ts'),
    payload: obj(e, 'payload') ?? {},
  };
}

/// tool_use_id for a tool_call: prefer `tool_use_id` (what the claude-code
/// mapper actually writes, mapper.go:369), then `id`/`toolCallId` for ACP
/// drivers. NB the mobile call side reads only `p['id']` — a latent bug that
/// makes claude-code pairing silently fail; we deliberately do not copy it.
export function callToolId(p: Entity): string | undefined {
  return str(p, 'tool_use_id') ?? str(p, 'id') ?? str(p, 'toolCallId');
}

function accentVar(kind: string, producer: string): string {
  switch (kind) {
    case 'text':
    case 'thought':
    case 'thinking':
    case 'reasoning':
      return 'var(--color-primary)';
    case 'tool_call':
      return 'var(--color-terminal-blue)';
    case 'tool_result':
    case 'diff':
      return 'var(--color-terminal-cyan)';
    case 'completion':
      return 'var(--color-success)';
    case 'error':
      return 'var(--color-error)';
    case 'lifecycle':
    case 'approval_request':
      return 'var(--color-warning)';
    case 'session.init':
    case 'plan':
      return 'var(--color-secondary)';
    case 'input.text':
      return 'var(--color-terminal-yellow)';
    default:
      return producer === 'user' ? 'var(--color-terminal-yellow)' : 'var(--text-muted)';
  }
}

function firstLine(s: string, max = 80): string {
  const line = s.split('\n', 1)[0] ?? '';
  return line.length > max ? `${line.slice(0, max)}…` : line || '(empty)';
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

function Collapsible({ label, children, open }: { label: string; children: ReactNode; open?: boolean }): JSX.Element {
  return (
    <details className="ev-collapse" open={open}>
      <summary>{label}</summary>
      {children}
    </details>
  );
}

/// A foldable text/thought block (director feedback: text and thinking cards
/// should be foldable in the transcript). The `<summary>` preview stays visible
/// in both states; `defaultOpen` decides the initial fold — assistant text opens
/// (it's the thing you're reading), reasoning stays collapsed (it's context you
/// expand on demand). Short blocks below the threshold render inline with no
/// disclosure, so the transcript isn't littered with triangles.
function FoldBlock({
  preview,
  defaultOpen,
  className,
  children,
}: {
  preview: string;
  defaultOpen: boolean;
  className?: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <details className={`ev-fold${className !== undefined ? ` ${className}` : ''}`} open={defaultOpen}>
      <summary className="ev-fold-sum">
        <span className="ev-fold-preview">{preview}</span>
      </summary>
      <div className="ev-fold-body">{children}</div>
    </details>
  );
}

/// A text block is worth folding once it spans several lines / is long enough
/// that collapsing it meaningfully shortens the transcript.
function isFoldable(s: string): boolean {
  return s.length > 240 || s.split('\n').length > 4;
}

function ToolResultBody({ result }: { result: Entity }): JSX.Element {
  const isErr = bool(result, 'is_error') === true;
  const denied = bool(result, 'denied') === true;
  const content = str(result, 'content') ?? jsonText(result['content']);
  return (
    <div className={`ev-result${isErr ? ' err' : ''}`}>
      <span className={`ev-tag${isErr ? ' err' : ''}`}>{denied ? 'denied' : isErr ? 'error' : 'result'}</span>
      <Collapsible label={firstLine(content)} open={isErr}>
        <pre className="ev-mono">{content}</pre>
      </Collapsible>
    </div>
  );
}

function ToolCallBody({ p, result }: { p: Entity; result?: Entity }): JSX.Element {
  const name = str(p, 'name') ?? 'tool';
  const input = p['input'];
  const hasInput = input !== undefined && input !== null && input !== '';
  return (
    <div className="ev-tool">
      <div className="ev-tool-head">
        <span className="ev-tag tool">tool</span>
        <strong>{name}</strong>
        {result !== undefined && (
          <span className={`ev-chip${bool(result, 'is_error') === true ? ' err' : ' ok'}`}>
            {bool(result, 'is_error') === true ? '✕' : '✓'}
          </span>
        )}
      </div>
      {hasInput && (
        <Collapsible label={firstLine(jsonText(input))}>
          <pre className="ev-mono">{jsonText(input)}</pre>
        </Collapsible>
      )}
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
      {path && <div className="ev-diff-path">{path}</div>}
      <pre className="ev-mono">
        {removed.map((l, i) => (
          <div key={`r${i}`} className="diff-del">
            - {l}
          </div>
        ))}
        {added.map((l, i) => (
          <div key={`a${i}`} className="diff-add">
            + {l}
          </div>
        ))}
      </pre>
    </div>
  );
}

function PlanBody({ p }: { p: Entity }): JSX.Element {
  const entries = arr(p, 'entries');
  return (
    <ul className="ev-plan">
      {entries.map((raw, i) => {
        const e = (raw !== null && typeof raw === 'object' ? raw : {}) as Entity;
        const status = str(e, 'status') ?? 'todo';
        const mark = status === 'completed' || status === 'done' ? '☑' : status === 'in_progress' ? '◐' : '☐';
        return (
          <li key={i} className={`plan-${status}`}>
            <span className="plan-mark">{mark}</span> {str(e, 'content') ?? ''}
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
  const raw = (str(p, 'text') ?? str(p, 'thinking') ?? str(p, 'reasoning') ?? '').trim();
  const isMarker = bool(p, 'marker_only') === true || raw === '' || raw === 'Thinking…' || raw === 'Thinking';
  // Marker-only frames have nothing to expand — render the label inline. Real
  // reasoning folds, collapsed by default (it's context, not the answer).
  if (isMarker) {
    return (
      <div className="ev-thought">
        <span className="ev-tag">thinking</span> Thinking…
      </div>
    );
  }
  return (
    <div className="ev-thought">
      <FoldBlock preview={`thinking · ${firstLine(raw, 72)}`} defaultOpen={false} className="ev-fold-thought">
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
      <Markdown text={text} />
    </div>
  );
}

function bodyFor(ev: FeedEvent, result?: Entity, callName?: string): ReactNode {
  const p = ev.payload;
  switch (ev.kind) {
    case 'text': {
      const body = str(p, 'text') ?? '';
      if (!isFoldable(body)) return <Markdown text={body} />;
      // Assistant text folds but stays open — you can collapse a long answer,
      // but you don't have to click to read it.
      return (
        <FoldBlock preview={firstLine(body, 80)} defaultOpen className="ev-fold-text">
          <Markdown text={body} />
        </FoldBlock>
      );
    }
    case 'thought':
    case 'thinking':
    case 'reasoning':
      return <ThoughtBody p={p} />;
    case 'tool_call':
      return <ToolCallBody p={p} result={result} />;
    case 'tool_result':
      return (
        <div className="ev-tool">
          <div className="ev-tool-head">
            <span className="ev-tag tool">tool</span>
            <strong>{callName ?? 'result'}</strong>
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
    case 'turn.result': {
      const status = str(p, 'status') ?? str(p, 'stop_reason') ?? 'done';
      const dur = num(p, 'duration_ms');
      const msgs = num(p, 'message_count');
      return (
        <div className="ev-turn">
          <span className="ev-tag">turn</span> {status}
          {dur !== undefined && ` · ${dur} ms`}
          {msgs !== undefined && ` · ${msgs} msgs`}
        </div>
      );
    }
    case 'usage': {
      const model = str(p, 'model');
      const inTok = num(p, 'input_tokens') ?? 0;
      const outTok = num(p, 'output_tokens') ?? 0;
      const cacheR = num(p, 'cache_read') ?? 0;
      return (
        <div className="ev-usage">
          {model ? `${model} · ` : ''}in {inTok} · out {outTok}
          {cacheR > 0 && ` · cache ${cacheR}`}
        </div>
      );
    }
    case 'session.init':
      return (
        <div className="ev-kv">
          <span className="ev-tag">session</span>
          {str(p, 'model') && <span> {str(p, 'model')}</span>}
          {arr(p, 'tools').length > 0 && <span className="muted"> · {arr(p, 'tools').length} tools</span>}
        </div>
      );
    case 'lifecycle':
      return (
        <div className="ev-line">
          <span className="ev-tag">lifecycle</span> {str(p, 'phase') ?? ''} {str(p, 'mode') ?? ''}
        </div>
      );
    case 'completion':
      return (
        <div className="ev-line">
          <span className="ev-tag">done</span> {str(p, 'subtype') ?? ''}
          {num(p, 'duration_ms') !== undefined && ` · ${num(p, 'duration_ms')} ms`}
        </div>
      );
    case 'system':
      return (
        <div className="ev-line muted">
          <span className="ev-tag">system</span> {str(p, 'subtype') ?? 'event'}
        </div>
      );
    default: {
      // Any unmapped frame that is thinking-ish (a thinking/reasoning payload
      // field, or a kind like `thinking`) renders as a thought, not a raw
      // payload dump (director feedback — thinking should read as thinking).
      if (str(p, 'thinking') !== undefined || str(p, 'reasoning') !== undefined || /think|reason/i.test(ev.kind)) {
        return <ThoughtBody p={p} />;
      }
      const text = str(p, 'text');
      if (text !== undefined) return <Markdown text={text} />;
      return (
        <div className="ev-generic">
          <span className="ev-kind-label">{ev.kind}</span>
          <Collapsible label="payload">
            <pre className="ev-mono">{jsonText(p)}</pre>
          </Collapsible>
        </div>
      );
    }
  }
}

export const EventCard = memo(function EventCard({
  ev,
  result,
  callName,
}: {
  ev: FeedEvent;
  result?: Entity;
  callName?: string;
}): JSX.Element {
  // Director/user input is placed in a distinct lane (see .ev--user) so the two
  // voices read apart on desktop's wider canvas.
  const isUser = ev.kind === 'input.text' || ev.producer === 'user';
  return (
    <div
      className={isUser ? 'ev ev--user' : 'ev'}
      data-seq={ev.seq}
      style={{ '--ev-accent': accentVar(ev.kind, ev.producer) } as CSSProperties}
    >
      <div className="ev-body">{bodyFor(ev, result, callName)}</div>
    </div>
  );
});
