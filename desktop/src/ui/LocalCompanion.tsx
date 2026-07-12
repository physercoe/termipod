import { useEffect, useMemo, useRef, useState } from 'react';
import { invoke } from '@tauri-apps/api/core';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { useWorkspace } from '../state/workspace';
import { Composer } from './Composer';
import type { CompanionContext } from './AgentCompanion';

/// The **local** half of the AgentCompanion: the assistant drives an engine CLI
/// running on THIS machine (via the Rust `local_agent_run` command) instead of a
/// hub agent on another host. One-shot, non-interactive "print" mode — the user's
/// message (with the surface context prepended) is sent as a single prompt and the
/// stdout comes back as one reply. The command is configurable (default
/// `claude -p`); it runs in the Author workspace folder when one is open, so the
/// local agent sees the files being edited.

interface Msg {
  role: 'user' | 'agent' | 'err';
  text: string;
}

const CMD_KEY = 'termipod.localAgent.cmd';

export function LocalCompanion({
  context,
  onInsert,
}: {
  context?: CompanionContext;
  onInsert?: (text: string) => void;
}): JSX.Element {
  const t = useT();
  const folder = useWorkspace((s) => s.folder);
  const [cmd, setCmd] = useState(() => localStorage.getItem(CMD_KEY) ?? 'claude -p');
  const [editCmd, setEditCmd] = useState(false);
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [busy, setBusy] = useState(false);
  const [useContext, setUseContext] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [msgs, busy]);

  function saveCmd(v: string): void {
    setCmd(v);
    try {
      localStorage.setItem(CMD_KEY, v);
    } catch {
      /* ignore */
    }
  }

  const lastReply = useMemo(() => {
    for (let i = msgs.length - 1; i >= 0; i -= 1) if (msgs[i].role === 'agent') return msgs[i].text;
    return undefined;
  }, [msgs]);

  async function send(body: string, _att: InputAttachments): Promise<void> {
    const parts = cmd.trim().split(/\s+/).filter(Boolean);
    if (parts.length === 0) throw new Error(t('companion.localNoCmd'));
    let prompt = body;
    if (useContext && context !== undefined) {
      const ctx = context.build().trim();
      if (ctx !== '') prompt = `${ctx}\n\n---\n\n${body}`;
    }
    setMsgs((m) => [...m, { role: 'user', text: body }]);
    setBusy(true);
    try {
      const reply = await invoke<string>('local_agent_run', {
        program: parts[0],
        args: parts.slice(1),
        prompt,
        cwd: folder,
      });
      setMsgs((m) => [...m, { role: 'agent', text: reply !== '' ? reply : t('companion.localEmpty') }]);
    } catch (e) {
      setMsgs((m) => [...m, { role: 'err', text: e instanceof Error ? e.message : String(e) }]);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="companion-local-cmd">
        {editCmd ? (
          <input
            className="companion-local-cmdin mono"
            value={cmd}
            autoFocus
            spellCheck={false}
            onChange={(e) => saveCmd(e.target.value)}
            onBlur={() => setEditCmd(false)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') setEditCmd(false);
            }}
          />
        ) : (
          <button className="companion-local-cmdbtn mono small" title={t('companion.localCmdHint')} onClick={() => setEditCmd(true)}>
            $ {cmd}
            {folder !== null && <span className="muted"> · {folder}</span>}
          </button>
        )}
      </div>
      <div className="companion-feed scroll">
        {msgs.length === 0 && <div className="companion-empty muted">{t('companion.localIntro')}</div>}
        {msgs.map((m, i) => (
          <div key={i} className={`companion-msg ${m.role}`}>
            {m.text}
          </div>
        ))}
        {busy && <div className="companion-msg agent muted">{t('companion.localRunning')}</div>}
        <div ref={bottomRef} />
      </div>
      {onInsert !== undefined && lastReply !== undefined && (
        <button className="companion-insert link-btn" onClick={() => onInsert(lastReply)}>
          {t('companion.insertReply')} ↧
        </button>
      )}
      {context !== undefined && (
        <label className="companion-ctx">
          <input type="checkbox" checked={useContext} onChange={(e) => setUseContext(e.target.checked)} />
          <span className="companion-ctx-label" title={context.label}>
            {t('companion.withContext').replace('{ctx}', context.label)}
          </span>
        </label>
      )}
      <Composer onSend={send} />
    </>
  );
}
