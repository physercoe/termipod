import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import type { SseHandle } from '../hub/sse';
import { arr, str, type Entity } from '../hub/types';
import { useT } from '../i18n';
import { useSession } from '../state/session';

function eventText(e: Entity): string {
  return arr(e, 'parts')
    .map((p) => (p !== null && typeof p === 'object' ? str(p as Entity, 'text') ?? '' : ''))
    .filter((s) => s !== '')
    .join('\n');
}

function eventTs(e: Entity): string {
  return str(e, 'ts') ?? str(e, 'received_ts') ?? '';
}

/// Project/team channels chat (parity Phase 4). `streamChannel` already existed
/// with no surface. Lists channels; a selected channel backfills recent events
/// then streams live (SSE), with a composer that posts a director message
/// (`type:"message"`, one text part — handlePostEvent).
export function ChannelsPanel({ onClose }: { onClose: () => void }): JSX.Element {
  const t = useT();
  const client = useSession((s) => s.client);
  const [selected, setSelected] = useState<string | null>(null);
  const [events, setEvents] = useState<Entity[]>([]);
  const [draft, setDraft] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  const listQ = useQuery({
    queryKey: ['channels', client?.transport.teamId],
    enabled: client !== null,
    refetchInterval: 20000,
    queryFn: () => client!.listChannels(),
  });

  useEffect(() => {
    if (client === null || selected === null) return;
    let cancelled = false;
    let handle: SseHandle | null = null;
    setEvents([]);
    setErr(null);
    void (async () => {
      try {
        const initial = await client.listChannelEvents(selected, { limit: 100 });
        if (cancelled) return;
        initial.sort((a, b) => eventTs(a).localeCompare(eventTs(b)));
        setEvents(initial);
        const last = initial.length > 0 ? eventTs(initial[initial.length - 1]) : undefined;
        handle = client.streamChannel(selected, {
          since: last,
          onEvent: (e) =>
            setEvents((prev) => {
              // Dedupe by id — a reconnect can replay events already in view.
              const ev = e as Entity;
              const id = str(ev, 'id');
              if (id !== undefined && prev.some((p) => str(p, 'id') === id)) return prev;
              return [...prev, ev];
            }),
          onError: (e) => setErr(e instanceof Error ? e.message : String(e)),
        });
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
      handle?.close();
    };
  }, [client, selected]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [events]);

  async function send(): Promise<void> {
    if (client === null || selected === null || draft.trim() === '') return;
    const body = draft;
    setDraft('');
    try {
      await client.postChannelMessage(selected, body);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setDraft(body);
    }
  }

  const channels = listQ.data ?? [];

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="sessions-panel" onMouseDown={(e) => e.stopPropagation()}>
        <div className="admin-tabs">
          <strong>{t('channels.title')}</strong>
          <span className="spacer" />
          <button onClick={onClose}>{t('admin.close')}</button>
        </div>
        <div className="sessions-body">
          <div className="sessions-list">
            {listQ.isLoading && <div className="muted region-pad">{t('channels.loading')}</div>}
            {channels.map((c) => {
              const id = str(c, 'id') ?? str(c, 'channel') ?? '';
              return (
                <button
                  key={id}
                  className={id === selected ? 'session-item active' : 'session-item'}
                  onClick={() => setSelected(id)}
                >
                  <span className="session-name"># {str(c, 'name') ?? id}</span>
                </button>
              );
            })}
            {!listQ.isLoading && channels.length === 0 && <div className="muted region-pad">{t('channels.none')}</div>}
          </div>
          <div className="sessions-detail chan-detail">
            {selected === null ? (
              <div className="muted region-pad">{t('channels.pick')}</div>
            ) : (
              <>
                <div className="chan-feed">
                  {events.map((e, i) => {
                    const text = eventText(e);
                    const from = str(e, 'from_id');
                    if (str(e, 'type') !== 'message' || text === '') {
                      return (
                        <div key={str(e, 'id') ?? i} className="chan-sys muted small">
                          {str(e, 'type') ?? 'event'}
                        </div>
                      );
                    }
                    return (
                      <div key={str(e, 'id') ?? i} className="chan-msg">
                        <span className="chan-from">{from !== undefined && from !== '' ? from : t('channels.you')}</span>
                        <span className="chan-text">{text}</span>
                      </div>
                    );
                  })}
                  {err !== null && <div className="error">{err}</div>}
                  <div ref={bottomRef} />
                </div>
                <div className="composer">
                  <input
                    value={draft}
                    placeholder={t('channels.placeholder')}
                    onChange={(e) => setDraft(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault();
                        void send();
                      }
                    }}
                  />
                  <button className="primary" onClick={() => void send()} disabled={draft.trim() === ''}>
                    {t('tx.send')}
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
