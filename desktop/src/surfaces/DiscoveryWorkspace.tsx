import { useMemo, useState } from 'react';
import { sourceById, type DiscoveryPaper, type DiscoverySourceId } from '../discovery';
import { useT } from '../i18n';
import { useDiscoveryHistory } from '../state/discoveryHistory';
import {
  refreshDiscoveryTargets,
  useDiscoveryMonitor,
} from '../state/discoveryMonitor';
import {
  buildRecommendationSeed,
  paperFingerprint,
  type DiscoveryCadence,
  type DiscoverySubscriptionKind,
  type DiscoveryUpdate,
} from '../state/discoveryMonitorCore';
import { useLibrary } from '../state/library';
import { Icon } from '../ui/Icon';
import { useOpenLink } from '../ui/OpenLinkContext';

export type DiscoveryWorkspaceView = 'explore' | 'updates' | 'subscriptions' | 'for-you';

function cadenceLabel(cadence: DiscoveryCadence, t: ReturnType<typeof useT>): string {
  return t(`read.monitorCadence.${cadence}`);
}

function timeLabel(timestamp: number | undefined): string {
  if (timestamp === undefined) return '—';
  return new Date(timestamp).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
}

function WorkspacePaperCard({
  paper,
  update,
  onAdd,
  onInspect,
}: {
  paper: DiscoveryPaper;
  update?: DiscoveryUpdate;
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const references = useLibrary((state) => state.references);
  const markRead = useDiscoveryMonitor((state) => state.markRead);
  const removeUpdate = useDiscoveryMonitor((state) => state.removeUpdate);
  const fingerprint = paperFingerprint(paper);
  const imported = references.find((reference) =>
    (paper.doi !== undefined && reference.doi?.toLocaleLowerCase() === paper.doi.toLocaleLowerCase()) ||
    (paper.paperId !== '' && reference.externalId === paper.paperId) ||
    paperFingerprint({ paperId: reference.externalId ?? '', doi: reference.doi, title: reference.title, year: reference.year }) === fingerprint,
  );
  return (
    <article className={`discover-card workspace-paper-card${update?.readAt === undefined && update !== undefined ? ' unread' : ''}`}>
      {update !== undefined && (
        <div className="workspace-paper-origin">
          <span>{update.originLabel}</span>
          <span className="spacer" />
          <span>{timeLabel(update.arrivedAt)}</span>
        </div>
      )}
      <div className="discover-card-title">{paper.title}</div>
      <div className="discover-card-meta muted small">
        {paper.authors.slice(0, 4).join(', ')}
        {paper.authors.length > 4 ? ' et al.' : ''}
        {paper.year !== undefined ? ` · ${paper.year}` : ''}
        {paper.venue !== undefined ? ` · ${paper.venue}` : ''}
        {paper.citationCount !== undefined ? ` · ${paper.citationCount} ${t('read.monitorCitations')}` : ''}
      </div>
      {paper.abstract !== undefined && <div className="workspace-paper-abstract">{paper.abstract}</div>}
      <div className="discover-card-actions">
        <button
          className="primary small"
          onClick={() => {
            if (update !== undefined) markRead(update.id);
            if (imported !== undefined) onInspect(imported.id);
            else onInspect(onAdd(paper));
          }}
        >
          {imported !== undefined ? t('read.inLibrary') : t('read.addToLibrary')}
        </button>
        {paper.url !== undefined && (
          <button className="small" onClick={() => openLink(paper.url ?? '')}>
            <Icon name="external" size={13} /> {t('read.monitorOpen')}
          </button>
        )}
        {update !== undefined && update.readAt === undefined && (
          <button className="small" onClick={() => markRead(update.id)}>{t('read.monitorMarkRead')}</button>
        )}
        {update !== undefined && (
          <button className="icon-btn small" title={t('read.removeSearch')} onClick={() => removeUpdate(update.id)}>
            <Icon name="close" size={13} />
          </button>
        )}
      </div>
    </article>
  );
}

export function DiscoveryUpdatesPanel({
  onAdd,
  onInspect,
}: {
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const updates = useDiscoveryMonitor((state) => state.updates);
  const refreshing = useDiscoveryMonitor((state) => state.refreshing);
  const lastRefreshAt = useDiscoveryMonitor((state) => state.lastRefreshAt);
  const markAllRead = useDiscoveryMonitor((state) => state.markAllRead);
  const clearRead = useDiscoveryMonitor((state) => state.clearRead);
  const runs = useDiscoveryMonitor((state) => state.runs);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const unread = updates.filter((entry) => entry.readAt === undefined).length;
  const shown = unreadOnly ? updates.filter((entry) => entry.readAt === undefined) : updates;
  const errors = Object.values(runs).filter((run) => run.lastError !== undefined);
  return (
    <div className="discovery-workspace-pane">
      <header className="discovery-workspace-header">
        <div>
          <h2>{t('read.monitorUpdates')}</h2>
          <p>{t('read.monitorUpdatesHint')}</p>
        </div>
        <div className="discovery-workspace-actions">
          <button disabled={refreshing} onClick={() => void refreshDiscoveryTargets({ force: true })}>
            <Icon name="refresh" size={14} /> {refreshing ? t('read.monitorRefreshing') : t('read.monitorRefresh')}
          </button>
          <button disabled={unread === 0} onClick={markAllRead}>{t('read.monitorMarkAllRead')}</button>
        </div>
      </header>
      <div className="discovery-workspace-toolbar">
        <div className="seg compact">
          <button className={!unreadOnly ? 'seg-btn active' : 'seg-btn'} onClick={() => setUnreadOnly(false)}>{t('read.monitorAll')}</button>
          <button className={unreadOnly ? 'seg-btn active' : 'seg-btn'} onClick={() => setUnreadOnly(true)}>
            {t('read.monitorUnread')} <span className="pill">{unread}</span>
          </button>
        </div>
        <span className="muted small">{t('read.monitorLastChecked').replace('{time}', timeLabel(lastRefreshAt))}</span>
        <span className="spacer" />
        <button className="link-btn" onClick={clearRead}>{t('read.monitorClearRead')}</button>
      </div>
      {errors.length > 0 && (
        <div className="discovery-monitor-warning">
          <Icon name="alert" size={14} /> {t('read.monitorSomeFailed').replace('{n}', String(errors.length))}
        </div>
      )}
      <div className="discover-results scroll">
        {shown.length === 0 ? (
          <div className="discovery-workspace-empty">
            <Icon name="check" size={24} />
            <strong>{unreadOnly ? t('read.monitorNoUnread') : t('read.monitorNoUpdates')}</strong>
            <span>{t('read.monitorNoUpdatesHint')}</span>
          </div>
        ) : shown.map((update) => (
          <WorkspacePaperCard key={update.id} paper={update.paper} update={update} onAdd={onAdd} onInspect={onInspect} />
        ))}
      </div>
    </div>
  );
}

const SUBSCRIPTION_KINDS: DiscoverySubscriptionKind[] = ['author', 'journal', 'topic', 'citation', 'rss'];
const CADENCES: DiscoveryCadence[] = ['daily', 'weekly', 'monthly'];

export function DiscoverySubscriptionsPanel(): JSX.Element {
  const t = useT();
  const references = useLibrary((state) => state.references);
  const saved = useDiscoveryHistory((state) => state.saved);
  const setSavedSchedule = useDiscoveryHistory((state) => state.setSavedSchedule);
  const subscriptions = useDiscoveryMonitor((state) => state.subscriptions);
  const runs = useDiscoveryMonitor((state) => state.runs);
  const addSubscription = useDiscoveryMonitor((state) => state.addSubscription);
  const removeSubscription = useDiscoveryMonitor((state) => state.removeSubscription);
  const setCadence = useDiscoveryMonitor((state) => state.setSubscriptionCadence);
  const [kind, setKind] = useState<DiscoverySubscriptionKind>('topic');
  const [value, setValue] = useState('');
  const [label, setLabel] = useState('');
  const [sourceId, setSourceId] = useState<DiscoverySourceId>('openalex');
  const [cadence, setNewCadence] = useState<DiscoveryCadence>('weekly');
  const [referenceId, setReferenceId] = useState('');

  const suggestions = useMemo(() => {
    if (kind === 'author') return [...new Set(references.flatMap((reference) => reference.authors))].sort();
    if (kind === 'journal') return [...new Set(references.map((reference) => reference.venue).filter((item): item is string => item !== undefined && item !== ''))].sort();
    if (kind === 'topic') return [...new Set(references.flatMap((reference) => [...(reference.topics ?? []), ...reference.tags]))].sort();
    return [];
  }, [kind, references]);

  function reset(nextKind: DiscoverySubscriptionKind): void {
    setKind(nextKind);
    setValue('');
    setLabel('');
    setReferenceId('');
  }

  function submit(): void {
    let targetValue = value.trim();
    let targetLabel = label.trim() || targetValue;
    if (kind === 'citation') {
      const reference = references.find((entry) => entry.id === referenceId);
      if (reference === undefined) return;
      const scholarCitesId = reference.scholar?.citesId;
      const externalOpenAlex = reference.externalId !== undefined && /openalex\.org\/W\d+/i.test(reference.externalId)
        ? reference.externalId
        : undefined;
      targetValue = scholarCitesId ?? reference.openAlexId ?? externalOpenAlex ?? reference.doi ?? '';
      targetLabel = reference.title;
    }
    if (targetValue === '') return;
    const citationReference = kind === 'citation' ? references.find((entry) => entry.id === referenceId) : undefined;
    const targetSource = citationReference?.scholar?.citesId !== undefined ? 'google-scholar' : sourceId;
    const id = addSubscription({ kind, label: targetLabel, value: targetValue, sourceId: targetSource, cadence, referenceId: referenceId || undefined });
    setValue('');
    setLabel('');
    setReferenceId('');
    void refreshDiscoveryTargets({ force: true, targetKey: `subscription:${id}` });
  }

  return (
    <div className="discovery-workspace-pane">
      <header className="discovery-workspace-header">
        <div><h2>{t('read.monitorSubscriptions')}</h2><p>{t('read.monitorSubscriptionsHint')}</p></div>
      </header>
      <div className="discovery-subscribe-card">
        <div className="discovery-subscribe-kind seg compact">
          {SUBSCRIPTION_KINDS.map((entry) => (
            <button key={entry} className={kind === entry ? 'seg-btn active' : 'seg-btn'} onClick={() => reset(entry)}>
              {t(`read.monitorKind.${entry}`)}
            </button>
          ))}
        </div>
        <div className="discovery-subscribe-form">
          {kind === 'citation' ? (
            <select value={referenceId} aria-label={t('read.monitorCitationWork')} onChange={(event) => setReferenceId(event.target.value)}>
              <option value="">{t('read.monitorChooseWork')}</option>
              {references.filter((reference) => reference.scholar?.citesId !== undefined || reference.openAlexId !== undefined || reference.doi !== undefined || reference.externalId?.includes('openalex.org') === true).map((reference) => (
                <option key={reference.id} value={reference.id}>{reference.title}</option>
              ))}
            </select>
          ) : (
            <>
              <input
                value={value}
                list={suggestions.length > 0 ? 'discovery-subscription-values' : undefined}
                placeholder={kind === 'rss' ? 'https://example.org/feed.xml' : t(`read.monitorPlaceholder.${kind}`)}
                onChange={(event) => setValue(event.target.value)}
              />
              <datalist id="discovery-subscription-values">{suggestions.map((entry) => <option key={entry} value={entry} />)}</datalist>
            </>
          )}
          {kind === 'rss' && <input value={label} placeholder={t('read.monitorOptionalName')} onChange={(event) => setLabel(event.target.value)} />}
          {kind !== 'rss' && kind !== 'citation' && (
            <select value={sourceId} aria-label={t('read.monitorProvider')} onChange={(event) => setSourceId(event.target.value as DiscoverySourceId)}>
              <option value="openalex">OpenAlex</option>
              <option value="semanticscholar">Semantic Scholar</option>
              <option value="crossref">Crossref</option>
              <option value="arxiv">arXiv</option>
              <option value="pubmed">PubMed</option>
            </select>
          )}
          <select value={cadence} aria-label={t('read.monitorSchedule')} onChange={(event) => setNewCadence(event.target.value as DiscoveryCadence)}>
            {CADENCES.map((entry) => <option key={entry} value={entry}>{cadenceLabel(entry, t)}</option>)}
          </select>
          <button className="primary" disabled={kind === 'citation' ? referenceId === '' : value.trim() === ''} onClick={submit}>
            <Icon name="plus" size={14} /> {t('read.monitorSubscribe')}
          </button>
        </div>
      </div>

      <section className="discovery-monitor-section">
        <h3>{t('read.monitorScheduledSearches')}</h3>
        {saved.length === 0 ? <p className="muted small">{t('read.savedSearchesEmpty')}</p> : saved.map((search) => {
          const run = runs[`saved-search:${search.id}`];
          return (
            <div className="discovery-monitor-row" key={search.id}>
              <Icon name="search" size={14} />
              <div className="discovery-monitor-row-main"><strong>{search.name}</strong><span>{sourceById(search.sourceId).label} · {timeLabel(run?.lastRunAt)}</span></div>
              {run?.lastError !== undefined && <span className="discovery-monitor-error" title={run.lastError}><Icon name="alert" size={13} /></span>}
              <select value={search.schedule ?? 'off'} onChange={(event) => {
                const next = event.target.value === 'off' ? undefined : event.target.value as DiscoveryCadence;
                setSavedSchedule(search.id, next);
                if (next !== undefined) void refreshDiscoveryTargets({ force: true, targetKey: `saved-search:${search.id}` });
              }}>
                <option value="off">{t('read.monitorOff')}</option>
                {CADENCES.map((entry) => <option key={entry} value={entry}>{cadenceLabel(entry, t)}</option>)}
              </select>
            </div>
          );
        })}
      </section>

      <section className="discovery-monitor-section grow">
        <h3>{t('read.monitorSubscriptions')}</h3>
        {subscriptions.length === 0 ? <p className="muted small">{t('read.monitorNoSubscriptions')}</p> : subscriptions.map((subscription) => {
          const run = runs[`subscription:${subscription.id}`];
          return (
            <div className="discovery-monitor-row" key={subscription.id}>
              <Icon name={subscription.kind === 'rss' ? 'globe' : subscription.kind === 'citation' ? 'link' : 'star'} size={14} />
              <div className="discovery-monitor-row-main">
                <strong>{subscription.label}</strong>
                <span>{t(`read.monitorKind.${subscription.kind}`)} · {timeLabel(run?.lastRunAt)}</span>
              </div>
              {run?.lastError !== undefined && <span className="discovery-monitor-error" title={run.lastError}><Icon name="alert" size={13} /></span>}
              <button className="icon-btn" title={t('read.monitorRefreshOne')} onClick={() => void refreshDiscoveryTargets({ force: true, targetKey: `subscription:${subscription.id}` })}><Icon name="refresh" size={13} /></button>
              <select value={subscription.cadence} onChange={(event) => setCadence(subscription.id, event.target.value as DiscoveryCadence)}>
                {CADENCES.map((entry) => <option key={entry} value={entry}>{cadenceLabel(entry, t)}</option>)}
              </select>
              <button className="icon-btn" title={t('read.monitorUnsubscribe')} onClick={() => removeSubscription(subscription.id)}><Icon name="trash" size={13} /></button>
            </div>
          );
        })}
      </section>
    </div>
  );
}

function storedCollection(): string {
  try { return localStorage.getItem('termipod.discover.forYouCollection') ?? ''; } catch { return ''; }
}

export function DiscoveryForYouPanel({
  onAdd,
  onInspect,
}: {
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const references = useLibrary((state) => state.references);
  const collections = useLibrary((state) => state.collections);
  const [collectionId, setCollectionId] = useState(storedCollection);
  const [results, setResults] = useState<DiscoveryPaper[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const seed = useMemo(() => buildRecommendationSeed(collectionId, collections, references), [collectionId, collections, references]);

  async function refresh(): Promise<void> {
    if (seed === null || seed.query === '') return;
    setBusy(true);
    setError(null);
    try {
      const existing = new Set(references.map((reference) => paperFingerprint({
        paperId: reference.externalId ?? '', doi: reference.doi, title: reference.title, year: reference.year,
      })));
      const papers = (await sourceById('openalex').search(seed.query, 40))
        .map((paper) => ({ ...paper, source: 'openalex' as const }))
        .filter((paper) => !existing.has(paperFingerprint(paper)))
        .slice(0, 25);
      setResults(papers);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setResults([]);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="discovery-workspace-pane">
      <header className="discovery-workspace-header">
        <div><h2>{t('read.monitorForYou')}</h2><p>{t('read.monitorForYouHint')}</p></div>
        <button className="primary" disabled={busy || seed === null || seed.query === ''} onClick={() => void refresh()}>
          <Icon name="refresh" size={14} /> {busy ? t('read.monitorRefreshing') : t('read.monitorRecommend')}
        </button>
      </header>
      <div className="discovery-recommend-seed">
        <label>
          <span>{t('read.monitorSeedCollection')}</span>
          <select value={collectionId} onChange={(event) => {
            setCollectionId(event.target.value);
            setResults([]);
            try { localStorage.setItem('termipod.discover.forYouCollection', event.target.value); } catch { /* ignore */ }
          }}>
            <option value="">{t('read.monitorChooseCollection')}</option>
            {collections.map((collection) => <option key={collection.id} value={collection.id}>{collection.name}</option>)}
          </select>
        </label>
        {seed !== null && (
          <div className="discovery-recommend-explain">
            <span>{t('read.monitorSeededBy').replace('{n}', String(seed.references.length))}</span>
            <div>{seed.terms.map((term) => <span key={term} className="pill">{term}</span>)}</div>
          </div>
        )}
      </div>
      {error !== null && <div className="error region-pad">{error}</div>}
      <div className="discover-results scroll">
        {collectionId === '' ? (
          <div className="discovery-workspace-empty"><Icon name="folder" size={24} /><strong>{t('read.monitorChooseCollection')}</strong><span>{t('read.monitorChooseCollectionHint')}</span></div>
        ) : seed?.references.length === 0 ? (
          <div className="discovery-workspace-empty"><Icon name="book" size={24} /><strong>{t('read.monitorEmptyCollection')}</strong><span>{t('read.monitorEmptyCollectionHint')}</span></div>
        ) : results.length === 0 && !busy ? (
          <div className="discovery-workspace-empty"><Icon name="star" size={24} /><strong>{t('read.monitorReadyRecommend')}</strong><span>{t('read.monitorReadyRecommendHint')}</span></div>
        ) : results.map((paper) => <WorkspacePaperCard key={paperFingerprint(paper)} paper={paper} onAdd={onAdd} onInspect={onInspect} />)}
      </div>
    </div>
  );
}
