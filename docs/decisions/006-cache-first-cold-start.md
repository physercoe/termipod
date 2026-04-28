# 006. Mobile renders cached snapshots before network

> **Type:** decision
> **Status:** Accepted (2026-04-27, shipped v1.0.304)
> **Audience:** contributors
> **Last verified vs code:** v1.0.316

**TL;DR.** On cold start, the mobile app reads each dashboard
endpoint's last on-disk snapshot synchronously and renders the UI
from cache. Network refresh runs on a microtask after `build()`
returns; only fresh data triggers a state update.

## Context

Pre-v1.0.303, `HubNotifier.build()` → `_loadConfig()` restored saved
credentials but never triggered `refreshAll()`. Only the bootstrap
wizard's `saveConfig()` did. Symptom: open the freshly installed APK
or relaunch with stored credentials, Activity has content (its
provider fetches independently), but Projects / Me / Hosts / Agents
are empty until the user pulls to refresh.

v1.0.303 fixed the missing refresh by scheduling
`Future.microtask(refreshAll)`. But that still meant the UI was empty
during the network roundtrip — even though the SQLite cache (live
since v1.0.208) had the answer locally in microseconds.

The user's framing was direct: *"the app should load cache first then
the app fetch the hub to refresh the cache and only new data has
arrived the cache then the UI read them to refresh."*

## Decision

`HubNotifier._loadConfig` opens the SQLite cache and reads each of
the six dashboard list endpoints' last snapshots in parallel via
`cache.get(hubKey, endpoint)`. The returned `HubState` has
`projects`, `attention`, `hosts`, `agents`, `templates`, `spawns`
populated from cache, with `staleSince` set to the oldest fetchedAt
across hits.

Endpoint keys exactly mirror `HubClient.list*Cached()`'s use of
`buildEndpointKey()` so the cache rows written by `readThrough` land
on the right reads (e.g., `/v1/teams/$t/attention?status=open`).

The microtask-scheduled `refreshAll` runs after build() returns and
overwrites with fresh data. Refresh's existing `clearStale` logic
clears the stale-from-hydration marker once each endpoint succeeds.

Coverage extended in v1.0.305 to the six remaining hot-path
single-resource fetches (`getAgent`, `getRun`, `getPlan` +
`listPlanSteps`, `getReview`, `listAgentFamilies`) — each detail
screen serves its last-known body from cache the instant it opens.

## Consequences

- Cold start feels instant on every relaunch.
- Offline open works for everything that's been viewed before
  (existing readThrough fallback continues to handle network failures
  by serving the cache with a `staleSince` indicator).
- Six extra `cache.get` calls during `_loadConfig` — sub-millisecond
  on local SQLite, well under the splash budget.
- True fresh installs (no SQLite rows yet) still see empty until
  network resolves; that's correct behavior.
- Bootstrap `saveConfig` path unchanged — there's no cache to
  hydrate from on first-ever setup.

## References

- Code: `lib/providers/hub_provider.dart` `_hydrateFromCache`,
  `lib/services/hub/hub_client.dart` `*Cached` family,
  `lib/services/hub/hub_read_through.dart`
- Plan: cache-first cold start (shipped v1.0.303–305)
- Storage rule: `feedback_storage_layering` memory — prefs = config,
  secure = secrets, SQLite cache = mutable server content
- Related earlier work: HubSnapshotCache (v1.0.208)
