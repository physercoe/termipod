import { useMemo, useState } from 'react';
import { sourceById, type DiscoveryPaper, type DiscoverySourceId } from '../discovery';
import { subscriptionGroup } from '../discovery/social';
import { useT } from '../i18n';
import { useDiscoveryHistory } from '../state/discoveryHistory';
import {
  refreshDiscoveryTargets,
  useDiscoveryMonitor,
} from '../state/discoveryMonitor';
import {
  buildRecommendationSeed,
  buildDiscoveryTrends,
  paperFingerprint,
  type DiscoveryCadence,
  type DiscoverySocialPost,
  type DiscoverySubscriptionGroup,
  type DiscoverySubscriptionKind,
  type DiscoveryUpdate,
} from '../state/discoveryMonitorCore';
import { useLibrary } from '../state/library';
import { Icon } from '../ui/Icon';
import { useOpenLink } from '../ui/OpenLinkContext';

export type DiscoveryWorkspaceView = 'explore' | 'updates' | 'subscriptions' | 'trends' | 'for-you';

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

function socialToPaper(post: DiscoverySocialPost): DiscoveryPaper {
  const title = post.title?.trim() || post.text.replace(/\s+/g, ' ').slice(0, 140);
  return {
    paperId: `social:${post.platform}:${post.id}`,
    title,
    authors: [post.author],
    year: post.publishedAt === undefined ? undefined : new Date(post.publishedAt).getUTCFullYear(),
    venue: post.platform === 'x' ? 'X' : post.platform[0]!.toLocaleUpperCase() + post.platform.slice(1),
    abstract: post.text,
    url: post.url,
  };
}

function WorkspaceSocialCard({
  update,
  onAdd,
  onInspect,
}: {
  update: DiscoveryUpdate & { social: DiscoverySocialPost };
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const openLink = useOpenLink();
  const markRead = useDiscoveryMonitor((state) => state.markRead);
  const removeUpdate = useDiscoveryMonitor((state) => state.removeUpdate);
  const setPaused = useDiscoveryMonitor((state) => state.setSubscriptionPaused);
  const post = update.social;
  const total = (post.engagement?.likes ?? 0) + (post.engagement?.reposts ?? 0) + (post.engagement?.replies ?? 0);
  return (
    <article className={`discover-card workspace-paper-card workspace-social-card${update.readAt === undefined ? ' unread' : ''}`}>
      <div className="workspace-paper-origin">
        <span className="workspace-platform">{post.platform === 'x' ? 'X' : post.platform}</span>
        <span>{update.originLabel}</span>
        <span className="spacer" />
        <span>{timeLabel(post.publishedAt ?? update.arrivedAt)}</span>
      </div>
      <div className="workspace-social-author">
        {post.avatarUrl !== undefined && <img src={post.avatarUrl} alt="" />}
        <strong>{post.author}</strong>
        {post.handle !== undefined && <span>{post.handle}</span>}
      </div>
      {post.title !== undefined && <div className="discover-card-title">{post.title}</div>}
      <div className="workspace-social-text">{post.text}</div>
      <div className="discover-card-meta muted small">
        {t('read.monitorWhyMatched').replace('{reason}', update.originLabel)}
        {total > 0 ? ` · ${total} ${t('read.monitorEngagement')}` : ''}
      </div>
      <div className="discover-card-actions">
        <button className="primary small" onClick={() => {
          markRead(update.id);
          onInspect(onAdd(socialToPaper(post)));
        }}><Icon name="plus" size={13} /> {t('read.monitorSave')}</button>
        <button className="small" onClick={() => openLink(post.url)}><Icon name="external" size={13} /> {t('read.monitorOpen')}</button>
        {update.readAt === undefined && <button className="small" onClick={() => markRead(update.id)}>{t('read.monitorMarkRead')}</button>}
        {update.originType === 'subscription' && (
          <button className="small" onClick={() => setPaused(update.originId, true)}>{t('read.monitorMuteSource')}</button>
        )}
        <button className="icon-btn small" title={t('read.removeSearch')} onClick={() => removeUpdate(update.id)}><Icon name="close" size={13} /></button>
      </div>
    </article>
  );
}

function WorkspaceUpdateCard({
  update,
  onAdd,
  onInspect,
}: {
  update: DiscoveryUpdate;
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  if (update.paper !== undefined) {
    return <WorkspacePaperCard paper={update.paper} update={update} onAdd={onAdd} onInspect={onInspect} />;
  }
  return <WorkspaceSocialCard update={update as DiscoveryUpdate & { social: DiscoverySocialPost }} onAdd={onAdd} onInspect={onInspect} />;
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
  const [contentType, setContentType] = useState<'all' | 'research' | 'social'>('all');
  const unread = updates.filter((entry) => entry.readAt === undefined).length;
  const shown = updates.filter((entry) =>
    (!unreadOnly || entry.readAt === undefined) &&
    (contentType === 'all' || (contentType === 'research' ? entry.paper !== undefined : entry.social !== undefined)),
  );
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
        <select value={contentType} aria-label={t('read.monitorContentType')} onChange={(event) => setContentType(event.target.value as typeof contentType)}>
          <option value="all">{t('read.monitorAllSources')}</option>
          <option value="research">{t('read.monitorResearch')}</option>
          <option value="social">{t('read.monitorSocial')}</option>
        </select>
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
        ) : shown.map((update) => <WorkspaceUpdateCard key={update.id} update={update} onAdd={onAdd} onInspect={onInspect} />)}
      </div>
    </div>
  );
}

const KINDS_BY_GROUP: Record<DiscoverySubscriptionGroup, DiscoverySubscriptionKind[]> = {
  research: ['author', 'journal', 'topic', 'citation'],
  social: ['bluesky-author', 'mastodon-author', 'youtube-channel', 'x-author'],
  monitors: ['bluesky-query', 'mastodon-tag', 'x-query'],
  feeds: ['rss', 'bluesky-feed'],
};
const GROUPS: DiscoverySubscriptionGroup[] = ['research', 'social', 'monitors', 'feeds'];
const CADENCES: DiscoveryCadence[] = ['daily', 'weekly', 'monthly'];

function subscriptionIcon(kind: DiscoverySubscriptionKind): 'globe' | 'link' | 'star' | 'search' | 'book' {
  if (kind === 'rss' || kind === 'youtube-channel' || kind === 'bluesky-feed') return 'globe';
  if (kind === 'citation') return 'link';
  if (kind.endsWith('-query') || kind === 'mastodon-tag' || kind === 'topic') return 'search';
  if (kind === 'journal') return 'book';
  return 'star';
}

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
  const setPaused = useDiscoveryMonitor((state) => state.setSubscriptionPaused);
  const [group, setGroup] = useState<DiscoverySubscriptionGroup>('research');
  const [kind, setKind] = useState<DiscoverySubscriptionKind>('topic');
  const [value, setValue] = useState('');
  const [label, setLabel] = useState('');
  const [sourceId, setSourceId] = useState<DiscoverySourceId>('openalex');
  const [cadence, setNewCadence] = useState<DiscoveryCadence>('weekly');
  const [referenceId, setReferenceId] = useState('');
  const [include, setInclude] = useState('');
  const [exclude, setExclude] = useState('');
  const [language, setLanguage] = useState('');
  const [minEngagement, setMinEngagement] = useState('0');
  const [excludeReposts, setExcludeReposts] = useState(true);

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

  function changeGroup(nextGroup: DiscoverySubscriptionGroup): void {
    setGroup(nextGroup);
    reset(KINDS_BY_GROUP[nextGroup][0]!);
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
    const social = group === 'social' || group === 'monitors';
    const id = addSubscription({
      kind,
      label: targetLabel,
      value: targetValue,
      sourceId: group === 'research' ? targetSource : undefined,
      cadence,
      referenceId: referenceId || undefined,
      filters: social ? {
        include: include.trim() || undefined,
        exclude: exclude.trim() || undefined,
        language: language.trim() || undefined,
        excludeReposts,
        minEngagement: Math.max(0, Number(minEngagement) || 0),
      } : undefined,
    });
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
        <div className="discovery-follow-groups seg compact">
          {GROUPS.map((entry) => (
            <button key={entry} className={group === entry ? 'seg-btn active' : 'seg-btn'} onClick={() => changeGroup(entry)}>
              {t(`read.monitorGroup.${entry}`)}
            </button>
          ))}
        </div>
        <div className="discovery-subscribe-kind seg compact">
          {KINDS_BY_GROUP[group].map((entry) => (
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
                placeholder={t(`read.monitorPlaceholder.${kind}`)}
                onChange={(event) => setValue(event.target.value)}
              />
              <datalist id="discovery-subscription-values">{suggestions.map((entry) => <option key={entry} value={entry} />)}</datalist>
            </>
          )}
          {(group !== 'research' || kind === 'rss') && <input value={label} placeholder={t('read.monitorOptionalName')} onChange={(event) => setLabel(event.target.value)} />}
          {group === 'research' && kind !== 'citation' && (
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
        {(group === 'social' || group === 'monitors') && (
          <details className="discovery-monitor-filters">
            <summary>{t('read.monitorFilters')}</summary>
            <div>
              <input value={include} placeholder={t('read.monitorInclude')} onChange={(event) => setInclude(event.target.value)} />
              <input value={exclude} placeholder={t('read.monitorExclude')} onChange={(event) => setExclude(event.target.value)} />
              <input value={language} placeholder={t('read.monitorLanguage')} onChange={(event) => setLanguage(event.target.value)} />
              <input type="number" min="0" value={minEngagement} aria-label={t('read.monitorMinEngagement')} placeholder={t('read.monitorMinEngagement')} onChange={(event) => setMinEngagement(event.target.value)} />
              <label><input type="checkbox" checked={excludeReposts} onChange={(event) => setExcludeReposts(event.target.checked)} /> {t('read.monitorExcludeReposts')}</label>
            </div>
          </details>
        )}
        {kind.startsWith('x-') && <div className="discovery-credential-hint"><Icon name="lock" size={13} /> {t('read.monitorXKeyHint')}</div>}
      </div>

      {group === 'monitors' && <section className="discovery-monitor-section">
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
      </section>}

      <section className="discovery-monitor-section grow">
        <h3>{t('read.monitorSubscriptions')}</h3>
        {subscriptions.filter((subscription) => subscriptionGroup(subscription.kind) === group).length === 0 ? <p className="muted small">{t('read.monitorNoSubscriptions')}</p> : subscriptions.filter((subscription) => subscriptionGroup(subscription.kind) === group).map((subscription) => {
          const run = runs[`subscription:${subscription.id}`];
          return (
            <div className="discovery-monitor-row" key={subscription.id}>
              <Icon name={subscriptionIcon(subscription.kind)} size={14} />
              <div className="discovery-monitor-row-main">
                <strong>{subscription.label}</strong>
                <span>{t(`read.monitorKind.${subscription.kind}`)} · {subscription.paused === true ? t('read.monitorPaused') : timeLabel(run?.lastRunAt)}</span>
              </div>
              {run?.lastError !== undefined && <span className="discovery-monitor-error" title={run.lastError === 'x-needs-key' ? t('read.monitorXKeyHint') : run.lastError}><Icon name="alert" size={13} /></span>}
              <button className={`icon-btn${subscription.paused === true ? '' : ' active'}`} title={subscription.paused === true ? t('read.monitorResume') : t('read.monitorPause')} onClick={() => setPaused(subscription.id, subscription.paused !== true)}><Icon name={subscription.paused === true ? 'play' : 'circle-half'} size={13} /></button>
              <button className="icon-btn" disabled={subscription.paused === true} title={t('read.monitorRefreshOne')} onClick={() => void refreshDiscoveryTargets({ force: true, targetKey: `subscription:${subscription.id}` })}><Icon name="refresh" size={13} /></button>
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

export function DiscoveryTrendsPanel({
  onAdd,
  onInspect,
}: {
  onAdd: (paper: DiscoveryPaper) => string;
  onInspect: (id: string) => void;
}): JSX.Element {
  const t = useT();
  const updates = useDiscoveryMonitor((state) => state.updates);
  const trends = useMemo(() => buildDiscoveryTrends(updates, Date.now()), [updates]);
  const [selected, setSelected] = useState<string | null>(null);
  const active = trends.find((trend) => trend.term === selected) ?? trends[0];
  const evidence = active === undefined ? [] : active.evidenceIds
    .map((id) => updates.find((update) => update.id === id))
    .filter((update): update is DiscoveryUpdate => update !== undefined);
  return (
    <div className="discovery-workspace-pane">
      <header className="discovery-workspace-header">
        <div><h2>{t('read.monitorTrends')}</h2><p>{t('read.monitorTrendsHint')}</p></div>
      </header>
      {trends.length === 0 ? (
        <div className="discovery-workspace-empty"><Icon name="crosshair" size={24} /><strong>{t('read.monitorNoTrends')}</strong><span>{t('read.monitorNoTrendsHint')}</span></div>
      ) : (
        <div className="discovery-trends-layout">
          <div className="discovery-trend-list scroll">
            {trends.map((trend) => {
              const max = Math.max(1, ...trend.days);
              return (
                <button key={trend.term} className={`discovery-trend-card${active?.term === trend.term ? ' active' : ''}`} onClick={() => setSelected(trend.term)}>
                  <span className="discovery-trend-name">{trend.term}</span>
                  <span className={`pill trend-${trend.confidence}`}>{t(`read.monitorConfidence.${trend.confidence}`)}</span>
                  <span className="discovery-trend-velocity">{trend.velocity.toFixed(1)}×</span>
                  <span className="discovery-trend-spark" aria-hidden="true">
                    {trend.days.map((value, index) => <i key={index} style={{ height: `${Math.max(2, value / max * 22)}px` }} />)}
                  </span>
                  <span className="muted small">{t('read.monitorTrendEvidence').replace('{items}', String(trend.evidenceIds.length)).replace('{authors}', String(trend.authors)).replace('{sources}', String(trend.sources))}</span>
                </button>
              );
            })}
          </div>
          <div className="discovery-trend-evidence scroll">
            <div className="discovery-workspace-toolbar">
              <strong>{active?.term}</strong>
              <span className="muted small">{t('read.monitorTrendDrilldown')}</span>
            </div>
            {evidence.map((update) => <WorkspaceUpdateCard key={update.id} update={update} onAdd={onAdd} onInspect={onInspect} />)}
          </div>
        </div>
      )}
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
