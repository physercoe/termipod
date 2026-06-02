import 'feed_render.dart';
import 'feed_reducer.dart';

/// The telemetry rollup that feeds `TelemetryStrip` — cumulative cost,
/// per-model token totals, context-window fill, rate limits, and the
/// cost/alarm signals. **LiveFeed-only** (ADR-040 open-question B): the Insight
/// surface draws its dashboard from the server digest (`RunReportCard`) and
/// hides the strip, so `InsightTranscript` never builds this.
///
/// Pure over the loaded event window + the out-of-band session-cost poll
/// (`_sessionCost`); lifted verbatim from `_AgentFeedState.build()` so the
/// behaviour is byte-identical — only its home changed. Unit-testable without a
/// widget.
class FeedTelemetry {
  final double totalCostUsd;
  final Map<String, ModelTokens> modelTotals;
  final int turnCount;
  final Map<String, dynamic>? latestRateLimit;
  final int? latestContextWindow;
  final int? latestContextUsed;
  final double? processCostUsd;
  final double? sessionCostUsdImputed;
  final Map<String, dynamic>? rateLimitsFromStatus;
  final bool? exceeds200k;

  /// True iff any telemetry signal is present — the gate the strip renders on.
  final bool hasTelemetry;

  const FeedTelemetry({
    required this.totalCostUsd,
    required this.modelTotals,
    required this.turnCount,
    required this.latestRateLimit,
    required this.latestContextWindow,
    required this.latestContextUsed,
    required this.processCostUsd,
    required this.sessionCostUsdImputed,
    required this.rateLimitsFromStatus,
    required this.exceeds200k,
    required this.hasTelemetry,
  });

  /// Fold [events] (the loaded window) plus the out-of-band [sessionCost] poll
  /// map into the strip's inputs. See the inline comments for the per-engine
  /// token-source rules (claude `turn.result.by_model`, codex cumulative
  /// `usage`, claude-code per-message `usage`, antigravity `status_line`).
  factory FeedTelemetry.fromEvents(
    List<Map<String, dynamic>> events,
    Map<String, dynamic>? sessionCost,
  ) {
    // Telemetry strip inputs: cumulative cost from all turn.result
    // events, per-model token totals aggregated from turn.result.by_model
    // (claude's modelUsage, normalized by driver_stdio.go — keys: input,
    // output, cache_read, cache_create, cost_usd per model name), and
    // latest rate_limit. We sum across all completed turns so the strip
    // shows session-wide usage, not just the most recent turn.
    //
    // by_model is the right source because claude can spawn sub-agents
    // (e.g. Haiku for small tasks under an Opus parent), each with its
    // own token totals. The bare `usage` event only carries the parent's
    // last-message numbers and undercounts when sub-agents are active.
    double totalCostUsd = 0.0;
    final modelTotals = <String, ModelTokens>{};
    Map<String, dynamic>? latestRateLimit;
    int turnCount = 0;
    // Codex publishes cumulative session totals on each
    // thread/tokenUsage/updated notification (kind=usage in the
    // typed vocabulary), tagged with `cumulative: true|"true"` and
    // `engine: <name>` by the frame profile. Claude's per-message
    // usage events lack the marker; they're ignored here and the
    // authoritative claude source is turn.result.by_model. The
    // latest cumulative event replaces — it's not a delta — so we
    // track it separately and fold it in once below.
    ModelTokens? cumulativeUsage;
    String cumulativeBucketKey = 'agent';
    // Latest known context-window stats (codex's
    // thread/tokenUsage/updated carries both modelContextWindow and the
    // cumulative total). The window can change mid-session if codex
    // hot-swaps models, so we always track the most recent values.
    int? latestContextWindow;
    int? latestContextUsed;
    // Per-message usage snapshot (claude-code path, v1.0.662). The
    // driver emits a `kind=usage` event per assistant message with
    // input + cache_read + cache_create token counts for THAT message
    // alone (not cumulative). The most recent event wins — its sum
    // equals the API call's prompt size, which equals what claude's
    // own `/context` slash command reports as "current context".
    // Replaces a pre-v1.0.662 fallback that summed per-turn
    // `by_model.input + cache_read + cache_create` across every API
    // call within a turn — for a turn with many tool-use iterations
    // that double-counted by N×, producing absurd >1M numbers on
    // long sessions.
    int? perMessageInput;
    int? perMessageCacheRead;
    int? perMessageCacheCreate;
    // v1.0.668: also capture output + model so we can synthesise a
    // ModelTokens entry for the token-flow pill. M4 doesn't emit
    // turn.result.by_model (the driver-of-record source for
    // modelTotals), so the pill stayed blank even though every
    // assistant message carried full usage. SET semantics here
    // (overwrite, not sum) — same anti-double-count rule as the
    // context-chip path.
    int? perMessageOutput;
    String? perMessageModel;
    for (final e in events) {
      final kind = (e['kind'] ?? '').toString();
      final p = e['payload'];
      if (p is! Map) continue;
      if (kind == 'turn.result') {
        turnCount += 1;
        final c = p['cost_usd'];
        if (c is num) totalCostUsd += c.toDouble();
        final byModel = p['by_model'];
        if (byModel is Map) {
          for (final entry in byModel.entries) {
            final v = entry.value;
            if (v is! Map) continue;
            final tot = modelTotals.putIfAbsent(
                entry.key.toString(), ModelTokens.empty);
            tot.add(v.cast<String, dynamic>());
          }
        }
      } else if (kind == 'rate_limit') {
        latestRateLimit = p.cast<String, dynamic>();
      } else if (kind == 'usage' && isCumulativeUsage(p)) {
        // Cumulative session totals (codex shape). The latest
        // notification supersedes; we don't sum. Claude's per-
        // message usage events lack the `cumulative` marker and
        // are handled in the next branch.
        final t = ModelTokens.empty();
        t.input = (p['input_tokens'] as num?)?.toInt() ?? 0;
        t.output = (p['output_tokens'] as num?)?.toInt() ?? 0;
        t.cacheRead = (p['cached_input_tokens'] as num?)?.toInt() ?? 0;
        cumulativeUsage = t;
        // Use the engine tag the profile sets on cumulative events
        // so the bucket key in the telemetry tooltip reads as a real
        // engine name rather than an empty string. Default falls back
        // to 'agent' if the upstream profile didn't tag.
        final engineTag = (p['engine'] as String?) ?? 'agent';
        cumulativeBucketKey = engineTag;
        // Context-window snapshot rides on the same event. For "fill"
        // we want the most recent turn's token count — that's what
        // the model sees the context filled with on the NEXT turn.
        // Codex's tokenUsage frame carries both `.total.*` (cumulative
        // across all turns, grows boundlessly) and `.last.*` (just
        // the most recent turn). The profile emits `last_total_tokens`
        // from `.last.totalTokens`; mobile prefers it when present
        // and falls back to `total_tokens` for legacy events on disk
        // (pre-v1.0.712 codex usage rows that only carry cumulative).
        //
        // The earlier comment on this branch claimed cumulative
        // matched codex's TUI statusline — that was wrong, codex's
        // statusline shows the per-turn last count. A long codex
        // session previously showed wildly inflated "context fill"
        // numbers (e.g. 169K/258K on a session whose actual fill was
        // ~19K) — the v1.0.712 smoke regression that prompted this
        // fix.
        final cw = (p['context_window'] as num?)?.toInt() ?? 0;
        final lastUsed = (p['last_total_tokens'] as num?)?.toInt();
        final cumulativeUsed = (p['total_tokens'] as num?)?.toInt() ?? 0;
        final used = lastUsed ?? cumulativeUsed;
        if (cw > 0) latestContextWindow = cw;
        if (used > 0) latestContextUsed = used;
      } else if (kind == 'usage') {
        // Per-message usage (claude-code path, v1.0.662). NOT
        // cumulative — each event reports the API call's prompt
        // size on its own; later events overwrite earlier ones.
        // Sum on display = input + cache_read + cache_create =
        // the claude `/context` number.
        final i = (p['input_tokens'] as num?)?.toInt();
        final cr = (p['cache_read'] as num?)?.toInt() ??
            (p['cache_read_input_tokens'] as num?)?.toInt();
        final cc = (p['cache_create'] as num?)?.toInt() ??
            (p['cache_creation_input_tokens'] as num?)?.toInt();
        if (i != null) perMessageInput = i;
        if (cr != null) perMessageCacheRead = cr;
        if (cc != null) perMessageCacheCreate = cc;
        // v1.0.668: also capture output + model so we can synthesise
        // a modelTotals entry below for the token-flow pill.
        final o = (p['output_tokens'] as num?)?.toInt();
        if (o != null) perMessageOutput = o;
        final m = (p['model'] as String?);
        if (m != null && m.isNotEmpty) perMessageModel = m;
        // v1.0.667: pick up `context_window` if present. The M4
        // mapper attaches it (derived from model name) so mobile can
        // render the context-utilisation chip. Without it the chip
        // suppresses itself (cw <= 0 → no tile). The cumulative
        // branch above already handled this; the per-message branch
        // didn't, leaving the chip blank for claude-code spawns
        // even though usage was flowing.
        final cw = (p['context_window'] as num?)?.toInt() ?? 0;
        if (cw > 0) latestContextWindow = cw;
      } else if (kind == 'status_line') {
        // v1.0.720 — antigravity's M4 path emits ONLY status_line
        // events for token + context-window data (no `usage` events
        // from the transcript — the agy transcript doesn't carry
        // token counts; statusLine is the authoritative source per
        // the antigravity statusLine research §2.4).
        //
        // The nested `context_window.current_usage` block is shape-
        // identical to claude-code's `usage` block, so we shape-shift
        // here: extract from that nested path and feed the same
        // perMessageInput / perMessageCacheRead / perMessageCacheCreate
        // state the claude-code branch above writes. The downstream
        // chip strip (latestInput / billableInput / token-flow pill)
        // then renders identically for antigravity without per-engine
        // branching at the render layer.
        //
        // claude-code stewards also receive status_line events but
        // also emit `usage` events from JSONL — the `usage` branch
        // above wins on latest-write semantics. So this branch is
        // additive and degrades cleanly for all engines that ship
        // a statusLine.
        final cw = p['context_window'];
        if (cw is Map) {
          final cur = cw['current_usage'];
          if (cur is Map) {
            // Same field names as claude-code's usage block (verified
            // on the dev host 2026-05-26; research doc §2.3 + §2.4).
            final i = (cur['input_tokens'] as num?)?.toInt();
            final cr =
                (cur['cache_read_input_tokens'] as num?)?.toInt();
            final cc =
                (cur['cache_creation_input_tokens'] as num?)?.toInt();
            final o = (cur['output_tokens'] as num?)?.toInt();
            if (i != null) perMessageInput = i;
            if (cr != null) perMessageCacheRead = cr;
            if (cc != null) perMessageCacheCreate = cc;
            if (o != null) perMessageOutput = o;
          }
          // antigravity carries the model's static context size at
          // `context_window.context_window_size`. claude-code carries
          // it as `usage.context_window`. Same semantic; different
          // path. Latest-wins set, same shape as the `usage` branch.
          final sz = (cw['context_window_size'] as num?)?.toInt() ?? 0;
          if (sz > 0) latestContextWindow = sz;
        }
        // Capture model from session.init-style top-level model field
        // (antigravity statusLine carries {model: {id, display_name}}).
        // claude-code's status_line carries the same shape, so this
        // is engine-agnostic.
        final m = p['model'];
        if (m is Map) {
          final n = (m['display_name'] as String?) ??
              (m['id'] as String?) ??
              (m['name'] as String?);
          if (n != null && n.isNotEmpty) perMessageModel = n;
        } else if (m is String && m.isNotEmpty) {
          perMessageModel = m;
        }
      }
    }
    // If no by_model rows arrived (codex's turn/completed doesn't
    // ship them), surface the cumulative usage as a single bucket.
    // The bucket key is shown in the tile's tooltip so we tag it
    // with the engine name rather than leaving it blank.
    if (modelTotals.isEmpty && cumulativeUsage != null) {
      modelTotals[cumulativeBucketKey] = cumulativeUsage;
    }
    // v1.0.668: synthesise a modelTotals entry from per-message usage
    // when no other source populated one. claude-code M4 doesn't emit
    // turn.result.by_model, so without this the token-flow pill
    // (which gates on modelTotals.isNotEmpty) stayed suppressed even
    // though every assistant message carried full usage. SET
    // semantics — `latestInput / latestCacheRead / latestCacheCreate`
    // get the per-message snapshot directly, and we DO NOT increment
    // `input / output / cacheRead / cacheCreate` (the cumulative
    // fields), because per-message events would otherwise sum across
    // a turn's many tool-use iterations and double-count by N× (the
    // pre-v1.0.662 1M-tokens bug).
    if (modelTotals.isEmpty &&
        (perMessageInput != null || perMessageOutput != null)) {
      final t = ModelTokens.empty();
      // Snapshot fields drive the chip; cumulative fields stay 0 so
      // the SUM-on-display logic in TelemetryStrip reads only the
      // per-message values via billableInput / output.
      t.latestInput = perMessageInput ?? 0;
      t.latestCacheRead = perMessageCacheRead ?? 0;
      t.latestCacheCreate = perMessageCacheCreate ?? 0;
      // Token-flow pill reads `billableInput` (input + cacheCreate)
      // and `output`. Populate them as snapshot too — they're meant
      // to reflect what the user paid for on the LATEST message, not
      // a session-wide aggregate that diverges from per-call usage.
      t.input = perMessageInput ?? 0;
      t.output = perMessageOutput ?? 0;
      t.cacheRead = perMessageCacheRead ?? 0;
      t.cacheCreate = perMessageCacheCreate ?? 0;
      final cw = latestContextWindow;
      if (cw != null && cw > 0) {
        t.contextWindow = cw;
      }
      modelTotals[perMessageModel ?? 'claude-code'] = t;
    }
    // Claude path for context window: the codex `usage` event already
    // populated latestContextWindow / latestContextUsed when present.
    // For claude (which carries the data per-model on turn.result and
    // does not emit cumulative `usage` events), pick the dominant
    // model from modelTotals — the one with the most output, since
    // sub-agents like Haiku produce trivial output relative to the
    // main agent. Use that model's contextWindow as capacity.
    if (latestContextWindow == null && modelTotals.isNotEmpty) {
      String? mainModel;
      var bestOutput = -1;
      modelTotals.forEach((name, t) {
        if (t.contextWindow > 0 && t.output > bestOutput) {
          mainModel = name;
          bestOutput = t.output;
        }
      });
      if (mainModel != null) {
        final t = modelTotals[mainModel]!;
        latestContextWindow = t.contextWindow;
      }
    }
    // For "used" prefer the per-message usage event (v1.0.662) over
    // the per-turn by_model snapshot. The per-message event reports
    // ONE API call's prompt — the right answer. The by_model
    // snapshot's `latestInput + latestCacheRead + latestCacheCreate`
    // double-counted within a multi-tool-use turn (every Bash/Read
    // iteration produced its own API call, all summed). Fall back
    // to the by_model snapshot only when the per-message stream is
    // absent (older drivers, future engines).
    if (latestContextUsed == null) {
      if (perMessageInput != null ||
          perMessageCacheRead != null ||
          perMessageCacheCreate != null) {
        final used = (perMessageInput ?? 0) +
            (perMessageCacheRead ?? 0) +
            (perMessageCacheCreate ?? 0);
        if (used > 0) latestContextUsed = used;
      } else if (modelTotals.isNotEmpty) {
        // Best-effort fallback for engines that don't emit per-message
        // usage. Pick the dominant model; reuse its latestInput +
        // latestCacheRead + latestCacheCreate. Accurate when the turn
        // had one API call; over-counted when the turn had many.
        String? mainModel;
        var bestOutput = -1;
        modelTotals.forEach((name, t) {
          if (t.output > bestOutput) {
            mainModel = name;
            bestOutput = t.output;
          }
        });
        if (mainModel != null) {
          final t = modelTotals[mainModel]!;
          final used =
              t.latestInput + t.latestCacheRead + t.latestCacheCreate;
          if (used > 0) latestContextUsed = used;
        }
      }
    }
    // ADR-036 W4-a — process-cost extracted from the latest
    // status_line frame's cost.total_cost_usd. Null when no
    // status_line has carried a cost block yet (cold-open race,
    // older claude versions, or operator removed the install).
    final processCostUsd = processCostFromEvents(events);
    // ADR-036 W4-c — session-cost imputed by the hub. Polled out-of-
    // band on the _sessionCostTimer; null until the first response
    // lands (or when sessionId is unset).
    double? sessionCostUsdImputed;
    final scRaw = sessionCost?['total_usd'];
    if (scRaw is num && (scRaw > 0 || (sessionCost?['tokens_by_model'] is Map
        && (sessionCost?['tokens_by_model'] as Map).isNotEmpty))) {
      sessionCostUsdImputed = scRaw.toDouble();
    }
    // ADR-036 W5 — rate_limits sub-block from the latest status_line
    // frame. Null until first status_line lands; either window may
    // still be absent on a given frame (tile self-gates per window).
    final rateLimitsFromStatus = rateLimitsFromEvents(events);
    // ADR-036 W6 — exceeds_200k_tokens alarm. True iff the latest
    // status_line carries the cap-breach signal; null/false suppress
    // the tile entirely.
    final exceeds200k = exceeds200kFromEvents(events);
    final hasTelemetry = turnCount > 0 ||
        modelTotals.isNotEmpty ||
        latestRateLimit != null ||
        latestContextWindow != null ||
        processCostUsd != null ||
        sessionCostUsdImputed != null ||
        rateLimitsFromStatus != null ||
        (exceeds200k == true);
    return FeedTelemetry(
      totalCostUsd: totalCostUsd,
      modelTotals: modelTotals,
      turnCount: turnCount,
      latestRateLimit: latestRateLimit,
      latestContextWindow: latestContextWindow,
      latestContextUsed: latestContextUsed,
      processCostUsd: processCostUsd,
      sessionCostUsdImputed: sessionCostUsdImputed,
      rateLimitsFromStatus: rateLimitsFromStatus,
      exceeds200k: exceeds200k,
      hasTelemetry: hasTelemetry,
    );
  }
}
