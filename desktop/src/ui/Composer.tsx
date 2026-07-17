import { useMemo, useRef, useState } from 'react';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { VoiceSession } from '../voice/session';
import { checkAddable, classify, compose, stage, type Pending } from './attach';

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
}: {
  onSend: (body: string, att: InputAttachments) => Promise<void>;
  /// When set, typing `@` opens a file picker over `items`; a pick inserts
  /// `@value` and calls `onPick` (the consumer attaches the file as context).
  mention?: { items: MentionItem[]; onPick: (item: MentionItem) => void };
}): JSX.Element {
  const t = useT();
  const [draft, setDraft] = useState('');
  const [staged, setStaged] = useState<Pending[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [recording, setRecording] = useState(false);
  const [atOpen, setAtOpen] = useState(false);
  const [atQuery, setAtQuery] = useState('');
  const [atIdx, setAtIdx] = useState(0);
  const fileRef = useRef<HTMLInputElement>(null);
  const textRef = useRef<HTMLInputElement>(null);
  const voiceRef = useRef<VoiceSession | null>(null);
  const draftBaseRef = useRef('');

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
    const session = new VoiceSession({
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
    });
    voiceRef.current = session;
    setRecording(true);
    setErr(null);
    await session.start();
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
              <button className="att-x" onClick={() => removeAt(p.id)} aria-label="remove">
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
        <button className="attach-btn" title={t('composer.attach')} onClick={() => fileRef.current?.click()} disabled={busy}>
          +
        </button>
        {isTauri() && (
          <button
            className={recording ? 'mic-btn recording' : 'mic-btn'}
            title={recording ? t('composer.stopVoice') : t('composer.voice')}
            onClick={() => void toggleVoice()}
            disabled={busy}
          >
            {recording ? '■' : '🎤'}
          </button>
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
        <input
          ref={textRef}
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
            if (e.key === 'Enter') {
              e.preventDefault();
              void send();
            }
          }}
        />
        <button className="primary" onClick={() => void send()} disabled={!canSend}>
          {t('tx.send')}
        </button>
      </div>
    </div>
  );
}
