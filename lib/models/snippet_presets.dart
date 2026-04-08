import '../providers/snippet_provider.dart';
import 'action_bar_presets.dart';

/// Preset snippets for each agent profile.
///
/// These are derived from the old per-profile slashCommands and are shown
/// in the snippet picker when the matching profile is active.
/// They are NOT stored in SharedPreferences — they're static data.
class SnippetPresets {
  SnippetPresets._();

  /// Get preset snippets for a given profile ID.
  /// Returns empty list for unknown profiles.
  static List<Snippet> forProfile(String profileId) {
    return _presets[profileId] ?? const [];
  }

  /// All profile IDs that have preset snippets.
  static Set<String> get profileIds => _presets.keys.toSet();

  static final Map<String, List<Snippet>> _presets = {
    ActionBarPresets.claudeCodeId: const [
      Snippet(id: 'preset-cc-help', name: '/help', content: '/help', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-compact', name: '/compact', content: '/compact', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-clear', name: '/clear', content: '/clear', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-diff', name: '/diff', content: '/diff', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-model', name: '/model', content: '/model', category: 'claude-code'),
      Snippet(id: 'preset-cc-config', name: '/config', content: '/config', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-vim', name: '/vim', content: '/vim', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-theme', name: '/theme', content: '/theme', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-exit', name: '/exit', content: '/exit', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-doctor', name: '/doctor', content: '/doctor', category: 'claude-code', sendImmediately: true),
    ],
    ActionBarPresets.codexId: const [
      Snippet(id: 'preset-cx-permissions', name: '/permissions', content: '/permissions', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-clear', name: '/clear', content: '/clear', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-compact', name: '/compact', content: '/compact', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-diff', name: '/diff', content: '/diff', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-model', name: '/model', content: '/model', category: 'codex'),
      Snippet(id: 'preset-cx-exit', name: '/exit', content: '/exit', category: 'codex', sendImmediately: true),
    ],
    ActionBarPresets.kimiCodeId: const [
      Snippet(id: 'preset-km-help', name: '/help', content: '/help', category: 'kimi-code', sendImmediately: true),
      Snippet(id: 'preset-km-compact', name: '/compact', content: '/compact', category: 'kimi-code', sendImmediately: true),
      Snippet(id: 'preset-km-clear', name: '/clear', content: '/clear', category: 'kimi-code', sendImmediately: true),
      Snippet(id: 'preset-km-model', name: '/model', content: '/model', category: 'kimi-code'),
      Snippet(id: 'preset-km-yolo', name: '/yolo', content: '/yolo', category: 'kimi-code', sendImmediately: true),
      Snippet(id: 'preset-km-web', name: '/web', content: '/web', category: 'kimi-code', sendImmediately: true),
      Snippet(id: 'preset-km-exit', name: '/exit', content: '/exit', category: 'kimi-code', sendImmediately: true),
    ],
    ActionBarPresets.openCodeId: const [
      Snippet(id: 'preset-oc-init', name: '/init', content: '/init', category: 'opencode', sendImmediately: true),
      Snippet(id: 'preset-oc-undo', name: '/undo', content: '/undo', category: 'opencode', sendImmediately: true),
      Snippet(id: 'preset-oc-redo', name: '/redo', content: '/redo', category: 'opencode', sendImmediately: true),
      Snippet(id: 'preset-oc-share', name: '/share', content: '/share', category: 'opencode', sendImmediately: true),
      Snippet(id: 'preset-oc-help', name: '/help', content: '/help', category: 'opencode', sendImmediately: true),
    ],
    ActionBarPresets.aiderId: const [
      Snippet(id: 'preset-ai-help', name: '/help', content: '/help', category: 'aider', sendImmediately: true),
      Snippet(id: 'preset-ai-add', name: '/add', content: '/add ', category: 'aider'),
      Snippet(id: 'preset-ai-drop', name: '/drop', content: '/drop ', category: 'aider'),
      Snippet(id: 'preset-ai-ask', name: '/ask', content: '/ask ', category: 'aider'),
      Snippet(id: 'preset-ai-code', name: '/code', content: '/code ', category: 'aider'),
      Snippet(id: 'preset-ai-architect', name: '/architect', content: '/architect ', category: 'aider'),
      Snippet(id: 'preset-ai-clear', name: '/clear', content: '/clear', category: 'aider', sendImmediately: true),
      Snippet(id: 'preset-ai-diff', name: '/diff', content: '/diff', category: 'aider', sendImmediately: true),
      Snippet(id: 'preset-ai-model', name: '/model', content: '/model ', category: 'aider'),
      Snippet(id: 'preset-ai-run', name: '/run', content: '/run ', category: 'aider'),
      Snippet(id: 'preset-ai-test', name: '/test', content: '/test', category: 'aider', sendImmediately: true),
      Snippet(id: 'preset-ai-undo', name: '/undo', content: '/undo', category: 'aider', sendImmediately: true),
      Snippet(id: 'preset-ai-exit', name: '/exit', content: '/exit', category: 'aider', sendImmediately: true),
    ],
  };

  /// Human-readable category label for display.
  static String categoryLabel(String category) {
    return switch (category) {
      'claude-code' => 'Claude Code',
      'codex' => 'Codex',
      'kimi-code' => 'Kimi Code',
      'opencode' => 'OpenCode',
      'aider' => 'Aider',
      'general' => 'General',
      'tmux' => 'Tmux',
      _ => category,
    };
  }
}
