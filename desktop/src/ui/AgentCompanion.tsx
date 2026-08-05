import { useEffect, useMemo, useRef, useState } from 'react';
import { str, type Entity } from '../hub/types';
import type { InputAttachments } from '../hub/client';
import { useT } from '../i18n';
import { isShell } from '../platform';
import { appendEvent, followAgent } from '../state/agentSource';
import { useAgentSource } from '../state/useAgentSource';
import { useAnnotation } from '../state/annotation';
import { agentEngine } from '../state/agentEngine';
import { loadCompanionBinding } from '../state/companionBinding';
import { drivingModeOf, promptCapabilities } from '../state/promptCapabilities';
import { useUiContext } from '../state/uiContext';
import { useAgentFamilies, useAgents, useAttention } from '../hub/queries';
import { useWorkspace } from '../state/workspace';
import { listWorkspaceFiles, readWorkspaceFile, type WorkspaceFile } from '../state/workspaceFiles';
import { Composer, type InjectImage } from './Composer';
import { InlineAttentionCards } from './ApprovalCards';
import { pendingAttentionFor } from './approvalRequest';
import { callToolId, EventCard, toFeedEvent } from './EventCard';
import { isHiddenInFeed } from './feedLens';
import { LocalAgentLauncher } from './LocalAgentLauncher';

// Cap per-mention file text so a large file can't blow the message context.
const MENTION_MAX = 100_000;

/// The **AgentCompanion** — an assistant panel bound to one agent. It lives in
/// the unified assistant dock (ui/AssistantDock.tsx, the Companion tab; the
/// per-surface Read/Author mounts are retired) and pairs an **event source**
/// (state/agentSource.ts) with the shared Composer + EventCard, so it's a
/// focused view of one agent's conversation with a composer that injects the
/// ACTIVE surface's context (the current paper / the current document — the
/// provider registry in state/companionContext.ts) into each message.
///
/// Since vision-parity L1 the panel names no producer: it reads whatever
/// `useAgentSource()` resolves, which is the hub SDK today and a desktop-local
/// driver once lane L3/L4 land (plan D-7 — the hub is an option, never a
/// prerequisite). With no source bound it degrades to an explanatory empty
/// state.

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
  // The bound event source (L1). Today it resolves to the hub SDK; the feed,
  // the composer and the cards below never ask which producer it is.
  const source = useAgentSource();
  const agentsQ = useAgents();
  // Pending approvals this agent raised — rendered inline (R1); the dock stays
  // the cross-agent aggregator over the same rows.
  const attentionQ = useAttention();
  const familiesQ = useAgentFamilies();
  const agents = agentsQ.data ?? [];
  const [agentId, setAgentId] = useState<string>(() =>
    // The dock companion's key falls back to the retired per-surface mounts'
    // keys (state/companionBinding.ts) so an existing binding survives.
    loadCompanionBinding((k) => localStorage.getItem(k), storageKey),
  );
  // Which PANEL this mount shows: the hub-attached chat, or the CLI launcher
  // that drops a local agent into the terminal dock. Desktop-only; persisted
  // per mount point.
  //
  // NB this is not the D-7 source kind, despite sharing the words "hub" and
  // "local" — `useAgentSource()` above answers *which producer feeds the
  // chat*, while this answers *whether the chat is what we render at all*. The
  // stored vocabulary is kept as-is so an existing choice survives; when a
  // local event source lands (plan L3) this toggle is what it retires.
  const [companionMode, setCompanionMode] = useState<'hub' | 'local'>(() =>
    isShell() && localStorage.getItem(`${storageKey}.src`) === 'local' ? 'local' : 'hub',
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
  // D2 annotation overlay: the "Ask agent" trigger shows only while the
  // UI-context-sharing toggle is on (the same toggle as D1). The companion
  // ARMS the overlay with itself as origin so the crop can come back here as
  // a chip; the kimi-web target needs no bound session (plan §3.4 step 2).
  const sharing = useUiContext((s) => s.enabled);
  const arm = useAnnotation((s) => s.arm);
  const handoff = useAnnotation((s) => s.handoff);
  const clearHandoff = useAnnotation((s) => s.clearHandoff);
  const registerCompanion = useAnnotation((s) => s.registerCompanion);
  const unregisterCompanion = useAnnotation((s) => s.unregisterCompanion);
  const [injectImage, setInjectImage] = useState<InjectImage | null>(null);
  useEffect(() => {
    if (handoff === null || handoff.storageKey !== storageKey) return;
    clearHandoff();
    setInjectImage({
      image: handoff.image,
      name: handoff.name,
      preview: handoff.preview,
      note: handoff.note,
      id: handoff.id,
    });
  }, [handoff, storageKey, clearHandoff]);
  // D2.1: report this mount's bound agent so a GLOBALLY armed annotation (the
  // status-bar chip / palette, which has no companion origin) can offer "Send
  // to <agent>" here. Hub source only — the local launcher has no compose box
  // to stage the chip in. Re-registers on binding/agents changes (the upsert
  // keeps the label fresh); unregisters on unmount or unbind. Removal must
  // NOT ride this effect's cleanup: React reruns cleanup on every dep change,
  // which would turn a re-report into remove+append and defeat the upsert's
  // in-place replacement — mount order is what the global arm's first-bound
  // pick reads. Unmount removal lives in its own effect below.
  useEffect(() => {
    if (companionMode !== 'hub' || agentId === '') {
      unregisterCompanion(storageKey);
      return;
    }
    const a = agents.find((x) => str(x, 'id') === agentId);
    registerCompanion({ storageKey, agentId, agentLabel: a !== undefined ? agentLabel(a) : agentId });
  }, [companionMode, agentId, agents, storageKey, registerCompanion, unregisterCompanion]);
  useEffect(() => () => unregisterCompanion(storageKey), [storageKey, unregisterCompanion]);
  useEffect(() => {
    if (folder === null || !isShell()) {
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

  function pickMode(s: 'hub' | 'local'): void {
    setCompanionMode(s);
    try {
      localStorage.setItem(`${storageKey}.src`, s);
    } catch {
      /* ignore */
    }
  }

  // Backfill + stream the selected agent. The ordering, the cursor and the
  // replay dedupe live in `followAgent`/`appendEvent` (state/agentSource.ts)
  // where `node --test` can hold them — this effect is now just the binding.
  useEffect(() => {
    if (source === null || agentId === '' || companionMode !== 'hub') {
      setEvents([]);
      return;
    }
    setEvents([]);
    setError(null);
    const handle = followAgent(source, agentId, {
      tail: 120,
      onBackfill: setEvents,
      onEvent: (ev) => setEvents((prev) => appendEvent(prev, ev)),
      onError: (err) => setError(err instanceof Error ? err.message : String(err)),
    });
    return () => handle.close();
  }, [source, agentId, companionMode]);

  // F3 — the bound agent's prompt modalities, resolved from the family
  // registry. Engine from `backend.kind` (a steward's own kind is its
  // template) and mode from the hub's RESOLVED `mode`. Ungated (undefined)
  // until the registry resolves — the gate is for an engine the registry SAYS
  // takes nothing, not for the moment nothing can be said; refusing on a
  // registry that is loading or unreachable would kill the annotation crop
  // and the attach button on no knowledge at all. A loaded list that does not
  // name this engine still resolves to "nothing attachable".
  const capabilities = useMemo(() => {
    if (familiesQ.data === undefined) return undefined;
    const agent = agents.find((x) => str(x, 'id') === agentId);
    // The bound agent not being in the list yet is the same unknown: its
    // record loading is not its engine declining.
    if (agent === undefined) return undefined;
    return promptCapabilities(agentEngine(agent), drivingModeOf(agent), familiesQ.data);
  }, [agents, agentId, familiesQ.data]);

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
        if (isHiddenInFeed(ev, false, nameById)) return false;
        if (ev.kind === 'tool_result') {
          const id = str(ev.payload, 'tool_use_id');
          if (id !== undefined && callIds.has(id)) return false;
        }
        return true;
      }),
    [feed, callIds, nameById],
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
    if (source === null || agentId === '') throw new Error(t('companion.noAgent'));
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
    await source.send(agentId, full, att);
    setMentioned([]);
  }

  const head = (
    <div className="companion-head">
      <span className="companion-title">{t('companion.title')}</span>
      <span className="spacer" />
      {isShell() && (
        <div className="companion-src" role="tablist">
          <button className={companionMode === 'hub' ? 'active' : ''} onClick={() => pickMode('hub')}>
            {t('companion.srcHub')}
          </button>
          <button className={companionMode === 'local' ? 'active' : ''} onClick={() => pickMode('local')}>
            {t('companion.srcLocal')}
          </button>
        </div>
      )}
      {companionMode === 'hub' && source !== null && (
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
  if (companionMode === 'local') {
    // The embedded kimi web UI moved to the app-level assistant dock
    // (ui/AssistantDock.tsx) — Local here is the CLI launcher only.
    return (
      <div className="companion">
        {head}
        <LocalAgentLauncher />
      </div>
    );
  }

  if (source === null) {
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
            return (
              <EventCard
                key={ev.id}
                ev={ev}
                agentId={agentId}
                callName={id !== undefined ? nameById.get(id) : undefined}
              />
            );
          }
          if (ev.kind === 'tool_result') {
            const id = str(ev.payload, 'tool_use_id');
            return (
              <EventCard
                key={ev.id}
                ev={ev}
                agentId={agentId}
                result={id !== undefined ? resultById.get(id) : undefined}
              />
            );
          }
          // agentId is what lets an approval/question card ANSWER (R1) — the
          // Companion is bound, so its cards are interactive.
          return <EventCard key={ev.id} ev={ev} agentId={agentId} />;
        })}
        {/* R1: the gate tool_call that raised these is hidden by toolGroups on
            the grounds that an inline card represents it — this is that card.
            Tail position because a pending item is, by definition, the newest
            thing waiting on the user. */}
        <InlineAttentionCards items={pendingAttentionFor(attentionQ.data ?? [], agentId)} />
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
                aria-label={t('common.remove')}
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
        capabilities={capabilities}
        draftKey={agentId !== '' ? `companion.${agentId}` : undefined}
        mention={
          folder !== null && wsFiles.length > 0
            ? { items: wsFiles.map((f) => ({ label: f.rel, value: f.rel })), onPick: (it) => addMention(it.value) }
            : undefined
        }
        injectImage={injectImage}
        annotate={
          sharing && isShell()
            ? {
                title: t('annotate.ask'),
                onClick: () => {
                  const a = agents.find((x) => str(x, 'id') === agentId);
                  arm({ storageKey, agentId, agentLabel: a !== undefined ? agentLabel(a) : agentId });
                },
              }
            : undefined
        }
      />
    </div>
  );
}
