import { useRef, useState } from 'react';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { VoiceSession } from '../voice/session';
import { checkAddable, classify, compose, stage, type Pending } from './attach';

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
}: {
  onSend: (body: string, att: InputAttachments) => Promise<void>;
}): JSX.Element {
  const t = useT();
  const [draft, setDraft] = useState('');
  const [staged, setStaged] = useState<Pending[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [recording, setRecording] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const voiceRef = useRef<VoiceSession | null>(null);
  const draftBaseRef = useRef('');

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
        <input
          value={draft}
          placeholder={t('tx.sendPlaceholder')}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
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
