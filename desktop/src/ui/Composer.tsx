import { useEffect, useMemo, useRef, useState } from 'react';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { VoiceSession } from '../voice/session';
import { Icon } from './Icon';
import { checkAddable, classify, compose, stage, type Pending } from './attach';

function mmss(total: number): string {
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${s.toString().padStart(2, '0')}`;
}

/// A `@`-mention candidate (a workspace file). `label` is shown + inserted as
/// `@label`; the consumer resolves the pick (e.g. reads the file as context).
export interface MentionItem {
  label: string;
  value: string;
}

function humanSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${bytes} B`;
}

/// Attachment-aware director composer (parity Phase 1c). A text field plus an
/// attach button that stages images/pdf/audio/video (base64) and text/code
/// files (inlined as fenced blocks), clamped to the hub caps client-side.
export function Composer({
  onSend,
  mention,
  draftKey,
  generating,
  onStop,
  inject,
}: {
  onSend: (body: string, att: InputAttachments) => Promise<void>;
  /// When set, typing `@` opens a file picker over `items`; a pick inserts
  /// `@value` and calls `onPick` (the consumer attaches the file as context).
  mention?: { items: MentionItem[]; onPick: (item: MentionItem) => void };
  /// When set, the draft text persists per-key across remounts / tab switches
  /// (localStorage), so switching agents/surfaces doesn't lose an in-progress
  /// message. Omit for an ephemeral composer.
  draftKey?: string;
  /// The agent is actively generating a turn (feed-derived, not lifecycle status)
  /// — with an empty draft, the primary action becomes Stop (#332). Typing a
  /// message swaps it back to Send (you can still queue input).
  generating?: boolean;
  onStop?: () => void;
  /// Push text into the draft (e.g. a quoted message). The `id` bump lets the
  /// same text re-inject; each new id appends once.
  inject?: { text: string; id: number } | null;
}): JSX.Element {
  const t = useT();
  const [draft, setDraftRaw] = useState('');
  const [staged, setStaged] = useState<Pending[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [recording, setRecording] = useState(false);
  const [recSeconds, setRecSeconds] = useState(0);
  const [atOpen, setAtOpen] = useState(false);
  const [atQuery, setAtQuery] = useState('');
  const [atIdx, setAtIdx] = useState(0);
  const fileRef = useRef<HTMLInputElement>(null);
  const textRef = useRef<HTMLTextAreaElement>(null);
  const voiceRef = useRef<VoiceSession | null>(null);
  const draftBaseRef = useRef('');

  const lsKey = draftKey !== undefined ? `termipod.draft.${draftKey}` : null;
  // Hydrate a persisted draft when the key changes (switching agents).
  useEffect(() => {
    if (lsKey === null) return;
    try {
      setDraftRaw(localStorage.getItem(lsKey) ?? '');
    } catch {
      /* ignore */
    }
  }, [lsKey]);
  function setDraft(v: string): void {
    setDraftRaw(v);
    if (lsKey !== null) {
      try {
        localStorage.setItem(lsKey, v);
      } catch {
        /* ignore */
      }
    }
  }

  // Inject pushed text (a quoted message) into the draft, once per id bump. A ref
  // mirrors the draft so the effect reads the current value without re-firing on
  // every keystroke.
  const draftRef = useRef(draft);
  draftRef.current = draft;
  // Seed with the current signal id so an already-consumed quote isn't
  // re-injected when the composer remounts (e.g. Insight → Live tab switch).
  const lastInjectRef = useRef(inject?.id ?? 0);
  useEffect(() => {
    if (inject === null || inject === undefined || inject.id === lastInjectRef.current) return;
    lastInjectRef.current = inject.id;
    const cur = draftRef.current;
    setDraft(cur.trim() === '' ? inject.text : `${cur}\n${inject.text}`);
    requestAnimationFrame(() => textRef.current?.focus());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inject]);

  // Auto-grow the textarea to fit its content (capped), so a multi-line message
  // is visible while typing instead of scrolling a one-line field.
  function autoGrow(el: HTMLTextAreaElement | null): void {
    if (el === null) return;
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  }
  useEffect(() => {
    autoGrow(textRef.current);
  }, [draft]);

  // Recording elapsed-time HUD tick.
  useEffect(() => {
    if (!recording) return;
    setRecSeconds(0);
    const id = setInterval(() => setRecSeconds((s) => s + 1), 1000);
    return () => clearInterval(id);
  }, [recording]);

  // The active `@query` immediately before the caret, if any (start-of-input or
  // after whitespace, no spaces in the token).
  const AT_RE = /(^|\s)@([^\s@]*)$/;
  const atMatches = useMemo(() => {
    if (mention === undefined || !atOpen) return [];
    const q = atQuery.toLowerCase();
    return mention.items.filter((it) => it.label.toLowerCase().includes(q)).slice(0, 8);
  }, [mention, atOpen, atQuery]);

  function onDraftChange(value: string, caret: number): void {
    setDraft(value);
    if (mention === undefined) return;
    const m = AT_RE.exec(value.slice(0, caret));
    if (m !== null) {
      setAtOpen(true);
      setAtQuery(m[2]);
      setAtIdx(0);
    } else {
      setAtOpen(false);
    }
  }

  function pickMention(it: MentionItem): void {
    const el = textRef.current;
    const caret = el?.selectionStart ?? draft.length;
    const before = draft.slice(0, caret);
    const after = draft.slice(caret);
    const m = AT_RE.exec(before);
    if (m === null) return;
    const start = m.index + m[1].length; // the '@'
    const insert = `@${it.value} `;
    setDraft(before.slice(0, start) + insert + after);
    setAtOpen(false);
    mention?.onPick(it);
    const pos = start + insert.length;
    requestAnimationFrame(() => {
      if (el !== null) {
        el.focus();
        el.setSelectionRange(pos, pos);
      }
    });
  }

  async function toggleVoice(): Promise<void> {
    if (recording) {
      await voiceRef.current?.finish();
      return;
    }
    draftBaseRef.current = draft.trim() === '' ? '' : `${draft.trim()} `;
    const session = new VoiceSession(
      {
        onTranscript: (text) => setDraft(`${draftBaseRef.current}${text}`),
        onDone: (text) => {
          if (text !== '') setDraft(`${draftBaseRef.current}${text} `);
          setRecording(false);
          voiceRef.current = null;
        },
        onError: (m) => {
          setErr(m);
          setRecording(false);
          voiceRef.current = null;
        },
      },
      { noApiKey: t('voice.noApiKey'), micDenied: t('voice.micDenied') },
    );
    voiceRef.current = session;
    setRecording(true);
    setErr(null);
    await session.start();
  }

  // Discard the recording without committing any transcript (the draft reverts
  // to whatever it held before recording started).
  async function discardVoice(): Promise<void> {
    await voiceRef.current?.cancel();
    voiceRef.current = null;
    setDraft(draftBaseRef.current.trimEnd());
    setRecording(false);
  }

  async function addFiles(files: FileList | null): Promise<void> {
    if (files === null) return;
    setErr(null);
    let acc = staged;
    for (const file of Array.from(files)) {
      const kind = classify(file);
      if (kind === null) {
        setErr(t('composer.unsupported').replace('{name}', file.name));
        continue;
      }
      const reason = checkAddable(file, kind, acc);
      if (reason !== null) {
        setErr(reason);
        continue;
      }
      try {
        const p = await stage(file, kind);
        acc = [...acc, p];
        setStaged(acc);
      } catch {
        setErr(t('composer.readFailed').replace('{name}', file.name));
      }
    }
    if (fileRef.current !== null) fileRef.current.value = '';
  }

  function removeAt(id: string): void {
    setStaged((prev) => prev.filter((p) => p.id !== id));
  }

  const canSend = !busy && (draft.trim() !== '' || staged.length > 0);

  async function send(): Promise<void> {
    if (!canSend) return;
    const { body, att } = compose(draft, staged);
    setBusy(true);
    setDraft('');
    setStaged([]);
    try {
      await onSend(body, att);
      setErr(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setDraft(draft); // restore text; attachments are not re-staged
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="composer-wrap">
      {err !== null && <div className="composer-err error">{err}</div>}
      {staged.length > 0 && (
        <div className="composer-chips">
          {staged.map((p) => (
            <span key={p.id} className={`att-chip k-${p.kind}`}>
              <span className="att-kind">{p.kind}</span>
              <span className="att-name">{p.name}</span>
              <span className="att-size muted">{humanSize(p.size)}</span>
              <button className="att-x" onClick={() => removeAt(p.id)} aria-label={t('common.remove')}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="composer">
        <input
          ref={fileRef}
          type="file"
          multiple
          hidden
          onChange={(e) => void addFiles(e.target.files)}
        />
        <button
          className="attach-btn"
          title={t('composer.attach')}
          aria-label={t('composer.attach')}
          onClick={() => fileRef.current?.click()}
          disabled={busy}
        >
          <Icon name="plus" size={16} />
        </button>
        {isTauri() && !recording && (
          <button
            className="mic-btn"
            title={t('composer.voice')}
            aria-label={t('composer.voice')}
            onClick={() => void toggleVoice()}
            disabled={busy}
          >
            <Icon name="mic" size={16} />
          </button>
        )}
        {isTauri() && recording && (
          <div className="rec-hud" role="status" aria-live="polite">
            <span className="rec-dot" aria-hidden="true" />
            <span className="rec-time mono">{mmss(recSeconds)}</span>
            <button className="rec-discard" title={t('composer.discardVoice')} aria-label={t('composer.discardVoice')} onClick={() => void discardVoice()}>
              <Icon name="close" size={14} />
            </button>
            <button className="rec-stop" title={t('composer.stopVoice')} aria-label={t('composer.stopVoice')} onClick={() => void toggleVoice()}>
              <Icon name="check" size={14} />
            </button>
          </div>
        )}
        {atOpen && atMatches.length > 0 && (
          <div className="mention-pop">
            {atMatches.map((it, i) => (
              <button
                key={it.value}
                className={i === atIdx ? 'mention-item active' : 'mention-item'}
                // mousedown (not click) so the pick lands before the input blurs.
                onMouseDown={(e) => {
                  e.preventDefault();
                  pickMention(it);
                }}
              >
                {it.label}
              </button>
            ))}
          </div>
        )}
        <textarea
          ref={textRef}
          className="composer-input"
          rows={1}
          value={draft}
          placeholder={t('tx.sendPlaceholder')}
          onChange={(e) => onDraftChange(e.target.value, e.target.selectionStart ?? e.target.value.length)}
          onKeyDown={(e) => {
            if (atOpen && atMatches.length > 0) {
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setAtIdx((n) => Math.min(atMatches.length - 1, n + 1));
                return;
              }
              if (e.key === 'ArrowUp') {
                e.preventDefault();
                setAtIdx((n) => Math.max(0, n - 1));
                return;
              }
              if (e.key === 'Enter' || e.key === 'Tab') {
                e.preventDefault();
                pickMention(atMatches[atIdx]);
                return;
              }
              if (e.key === 'Escape') {
                e.preventDefault();
                setAtOpen(false);
                return;
              }
            }
            // Enter sends; Shift+Enter inserts a newline (standard chat idiom).
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
        />
        {generating === true && draft.trim() === '' && staged.length === 0 ? (
          <button
            className="primary composer-send composer-stop"
            onClick={() => onStop?.()}
            title={t('tx.stop')}
            aria-label={t('tx.stop')}
          >
            <Icon name="square" size={14} />
          </button>
        ) : (
          <button
            className="primary composer-send"
            onClick={() => void send()}
            disabled={!canSend}
            title={t('tx.send')}
            aria-label={t('tx.send')}
          >
            <Icon name="send" size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
