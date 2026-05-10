# Shared agent_events data layer (Option B from compact-vs-duplicate review)

> **Type:** plan
> **Status:** P1 partially shipped (provider infrastructure landed v1.0.477→fixed v1.0.478; overlay migration deferred); P2 bundles overlay + AgentFeed migration as a single post-MVP wedge
> **Audience:** contributors
> **Last verified vs code:** v1.0.478

**TL;DR.** Two surfaces (`agent_feed.dart` for Sessions chat,
`steward_overlay_controller.dart` for the floating panel)
maintain independent SSE subscriptions, backfill HTTP calls,
in-memory event lists, and reconnect logic for the **same**
`(agentId, sessionId)` keys. This plan extracts that lifecycle
into a single Riverpod-family provider (`agentEventsProvider`)
that both surfaces consume, plus future ones. **Status update
2026-05-10**: P1's provider infrastructure shipped in v1.0.478
(after a v1.0.477 build failure documented below). The overlay
migration that was originally bundled into P1 has been pulled
out and folded into P2 because Riverpod 3.x's lifecycle for
Notifier-side `ref.listen` against an async-resolved family
key needs a split-provider refactor that's the same shape as
the AgentFeed migration. Both deferred to post-MVP — there's
no demo-critical reason to do them under MVP pressure, and
bundling cuts the test-pass amortisation in half.

## Why now (and why phased)

The compact-mode rework (v1.0.476) addressed the user-visible
*content* duplication — the overlay no longer renders a parallel
transcript. What remains is *infrastructure* duplication:

- Two SSE streams to the same agent's bus when both surfaces are
  open (small bandwidth cost, real architectural cost — any
  protocol or event-shape change has to be made twice).
- Two backfill round-trips on cold open.
- Two in-memory event caches that can drift on reconnect.
- One surface (`agent_feed`) has a full reconnect-backoff state
  machine; the other (`overlay`) has none — silent disconnects
  can leave the overlay stale until app restart.

`agent_feed.dart` is 4803 LOC and the primary chat surface;
extracting its lifecycle is real risk to the demo arc. The
overlay is the secondary surface and a natural first migrant.
If P1's provider design holds up under that lighter use, P2
gets to migrate `agent_feed` from a known-good base. If P1
exposes design flaws, fixing them before touching the primary
surface is much cheaper.

## Goals

After this plan completes:

1. One `Stream<Map<String, dynamic>>` SSE subscription per
   `(agentId, sessionId)` key, regardless of how many UI
   surfaces consume it.
2. One in-memory event ring buffer per key.
3. One reconnect path with backoff (so the overlay finally
   benefits from the same recovery `agent_feed` already has).
4. Both `agent_feed.dart` and the overlay controller become
   pure consumers — no direct calls to `streamAgentEvents` /
   `listAgentEventsCached` / `listAgentEventsCacheOnly`.
5. Future consumers (project-detail summaries, attention
   context, session previews) can subscribe via one line of
   Riverpod without rebuilding the lifecycle code.

## Non-goals

- **Unifying the rendering paths.** `agent_feed`'s full-fidelity
  cards and the overlay's compact pills are *legitimately
  different views*. Sharing the data layer doesn't merge the
  rendering layer — that would be wrong.
- **Hub-side changes.** The hub already exposes the right
  primitives (REST + SSE). The plan is mobile-only.
- **Event persistence beyond what `HubSnapshotCache` already
  does.** Same snapshot cache; just consolidated readers.
- **Replacing the AgentFeed widget**. AgentFeed remains the
  rendering surface; only its `_State` lifecycle gets cleaved
  into the provider in P2.
- **A general "session state" provider.** This plan covers the
  agent_events stream specifically. `_maybeBackfillSessionInit`
  (one-shot session.init metadata fetch) and the mode/model
  picker payload reporting stay in `agent_feed.dart`'s State
  for P2 — they're UI-routing concerns, not raw event flow.

## Inventory of what's currently duplicated

Every entry below exists in BOTH `agent_feed.dart` and the
overlay controller today (or only in agent_feed, where the
overlay would benefit if it shared):

| Concern | `agent_feed.dart` | overlay controller |
|---|---|---|
| SSE subscribe | `_subscribe` line 660 | `_bootstrap` line 215 |
| Backfill cache-then-refresh | lines 561, 597 | line 254 |
| Cache-only first paint | line 561 | NOT IMPLEMENTED |
| Recent events ring buffer | `_events` list | none — demuxes immediately |
| Seq cursor management | `_maxSeq`, agent-id-filtered for resume | basic `_maxSeq` helper |
| Reconnect with backoff | `_reconnectTimer` line 305 + line 783 | NOT IMPLEMENTED |
| `staleSince` indicator | line 611 | line 233 |
| Older-history paging | `_loadOlder` + `_atHead` | NOT NEEDED |
| Dedup by event id | `_ids` set | NOT IMPLEMENTED |
| `session.init` one-shot backfill | `_maybeBackfillSessionInit` | NOT NEEDED |

Things that legitimately stay per-consumer:
- Rendering (full-fidelity cards vs compact pills)
- Scroll position + `initialSeq` jump
- Mode/model + session.init payload **reporting** to parent (UI concern)
- Demuxing (overlay's `_eventToMessage`)

## Architectural shape

### Provider definition

```dart
// lib/providers/agent_events_provider.dart

@immutable
class AgentEventsKey {
  final String agentId;
  final String? sessionId;
  const AgentEventsKey(this.agentId, this.sessionId);
  // explicit equals/hashCode so .family caches per pair
}

@immutable
class AgentEventsState {
  /// ASC by seq within the loaded window. May not start at seq=1
  /// if older history hasn't been paged in.
  final List<Map<String, dynamic>> events;
  /// True while initial backfill is in flight (cache may already
  /// be painted; `events` non-empty + `loading` true is valid
  /// when the cached frame landed but the network refresh hasn't).
  final bool loading;
  /// True once a `loadOlder` returned fewer rows than requested —
  /// no more older history exists on the hub side. AgentFeed's UI
  /// disables the "load older" pager once true.
  final bool atHead;
  /// Non-null when the live network is unreachable + we're serving
  /// cached or stale data. Drives the "Offline · last updated X"
  /// banner shared between consumers.
  final DateTime? staleSince;
  /// Non-null on a hard error (transport / 4xx during backfill).
  final String? error;
  /// `seq` cursor for the next live frame; used internally and
  /// exposed for surfaces that want to drive a separate widget
  /// (e.g. compact-overlay's text demuxer state).
  final int? maxSeq;
}

class AgentEventsNotifier
    extends FamilyNotifier<AgentEventsState, AgentEventsKey> {
  StreamSubscription<Map<String, dynamic>>? _sub;
  Timer? _reconnect;
  Set<String> _ids = {};

  @override
  AgentEventsState build(AgentEventsKey key) {
    ref.onDispose(_close);
    _bootstrap();   // fire-and-forget
    return const AgentEventsState(
      events: [], loading: true, atHead: false,
    );
  }

  Future<void> _bootstrap() async { /* cacheOnly + cached + subscribe */ }
  Future<void> loadOlder() async { /* paging — for AgentFeed P2 */ }
  Future<void> refresh() async { /* explicit reload */ }
  void _close() { _sub?.cancel(); _reconnect?.cancel(); }
}

final agentEventsProvider = NotifierProvider.autoDispose
    .family<AgentEventsNotifier, AgentEventsState, AgentEventsKey>(
  AgentEventsNotifier.new,
);
```

`autoDispose` matters: when no consumer watches the provider, the
SSE subscription closes. When the overlay's puck is collapsed
AND no Sessions chat is open, the steward stream closes and we
stop paying the bandwidth. That's correct behavior.

### Migration target shapes

#### Overlay (P1)

```dart
class StewardOverlayController extends Notifier<StewardOverlayState> {
  @override
  StewardOverlayState build() {
    // Listen via ref.listen on the shared provider once we know
    // (agentId, sessionId). Demuxer fires per new event.
    ref.listen<AgentEventsState>(
      agentEventsProvider(AgentEventsKey(agentId, sessionId)),
      (prev, next) => _ingestNew(prev, next),
    );
    return const StewardOverlayState();
  }
  // _eventToMessage / _intentToMessage / _hydrateFromEvents stay
  // — the demuxing is overlay-specific and that's the right
  // separation. They now operate on events from the shared
  // provider instead of from a private subscription.
}
```

The overlay controller becomes a thin demuxer atop the shared
provider. ~250 LOC drops out (the SSE+backfill code).

#### AgentFeed (P2)

```dart
class _AgentFeedState extends ConsumerState<AgentFeed> {
  @override
  Widget build(BuildContext context) {
    final key = AgentEventsKey(widget.agentId, widget.sessionId);
    final s = ref.watch(agentEventsProvider(key));
    // s.events / s.loading / s.atHead / s.staleSince / s.error
    // drive the existing rendering. _maxSeq, _ids, _reconnectTimer,
    // _events all DELETED — provider owns them.
  }

  void _onLoadOlder() => ref
      .read(agentEventsProvider(key).notifier)
      .loadOlder();
}
```

`_maybeBackfillSessionInit` and the mode/model picker reporting
stay in AgentFeed's State (they're UI-routing concerns, not
raw event flow). `initialSeq` scroll-jump stays in AgentFeed
(scroll position is per-render-tree). The session.init backfill
might benefit from migrating later but P2 keeps it as-is to
narrow risk.

## Workband layout

### P1 — Provider infrastructure only (✅ shipped v1.0.478)

What landed:

- `lib/providers/agent_events_provider.dart` —
  `NotifierProvider.autoDispose.family<AgentEventsNotifier,
  AgentEventsState, AgentEventsKey>` with all the lifecycle
  primitives the plan specified:
  - `_bootstrap`: cache-only first paint → cache-then-refresh →
    subscribe with `sinceSeq`
  - Reconnect with exponential backoff (1s → 16s capped)
  - Idle-drop signature suppression matching `agent_feed.dart`'s
    pattern
  - Dedup by event id with bounded ring buffer (200)
  - `staleSince` / `error` state surfaces
  - `loadOlder()` stub (post-MVP P2 needs it for AgentFeed paging)
  - `refresh()` for explicit reload
  - `autoDispose` semantics

The provider has **no callers in v1.0.478** — it sits as
infrastructure ready for P2's consumers. This is intentional;
see "v1.0.477 lesson" below.

### v1.0.477 lesson — overlay migration pulled into P2

The original P1 W1.2 migrated the steward overlay controller to
consume the shared provider via `ref.listenManual`. v1.0.477
shipped this and the Android release build failed
`flutter analyze` with:

> error • The method 'listenManual' isn't defined for the type
> 'Ref' • lib/widgets/steward_overlay/steward_overlay_controller.dart

**Root cause.** Riverpod 3.x's Notifier-side `Ref` does not
expose `listenManual` — that method only exists on `WidgetRef`
(used inside `ConsumerStatefulWidget`). Calling `ref.listen`
inside `Notifier.build()` is fine, but the overlay's family key
is async-resolved (`(agentId, sessionId)` come from
`ensureGeneralSteward()` + sessions refresh), so build-time
listening with a stable key isn't possible without a
split-provider refactor:

- Provider A — `FutureProvider.autoDispose` resolving
  `(agentId, sessionId)` for the team-general steward.
- Provider B — `Notifier.autoDispose.family<AgentEventsKey>`
  that takes the resolved key as its family arg, watches
  `agentEventsProvider(key)` in build via `ref.watch`, and
  exposes the demuxed messages as state.

That's a different shape than v1.0.477 attempted. It's also
the **same shape** AgentFeed needs in P2 (its `(agentId,
sessionId)` are already provided synchronously by its
constructor, but the watch-via-build pattern is identical).
Bundling overlay + AgentFeed migrations under one wedge
amortises the test pass and keeps the split-provider design
consistent across both consumers.

v1.0.478 reverted the overlay migration. Overlay continues to
own its own SSE subscription + backfill + reconnect logic
(unchanged from v1.0.476). Provider sits ready for P2.

### P2 — Bundled overlay + AgentFeed migration (~750 LOC modified, ~10–15 days, post-MVP)

This phase is genuinely optional pre-MVP. Only commit to it
once the demo arc is settled and there's bandwidth for primary-
surface refactor risk.

#### W2.1 — Split-provider scaffolding

Foundational shape both consumers will use:

- **`stewardSubjectProvider`** — `FutureProvider.autoDispose`
  resolving `(agentId, sessionId)` for the team-general
  steward via `ensureGeneralSteward()` + sessions refresh.
  Replaces the overlay's `_bootstrap` async resolution.
- **`agentEventsMessagesProvider`** —
  `Notifier.autoDispose.family<AgentEventsKey>` that takes the
  resolved key as family arg, watches
  `agentEventsProvider(key)` in build via `ref.watch`, and
  exposes the demuxed messages as state. The overlay's
  `_eventToMessage` / `_intentToMessage` move here as the
  demuxer.

Building this split first proves the lifecycle shape on the
overlay (the lower-risk consumer) before touching AgentFeed.

#### W2.2 — Migrate overlay to the split-provider shape

- `StewardOverlayController` becomes a thin Notifier that
  - reads `stewardSubjectProvider`
  - watches `agentEventsMessagesProvider(key)` once subject
    resolves
  - keeps only `sendUserText` + the live-`mobile.intent`
    dispatch (which still fires snackbar + navigation as a
    side effect)
- All SSE / backfill / reconnect / dedup code deleted (the
  shared `agentEventsProvider` already does it).
- `StewardOverlayChat` UI continues to consume the overlay
  controller's state; no UI surface changes.

Net code drop in overlay: ~250 LOC. **Net win for the overlay
even before AgentFeed migrates**: cache-only first paint and
reconnect-with-backoff (capabilities the overlay currently
lacks).

#### W2.3 — Add `loadOlder` to provider

- Implements the older-history pagination AgentFeed needs
- Page size = 200 (matches AgentFeed's `_pageSize`)
- Sets `atHead = true` when fewer rows than requested return
- Prepends to `events` list, recomputes `maxSeq`
- Test in isolation (no UI) before W2.4 lands.

#### W2.4 — Replace AgentFeed lifecycle with provider consumption

- Delete `_events`, `_ids`, `_maxSeq`, `_reconnectTimer`,
  `_staleSince`, `_atHead`, `_loading`, `_error` State fields
- Replace `_open` / `_subscribe` / `_loadOlder` private methods
  with provider calls
- Preserve `_maybeBackfillSessionInit` as a one-shot in State
  (UI concern; future cleanup if needed)
- Preserve `initialSeq` scroll-jump (UI concern)
- Preserve mode/model picker reporting (UI concern)
- Preserve agent-id-filtered seq cursor (this gets folded into
  the provider's `loadOlder` cursor handling)

#### W2.5 — Test coverage before merge

This is the gating risk. AgentFeed has subtle behaviors that
QA needs to verify post-refactor:

- Cold-open with empty cache (loading state)
- Cold-open with full cache (cache-only paint then refresh)
- Cold-open while offline (cache-only + staleSince banner)
- Open session with `initialSeq` from attention deep-link
- Live event ingestion + scroll-to-tail
- Live event when scrolled up — should NOT auto-scroll
- `loadOlder` pager — keeps scroll position
- `loadOlder` returns 0 — sets `atHead`, hides pager
- SSE drop + reconnect — events resume from `sinceSeq`
- Resume case (new agent on same session) — seq cursor ignores
  prior agent's higher seqs
- session.init backfill fires when in-scope feed lacks one

Plus the overlay-specific QA from the original W1.3:

- Overlay cold-open paints from cache instantly (something it
  doesn't do today)
- Overlay survives an SSE reconnect (today silent disconnects
  leave the panel stale until app restart)
- Overlay + AgentFeed open simultaneously share ONE SSE stream
  (verifiable via hub logs: only one bus subscriber per agent)
- Closing both surfaces closes the subscription (autoDispose)

Each item gets a manual QA pass + ideally a widget test.

#### W2.6 — Ship P2

- One bundled commit + tag once all QA cases pass
- Roll back path: revert single commit; the provider stays in
  place but AgentFeed reverts to private subscriptions

### P3 — Future consumers (incremental)

Anywhere new code wants agent_events, it consumes the provider:

- Project-detail "recent activity" snippet (compact view of a
  session's last few events)
- Attention detail "context" section (transcript leading up to
  the request — currently fetched ad-hoc per attention)
- Session list "preview" line (last steward reply truncated)

Each is a small wedge; the shared provider amortizes the cost.

## Open questions for plan review

### Q1 — Recent-window size

The provider keeps a recent ring buffer per key. AgentFeed
needs 200 for cold-open + paging. Overlay needs ~20.

Options:
- **A.** Provider stores 200 always; overlay slices the tail
- **B.** Per-consumer hint — first watcher wins on size
- **C.** Provider stores everything that flowed through during
  this provider lifetime (no cap until autoDispose)

**Recommended A.** Simplest contract, predictable memory, agent_feed
behavior preserved. Overlay slices what it needs.

### Q2 — When to refresh on resubscribe?

When a consumer (re)mounts and the provider already has events
loaded from a prior consumer, do we:

- **A.** Trust the existing window + stream; no new fetch
- **B.** Always run a refresh (cache-then-network) on resubscribe
- **C.** Only refresh if `staleSince != null`

**Recommended C.** Respects the existing offline-banner contract
without burning network. AgentFeed today does roughly this.

### Q3 — `autoDispose` granularity

`autoDispose` per family key means each `(agentId, sessionId)`
disposes independently when no consumers watch. This is right
for the steward overlay (collapsed → no consumer → dispose) but
edge case: a brief navigation transition (close session chat,
open overlay 50ms later) tears down + rebuilds the subscription
each time.

Options:
- **A.** Plain autoDispose (ref counting)
- **B.** `keepAlive` for 5–10s after last consumer drops
- **C.** Manual lifecycle via `ref.keepAlive()` inside the notifier
   based on a heuristic

**Recommended A** for P1; revisit if churn is observed.

### Q4 — Phase 2 timing

P2 is high-risk for demo-critical code. Three timing options:

- **A.** Schedule P2 immediately after P1 lands
- **B.** Hold P2 until post-MVP demo passes
- **C.** Hold P2 indefinitely; let agent_feed coexist with the
  provider until a forcing function arrives (e.g. a new feature
  needs deeper integration)

**Locked B (2026-05-10).** P1's provider infrastructure shipped
in v1.0.478. P2 (overlay + AgentFeed migration bundle) is
explicitly post-MVP. The principal pulled the overlay migration
out of P1 after the v1.0.477 build failure since the
split-provider refactor needed for the overlay is the same
shape AgentFeed needs — bundling them under one wedge cuts the
test pass in half.

### Q5 — Split into multiple files?

`AgentEventsKey` + `AgentEventsState` + `AgentEventsNotifier`
together would be ~400 LOC. Split:

- **A.** Single file (`agent_events_provider.dart`)
- **B.** Three files (key + state + notifier)
- **C.** Two files (provider + types in separate `agent_events_state.dart`)

**Recommended A.** Other providers in `lib/providers/` are
typically single-file (`hub_provider.dart` is 1000+ LOC); follows
local convention.

## Risks

- **Provider lifecycle bugs corrupt agent_feed in P2.** The
  reconnect / paging / dedup logic is subtle. Mitigation: P1
  proves the design under overlay's lighter use; P2's QA pass
  is heavy.
- **Hot-reload + provider state.** Today's widget-state
  pattern resets on hot-reload (clean slate). Provider state
  may persist or partial-reload depending on Riverpod version.
  Document expected dev workflow.
- **autoDispose churn during navigation.** See Q3. If observed
  in P1 with the overlay, switch to keepAlive before P2.
- **Memory pressure with multiple keys.** Each Sessions tab
  view potentially keeps a 200-event window alive. Bounded by
  active consumers + `autoDispose`. AgentFeed already keeps
  the same window today; provider doesn't make it worse.
- **Test infra gap.** The mobile codebase has limited automated
  tests for chat surfaces. Adding widget tests for AgentFeed
  pre-P2 is its own ~2 days of work; needs to happen first or
  P2 leans entirely on manual QA.

## Out of scope (explicitly)

- Render-layer unification.
- Hub-side multiplexing or any protocol change.
- New event kinds.
- Persistent message storage (offline-write queue, etc.).
- Cross-team event flow.

## Sizing

Updated 2026-05-10 to reflect the v1.0.477 lesson: the original
P1 W1.2 overlay-migration LOC moves into P2 (now W2.2) since it
needs the same split-provider shape AgentFeed needs in W2.4.

| Phase | LOC delta | Risk | Days | Status |
|---|---|---|---|---|
| P1 provider skeleton | +463 | low | shipped | ✅ v1.0.478 |
| P2 W2.1 split-provider scaffolding | +200 | medium | 2 | post-MVP |
| P2 W2.2 overlay migration | -250 / +120 | medium | 2 | post-MVP |
| P2 W2.3 loadOlder on provider | +100 | low | 1 | post-MVP |
| P2 W2.4 agent_feed migration | -400 / +200 | high | 5-8 | post-MVP |
| P2 W2.5 widget tests + QA | +200 | high | 3-5 | post-MVP |
| P2 W2.6 ship | 0 | low | 0.5 | post-MVP |
| **P2 total** | **~+170 net** | **high** | **~13-18** | **post-MVP** |
| P3 per consumer | +30-80 | low | 0.5 each | n/a |

P1's net code add (+463 LOC for the provider, no callers in
v1.0.478) is the cost of doing the infrastructure first. P2
amortizes both consumer migrations against one test pass and
one design lock; building it at the same time is cheaper than
shipping the overlay migration alone now and AgentFeed later.

## Recommended path

1. **Pre-MVP:** ✅ done — P1 provider infrastructure shipped in
   v1.0.478. Sits ready for consumers; no caller yet.
2. **Post-MVP:** P2 bundles the overlay + AgentFeed migrations
   under one wedge with the split-provider refactor. Build
   widget tests for AgentFeed FIRST — the chat surface is too
   important to refactor without coverage. Then W2.1 → W2.2 in
   series (overlay migrates first, lower-risk consumer); then
   W2.3 → W2.4 (loadOlder + AgentFeed) in series; then W2.5
   QA; then W2.6 ship.
3. **As-needed:** P3 consumers (project-detail recent-activity,
   attention-detail context, session-list previews) plug in
   incrementally once P2 lands.

## Concrete next step

Resolved 2026-05-10. P1 shipped in v1.0.478. P2 deferred to
post-MVP per the principal. Next concrete action when P2
begins: scaffold the split-provider pair (`stewardSubjectProvider`
+ `agentEventsMessagesProvider.family`) — that's W2.1 — and
prove the lifecycle on the overlay before touching AgentFeed.
