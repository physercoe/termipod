import { memo, type ReactNode } from 'react';
import { Icon, type IconName } from './Icon';
import { Markdown } from './Markdown';
import { arr, bool, num, obj, str, type Entity } from '../hub/types';

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
/// neutral = everything else) instead of a per-kind rainbow. NB the tool/verb
/// affordance strings are English constants here (matching this file's existing
/// hardcoded tags); routing the transcript chrome through i18n is part of the
/// #318 sweep.

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

/// A tool call's one-line, natural-language summary (#332): an icon, a verb, and
/// the *key argument* inline (bash → the command, read/write/edit → the path,
/// search → the pattern). The full JSON hides behind a single disclosure — this
/// one change removes most of the feed's visual noise. Unknown tools fall back to
/// the tool name + the first string argument.
function toolMeta(name: string, input: unknown): { icon: IconName; verb: string; arg?: string } {
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
    return { icon: 'terminal', verb: 'Ran', arg: pick('command', 'cmd', 'script') };
  }
  if (/(multi.?edit|edit|update|replace|patch)/.test(n)) {
    return { icon: 'pen', verb: 'Edited', arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(write|create|save)/.test(n)) {
    return { icon: 'pen', verb: 'Wrote', arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(read|cat|view|open)/.test(n)) {
    return { icon: 'file-text', verb: 'Read', arg: pick('file_path', 'path', 'filepath', 'file') };
  }
  if (/(grep|search|glob|find|ripgrep)/.test(n)) {
    return { icon: 'search', verb: 'Searched', arg: pick('pattern', 'query', 'glob', 'q', 'regex') };
  }
  if (/(ls|list|dir|tree)/.test(n)) {
    return { icon: 'folder', verb: 'Listed', arg: pick('path', 'dir', 'directory') };
  }
  if (/(web|fetch|http|url|browser|curl)/.test(n)) {
    return { icon: 'globe', verb: 'Fetched', arg: pick('url', 'uri', 'query') };
  }
  // Fallback: the tool name as the verb, first string-valued argument inline.
  const firstStr = Object.values(p).find((v): v is string => typeof v === 'string' && v !== '');
  return { icon: 'wrench', verb: name, arg: firstStr };
}

function ToolResultBody({ result }: { result: Entity }): JSX.Element {
  const isErr = bool(result, 'is_error') === true;
  const denied = bool(result, 'denied') === true;
  const content = str(result, 'content') ?? jsonText(result['content']);
  const label = denied ? 'Denied' : isErr ? 'Error' : 'Result';
  return (
    <div className={`ev-result${isErr ? ' err' : ''}`}>
      <Collapsible label={`${label} · ${firstLine(content)}`} open={isErr}>
        <pre className="ev-mono">{content}</pre>
      </Collapsible>
    </div>
  );
}

function ToolCallBody({ p, result }: { p: Entity; result?: Entity }): JSX.Element {
  const name = str(p, 'name') ?? 'tool';
  const input = p['input'];
  const hasInput = input !== undefined && input !== null && input !== '';
  const meta = toolMeta(name, input);
  const errored = result !== undefined && bool(result, 'is_error') === true;
  return (
    <div className="ev-tool">
      <div className="ev-tool-head">
        <Icon name={meta.icon} size={15} className="ev-tool-ico" />
        <span className="ev-tool-verb">{meta.verb}</span>
        {meta.arg !== undefined && <code className="ev-tool-arg">{truncate(meta.arg)}</code>}
        {result !== undefined && (
          <span className={`ev-chip${errored ? ' err' : ' ok'}`} aria-hidden="true">
            <Icon name={errored ? 'close' : 'check'} size={12} />
          </span>
        )}
      </div>
      {hasInput && (
        <Collapsible label="Arguments">
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
  const raw = (str(p, 'text') ?? str(p, 'thinking') ?? str(p, 'reasoning') ?? '').trim();
  const isMarker = bool(p, 'marker_only') === true || raw === '' || raw === 'Thinking…' || raw === 'Thinking';
  // Marker-only frames have nothing to expand — render the label inline. Real
  // reasoning folds, collapsed by default (it's context, not the answer).
  if (isMarker) {
    return (
      <div className="ev-thought">
        <span className="ev-think-lead">Thinking…</span>
      </div>
    );
  }
  return (
    <div className="ev-thought">
      <FoldBlock preview={`Thinking · ${firstLine(raw, 72)}`} defaultOpen={false} className="ev-fold-thought">
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
            <Icon name="wrench" size={15} className="ev-tool-ico" />
            <span className="ev-tool-verb">{callName ?? 'Result'}</span>
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
          Turn {status}
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
          Session
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
          Done {str(p, 'subtype') ?? ''}
          {num(p, 'duration_ms') !== undefined && ` · ${num(p, 'duration_ms')} ms`}
        </div>
      );
    case 'system':
      return (
        <div className="ev-line muted">
          {str(p, 'subtype') ?? 'event'}
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
  // #332: three tones (user / error / neutral); boxed only where action lives,
  // bare (borderless) for prose + telemetry. Director/user input reads as a
  // distinct right-aligned bubble (the desktop has horizontal room the phone
  // doesn't, so the two voices read apart).
  const tone = toneFor(ev.kind, ev.producer);
  const boxed = isBoxed(ev.kind, tone);
  const cls = [
    'ev',
    boxed ? 'ev--boxed' : 'ev--bare',
    tone === 'user' ? 'ev--user' : tone === 'error' ? 'ev--error' : '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <div className={cls} data-seq={ev.seq}>
      <div className="ev-body">{bodyFor(ev, result, callName)}</div>
    </div>
  );
});
