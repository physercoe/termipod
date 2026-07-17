import { useEffect, useMemo, useRef, useState } from 'react';
import type { SseHandle } from '../hub/sse';
import { num, str, type Entity } from '../hub/types';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { isTauri } from '../platform';
import { useSession } from '../state/session';
import { useAgents } from '../hub/queries';
import { useWorkspace } from '../state/workspace';
import { listWorkspaceFiles, readWorkspaceFile, type WorkspaceFile } from '../state/workspaceFiles';
import { Composer } from './Composer';
import { callToolId, EventCard, toFeedEvent } from './EventCard';
import { isHiddenInFeed } from './feedLens';
import { LocalAgentLauncher } from './LocalAgentLauncher';

// Cap per-mention file text so a large file can't blow the message context.
const MENTION_MAX = 100_000;

/// The **AgentCompanion** — a hub-attached assistant panel that mounts alongside a
/// surface (Read J1, Author J2). It reuses the hub SDK's agent stream + the shared
/// Composer + EventCard, so it's a focused, surface-scoped view of one agent's
/// conversation with a composer that injects the surface's context (the current
/// paper / the current document) into each message.
///
/// Hub-attached is the default; when no hub is bound it degrades to an explanatory
/// empty state (the offline *local* runner — a desktop-local agent over ConPTY —
/// is the separate P2 half tracked in author-agent-assist-and-diagrams).

export interface CompanionContext {
  label: string; // shown in the context chip (paper/doc title)
  build: () => string; // the context block prepended to a sent message
}

function agentLabel(a: Entity): string {
  const handle = str(a, 'handle') ?? str(a, 'name') ?? '';
  if (handle !== '') return handle;
  const kind = str(a, 'kind') ?? '';
  const id = str(a, 'id') ?? '';
  return kind !== '' ? `${kind} · ${id.slice(0, 8)}` : id.slice(0, 8);
}

export function AgentCompanion({
  storageKey,
  context,
  onInsert,
}: {
  storageKey: string;
  context?: CompanionContext;
  onInsert?: (text: string) => void;
}): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const agentsQ = useAgents();
  const agents = agentsQ.data ?? [];
  const [agentId, setAgentId] = useState<string>(() => localStorage.getItem(storageKey) ?? '');
  // Hub-attached (an agent on some host) vs a local agent running on THIS machine.
  // Local is desktop-only; the choice is persisted per mount point.
  const [source, setSource] = useState<'hub' | 'local'>(() =>
    isTauri() && localStorage.getItem(`${storageKey}.src`) === 'local' ? 'local' : 'hub',
  );
  const [events, setEvents] = useState<Entity[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [useContext, setUseContext] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);
  // `@`-mention support: the workspace files (candidates) + the files the user has
  // mentioned for the next message (attached as context on send).
  const folder = useWorkspace((s) => s.folder);
  const [wsFiles, setWsFiles] = useState<WorkspaceFile[]>([]);
  const [mentioned, setMentioned] = useState<WorkspaceFile[]>([]);
  useEffect(() => {
    if (folder === null || !isTauri()) {
      setWsFiles([]);
      return;
    }
    let cancelled = false;
    void listWorkspaceFiles(folder).then((fs) => {
      if (!cancelled) setWsFiles(fs);
    });
    return () => {
      cancelled = true;
    };
  }, [folder]);
  function addMention(rel: string): void {
    const f = wsFiles.find((x) => x.rel === rel);
    if (f !== undefined) setMentioned((m) => (m.some((x) => x.rel === rel) ? m : [...m, f]));
  }

  // Auto-select the first running agent when connected and none is chosen.
  useEffect(() => {
    if (agentId === '' && agents.length > 0) {
      const first = str(agents[0], 'id') ?? '';
      if (first !== '') setAgentId(first);
    }
  }, [agents, agentId]);

  function pickAgent(id: string): void {
    setAgentId(id);
    try {
      localStorage.setItem(storageKey, id);
    } catch {
      /* ignore */
    }
  }

  function pickSource(s: 'hub' | 'local'): void {
    setSource(s);
    try {
      localStorage.setItem(`${storageKey}.src`, s);
    } catch {
      /* ignore */
    }
  }

  // Backfill + stream the selected agent (mirrors AgentTranscript's proven path).
  useEffect(() => {
    if (client === null || agentId === '' || source !== 'hub') {
      setEvents([]);
      return;
    }
    let cancelled = false;
    let handle: SseHandle | null = null;
    setEvents([]);
    setError(null);
    void (async () => {
      try {
        const initial = await client.listAgentEvents(agentId, { tail: 120 });
        if (cancelled) return;
        initial.sort((a, b) => (num(a, 'seq') ?? 0) - (num(b, 'seq') ?? 0));
        setEvents(initial);
        const last = initial.length > 0 ? num(initial[initial.length - 1], 'seq') : undefined;
        handle = client.streamAgent(agentId, {
          since: last !== undefined ? String(last) : undefined,
          onEvent: (e) => setEvents((prev) => [...prev, e as Entity]),
          onError: (err) => setError(err instanceof Error ? err.message : String(err)),
        });
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => {
      cancelled = true;
      handle?.close();
    };
  }, [client, agentId]);

  const feed = useMemo(() => events.map((e, i) => toFeedEvent(e, i)), [events]);

  const { resultById, nameById, callIds } = useMemo(() => {
    const resultById = new Map<string, Entity>();
    const nameById = new Map<string, string>();
    const callIds = new Set<string>();
    for (const ev of feed) {
      if (ev.kind === 'tool_result') {
        const id = str(ev.payload, 'tool_use_id');
        if (id !== undefined) resultById.set(id, ev.payload);
      } else if (ev.kind === 'tool_call') {
        const id = callToolId(ev.payload);
        if (id !== undefined) {
          callIds.add(id);
          const name = str(ev.payload, 'name');
          if (name !== undefined) nameById.set(id, name);
        }
      }
    }
    return { resultById, nameById, callIds };
  }, [feed]);

  // Visible feed: hide noise + fold tool_results that a tool_call already shows.
  const visible = useMemo(
    () =>
      feed.filter((ev) => {
        if (isHiddenInFeed(ev, false)) return false;
        if (ev.kind === 'tool_result') {
          const id = str(ev.payload, 'tool_use_id');
          if (id !== undefined && callIds.has(id)) return false;
        }
        return true;
      }),
    [feed, callIds],
  );

  // The most recent assistant text, for "insert into document" (Author).
  const lastReply = useMemo(() => {
    for (let i = feed.length - 1; i >= 0; i -= 1) {
      const ev = feed[i];
      if (ev.kind === 'text' && ev.producer !== 'user') {
        const text = str(ev.payload, 'text');
        if (text !== undefined && text.trim() !== '') return text;
      }
    }
    return undefined;
  }, [feed]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [events]);

  async function send(body: string, att: InputAttachments): Promise<void> {
    if (client === null || agentId === '') throw new Error(t('companion.noAgent'));
    let full = body;
    // Resolve @-mentioned workspace files into fenced context blocks.
    if (mentioned.length > 0) {
      const blocks: string[] = [];
      for (const f of mentioned) {
        try {
          const text = await readWorkspaceFile(f.path);
          blocks.push(`File \`${f.rel}\`:\n\n\`\`\`\n${text.slice(0, MENTION_MAX)}\n\`\`\``);
        } catch {
          /* skip an unreadable mention */
        }
      }
      if (blocks.length > 0) full = `${blocks.join('\n\n')}\n\n---\n\n${full}`;
    }
    if (useContext && context !== undefined) {
      const ctx = context.build().trim();
      if (ctx !== '') full = `${ctx}\n\n---\n\n${full}`;
    }
    await client.postAgentInput(agentId, full, att);
    setMentioned([]);
  }

  const head = (
    <div className="companion-head">
      <span className="companion-title">{t('companion.title')}</span>
      <span className="spacer" />
      {isTauri() && (
        <div className="companion-src" role="tablist">
          <button className={source === 'hub' ? 'active' : ''} onClick={() => pickSource('hub')}>
            {t('companion.srcHub')}
          </button>
          <button className={source === 'local' ? 'active' : ''} onClick={() => pickSource('local')}>
            {t('companion.srcLocal')}
          </button>
        </div>
      )}
      {source === 'hub' && client !== null && (
        <select className="companion-agent" value={agentId} onChange={(e) => pickAgent(e.target.value)}>
          <option value="">{t('companion.pickAgent')}</option>
          {agents.map((a) => {
            const id = str(a, 'id') ?? '';
            return (
              <option key={id} value={id}>
                {agentLabel(a)}
              </option>
            );
          })}
        </select>
      )}
    </div>
  );

  // Local agent runs on this machine — launched into the shared terminal dock
  // (raw CLI, cwd = workspace) rather than embedded here, so it fits the width and
  // is reachable from any tab. The structured/chat interaction stays the hub path
  // above; a local structured-protocol driver is the tracked follow-up.
  if (source === 'local') {
    return (
      <div className="companion">
        {head}
        <LocalAgentLauncher />
      </div>
    );
  }

  if (client === null) {
    return (
      <div className="companion">
        {head}
        <div className="companion-empty muted">{t('companion.offline')}</div>
      </div>
    );
  }

  return (
    <div className="companion">
      {head}
      {agents.length === 0 && <div className="companion-empty muted">{t('companion.noAgents')}</div>}
      {error !== null && <div className="companion-err error small">{error}</div>}
      <div className="companion-feed scroll">
        {visible.map((ev) => {
          if (ev.kind === 'tool_call') {
            const id = callToolId(ev.payload);
            return <EventCard key={ev.id} ev={ev} callName={id !== undefined ? nameById.get(id) : undefined} />;
          }
          if (ev.kind === 'tool_result') {
            const id = str(ev.payload, 'tool_use_id');
            return <EventCard key={ev.id} ev={ev} result={id !== undefined ? resultById.get(id) : undefined} />;
          }
          return <EventCard key={ev.id} ev={ev} />;
        })}
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
      {mentioned.length > 0 && (
        <div className="companion-mentions">
          {mentioned.map((f) => (
            <span key={f.rel} className="att-chip k-file" title={f.path}>
              <span className="att-name">@{f.rel}</span>
              <button
                className="att-x"
                aria-label="remove"
                onClick={() => setMentioned((m) => m.filter((x) => x.rel !== f.rel))}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <Composer
        onSend={send}
        mention={
          folder !== null && wsFiles.length > 0
            ? { items: wsFiles.map((f) => ({ label: f.rel, value: f.rel })), onPick: (it) => addMention(it.value) }
            : undefined
        }
      />
    </div>
  );
}
