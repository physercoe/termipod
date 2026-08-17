import { invoke } from '../bridge';
import { isShell } from '../platform';
import { getXBearerToken } from '../state/discoverySecrets';
import { proxyForConnection } from '../state/proxy';
import type {
  DiscoveryResult,
  DiscoverySocialPost,
  DiscoverySubscription,
  DiscoverySubscriptionGroup,
  DiscoverySubscriptionKind,
} from '../state/discoveryMonitorCore';
import { fetchDiscoveryFeed } from './rss';

type Row = Record<string, unknown>;

function row(value: unknown): Row {
  return value !== null && typeof value === 'object' ? value as Row : {};
}

function rows(value: unknown): Row[] {
  return Array.isArray(value) ? value.filter((entry): entry is Row => entry !== null && typeof entry === 'object') : [];
}

function str(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value.trim() : undefined;
}

function num(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function timestamp(value: unknown): number | undefined {
  const parsed = typeof value === 'string' ? Date.parse(value) : NaN;
  return Number.isFinite(parsed) ? parsed : undefined;
}

function plainText(value: string): string {
  return value
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<[^>]+>/g, '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    .trim();
}

function blueskyPosts(payload: unknown): DiscoverySocialPost[] {
  return rows(row(payload).feed).flatMap((entry) => {
    const post = row(entry.post);
    const author = row(post.author);
    const record = row(post.record);
    const id = str(post.uri);
    const text = str(record.text);
    const handle = str(author.handle);
    if (id === undefined || text === undefined || handle === undefined) return [];
    const rkey = id.split('/').at(-1) ?? '';
    return [{
      id,
      platform: 'bluesky' as const,
      author: str(author.displayName) ?? handle,
      handle: `@${handle}`,
      text,
      url: `https://bsky.app/profile/${handle}/post/${rkey}`,
      publishedAt: timestamp(record.createdAt),
      language: Array.isArray(record.langs) ? str(record.langs[0]) : undefined,
      avatarUrl: str(author.avatar),
      engagement: { likes: num(post.likeCount), reposts: num(post.repostCount), replies: num(post.replyCount) },
    }];
  });
}

function mastodonPosts(payload: unknown): DiscoverySocialPost[] {
  const root = row(payload);
  const defaultAccount = row(root.account);
  return rows(root.statuses).flatMap((status) => {
    const account = Object.keys(row(status.account)).length > 0 ? row(status.account) : defaultAccount;
    const id = str(status.id);
    const url = str(status.url);
    const content = str(status.content);
    if (id === undefined || url === undefined || content === undefined) return [];
    const handle = str(account.acct);
    return [{
      id,
      platform: 'mastodon' as const,
      author: str(account.display_name) ?? handle ?? 'Mastodon',
      handle: handle === undefined ? undefined : `@${handle}`,
      text: plainText(content),
      url,
      publishedAt: timestamp(status.created_at),
      language: str(status.language),
      avatarUrl: str(account.avatar_static) ?? str(account.avatar),
      tags: rows(status.tags).map((tag) => str(tag.name)).filter((tag): tag is string => tag !== undefined),
      engagement: { likes: num(status.favourites_count), reposts: num(status.reblogs_count), replies: num(status.replies_count) },
    }];
  });
}

function xPosts(payload: unknown): DiscoverySocialPost[] {
  const root = row(payload);
  const users = new Map(rows(row(root.includes).users).map((user) => [str(user.id) ?? '', user]));
  return rows(root.data).flatMap((post) => {
    const id = str(post.id);
    const text = str(post.text);
    const author = users.get(str(post.author_id) ?? '');
    const username = str(author?.username);
    if (id === undefined || text === undefined || username === undefined) return [];
    const metrics = row(post.public_metrics);
    const hashtags = rows(row(post.entities).hashtags).map((tag) => str(tag.tag)).filter((tag): tag is string => tag !== undefined);
    return [{
      id,
      platform: 'x' as const,
      author: str(author?.name) ?? username,
      handle: `@${username}`,
      text,
      url: `https://x.com/${username}/status/${id}`,
      publishedAt: timestamp(post.created_at),
      language: str(post.lang),
      avatarUrl: str(author?.profile_image_url),
      tags: hashtags,
      engagement: {
        likes: num(metrics.like_count),
        reposts: num(metrics.retweet_count),
        replies: num(metrics.reply_count),
      },
    }];
  });
}

function terms(value: string | undefined): string[] {
  return (value ?? '').split(',').map((entry) => entry.trim().toLocaleLowerCase()).filter(Boolean);
}

function engagement(post: DiscoverySocialPost): number {
  return (post.engagement?.likes ?? 0) + (post.engagement?.reposts ?? 0) + (post.engagement?.replies ?? 0);
}

function filterPosts(posts: DiscoverySocialPost[], subscription: DiscoverySubscription): DiscoverySocialPost[] {
  const include = terms(subscription.filters?.include);
  const exclude = terms(subscription.filters?.exclude);
  const language = subscription.filters?.language?.trim().toLocaleLowerCase() ?? '';
  const minimum = subscription.filters?.minEngagement ?? 0;
  return posts.filter((post) => {
    const content = `${post.title ?? ''} ${post.text} ${(post.tags ?? []).join(' ')}`.toLocaleLowerCase();
    return (include.length === 0 || include.some((term) => content.includes(term))) &&
      !exclude.some((term) => content.includes(term)) &&
      (language === '' || post.language?.toLocaleLowerCase().startsWith(language) === true) &&
      engagement(post) >= minimum;
  });
}

export function subscriptionGroup(kind: DiscoverySubscriptionKind): DiscoverySubscriptionGroup {
  if (kind === 'rss' || kind === 'bluesky-feed') return 'feeds';
  if (kind === 'bluesky-query' || kind === 'mastodon-tag' || kind === 'x-query') return 'monitors';
  if (kind === 'bluesky-author' || kind === 'mastodon-author' || kind === 'youtube-channel' || kind === 'x-author') return 'social';
  return 'research';
}

function youtubeFeedUrl(value: string): string {
  const trimmed = value.trim();
  const match = trimmed.match(/(?:youtube\.com\/channel\/)?(UC[\w-]{20,})/i);
  const channelId = match?.[1] ?? trimmed;
  if (!/^UC[\w-]{20,}$/i.test(channelId)) throw new Error('youtube-channel-id-required');
  return `https://www.youtube.com/feeds/videos.xml?channel_id=${encodeURIComponent(channelId)}`;
}

export async function fetchSocialSubscription(subscription: DiscoverySubscription): Promise<DiscoveryResult[]> {
  if (subscription.kind === 'youtube-channel') {
    const feed = await fetchDiscoveryFeed(youtubeFeedUrl(subscription.value));
    const posts: DiscoverySocialPost[] = feed.papers.map((paper) => ({
      id: paper.paperId,
      platform: 'youtube',
      author: paper.authors[0] ?? feed.title,
      title: paper.title,
      text: paper.abstract ?? '',
      url: paper.url ?? paper.paperId,
      publishedAt: paper.publishedAt,
    }));
    return filterPosts(posts, subscription).map((social) => ({ social }));
  }
  if (!isShell()) throw new Error('social-connectors-shell-required');
  const needsX = subscription.kind === 'x-author' || subscription.kind === 'x-query';
  const apiKey = needsX ? await getXBearerToken() : '';
  if (needsX && apiKey === '') throw new Error('x-needs-key');
  const payload = await invoke<unknown>('discovery_social_fetch', {
    provider: subscription.kind,
    value: subscription.value,
    language: subscription.filters?.language ?? '',
    excludeReposts: subscription.filters?.excludeReposts ?? true,
    apiKey,
    proxy: proxyForConnection('discovery') ?? null,
  });
  const posts = subscription.kind.startsWith('bluesky-')
    ? blueskyPosts(payload)
    : subscription.kind.startsWith('mastodon-') ? mastodonPosts(payload) : xPosts(payload);
  return filterPosts(posts, subscription).map((social) => ({ social }));
}

export const SOCIAL_KINDS = new Set<DiscoverySubscriptionKind>([
  'bluesky-author', 'bluesky-feed', 'bluesky-query', 'mastodon-author', 'mastodon-tag',
  'youtube-channel', 'x-author', 'x-query',
]);
