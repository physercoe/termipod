# Shared agent_events data layer (Option B from compact-vs-duplicate review)

> **Type:** plan
> **Status:** Open
> **Audience:** contributors
> **Last verified vs code:** v1.0.476

**TL;DR.** Today, two surfaces (`agent_feed.dart` for Sessions
chat, `steward_overlay_controller.dart` for the floating panel)
maintain independent SSE subscriptions, backfill HTTP calls,
in-memory event lists, and reconnect logic for the **same**
`(agentId, sessionId)` keys. This plan extracts that lifecycle
into a single Riverpod-family provider (`agentEventsProvider`)
that both surfaces consume, plus future ones. Phased: **P1
builds the provider + migrates the overlay only** (low-risk);
**P2 migrates `agent_feed.dart`** (high-risk, primary-surface,
genuinely optional pre-MVP); **P3 onboards future consumers**.
Each phase is independently shippable.

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

### P1 — Build provider + migrate overlay (~400 + ~150 LOC, 3-5 days)

#### W1.1 — `agentEventsProvider` skeleton

- `AgentEventsKey`, `AgentEventsState`, `AgentEventsNotifier`
- `_bootstrap`: cache-only first paint → cache-then-refresh →
  subscribe with `sinceSeq`
- Reconnect with backoff (5s → 10s → 30s capped)
- Dedup by event id
- `staleSince` from `CachedResponse`
- `loadOlder` stub (returns immediately for P1; AgentFeed needs
  it in P2)
- `refresh` clears `staleSince` and re-fetches

Tests: provider state transitions in isolation (no UI). Stub
HubClient with a fake stream + fake cached responses.

#### W1.2 — Overlay migration

- `StewardOverlayController._bootstrap` → ensures steward agent
  + session, then waits one frame and starts watching
  `agentEventsProvider(AgentEventsKey(agentId, sessionId))`
- Drop `_sub`, `_initStarted` is the only init flag still needed
- `_handleEvent` / `_dispatchIntentLive` become reactions to
  new events from the provider listener
- Backfill logic deleted — the provider does it
- Reconnect logic deleted — the provider does it (overlay
  finally gets reconnect for free)

Tests: overlay still displays the right messages on cold open
+ live updates. Run the same QA matrix that v1.0.476 passes.

#### W1.3 — Acceptance for P1

- Overlay cold-open paints from cache instantly (something it
  *didn't* do before — this is a P1 net-add, not just a refactor)
- Overlay survives an SSE reconnect (today it doesn't, silently)
- AgentFeed and overlay open simultaneously share ONE SSE
  stream (verifiable via hub logs: only one bus subscriber per
  agent)
- Closing both surfaces closes the subscription (autoDispose)

P1 ships as a minor (`v1.0.NNN`) when complete.

### P2 — Migrate AgentFeed (~600 LOC modified, 1-2 weeks)

This phase is genuinely optional pre-MVP. Only commit to it
once P1 has bedded in for at least a week of demo use.

#### W2.1 — Add `loadOlder` to provider

- Implements the older-history pagination AgentFeed needs
- Page size = 200 (matches AgentFeed's `_pageSize`)
- Sets `atHead = true` when fewer rows than requested return
- Prepends to `events` list, recomputes `maxSeq`

#### W2.2 — Replace AgentFeed lifecycle with provider consumption

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

#### W2.3 — Test coverage before merge

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

Each item gets a manual QA pass + ideally a widget test.

#### W2.4 — Ship P2

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

**Recommended B.** P1 is justified on its own merits (overlay
gets reconnect + cache-paint for free, plus shared subscription).
P2 is a cleanup pass that doesn't affect demo capability.

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

| Phase | LOC delta | Risk | Days | Pre-MVP? |
|---|---|---|---|---|
| P1 W1.1 provider skeleton | +400 | low | 2 | yes |
| P1 W1.2 overlay migration | -250 / +150 | low | 1 | yes |
| P1 W1.3 acceptance | 0 | low | 0.5 | yes |
| **P1 total** | **~+300 net** | **low** | **~3-5** | **yes** |
| P2 W2.1 loadOlder | +100 | low | 1 | optional |
| P2 W2.2 agent_feed migration | -400 / +200 | high | 5-8 | optional |
| P2 W2.3 QA | 0 | high | 3-5 | optional |
| **P2 total** | **~-100 net** | **high** | **~10-15** | **optional** |
| P3 per consumer | +30-80 | low | 0.5 each | n/a |

Gross numbers favor P1 on its own — net code shrinks slightly
(provider adds; overlay sheds), the overlay gains capabilities it
didn't have (cache-paint + reconnect), and the shared
infrastructure is in place for any future consumer. P2 is a
real refactor with real risk; only worth doing once the
provider has been proven by P1 and the demo arc has cleared.

## Recommended path

1. **Pre-MVP:** ship P1 only. Net win: shared SSE subscription
   when both surfaces open, overlay gets reconnect + cache-paint,
   future consumers have a clean home. Ships in ~3-5 days.
2. **Post-MVP:** decide on P2 based on whether the duplication
   has become an actual maintenance pain point (event-shape
   change requiring two edits, divergence between surfaces, etc.).
3. **As-needed:** add P3 consumers when they show up.

If P2 ever happens, build widget tests first — the chat surface
is too important to refactor without coverage.

## Concrete next step

Open the question with the principal: "Does the P1-only path
match your intent for 'plan Option B carefully'? Or should I
plan for P1 + P2 immediately?" The plan answers either way; the
phasing decision is yours.
