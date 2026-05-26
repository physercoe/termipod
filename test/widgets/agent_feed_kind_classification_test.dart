// Fix B from docs/discussions/consumer-side-dispatch-contracts.md.
//
// Cross-cutting contract test: every `kind` literal a producer emits
// to agent_events (from hub-side PostAgentEvent calls or profile
// rule emit.kind values in agent_families.yaml) must be classified
// by the mobile consumer side. Three classifications are accepted:
//
//   1. **Turn-active** — listed in `kAgentTurnActiveKinds` in
//      lib/widgets/agent_feed.dart. Drives the cancel-on-send
//      composer overlay.
//
//   2. **Always-hidden** — listed in `kAgentFeedAlwaysHiddenKinds`.
//      Chip-source only; never renders as a transcript bubble.
//
//   3. **Explicitly ignored by busy inference** — listed in
//      `kKindsExplicitlyIgnoredByBusyInference` below. The kind
//      isn't turn-active, isn't always-hidden, but the consumer
//      acknowledges it exists. Each entry carries a one-line
//      rationale comment.
//
// A producer adding a new kind that isn't in one of these three
// sets fails this test before merge — preventing the v1.0.667 /
// v1.0.699 / v1.0.717 / v1.0.720 class of "added kind, forgot
// consumer" bugs.
//
// The grep-the-codebase approach trades some test runtime for
// stronger coverage: when CI runs flutter test, this test parses
// the actual source tree, so it sees what real engines really
// emit, not what the test author thought they'd emit.

import 'dart:io';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_feed.dart';

// The third bucket: kinds the consumer side has actively decided
// are not turn-active and not always-hidden. Each entry carries a
// rationale that survives turnover.
//
// This set is the "we know about this kind and have chosen to
// ignore it for busy inference" register. It exists because the
// allowlist-only contract (Fix A) covers turn-active kinds, but
// some kinds are genuinely non-turn-active AND should be visible
// in the transcript bubble layer. Without this register, the
// contract test would flag those as un-classified.
const _kKindsExplicitlyIgnoredByBusyInference = <String>{
  // Terminal kinds — handled by explicit short-circuit branches in
  // _isAgentBusy (return false directly). Listed here so the
  // contract test knows they're intentional.
  'turn.result',
  'completion',
  'lifecycle',
  // Carries multi-step output (between two tool_calls in one turn).
  // Doesn't signal motion by itself; the next tool_call or text
  // does. Anti-race: if tool_result lands after the next tool_call,
  // we want the tool_call to win.
  'tool_result',
  // Grab-bag telemetry: mcp_server_startup, turn_started markers,
  // server-startup pings, error notices. The walker passes through;
  // a real text/tool_call signal wins.
  'system',
  // Approval-request UI hook: rendered as a card on mobile, doesn't
  // signal turn motion. Resolved via the /decide path.
  'approval_request',
  'attention_request',
  // Permission-prompt UI hook (claude-code M4 — ADR-027 W6).
  // Surface-side; doesn't represent agent motion.
  'permission_prompt',
  // Per-turn token telemetry — codex cumulative shape, claude per-
  // message shape, antigravity-via-statusLine reduction. Default-
  // ignored by the allowlist (v1.0.721); listed here to record the
  // decision.
  'usage',
  // Rate-limit window telemetry — chip-source, never turn-active.
  // ADR-036 D7. Default-ignored by the allowlist; listed here for
  // the contract test.
  'rate_limit',
  // statusLine snapshot — ADR-036 D4. Chip-source only.
  // Default-ignored by the allowlist; listed here for the contract
  // test.
  'status_line',
  // Forward-compat catch-all from profile evaluators. Codex
  // post-handshake notifications without profile rules
  // (thread/goal/cleared, configWarning, remoteControl/status/
  // changed) land here. v1.0.717's bug was this kind defaulting to
  // busy; v1.0.721's allowlist inversion makes the default safe.
  'raw',
  // The async input-router echo of user input. Producer is "user"
  // so _isAgentBusy already skips it via the producer filter, but
  // the kind exists in agent_events and contributors might wonder
  // why it's not classified.
  'input.text',
  'input.cancel',
  'input.approval',
  'input.answer',
  'input.attention_reply',
  'input.attach',
  'input.set_mode',
  'input.set_model',
  // Codex/claude tool-result frame variants the profile emits with
  // a typed sub-kind. Same family as `tool_result`.
  'tool_use',
  // Subagent + cross-engine bookkeeping (codex spawns its own
  // sub-conversation via item.type=subagent; antigravity emits
  // INVOKE_SUBAGENT). Surface bookkeeping, not motion.
  'subagent_spawned',
  // Codex/claude `error` events from the driver's fatal-frame
  // handler (driver_stdio.go:397) and profile error rules
  // (agent_families.yaml `kind: error` at lines 254, 435). Renders
  // as an error card on mobile; doesn't signal turn motion.
  'error',
  // ACP file-change diff frame (driver_acp.go:1389). Informational
  // mid-turn update; the next tool_call / text / turn.result drives
  // motion classification.
  'diff',
};

void main() {
  group('Fix B — kind classification contract (v1.0.721)', () {
    test(
      'every emitted kind is classified somewhere',
      () async {
        // Collect every literal kind a producer emits. Two sources:
        //  (a) hub Go source: PostAgentEvent(..., "<kind>", ...) and
        //      e.Kind / agent_events table inserts.
        //  (b) agent_families.yaml frame profiles: emit.kind values.
        // Both are read as plain text to keep the test independent
        // of any AST tooling.
        final kinds = await _collectEmittedKinds();
        expect(
          kinds.isNotEmpty,
          isTrue,
          reason: 'sanity: should find at least the well-known kinds',
        );
        expect(kinds, contains('text'));
        expect(kinds, contains('turn.result'));

        final classified = <String>{
          ...kAgentTurnActiveKinds,
          ...kAgentFeedAlwaysHiddenKinds,
          ..._kKindsExplicitlyIgnoredByBusyInference,
        };

        final unclassified = kinds.difference(classified);
        expect(
          unclassified,
          isEmpty,
          reason: '\n'
              'The following kinds appear in producer source but are\n'
              'not classified in any of: kAgentTurnActiveKinds /\n'
              'kAgentFeedAlwaysHiddenKinds /\n'
              '_kKindsExplicitlyIgnoredByBusyInference.\n'
              '\n'
              'Pick one:\n'
              '  - If it signals motion → add to kAgentTurnActiveKinds.\n'
              '  - If chip-source only → add to kAgentFeedAlwaysHiddenKinds.\n'
              '  - Otherwise → add to\n'
              '    test/widgets/agent_feed_kind_classification_test.dart::\n'
              '    _kKindsExplicitlyIgnoredByBusyInference with a one-\n'
              '    line rationale.\n'
              '\n'
              'See docs/discussions/consumer-side-dispatch-contracts.md\n'
              'for the design rationale.\n'
              '\n'
              'Unclassified: $unclassified',
        );
      },
      // The test scans the source tree (~hundreds of Go + YAML
      // files). 30s timeout is plenty; default 5s isn't.
      timeout: const Timeout(Duration(seconds: 30)),
    );

    test('classification sets do not overlap with turn-active', () {
      // A kind cannot be both turn-active AND always-hidden. Those
      // would be contradictory consumer-side decisions.
      final overlap = kAgentTurnActiveKinds.intersection(
        kAgentFeedAlwaysHiddenKinds,
      );
      expect(
        overlap,
        isEmpty,
        reason: 'turn-active ∩ always-hidden must be empty: $overlap',
      );
      final overlap2 = kAgentTurnActiveKinds.intersection(
        _kKindsExplicitlyIgnoredByBusyInference,
      );
      expect(
        overlap2,
        isEmpty,
        reason: 'turn-active ∩ explicitly-ignored must be empty: $overlap2',
      );
    });
  });
}

// Collect every literal kind string a producer emits. Two strategies:
//
//  - Go: regex-scan every .go file for the patterns producers use.
//    The hub's PostAgentEvent has the kind in the third positional
//    arg; we match a fairly tight regex to avoid false positives.
//
//  - YAML: regex-scan agent_families.yaml for `emit.kind: <value>`
//    lines under frame_profile rules.
//
// Strategy: just match common shapes. False positives (e.g. a kind
// referenced in a comment or test string) classified as
// "_kKindsExplicitlyIgnoredByBusyInference" cost a one-line entry;
// false negatives (a real kind we miss) leak the bug we're trying
// to prevent, so favour false positives.
Future<Set<String>> _collectEmittedKinds() async {
  final kinds = <String>{};
  final repoRoot = _findRepoRoot();

  // Hub Go sources. Tight regex set — we want kinds that go into
  // the `agent_events.kind` column, not the loose `kind|Kind`
  // assignment pattern which also picks up audit subject kinds,
  // envelope kinds, route prefixes, etc. The narrower scope below
  // captures only the patterns directly adjacent to agent_events
  // insertion.
  await for (final entity
      in Directory('${repoRoot.path}/hub').list(recursive: true)) {
    if (entity is! File || !entity.path.endsWith('.go')) continue;
    if (entity.path.endsWith('_test.go')) continue;
    final text = await entity.readAsString();
    // PostAgentEvent(ctx, agentID, "<kind>", producer, payload).
    // The third positional argument is the kind. Tight regex: each
    // pre-kind arg is a simple expression (no commas, no parens),
    // so we don't bleed into nested maps/structs that come AFTER
    // the kind. Validated against a Python-grep cross-check on the
    // current tree (18 distinct kinds, all real).
    //
    // We DO NOT scan `e.Kind = "..."` / `Kind: "..."` style assigns
    // — too noisy (matches audit subject_kind columns, envelope
    // kinds, route prefixes, struct-field assignments unrelated to
    // agent_events). PostAgentEvent is the canonical hub-side
    // ingress for agent_events rows; profile-emitted kinds are
    // caught by the YAML scan below.
    final post = RegExp(
      r'PostAgentEvent\(\s*[^,()]+\s*,\s*[^,()]+\s*,\s*"'
      r'([a-zA-Z][a-zA-Z0-9._]*)"',
    );
    for (final m in post.allMatches(text)) {
      kinds.add(m.group(1)!);
    }
  }

  // Profile rule emits in agent_families.yaml.
  final familiesPath = File(
    '${repoRoot.path}/hub/internal/agentfamilies/agent_families.yaml',
  );
  if (familiesPath.existsSync()) {
    final text = await familiesPath.readAsString();
    // emit.kind sits on its own line as `      kind: <value>` under
    // `emit:`. Loose match: `kind: <ident>` not preceded by `#`.
    final emit = RegExp(r'^\s*kind:\s*([a-zA-Z][a-zA-Z0-9._]*)\s*$',
        multiLine: true);
    for (final m in emit.allMatches(text)) {
      final k = m.group(1)!;
      // Filter out frame-profile keys that aren't event kinds:
      // - "kind" in the top-level family/spec shape
      // - "frame_translator: profile" etc.
      // Real event kinds are lowercase-words with optional dots.
      // Drop obvious YAML scalars that aren't kind strings.
      if (k == 'profile' || k == 'family' || k == 'antigravity' ||
          k == 'claude-code' || k == 'codex' || k == 'gemini-cli' ||
          k == 'kimi-code') {
        continue;
      }
      kinds.add(k);
    }
  }

  // Strip helpers that aren't real kinds (loose-match false
  // positives from local variable assignments).
  kinds.removeWhere(_isObviousFalsePositive);

  return kinds;
}

bool _isObviousFalsePositive(String s) {
  // Single-word names that are clearly engine families or other
  // non-kind concepts. Conservative: when in doubt, leave it in
  // (and add a classification later).
  const knownNonKinds = <String>{
    'profile', 'family', 'agent', 'engine',
    'antigravity', 'claude-code', 'codex', 'gemini-cli', 'kimi-code',
    'command', 'never', 'always',
  };
  return knownNonKinds.contains(s);
}

Directory _findRepoRoot() {
  // pubspec.yaml sits at the repo root. Walk up from the test's
  // working directory (Flutter sets CWD to the package root, so
  // this is usually a no-op).
  var dir = Directory.current;
  for (var i = 0; i < 10; i++) {
    if (File('${dir.path}/pubspec.yaml').existsSync()) {
      return dir;
    }
    final parent = dir.parent;
    if (parent.path == dir.path) break;
    dir = parent;
  }
  // Last-ditch: assume we ARE the root.
  return Directory.current;
}
