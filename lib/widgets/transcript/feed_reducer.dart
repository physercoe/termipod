// AgentFeed reducer — pure functions over the agent_events stream.
//
// Layer 0 of the agent_feed split (docs/plans/agent-feed-split.md, W0).
// Everything here is a free function with no widget/State dependency: the
// event-kind classification contract (`kAgentTurnActiveKinds`,
// `kAgentFeedAlwaysHiddenKinds`, `agentIsBusy`, `isHiddenInFeed`), the
// dedupe/replay keys, and the telemetry/cost/rate-limit/envelope
// formatters. Pinned by the ten `test/widgets/agent_feed_*` reducer
// tests, which import these symbols through `live_feed.dart`
// (re-exported there) so the split is behavior-preserving by construction.
//
// Keeping this layer pure is the point: the dispatch-classification logic
// was the site of repeated fails-open regressions (v1.0.667/699/717/
// 720/721) while it lived buried inside `_AgentFeedState`. A standalone,
// directly-tested module makes that class of bug structurally visible.
import 'package:flutter/material.dart';

import '../../theme/design_colors.dart';
import 'fold_maps.dart' show callToolIdOf;

/// True when the event payload carries the `replay: true` flag the M1
/// driver stamps on session/update notifications streamed inside a
/// `session/load` window (ADR-021 W1.2). Used by the feed's ingest
/// filter (W1.3) to drop frames whose content already appears in the
/// cached transcript so the user doesn't see every prior turn twice
/// after a resume.
bool agentEventIsReplay(Map<String, dynamic> evt) {
  final p = evt['payload'];
  if (p is! Map) return false;
  return p['replay'] == true;
}

/// Computes a content-stable dedupe key for an agent_event. The key
/// must be the same for a freshly-streamed replay frame and the
/// originally-streamed live frame, so we can identify equivalence
/// across agent_id and seq (those differ between the dead agent that
/// produced the original event and the resumed agent re-emitting it
/// during session/load replay). Returns null for events whose shape
/// has no stable identity — those are passed through (better duplicate
/// than dropped).
///
/// Keying by kind:
///   text / thought    → kind + length-prefixed text body. Length
///                       prefix prevents prefix-collision (turn 1's
///                       "hello" colliding with turn 2's "hello world"
///                       once both have grown).
///   tool_call         → kind + tool_call_id (agent-stable across
///                       restart for the same logical call).
///   tool_call_update  → kind + tool_call_id + status (status carries
///                       the lifecycle position so multiple updates
///                       per call don't collapse into one).
///   approval_request  → kind + request_id.
/// Event kinds that are ALWAYS hidden from the feed-bubble layer.
///
/// These are reducer-over-events signals — chips, telemetry strips,
/// AppBar headers consume them — rendering them as transcript bubbles
/// would double-count the same data with no extra signal.
///
/// - `session.init` — lives in the AppBar chip (model/version/cwd).
/// - `usage` — feeds the token/cost strip; per-message frame.
/// - `rate_limit` — drives the rate-limit row in the telemetry strip.
/// - `status_line` — claude-code statusLine snapshot (ADR-036 D4):
///   periodic snapshot, chip-source only, not a lifecycle event.
///   ~10s cadence; rendering as bubbles would also spam the
///   transcript with cold-open frames (~360 frames/hour).
/// - `turn.start` — the turn-boundary marker (ADR-038 §3). A
///   structural anchor the hub's turn index + analysis-mode position
///   bar consume; it carries no body, so it's never a transcript
///   bubble (its sibling `turn.result` is likewise non-bubble, handled
///   by the terminal short-circuit in `_isAgentBusy`).
///
/// Public so widget tests can assert membership without spinning the
/// full `_AgentFeedState` widget tree.
const kAgentFeedAlwaysHiddenKinds = <String>{
  'session.init',
  'usage',
  'rate_limit',
  'status_line',
  'turn.start',
};

/// Event kinds that DECISIVELY signal an in-flight turn.
///
/// **v1.0.721 — allowlist invert (`docs/discussions/consumer-side-
/// dispatch-contracts.md` Fix A).** Previously this contract was a
/// denylist (`kAgentBusyInferenceSkipKinds`) — every new pre-turn-
/// active event kind required a one-line addition or the busy pill
/// stuck on forever. The denylist took four hits in 48h before this
/// inversion landed:
///
///   - v1.0.667 — codex `usage` events at end-of-turn (wire order
///     `turn.result → text → usage`) sat as the LATEST event,
///     falling through to the "anything else is busy" branch.
///   - v1.0.699 — claude-code `status_line` cold-open frames fired
///     before any turn, sticking busy(cancel) until the first turn.
///   - v1.0.717 — codex `raw` events on resume (post-handshake
///     `thread/goal/cleared` etc. via the profile's forward-compat
///     catch-all, `agent_families.yaml:782-787`) sat as the latest
///     event on parked-after-resume conversations.
///   - The same class also bit consumer-side dispatch in v1.0.720
///     (chip-strip reducer; different shape — not solvable by
///     allowlist alone, see Fix B below).
///
/// The new contract: **default = idle.** A kind missing from this
/// allowlist appears idle even when the agent is busy — at worst,
/// the cancel button hides, the user sends another prompt, and the
/// next real text/tool_call event pushes inference back to busy
/// within a tick. That UX is strictly better than the pre-v1.0.721
/// inverse (cancel button shown but no turn to cancel → user taps,
/// nothing happens, user gives up).
///
/// The allowlist is small and stable (producer-side new kinds are
/// almost always telemetry); the denylist grew unboundedly as
/// every engine added telemetry channels.
///
/// Membership rationale per kind:
///   - `text` — assistant streaming output. Live mid-turn.
///   - `tool_call` — tool dispatched, waiting for result. Mid-turn.
///   - `thought` — codex/claude reasoning blocks emitted before the
///     final text. Mid-turn.
///   - `plan` — claude/agy plan-update frames; the agent is actively
///     re-thinking. Mid-turn.
///
/// Notably NOT here (and not in the explicit terminal-kinds switch
/// in `_isAgentBusy`):
///   - `tool_result` — sits between two `tool_call`s in a multi-tool
///     turn. By itself doesn't mean motion (the next `tool_call`
///     or `text` does). Keeping it out avoids a per-direction race
///     where the result lands after the next tool_call.
///   - `usage`, `rate_limit`, `status_line`, `raw`, `system` — pure
///     telemetry / forward-compat catch-all. Default-idle by the
///     allowlist inversion.
///   - `session.init`, `turn.result`, `completion`, `lifecycle` —
///     handled by explicit branches in `_isAgentBusy` (terminal,
///     short-circuit to idle).
///
/// **Adding a new kind that DOES signal busy:** add it here and
/// extend the contract test in
/// `test/widgets/agent_feed_kind_classification_test.dart`.
///
/// **Adding a new kind that does NOT signal busy:** add it to that
/// same test's `kKindsExplicitlyIgnoredByBusyInference` set with a
/// one-line rationale. The test fails on any unclassified kind, so
/// the next producer-side addition can't ship without a consumer-
/// side decision.
const kAgentTurnActiveKinds = <String>{
  'text',
  'tool_call',
  'tool_call_update', // ACP streaming variant — tool call is updating mid-flight
  'thought',
  'plan',
};

/// Rate-limits reducer (ADR-036 D7 — W5).
///
/// Walks the supplied event list newest-last and returns the latest
/// `status_line.payload.rate_limits` block, or null if no status_line
/// frame has carried one yet (cold-open race; older claude versions
/// without statusLine; operator removed the install).
///
/// Latest-wins: each statusLine frame ships a fresh snapshot of the
/// rolling-window state (it's an instantaneous percentage, not a
/// delta), so we just take the most recent.
///
/// Shape returned (verbatim from the wire — per ADR-036 D7 §payload
/// example):
///
///     { "five_hour": {"used_percentage": int, "resets_at": int (epoch s)},
///       "seven_day":{"used_percentage": int, "resets_at": int (epoch s)} }
///
/// Either sub-block may be absent on a given frame; callers must
/// null-check each before rendering.
///
/// Public so widget tests can exercise the reducer without spinning
/// the full agent_feed widget tree.
Map<String, dynamic>? rateLimitsFromEvents(List<Map<String, dynamic>> events) {
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if ((e['kind'] ?? '').toString() != 'status_line') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final rl = p['rate_limits'];
    if (rl is Map) return rl.cast<String, dynamic>();
  }
  return null;
}

/// Returns the verbatim latest `status_line` payload (or null if no
/// such event has fired yet). v1.0.706 polish — the session-details
/// sheet uses this to surface live mutable state (effort, thinking,
/// fast_mode, output_style) that session.init carries only at spawn
/// time and a /style or /thinking mid-session has since changed.
///
/// Walks newest-last and returns the FIRST status_line event's
/// payload as a `Map<String, dynamic>` — claude resends the full
/// snapshot every ~10s so the latest frame is always authoritative.
/// Null = cold open, older claude versions without statusLine, or
/// the operator removed the install.
Map<String, dynamic>? latestStatusLinePayload(
    List<Map<String, dynamic>> events) {
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if ((e['kind'] ?? '').toString() != 'status_line') continue;
    final p = e['payload'];
    if (p is Map) return p.cast<String, dynamic>();
  }
  return null;
}

/// Format a `resets_at` Unix-epoch-seconds value for the rate-limit
/// chip's sub-line (ADR-036 W5 + v1.0.704 polish).
///
/// Always emits a compact countdown: `43m`, `3h43m`, `3d19h`. No
/// `in`/`resets` prefix — the chip's label already establishes the
/// context (`5h  72%`) so the sub-line just answers "how long until
/// reset". Width-bounded by construction (max 6 chars: `13d23h`),
/// which keeps both tiles' baselines aligned in the strip's `Row`.
///
/// The absolute wall-clock form (`Mon 03:00`) moved to the tile
/// tooltip — Flutter's `Tooltip` widget activates on long-press on
/// mobile, matching the user's "detail on long-press" expectation.
/// See [formatRateLimitResetsAtAbsolute].
///
/// Both forms render in device-local TZ per ADR-036 D7 — the wire
/// field is TZ-agnostic epoch and the wall-clock-rendering TZ is a
/// chip-side concern.
///
/// Edge cases:
///   - past timestamps → "now" (the window already reset; the next
///     status_line frame will refresh the value with the new resets_at).
///   - null / non-positive epoch → empty string (caller drops the
///     sub-line cleanly).
///   - epoch farther than 14 days out → empty string (sanity bound;
///     rate-limit windows reset within days, not weeks; protects
///     against a unit-mistake landing a microsecond value here).
///   - sub-minute horizons → "<1m" so we never show a misleading
///     "0m" in the same minute as a reset.
///
/// [now] is the reference time (defaults to `DateTime.now()`). Tests
/// override to pin formatter output without sleeping.
String formatRateLimitResetsAt(int? epochSeconds, {DateTime? now}) {
  if (epochSeconds == null || epochSeconds <= 0) return '';
  final ref = now ?? DateTime.now();
  // resets_at is Unix-epoch SECONDS per the probe (claude ships values
  // like 1779640200 — 10 digits = year-2026 in seconds; year-2026 in ms
  // would be 13 digits and start with 17_796_…). Build the reference
  // DateTime in UTC then convert to local for rendering.
  final ts = DateTime.fromMillisecondsSinceEpoch(
    epochSeconds * 1000,
    isUtc: true,
  ).toLocal();
  final refLocal = ref.toLocal();
  final diff = ts.difference(refLocal);
  if (diff.isNegative || diff == Duration.zero) return 'now';
  if (diff.inDays > 14) return ''; // sanity bound; misinterpreted unit
  // Compact ladder — minutes / hours+minutes / days+hours. The unit
  // boundaries are exact: 60m → 1h, 24h → 1d. We deliberately do not
  // mix three units (e.g. "3d19h45m") — the next-finer unit is signal
  // enough at every scale and the extra char eats horizontal budget.
  if (diff.inMinutes < 1) return '<1m';
  if (diff.inHours < 1) return '${diff.inMinutes}m';
  if (diff.inDays < 1) {
    final h = diff.inHours;
    final m = diff.inMinutes % 60;
    return m == 0 ? '${h}h' : '${h}h${m}m';
  }
  final d = diff.inDays;
  final h = diff.inHours % 24;
  return h == 0 ? '${d}d' : '${d}d${h}h';
}

/// Absolute-form companion to [formatRateLimitResetsAt] — renders the
/// reset wall-clock in device-local TZ as `Mon 03:00`. Used in the
/// rate-limit chip tooltip so long-press surfaces the precise reset
/// time even though the sub-line stays compact (v1.0.704 polish).
///
/// Returns empty string for the same defensive inputs as the compact
/// formatter (null / non-positive / past / >14d out) so the tooltip
/// composer can splice it cleanly without a separate gate.
String formatRateLimitResetsAtAbsolute(int? epochSeconds, {DateTime? now}) {
  if (epochSeconds == null || epochSeconds <= 0) return '';
  final ref = now ?? DateTime.now();
  final ts = DateTime.fromMillisecondsSinceEpoch(
    epochSeconds * 1000,
    isUtc: true,
  ).toLocal();
  final refLocal = ref.toLocal();
  final diff = ts.difference(refLocal);
  if (diff.isNegative || diff == Duration.zero) return '';
  if (diff.inDays > 14) return '';
  return _fmtAbsoluteShort(ts);
}

String _fmtAbsoluteShort(DateTime localTs) {
  // "Mon 03:00" — three-letter weekday, zero-padded HH:MM. Done
  // longhand (not via intl) so the test harness doesn't need a
  // locale package; the chip cadence + display is engineering-
  // oriented and a fixed English form is appropriate.
  const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
  // DateTime.weekday is 1..7 (Mon..Sun) — matches our array index.
  final day = days[(localTs.weekday - 1).clamp(0, 6)];
  final hh = localTs.hour.toString().padLeft(2, '0');
  final mm = localTs.minute.toString().padLeft(2, '0');
  return '$day $hh:$mm';
}

/// Alarm-tier color for a rate-limit window used-percentage (ADR-036
/// W5 §alarm tier). 80% → amber; 95% → red; otherwise success-green.
/// Public for testability. Returns DesignColors-level values so the
/// strip caller doesn't have to repeat the threshold logic.
({Color color, String severity}) rateLimitAlarmTier(num? usedPercentage) {
  final p = (usedPercentage ?? 0).toDouble();
  if (p >= 95) return (color: DesignColors.error, severity: 'red');
  if (p >= 80) return (color: DesignColors.warning, severity: 'amber');
  return (color: DesignColors.success, severity: 'green');
}

/// Hard-cap alarm reducer (ADR-036 W6).
///
/// Walks events newest-last and returns the latest
/// `status_line.payload.exceeds_200k_tokens` boolean, or null when no
/// status_line frame has carried the field yet.
///
/// The bool surfaces claude's own warning that the next API call's
/// prompt will exceed the 200K-token hard cap on plans that have it
/// (independent of the model's nominal context window — a plan can
/// cap below the model's window). When true the chip pair turns red
/// and prompts `/clear`. Returns null when absent so the caller can
/// self-gate (chip suppresses entirely vs rendering a literal
/// "false" reassurance the wire didn't actually send).
bool? exceeds200kFromEvents(List<Map<String, dynamic>> events) {
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if ((e['kind'] ?? '').toString() != 'status_line') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final v = p['exceeds_200k_tokens'];
    if (v is bool) return v;
  }
  return null;
}

/// Session-name fallback reducer (ADR-036 W6).
///
/// Returns the latest non-empty `status_line.payload.session_name`,
/// or null when nothing useful has been carried yet. claude derives
/// this label autonomously after the first few turns of a session
/// (e.g. "List directory files"). Hub-side `sessions.title` (user-
/// set) always wins over this fallback — the caller layers the
/// precedence; this reducer just sources the candidate.
///
/// Intentionally NOT persisted to the hub. Reading fresh from
/// status_line every render means `/clear`'s new session can show
/// its own claude-derived name without state leaking from the prior
/// conversation (the rotation handler from W3 already nukes
/// `engine_session_id` on rotation, but the name is a separate
/// channel).
///
/// Empty strings from the wire are normalized to null so the caller
/// has one falsy check instead of two.
String? sessionNameFromEvents(List<Map<String, dynamic>> events) {
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if ((e['kind'] ?? '').toString() != 'status_line') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final raw = p['session_name'];
    if (raw is String && raw.isNotEmpty) return raw;
  }
  return null;
}

/// Session-cost tooltip composer (ADR-036 D8 chip 2 — W4-c).
///
/// Renders the multi-line tooltip text for the session-cost chip from
/// the GET /sessions/{id}/cost response. Layout:
///
///   $X.XXXX session — imputed against the public API rate sheet.
///   Preserved across resumes (never resets within a session).
///   Subscription users aren't actually billed this.
///
///   Usage by model:
///   • opus 4.7: $X.XXX (↑N in / ↓N out / cache K)
///   • sonnet 4.6: $X.XXX (↑N in / ↓N out)
///
///   Rates as of YYYY-MM-DD (operator override / embedded).
///   Not priced: claude-future-99
///   Pair: session vs process — see the process-cost chip…
///
/// [detail] is the raw response map; tolerant of null + missing
/// fields. Public so widget tests can pin the rendered string without
/// spinning the full strip.
String buildSessionCostTooltipFromDetail(
  double totalUsd,
  Map<String, dynamic>? detail, {
  bool pair = false,
}) {
  final buf = StringBuffer()
    ..write('\$')
    ..write(totalUsd.toStringAsFixed(4))
    ..write(' session — imputed against the public API rate sheet. ')
    ..write(
        'Preserved across resumes (never resets within a session). ')
    ..write('Subscription users aren\'t actually billed this.');

  final breakdown = detail?['breakdown_by_model'];
  final tokens = detail?['tokens_by_model'];
  if (breakdown is Map && breakdown.isNotEmpty) {
    buf.write('\n\nUsage by model:');
    final entries = breakdown.entries.toList()
      ..sort((a, b) => '${a.key}'.compareTo('${b.key}'));
    for (final e in entries) {
      final model = e.key.toString();
      final usd = (e.value is num) ? (e.value as num).toDouble() : 0.0;
      buf
        ..write('\n• ')
        ..write(_shortModelNameForTooltip(model))
        ..write(': \$')
        ..write(usd.toStringAsFixed(4));
      if (tokens is Map && tokens[model] is Map) {
        final tc = (tokens[model] as Map).cast<String, dynamic>();
        final i = (tc['input'] as num?)?.toInt() ?? 0;
        final o = (tc['output'] as num?)?.toInt() ?? 0;
        final cr = (tc['cache_read'] as num?)?.toInt() ?? 0;
        buf
          ..write(' (↑')
          ..write(i)
          ..write(' in / ↓')
          ..write(o)
          ..write(' out');
        if (cr > 0) buf..write(' / cache ')..write(cr);
        buf.write(')');
      }
    }
  }
  final snapshot = (detail?['snapshot_date'] as String?) ?? '';
  final origin = (detail?['origin'] as String?) ?? '';
  if (snapshot.isNotEmpty || origin.isNotEmpty) {
    buf.write('\n\nRates as of ');
    buf.write(snapshot.isEmpty ? 'unknown' : snapshot);
    if (origin.isNotEmpty) buf..write(' (')..write(origin)..write(' tier)');
    buf.write('.');
  }
  final missingRaw = detail?['missing_models'];
  if (missingRaw is List && missingRaw.isNotEmpty) {
    buf.write('\nNot priced: ');
    buf.write(missingRaw.map((m) => m.toString()).join(', '));
  }
  if (pair) {
    buf.write(
        '\n\nPair: session vs process — see the process-cost chip to its '
        'left for the live in-process meter that resets on respawn.');
  }
  return buf.toString();
}

String _shortModelNameForTooltip(String raw) {
  if (raw.startsWith('claude-')) {
    final parts = raw.split('-');
    if (parts.length >= 4) return '${parts[1]} ${parts[2]}.${parts[3]}';
  }
  return raw;
}

/// Process-cost reducer (ADR-036 D8 chip 1 — W4-a).
///
/// Walks the supplied event list newest-last and returns the latest
/// `status_line.payload.cost.total_cost_usd` it finds, or null if no
/// status_line event carries a cost block yet (cold-open race, older
/// claude versions without statusLine, or operator removed the
/// install).
///
/// The latest-wins semantics matter: statusLine fires at ~10s cadence
/// and EACH frame is a fresh snapshot of the process-cumulative cost
/// (not a delta). Summing would double-count by hundreds within a
/// session; we just take the most recent value.
///
/// Returns null (not 0.0) when no cost has been observed — the chip
/// self-gates on null per ADR-036 D9 ("blank > wrong"). A real
/// claude process showing `cost.total_cost_usd = 0` IS a valid
/// number (fresh respawn, no turn yet); it would render as
/// "$0.0000".
///
/// Public so widget tests can exercise the reducer without spinning
/// the full agent_feed widget tree.
double? processCostFromEvents(List<Map<String, dynamic>> events) {
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if ((e['kind'] ?? '').toString() != 'status_line') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final cost = p['cost'];
    if (cost is! Map) continue;
    final v = cost['total_cost_usd'];
    if (v is num) return v.toDouble();
  }
  return null;
}

String? agentEventReplayKey(Map<String, dynamic> evt) {
  final kind = (evt['kind'] ?? '').toString();
  final raw = evt['payload'];
  if (raw is! Map) return null;
  final payload = raw.cast<String, dynamic>();
  switch (kind) {
    case 'text':
    case 'thought':
      final text = (payload['text'] ?? '').toString();
      if (text.isEmpty) return null;
      return '$kind:${text.length}:$text';
    case 'tool_call':
      final id = (payload['id'] ?? payload['toolCallId'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id';
    case 'tool_call_update':
      final id = (payload['toolCallId'] ?? payload['id'] ?? '').toString();
      final status = (payload['status'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id:$status';
    case 'approval_request':
      final id = (payload['request_id'] ?? '').toString();
      if (id.isEmpty) return null;
      return '$kind:$id';
  }
  return null;
}

/// ADR-021 W2.5 — extract the latest mode + model state advertised by
/// the agent from a list of agent_events (newest-last). Walks events
/// in reverse for the most recent `current_mode_update` /
/// `current_model_update` system notifications (gemini ACP shape) and
/// returns a `(currentMode, availableModes, currentModel,
/// availableModels)` tuple as a plain map so test fixtures don't have
/// to construct private types.
///
/// Returns null when neither a mode nor a model has been advertised —
/// the strip widget hides itself in that case.
Map<String, dynamic>? modeModelStateFromEvents(List<Map<String, dynamic>> events) {
  String? currentMode;
  List<Map<String, dynamic>>? availableModes;
  String? currentModel;
  List<Map<String, dynamic>>? availableModels;
  // W7c — capture each of the four fields independently from the
  // latest event that carries it, NOT from the sibling inside the
  // same event. W7b synthetic events (posted after set_mode/set_model
  // RPC success) ship only the new currentModeId/currentModelId; the
  // available* lists live on the older session/new (or W7 carryover)
  // event. Pre-W7c the reducer captured the list inside the same
  // event branch as the id, so once a W7b synth arrived the picker
  // saw currentModel without availableModels and the hasModel gate
  // hid the chip. Independent capture means the latest id pairs with
  // whatever list-bearing event came before it.
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    final kind = (e['kind'] ?? '').toString();
    if (kind != 'system') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final body = p.cast<String, dynamic>();
    if (currentMode == null && body['currentModeId'] is String) {
      currentMode = body['currentModeId'] as String;
    }
    if (availableModes == null && body['availableModes'] is List) {
      availableModes = [
        for (final m in (body['availableModes'] as List))
          if (m is Map) m.cast<String, dynamic>(),
      ];
    }
    if (currentModel == null && body['currentModelId'] is String) {
      currentModel = body['currentModelId'] as String;
    }
    if (availableModels == null && body['availableModels'] is List) {
      availableModels = [
        for (final m in (body['availableModels'] as List))
          if (m is Map) m.cast<String, dynamic>(),
      ];
    }
    if (currentMode != null && currentModel != null &&
        availableModes != null && availableModels != null) {
      break;
    }
  }
  if (currentMode == null && currentModel == null) return null;
  return <String, dynamic>{
    'currentMode': currentMode,
    'availableModes': availableModes ?? const <Map<String, dynamic>>[],
    'currentModel': currentModel,
    'availableModels': availableModels ?? const <Map<String, dynamic>>[],
  };
}

/// Maps an ADR-032 envelope endpoint role to a human label for the
/// transcript header.
///
/// Fallback only — when the hub stamps `payload.from_label` via
/// `renderEnvelopeSenderLabel` (server-side, ADR-032 D-10), the mobile
/// feed prefers that operator-template-resolved string so a YAML edit
/// to `roles.principal` reaches both the engine and the mobile UI in
/// lockstep. Pre-v1.0.710 events on disk, A2A relay paths that don't
/// pass through the hub handler, and tests that don't seed the
/// envelope loader all fall through to this static map. Kept in sync
/// with the default `hub/templates/envelope/active.yaml` so the
/// degraded path still looks correct.
String envelopeRoleLabel(String role) {
  switch (role) {
    case 'principal':
      return 'the principal';
    case 'system':
      return 'the system';
    case 'peer_steward':
      return 'peer steward';
    case 'peer_worker':
      return 'peer worker';
    default:
      return role;
  }
}

/// Resolves the rendered sender label for an envelope's `from:` row.
/// Precedence: `payload.from_label` (operator-template-resolved at
/// hub send-time) → `@handle (<static role label>)` → bare static
/// label. Public for `agent_compose_envelope_label_test.dart`.
String envelopeSenderLabel({
  required String role,
  required String handle,
  String? fromLabel,
}) {
  final stamped = (fromLabel ?? '').trim();
  if (stamped.isNotEmpty) return stamped;
  if (handle.isNotEmpty) return '@$handle (${envelopeRoleLabel(role)})';
  return envelopeRoleLabel(role);
}

/// Dart port of `formatAttentionReplyText` (Go: driver_stdio.go).
/// Renders the structured payload of an `input.attention_reply` event
/// into the literal text the agent sees on its user turn — so the
/// transcript card matches what was sent on the wire. Both sides must
/// stay in sync; pinned by `test/widgets/attention_reply_render_test.dart`.
String renderAttentionReplyText(Map<String, dynamic> p) {
  final kind = (p['kind'] ?? '').toString();
  final reqID = (p['request_id'] ?? '').toString();
  final decision = (p['decision'] ?? '').toString();
  final body = (p['body'] ?? '').toString();
  final option = (p['option_id'] ?? '').toString();
  final reason = (p['reason'] ?? '').toString();

  var prefix = '';
  if (reqID.isNotEmpty) {
    final short = reqID.length > 8 ? reqID.substring(0, 8) : reqID;
    prefix = '[reply to $kind $short] ';
  }

  switch (kind) {
    case 'approval_request':
      switch (decision) {
        case 'approve':
          return reason.isEmpty
              ? '${prefix}Approved.'
              : '${prefix}Approved. Reason: $reason';
        case 'reject':
          return reason.isEmpty
              ? '${prefix}Rejected.'
              : '${prefix}Rejected. Reason: $reason';
      }
      return prefix + decision;
    case 'select':
      if (decision == 'reject') {
        return reason.isEmpty
            ? '${prefix}No option chosen.'
            : '${prefix}No option chosen. Reason: $reason';
      }
      if (option.isNotEmpty) return '${prefix}Selected: $option';
      return '${prefix}Selected.';
    case 'help_request':
      if (decision == 'reject') {
        return reason.isEmpty
            ? '${prefix}Dismissed without reply.'
            : '${prefix}Dismissed without reply. Reason: $reason';
      }
      if (body.isNotEmpty) return prefix + body;
      return '$prefix(empty reply)';
  }
  if (body.isNotEmpty) return prefix + body;
  return prefix + decision;
}

// ───────────────────────────────────────────────────────────────────
// Classifiers lifted out of `_AgentFeedState` in W0 (instance-field
// reads became parameters). Pure; pinned by the agent_feed_* tests.
// ───────────────────────────────────────────────────────────────────

/// True when a stream-error [reason] is a benign idle-drop (the hub or a
/// proxy closed an idle SSE connection) rather than a real failure worth
/// surfacing to the user.
bool isIdleDropSignature(String reason) {
  final lc = reason.toLowerCase();
  return lc.contains('connection closed') ||
      lc.contains('connection reset') ||
      lc.contains('connection abort') ||
      lc.contains('connection terminated') ||
      lc.contains('http2streamlimit') ||
      lc.contains('stream closed') ||
      lc.contains('before full body received');
}

/// True when the latest non-user event in [events] signals an in-flight
/// turn. Walks newest-first: terminal kinds (`turn.result`/`completion`/
/// `session.init`, `exited`/`stopped` lifecycle) short-circuit to idle;
/// only [kAgentTurnActiveKinds] decisively signal busy; everything else
/// is no-signal and the walk continues. Default = idle (v1.0.721
/// allowlist inversion).
bool agentIsBusy(List<Map<String, dynamic>> events) {
  for (final e in events.reversed) {
    final producer = (e['producer'] ?? '').toString();
    if (producer == 'user') continue; // user inputs don't move the state
    final kind = (e['kind'] ?? '').toString();
    if (kind == 'turn.result' || kind == 'completion') return false;
    // session.init is a one-shot handshake event — it lands once
    // per resume/start and means "ready, waiting for input." If
    // it's the most recent agent event (no turn-active signal
    // after it), the agent is idle, not busy.
    if (kind == 'session.init') return false;
    if (kind == 'lifecycle') {
      final p = e['payload'];
      final phase = p is Map ? (p['phase'] ?? '').toString() : '';
      if (phase == 'exited' || phase == 'stopped') return false;
      // 'started' and other lifecycle phases are ambiguous; keep
      // scanning so a recent text/tool_call wins the decision.
      continue;
    }
    // ALLOWLIST: only kinds we've classified as turn-active signal
    // motion. Everything else — telemetry (usage / rate_limit /
    // status_line / raw / system), unknown future kinds — is
    // treated as no-signal and the walker continues scanning.
    if (kAgentTurnActiveKinds.contains(kind)) return true;
  }
  return false;
}

/// Merge across all `session.init` events in [events] (later events
/// overwrite earlier fields, earlier-only fields persist), or null when
/// none. Most engines emit session.init once; antigravity emits two
/// partials that must merge rather than shadow (the second carries only
/// `{model}` and would otherwise drop the engine pill).
Map<String, dynamic>? latestSessionInitPayload(
    List<Map<String, dynamic>> events) {
  Map<String, dynamic>? merged;
  for (final e in events) {
    if ((e['kind'] ?? '').toString() != 'session.init') continue;
    final p = e['payload'];
    if (p is! Map) continue;
    final m = p.cast<String, dynamic>();
    if (merged == null) {
      merged = Map<String, dynamic>.from(m);
    } else {
      merged.addAll(m);
    }
  }
  return merged;
}

/// Coerce a dynamic wire value into a `List<String>` (empty when absent
/// or not a list).
List<String> stringList(Object? v) {
  if (v is! List) return const [];
  return [for (final e in v) e.toString()];
}

// Names of MCP gate tools whose tool_call card is hidden because the
// inline attention card represents the same gesture. Used by the
// tool_call_update visibility rule so updates for gated tools fall
// back to a standalone card when the parent is suppressed.
const _kGateToolNames = <String>{
  'permission_prompt',
  'request_select',
  'request_decision',
  'request_approval',
  'request_help',
};

/// True when [name] is an MCP gate tool (bare or `mcp__<server>__`-
/// prefixed).
bool isGatedToolName(String name) {
  if (_kGateToolNames.contains(name)) return true;
  for (final g in _kGateToolNames) {
    if (name.endsWith('__$g')) return true;
  }
  return false;
}

const _kVerboseOnlyKinds = <String>{
  'lifecycle',
  'completion',
  'raw',
  'system',
};

/// True when [kind] is a verbose-gated family (hidden unless the verbose
/// toggle is on), with the `system`/non-init carve-out so a real system
/// message isn't silently dropped.
bool isVerboseOnly(String kind, Object? payload) {
  if (!_kVerboseOnlyKinds.contains(kind)) return false;
  // `system` is a generic envelope. Init lands in the header already
  // (handled by the caller). Don't suppress the rest just because the
  // family is verbose-gated — fall through to render text payloads, etc.
  if (kind == 'system' && payload is Map) {
    final sub = (payload['subtype'] ?? '').toString();
    if (sub.isNotEmpty && sub != 'init') return false;
  }
  return true;
}

// Folded-into-parent kinds drop out of the visible list. tool_result is
// only hidden when the matching tool_call is in scope (toolNames has its
// id) — a stray result with no parent call still renders so we never
// silently lose data.
//
// Telemetry-only kinds (usage, rate_limit, turn.result) live in the
// strip above the feed; rendering them as cards too would duplicate
// the signal.
//
// Verbose-gated kinds (W1.B):
//   lifecycle      — started/stopped frames; the agent's status pill
//                    on the steward badge already conveys this.
//   completion     — deprecated alias for turn.result; already covered
//                    by the telemetry strip.
//   raw            — thinking blocks + unrecognized frames; debug-only.
//   system         — non-init system frames; init is in the header.
// input.* events are NOT hidden by default — the compose box clears
// after send, so the user needs to see their own message echoed back
// in the transcript or the chat reads as one-sided.
// All verbose-gated kinds are revealed when [verbose] is true.
/// True when event [e] should be hidden from the transcript-bubble layer,
/// given the in-scope tool_call ids ([toolNames]) and whether the
/// [verbose] toggle is on. (The `_verbose` field read became [verbose].)
bool isHiddenInFeed(
  Map<String, dynamic> e,
  Map<String, String> toolNames, {
  required bool verbose,
}) {
  final kind = (e['kind'] ?? '').toString();
  // Always-hidden kinds drive chips / telemetry strips / AppBar
  // headers instead of transcript bubbles — rendering them as
  // cards would duplicate the signal with no extra data. See
  // [kAgentFeedAlwaysHiddenKinds] for the testable kind-list
  // (adding `status_line` here in v1.0.699 fixed the cold-open
  // JSON bubble that ADR-036 D4 explicitly forbids).
  //
  // tool_call_update and turn.result are demoted to verbose-only
  // (handled below). They still drive folding (parent tool_call
  // card status pill) and the telemetry strip respectively, but on
  // the rare occasion the user wants to inspect the wire frames —
  // e.g. confirming an approval flow's tool result content reached
  // them, or seeing the cancelled stopReason that ended a turn —
  // the verbose toggle now reveals them.
  if (kAgentFeedAlwaysHiddenKinds.contains(kind)) {
    return true;
  }
  if (kind == 'tool_call') {
    // Hide MCP "gate" tool_calls — the ones whose effect is to open
    // an attention_item that mobile already renders as an inline
    // card. Showing both surfaces (the tool_call card + the
    // attention card) double-counts the same event.
    //
    // Three gates today, all under mcp__termipod__:
    //   - permission_prompt — claude-code's --permission-prompt-tool
    //     contract. Rendered as the inline approval card.
    //   - request_select — multi-choice. Rendered as the inline
    //     SELECT card.
    //   - request_approval — generic ask-for-human-yes/no. Rendered
    //     as an attention item on the Me page (no inline card, but
    //     the tool_call card is still noisy).
    // Bare names also accepted (no `mcp__<server>__` prefix) so
    // alternate engines that surface the same tool names hide too.
    final p = e['payload'];
    if (p is Map) {
      final name = (p['name'] ?? '').toString();
      const gates = {
        'permission_prompt',
        'request_select',
        // Back-compat: an agent spawned with a stale prompt template
        // may still call request_decision; the server aliases to
        // request_select but the tool_call event keeps the old name.
        // Hide both so the duplicate-card fix covers either spelling.
        'request_decision',
        'request_approval',
      };
      if (gates.contains(name)) return true;
      for (final g in gates) {
        if (name.endsWith('__$g')) return true;
      }
    }
    return false;
  }
  if (kind == 'tool_result') {
    final p = e['payload'];
    if (p is Map) {
      final id = p['tool_use_id']?.toString() ?? '';
      if (id.isNotEmpty && toolNames.containsKey(id)) return true;
    }
    return false;
  }
  if (kind == 'tool_call_update') {
    // Folds into the parent tool_call card when there IS a visible
    // parent — rendering the standalone card too would just
    // duplicate the latest status pill the parent already shows.
    // For gated tools (request_approval/select/help_request — the
    // request_* MCP gates) the parent is hidden by the gate rule
    // above, so the standalone card becomes the only place to see
    // the wire-level result content (e.g. the attention_id +
    // severity payload the agent received). Same fall-through for
    // updates whose toolCallId never had a matching tool_call event
    // (drivers that emit updates without an opening frame).
    final p = e['payload'];
    if (p is Map) {
      final id = (p['toolCallId'] ?? p['tool_call_id'] ?? '').toString();
      if (id.isNotEmpty) {
        final parentName = toolNames[id] ?? '';
        if (parentName.isNotEmpty && !isGatedToolName(parentName)) {
          return true;
        }
      }
    }
    return false;
  }
  if (kind == 'turn.result') {
    // The normal `end_turn` boundary fires on every clean turn —
    // showing it as a card would clutter the transcript on every
    // reply. Cancelled / errored / max-token / refused turns are
    // unusual signals worth surfacing inline (e.g. a cancelled
    // turn that fell because attention_reply replaced it). The
    // telemetry strip aggregates ALL turn.results regardless.
    final p = e['payload'];
    if (p is Map) {
      final reason = (p['stop_reason'] ?? '').toString();
      if (reason == 'end_turn' || reason == '') return true;
    }
    return false;
  }
  if (!verbose && isVerboseOnly(kind, e['payload'])) return true;
  return false;
}

/// Single-select transcript lens (docs/plans/agent-transcript-debug-
/// and-header-parity.md, P1). Narrows the visible feed to one family so
/// a long run can be debugged without scrolling every row. `all` is the
/// default (no filtering). Orthogonal to the verbose toggle, which
/// controls debug *depth* rather than which family is shown.
enum FeedLens { all, text, turns, tools, errors }

/// Kinds the [FeedLens.text] lens keeps — the readable conversation:
/// assistant prose, reasoning blocks, and the user's own messages.
const _kFeedLensTextKinds = <String>{'text', 'thought', 'input.text'};

/// Kinds the [FeedLens.turns] lens keeps — the inbound turn boundaries
/// that *drive* the agent rather than its replies: the user's own input,
/// A2A messages from peers (which arrive as `input.text` envelopes with a
/// peer `from`), the control turns (cancel / approval / attention reply),
/// and genuine `system` notices. Lets a long run be navigated turn-by-turn.
/// NB: background-task lifecycle `system` events are excluded — see
/// [isBackgroundTaskSystemEvent].
const _kFeedLensTurnKinds = <String>{
  'input.text',
  'input.cancel',
  'input.approval',
  'input.attention_reply',
  'system',
};

/// True for a `system` event that is background-task lifecycle telemetry
/// rather than a conversational notice — claude-code emits these to track
/// Task / background-`bash` runs:
///
///     {"type":"system","subtype":"task_started","task_id":"…",
///      "task_type":"local_bash", …}
///
/// They carry a `task_id` and a `task_*` subtype. They are NOT turns (a
/// tester saw a `task_started` card appear in the Turns lens), so the
/// Turns lens filters them out. Pure + testable.
bool isBackgroundTaskSystemEvent(Map<String, dynamic> e) {
  if ((e['kind'] ?? '').toString() != 'system') return false;
  final p = e['payload'];
  if (p is! Map) return false;
  if (p['task_id'] != null) return true;
  final sub = (p['subtype'] ?? '').toString();
  return sub.startsWith('task_');
}

/// Kinds the [FeedLens.tools] lens keeps — every tool-related card that
/// survives folding (a standalone `tool_result`/`tool_call_update` shows
/// when its parent call is out of scope, e.g. a gated tool).
const _kFeedLensToolKinds = <String>{
  'tool_call',
  'tool_result',
  'tool_call_update',
};

/// True when [e] is an error-signal event for the [FeedLens.errors]
/// lens. Aligned with how [AgentEventCard] paints failure: a bare
/// `kind == 'error'` event, a `tool_result` carrying `is_error == true`,
/// or a `tool_call` whose paired result/update resolved to a failure.
///
/// Runs over the post-fold visible list, where a tool_call's result and
/// updates have been merged into the parent card — so the parent's
/// resolved status is recovered from [toolResults] / [toolUpdates]
/// (the same maps the card itself reads), keyed by tool_use id.
bool agentEventIsError(
  Map<String, dynamic> e,
  Map<String, Map<String, dynamic>> toolResults,
  Map<String, Map<String, dynamic>> toolUpdates,
) {
  final kind = (e['kind'] ?? '').toString();
  if (kind == 'error') return true;
  final p = e['payload'];
  if (kind == 'tool_result') {
    return p is Map && p['is_error'] == true;
  }
  if (kind == 'tool_call') {
    // callToolIdOf, not p['id'] — the log-tail claude-code mapper writes the
    // call id as tool_use_id only, and the pairing maps key on that value.
    final id = p is Map ? callToolIdOf(p) : '';
    if (id.isEmpty) return false;
    final res = toolResults[id];
    if (res != null) {
      final rp = res['payload'];
      if (rp is Map && rp['is_error'] == true) return true;
    }
    final upd = toolUpdates[id];
    if (upd != null) {
      final st = (upd['status'] ?? '').toString();
      if (st == 'failed' || st == 'error') return true;
    }
  }
  return false;
}

/// Canonical run-scoped error classification (ADR-038 §1) over the **raw**
/// event stream — the per-run digest's union: `kind=='error'` ∪
/// `tool_result.is_error` ∪ a failed `tool_call_update` ∪
/// `turn.result.status != 'success'`. Each matching event counts once (no
/// cross-event dedup). This MIRRORS the hub's `canonicalErrorClass` /
/// `canonicalErrorSQLPredicate` (Go) and is pinned to the shared vector
/// `hub/internal/server/testdata/digest_canonical_vector.json` by
/// `test/widgets/digest_canonical_vector_test.dart`.
///
/// Distinct from [agentEventIsError], which classifies the POST-FOLD visible
/// *card* (a tool_call card carrying its merged result) for the live-tail
/// lens — a subset that does not include failed turns. Use this raw-stream
/// form when reconciling with the digest / insights `total_errors`.
String? agentEventCanonicalErrorClass(Map<String, dynamic> e) {
  final kind = (e['kind'] ?? '').toString();
  final p = e['payload'];
  switch (kind) {
    case 'error':
      final t = (p is Map ? (p['type'] ?? '') : '').toString();
      return t.isNotEmpty ? 'error:$t' : 'error';
    case 'tool_result':
      if (p is Map && p['is_error'] == true) return 'tool_error';
      return null;
    case 'tool_call_update':
      if (p is Map) {
        final st = (p['status'] ?? '').toString();
        if (st == 'failed' || st == 'error') return 'tool_error';
      }
      return null;
    case 'turn.result':
      final st =
          (p is Map ? (p['status'] ?? 'success') : 'success').toString();
      if (st != 'success') return 'failed_turn';
      return null;
  }
  return null;
}

/// Total canonical errors across a raw event list (ADR-038 §1).
int agentRunCanonicalErrorCount(List<Map<String, dynamic>> events) {
  var n = 0;
  for (final e in events) {
    if (agentEventCanonicalErrorClass(e) != null) n++;
  }
  return n;
}

/// Kinds that anchor a *turn* for the full-screen turn-stepper
/// (docs/plans/agent-transcript-debug-and-header-parity.md — turn-nav
/// follow-up). A turn starts at an inbound prompt: the user's own
/// `input.text` or an A2A peer message (which also arrives as an
/// `input.text` envelope). Deliberately narrower than the [FeedLens.turns]
/// *filter*: the stepper jumps between exchange starts, and `input.text`
/// is always rendered (never folded/hidden), so a seek to its seq always
/// has a card to land on — whereas `turn.result` (the agent-side turn
/// count) is usually folded away and wouldn't be seekable.
const kFeedTurnAnchorKinds = <String>{'input.text'};

/// Accent colour for an event of [kind] from [producer] — the SAME
/// mapping the transcript card paints with, so the minimap ticks line up
/// with the cards' colours (a tester asked for this). Single source of
/// truth: `AgentEventCard._accentFor` delegates here.
Color agentEventAccent(String kind, String producer) {
  switch (kind) {
    case 'text':
    case 'thought':
      return DesignColors.primary;
    case 'tool_call':
      return DesignColors.terminalBlue;
    case 'tool_result':
      return DesignColors.terminalCyan;
    case 'completion':
      return DesignColors.success;
    case 'error':
      return DesignColors.error;
    case 'lifecycle':
      return DesignColors.warning;
    case 'session.init':
      return DesignColors.secondary;
    case 'approval_request':
      return DesignColors.warning;
    case 'plan':
      return DesignColors.secondary;
    case 'diff':
      return DesignColors.terminalCyan;
    default:
      return producer == 'user'
          ? DesignColors.terminalYellow
          : DesignColors.textMuted;
  }
}

/// True when [e] starts a turn the stepper should land on: an inbound
/// prompt ([kFeedTurnAnchorKinds]) that a *human or peer* sent — NOT a
/// system-injected one. System envelopes (`producer == 'system'`, or an
/// `input.text` whose `from.role == 'system'`) are framing/context the
/// hub adds, not a turn the user wants to jump between (the "system agent"
/// cards a tester flagged). Pure + testable.
bool isTurnAnchorEvent(Map<String, dynamic> e) {
  if (!kFeedTurnAnchorKinds.contains((e['kind'] ?? '').toString())) {
    return false;
  }
  if ((e['producer'] ?? '').toString() == 'system') return false;
  final p = e['payload'];
  if (p is Map) {
    final from = p['from'];
    if (from is Map && (from['role'] ?? '').toString() == 'system') {
      return false;
    }
  }
  return true;
}

/// Indices into [rendered] (the on-screen, post-fold list) that start a
/// turn — see [isTurnAnchorEvent]. Pure so the nav math is testable
/// without spinning the widget tree.
List<int> turnAnchorIndices(List<Map<String, dynamic>> rendered) {
  final out = <int>[];
  for (var i = 0; i < rendered.length; i++) {
    if (isTurnAnchorEvent(rendered[i])) out.add(i);
  }
  return out;
}

/// True when event [e] passes the active [lens]. Operates on the
/// post-fold visible list (see [agentEventIsError] for why the
/// [toolResults] / [toolUpdates] maps are threaded through). `all`
/// passes everything; the others narrow to one family.
bool agentEventMatchesLens(
  Map<String, dynamic> e,
  FeedLens lens,
  Map<String, Map<String, dynamic>> toolResults,
  Map<String, Map<String, dynamic>> toolUpdates,
) {
  switch (lens) {
    case FeedLens.all:
      return true;
    case FeedLens.text:
      return _kFeedLensTextKinds.contains((e['kind'] ?? '').toString());
    case FeedLens.turns:
      final tk = (e['kind'] ?? '').toString();
      if (!_kFeedLensTurnKinds.contains(tk)) return false;
      // Background-task lifecycle system events are telemetry, not turns.
      if (tk == 'system' && isBackgroundTaskSystemEvent(e)) return false;
      return true;
    case FeedLens.tools:
      return _kFeedLensToolKinds.contains((e['kind'] ?? '').toString());
    case FeedLens.errors:
      return agentEventIsError(e, toolResults, toolUpdates);
  }
}

/// The event `kind` set a lens narrows to — the server-side `kind=` query the
/// Insight "lens as a keyset query" pages (ADR-039). Returns `null` for the
/// lenses that are NOT a simple kind set: `all` (everything) and `errors` (a
/// derived predicate — failed tool_call/turn.result, not a kind). Callers fall
/// back to the loaded-window client filter for those. The returned set is a
/// SUPERSET of the rendered lens for `turns` (it can't encode the
/// background-task `system` exclusion in [agentEventMatchesLens]), so a buffer
/// fed by this set must still pass each row through [agentEventMatchesLens].
/// Single source of truth: the same `_kFeedLens*Kinds` constants the predicate
/// uses.
Set<String>? feedLensKinds(FeedLens lens) {
  switch (lens) {
    case FeedLens.text:
      return _kFeedLensTextKinds;
    case FeedLens.turns:
      return _kFeedLensTurnKinds;
    case FeedLens.tools:
      return _kFeedLensToolKinds;
    case FeedLens.all:
    case FeedLens.errors:
      return null;
  }
}

/// True when a `usage` payload carries cumulative session totals
/// (codex's thread/tokenUsage/updated). Accepts a real bool or the
/// string "true" — the frame-profile evaluator only emits strings.
bool isCumulativeUsage(Map p) {
  final v = p['cumulative'];
  if (v is bool) return v;
  if (v is String) return v.toLowerCase() == 'true';
  return false;
}

// Codex emits item/agentMessage/delta as a stream of small chunks
// while a turn is generating. The driver throttles + buffers them
// into `kind=text, partial: true` events that share a message_id;
// each carries the full accumulated text so far (not a delta). The
// final item/completed produces a normal `kind=text` event with the
// same message_id and no partial flag.
//
// Mobile collapse: walk events in order. The first partial for a
// message_id opens a chain — its index in the rendered list is
// remembered. Subsequent text events (partial OR final) for the
// same message_id replace the chain entry instead of appending. A
// text event with no partial flag and no preceding partial chain
// (claude's case) appends normally — we only redirect events whose
// message_id is already a known chain root, so claude's per-block
// text events with the same message_id keep stacking the way they
// do today.
List<Map<String, dynamic>> collapseStreamingPartials(
    List<Map<String, dynamic>> events) {
  final out = <Map<String, dynamic>>[];
  // Chains are namespaced by kind so a `text` and a `thought` event
  // that happen to share a message_id (e.g. when the engine reuses
  // turn-local ids across kinds) don't fold into each other.
  final chainIdx = <String, int>{};
  for (final e in events) {
    final kind = (e['kind'] ?? '').toString();
    // gemini-cli streams thought chunks the same way it streams
    // text — incremental session/update frames the driver
    // accumulates and re-emits with shared message_id + partial:true
    // (driver_acp.go handleNotification, agent_thought_chunk arm).
    // Without thought in this allowlist they stack as N redundant
    // cards each carrying the cumulative text so far.
    //
    // `plan` folds the same way (agent-transcript-redesign §6 P1, G3):
    // every ACP plan update is a FULL snapshot of the agent's todo
    // list, not a delta, and the hub stamps each with a stable
    // per-turn message_id + partial:true (driver_acp.go plan arm).
    // Folding the chain by kind+message_id turns N snapshot cards per
    // turn into ONE checklist card that updates in place. A plan event
    // with no message_id (or no partial chain — e.g. an engine that
    // emits a single non-streaming plan) falls through to the append
    // path below, so pre-stamp transcripts render unchanged.
    if (kind != 'text' && kind != 'thought' && kind != 'plan') {
      out.add(e);
      continue;
    }
    final p = e['payload'];
    String? mid;
    bool isPartial = false;
    if (p is Map) {
      final m = (p['message_id'] ?? '').toString();
      if (m.isNotEmpty) mid = m;
      final pv = p['partial'];
      isPartial = (pv == true || pv == 'true');
    }
    if (mid == null) {
      out.add(e);
      continue;
    }
    final chainKey = '$kind:$mid';
    final existing = chainIdx[chainKey];
    if (existing != null) {
      // We're in a streaming chain for this kind+message_id — every
      // subsequent event (partial or final) replaces the entry.
      out[existing] = e;
    } else if (isPartial) {
      // First partial for this kind+message_id opens a chain.
      chainIdx[chainKey] = out.length;
      out.add(e);
    } else {
      // Regular event with no preceding partial — claude's shape;
      // append without opening a chain.
      out.add(e);
    }
  }
  return out;
}

// ── Tool-call grouping (agent-transcript-redesign §6 P1) ─────────────────
//
// Render-layer batching for the visible feed: a run of ≥2 CONSECUTIVE
// `tool_call` events renders as ONE group card (kimi-web's `tool-stack`
// rule, §2.3 / decision §7.3) instead of N individually-collapsed cards.
// This is presentation only — the reducer runs on the POST-lens,
// POST-hide, POST-collapse list, so FoldMaps, busy inference, lens
// predicates, counts, and the event stream itself are untouched; a lens
// change simply regroups the filtered list.

/// A run of consecutive `tool_call` events rendered as one card.
/// Never empty — [groupConsecutiveToolCalls] only builds one for runs
/// of ≥2 (a lone call stays a standalone card, the kimi-web
/// `position: single` rule).
class ToolCallGroup {
  final List<Map<String, dynamic>> events;
  const ToolCallGroup(this.events);
}

/// One row of the rendered feed after grouping: either a single event
/// ([group] == null) or a [ToolCallGroup] ([event] == null). Kept as
/// one class with nullable arms (rather than a sealed hierarchy) so the
/// ListView itemBuilder switches without a cast ladder.
class FeedDisplayItem {
  final Map<String, dynamic>? event;
  final ToolCallGroup? group;
  const FeedDisplayItem.single(Map<String, dynamic> e)
      : event = e,
        group = null;
  const FeedDisplayItem.grouped(ToolCallGroup g)
      : group = g,
        event = null;

  bool get isGroup => group != null;

  /// All events behind this row, in feed order (one for a single row).
  Iterable<Map<String, dynamic>> get events =>
      group?.events ?? [event!];

  /// The seq this row anchors on: its own seq for a single row, the
  /// FIRST member's seq for a group. The seek/stepper machinery maps
  /// rows ↔ seqs through this anchor, so a jump lands on the group that
  /// CONTAINS the target turn's activity.
  int get anchorSeq =>
      (events.first['seq'] as num?)?.toInt() ?? 0;

  /// True when [seq] is one of the events behind this row — the
  /// "view in context" seek must land on a group when the target seq is
  /// any member, not just the anchor.
  bool containsSeq(int seq) =>
      events.any((e) => (e['seq'] as num?)?.toInt() == seq);
}

/// Reads the hub-stamped `turn_id` off an event payload (driver_acp.go
/// `stampTurnID`; empty when the driver doesn't stamp one — e.g. hub-
/// synthesized replay events between turns).
String _turnIdOf(Map<String, dynamic> e) {
  final p = e['payload'];
  return p is Map ? (p['turn_id'] ?? '').toString() : '';
}

/// Groups runs of ≥2 consecutive `tool_call` events in the VISIBLE
/// (post-lens, post-hide, post-collapse) list into [ToolCallGroup]
/// display items. A run breaks at:
///   - any non-`tool_call` visible row (assistant prose flows BETWEEN
///     groups — the kimi-web turn-rendering shape), and
///   - a `turn_id` change, but only when BOTH neighbours carry a
///     non-empty stamp: engines that never stamp turn_id keep pure
///     adjacency grouping, and a stamped event adjacent to an
///     unstamped one (replay seams) still groups instead of
///     fragmenting into singletons.
/// Pure: same input → same output, no widget or State dependency.
List<FeedDisplayItem> groupConsecutiveToolCalls(
    List<Map<String, dynamic>> visible) {
  final out = <FeedDisplayItem>[];
  var run = <Map<String, dynamic>>[];
  void flush() {
    if (run.length >= 2) {
      out.add(FeedDisplayItem.grouped(
          ToolCallGroup(List<Map<String, dynamic>>.unmodifiable(run))));
    } else if (run.length == 1) {
      // A lone call renders standalone — a one-card "group" is just
      // the existing card with extra chrome.
      out.add(FeedDisplayItem.single(run.first));
    }
    run = <Map<String, dynamic>>[];
  }

  for (final e in visible) {
    if ((e['kind'] ?? '').toString() != 'tool_call') {
      flush();
      out.add(FeedDisplayItem.single(e));
      continue;
    }
    if (run.isNotEmpty) {
      final prevTurn = _turnIdOf(run.last);
      final thisTurn = _turnIdOf(e);
      if (prevTurn.isNotEmpty && thisTurn.isNotEmpty && prevTurn != thisTurn) {
        flush();
      }
    }
    run.add(e);
  }
  flush();
  return out;
}

/// The display status of one `tool_call` card, resolved from its fold
/// lineage — the SAME derivation the standalone card uses
/// (`AgentEventCard._toolCallBody`), extracted so the group card's
/// rows and aggregate state can't drift from it: a streaming
/// `tool_call_update` status wins over the creation-frame status; a
/// paired `tool_result` resolves to completed/failed by `is_error`
/// (covers drivers that emit no updates); nothing yet → pending.
String toolCallDisplayStatus(
  Map<String, dynamic> callPayload,
  Map<String, dynamic>? updatePayload,
  Map<String, dynamic>? resultPayload,
) {
  final updateStatus =
      (updatePayload?['status'] ?? callPayload['status'] ?? '').toString();
  if (updateStatus.isNotEmpty) return updateStatus;
  if (resultPayload != null) {
    return resultPayload['is_error'] == true ? 'failed' : 'completed';
  }
  return 'pending';
}

/// Aggregate state of a tool-call group, kimi-web precedence
/// (§2.3, decision §7.3): **running > error > done** — a group still
/// waiting on any call reads running even if a sibling already failed;
/// only once nothing is in flight does a failure define the group.
enum ToolGroupState { running, error, done }

/// Per-row state behind [toolCallGroupState] / the group card's row
/// glyph. A call counts as error when its resolved status says so OR
/// its paired result/update does ([agentEventIsError] — the same
/// failure classification the Errors lens paints with); a non-terminal
/// status (pending / in_progress / driver-specific) reads running.
ToolGroupState toolCallRowState(
  Map<String, dynamic> callEvent,
  Map<String, Map<String, dynamic>> toolResults,
  Map<String, Map<String, dynamic>> toolUpdates,
) {
  if (agentEventIsError(callEvent, toolResults, toolUpdates)) {
    return ToolGroupState.error;
  }
  final p = callEvent['payload'];
  final payload = p is Map ? p.cast<String, dynamic>() : <String, dynamic>{};
  final id = callToolIdOf(payload);
  final resEvent = id.isNotEmpty ? toolResults[id] : null;
  final rp = resEvent != null && resEvent['payload'] is Map
      ? (resEvent['payload'] as Map).cast<String, dynamic>()
      : null;
  final status = toolCallDisplayStatus(
      payload, id.isNotEmpty ? toolUpdates[id] : null, rp);
  if (status == 'failed' || status == 'error') return ToolGroupState.error;
  if (status == 'completed') return ToolGroupState.done;
  return ToolGroupState.running;
}

/// Aggregate state for the group header: running if ANY row is still
/// in flight (short-circuits — the top precedence), else error if any
/// row failed, else done.
ToolGroupState toolCallGroupState(
  ToolCallGroup group,
  Map<String, Map<String, dynamic>> toolResults,
  Map<String, Map<String, dynamic>> toolUpdates,
) {
  var anyError = false;
  for (final e in group.events) {
    switch (toolCallRowState(e, toolResults, toolUpdates)) {
      case ToolGroupState.running:
        return ToolGroupState.running;
      case ToolGroupState.error:
        anyError = true;
      case ToolGroupState.done:
        break;
    }
  }
  return anyError ? ToolGroupState.error : ToolGroupState.done;
}

/// Failed-row count for the group header's `· N failed` suffix — errors
/// surface in the header even while the aggregate reads running.
int toolCallGroupErrorCount(
  ToolCallGroup group,
  Map<String, Map<String, dynamic>> toolResults,
  Map<String, Map<String, dynamic>> toolUpdates,
) {
  var n = 0;
  for (final e in group.events) {
    if (toolCallRowState(e, toolResults, toolUpdates) == ToolGroupState.error) {
      n++;
    }
  }
  return n;
}

/// The compact progress preview a tool_call card pulls from its latest
/// `tool_call_update` — the first text content block of the ACP
/// update's content list. Shared by the standalone card and the group
/// rows so both render the same lineage. Larger outputs land in
/// tool_result anyway; this is just for at-a-glance progress.
String? toolCallUpdatePreview(Map<String, dynamic>? updatePayload) {
  final content = updatePayload?['content'];
  if (content is List) {
    for (final b in content) {
      if (b is Map && b['type'] == 'content') {
        final inner = b['content'];
        if (inner is Map && inner['type'] == 'text') {
          return inner['text']?.toString();
        }
      }
    }
  }
  return null;
}

/// The one-line "key argument" a group row shows next to the tool verb
/// (kimi-web ToolRow shape): the command for Bash, the path for file
/// tools, the pattern for search tools. Empty when the input doesn't
/// carry a recognizable key — the row just shows the verb.
String toolCallKeyArg(String name, Object? input) {
  if (input is! Map) return '';
  String pick(List<String> keys) {
    for (final k in keys) {
      final v = input[k];
      if (v is String && v.isNotEmpty) return v;
    }
    return '';
  }

  switch (name) {
    case 'Bash':
      return pick(['command']);
    case 'Read':
    case 'Edit':
    case 'MultiEdit':
    case 'Write':
    case 'NotebookEdit':
    case 'NotebookRead':
      return pick(['file_path', 'notebook_path', 'path']);
    case 'Glob':
    case 'Grep':
      return pick(['pattern']);
    case 'WebFetch':
      return pick(['url']);
    case 'WebSearch':
      return pick(['query']);
    case 'Task':
      return pick(['description', 'prompt']);
  }
  // Unknown / engine-specific tools: probe the common argument keys so
  // e.g. kimi's Bash-alikes still get a one-liner; otherwise nothing.
  return pick(['command', 'file_path', 'path', 'pattern', 'query', 'url']);
}

/// Diffstat for the group row (`+N −M`), or null when the call carries
/// no measurable change. An explicit payload `diffstat` string wins
/// (kimi's `display` hints can carry one verbatim); otherwise computed
/// from the edit inputs — old vs new string line counts for
/// Edit/MultiEdit, content lines for Write. Line-count deltas only:
/// cheap, deterministic, and honest (no LCS).
String? toolCallDiffstat(String name, Map<String, dynamic> payload) {
  final explicit = (payload['diffstat'] ?? '').toString();
  if (explicit.isNotEmpty) return explicit;
  final input = payload['input'];
  if (input is! Map) return null;
  int linesOf(Object? s) =>
      s is String && s.isNotEmpty ? '\n'.allMatches(s).length + 1 : 0;
  switch (name) {
    case 'Edit':
      final adds = linesOf(input['new_string']);
      final dels = linesOf(input['old_string']);
      if (adds == 0 && dels == 0) return null;
      return '+$adds −$dels';
    case 'MultiEdit':
      var adds = 0, dels = 0;
      final edits = input['edits'];
      if (edits is List) {
        for (final e in edits) {
          if (e is! Map) continue;
          adds += linesOf(e['new_string']);
          dels += linesOf(e['old_string']);
        }
      }
      if (adds == 0 && dels == 0) return null;
      return '+$adds −$dels';
    case 'Write':
      final adds = linesOf(input['content']);
      if (adds == 0) return null;
      return '+$adds';
  }
  return null;
}

/// Plan P2 (agent-run-analysis-mode) — the monotonic "event N of M" log
/// position. The loaded transcript is a contiguous tail slice spanning seqs
/// [minSeq, maxSeq]; [viewFrac] (0 = top / oldest loaded, 1 = tail) maps
/// linearly across it, and per-agent seq is the dense 1-based run ordinal, so
/// N is the viewport-top seq. M is the run-lifetime total ([totalEventCount]
/// from the digest) — or the newest loaded seq before the digest resolves, so
/// the readout is never blank. Returns null when there is no window to
/// position within (nothing loaded). N is clamped into [1, M] so a multi-agent
/// session (where per-agent seq isn't a run-wide ordinal) still reads sanely.
({int n, int m})? feedLogPosition({
  required int minSeq,
  required int maxSeq,
  required double viewFrac,
  int? totalEventCount,
}) {
  if (maxSeq <= 0) return null;
  final m = (totalEventCount != null && totalEventCount > 0)
      ? totalEventCount
      : maxSeq;
  final lo = minSeq > 0 ? minSeq : 1;
  final span = maxSeq - lo;
  final raw = span <= 0 ? maxSeq : (lo + viewFrac * span).round();
  final n = raw.clamp(1, m).toInt();
  return (n: n, m: m);
}

/// Plan P2 — full-run minimap anchors as `(frac, seq, isError)` tuples, where
/// `frac = seq / total` positions each anchor by its run ordinal (so the strip
/// is a whole-run overview, not a shrink of the loaded slice). Turn anchors
/// come first and error anchors last, so error ticks paint on top. `total <= 0`
/// (digest not resolved) yields no anchors. The caller maps these to coloured
/// minimap marks; keeping the math here makes it directly testable.
List<({double frac, int seq, bool isError})> feedRunAnchorMarks({
  required List<int> errorSeqs,
  required List<int> turnSeqs,
  required int total,
}) {
  if (total <= 0) return const [];
  double f(int seq) => (seq / total).clamp(0.0, 1.0);
  return [
    for (final s in turnSeqs) (frac: f(s), seq: s, isError: false),
    for (final s in errorSeqs) (frac: f(s), seq: s, isError: true),
  ];
}
