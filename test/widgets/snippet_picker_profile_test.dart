import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/models/action_bar_presets.dart';
import 'package:termipod/models/snippet_presets.dart';
import 'package:termipod/widgets/action_bar/snippet_picker_sheet.dart';

// #68 — the steward/agent chat bolt icon opened the snippet picker without
// pinning the agent's engine, so it fell back to the global action-bar profile
// (default general-terminal, which has no presets) and could show the wrong
// engine's slash commands. The picker now resolves its preset profile via
// resolveSnippetProfileId, preferring a pinned engine over the panel/global
// profile.

void main() {
  group('resolveSnippetProfileId (#68)', () {
    test('a pinned engine wins over the panel/global profile', () {
      expect(
        resolveSnippetProfileId(
          engineProfileId: ActionBarPresets.claudeCodeId,
          panelProfileId: ActionBarPresets.generalTerminalId,
        ),
        ActionBarPresets.claudeCodeId,
      );
      // Even when the global profile is a *different* engine (the reported
      // bug: a stale codex selection leaking into a claude-code chat).
      expect(
        resolveSnippetProfileId(
          engineProfileId: ActionBarPresets.claudeCodeId,
          panelProfileId: ActionBarPresets.codexId,
        ),
        ActionBarPresets.claudeCodeId,
      );
    });

    test('null / empty engine falls back to the panel profile', () {
      expect(
        resolveSnippetProfileId(
          engineProfileId: null,
          panelProfileId: ActionBarPresets.codexId,
        ),
        ActionBarPresets.codexId,
      );
      expect(
        resolveSnippetProfileId(
          engineProfileId: '',
          panelProfileId: ActionBarPresets.generalTerminalId,
        ),
        ActionBarPresets.generalTerminalId,
      );
    });
  });

  group('SnippetPresets root cause (#68)', () {
    test('the engine kind == the preset profile id, and those have presets',
        () {
      // backend.kind ('claude-code' / 'codex' / 'kimi-code') is passed
      // straight through as the profile id, so each must resolve to a
      // non-empty preset list.
      expect(SnippetPresets.forProfile(ActionBarPresets.claudeCodeId),
          isNotEmpty);
      expect(SnippetPresets.forProfile(ActionBarPresets.codexId), isNotEmpty);
      expect(SnippetPresets.forProfile(ActionBarPresets.kimiCodeId),
          isNotEmpty);
    });

    test('kimi preset mirrors the live ACP catalog (P3 fallback)', () {
      // The static preset is the no-catalog fallback for the dynamic '/'
      // strip (session.init.slash_commands, P3). Pin the core commands the
      // kimi-code 0.28.1 ACP catalog always carries so a drifted prune
      // fails loudly.
      final names =
          SnippetPresets.forProfile(ActionBarPresets.kimiCodeId)
              .map((s) => s.name)
              .toSet();
      for (final core
          in {'/compact', '/status', '/usage', '/mcp', '/tasks', '/help'}) {
        expect(names, contains(core));
      }
      // Command detection routes kimi panes to the kimi profile.
      expect(ActionBarPresets.detectProfileId('kimi acp'),
          ActionBarPresets.kimiCodeId);
    });

    test('the global default (general-terminal) has NO presets — the bug', () {
      // This is exactly why the fallback showed nothing: general-terminal is
      // the global default but carries no preset snippets.
      expect(
        SnippetPresets.forProfile(ActionBarPresets.generalTerminalId),
        isEmpty,
      );
    });
  });
}
