import '../providers/snippet_provider.dart';
import 'action_bar_presets.dart';

/// Preset snippets for each agent profile.
///
/// Snippets follow the CLI-agent slash-command shape of
/// `/cmd +option +argument`. Enumerated options use
/// [SnippetVarKind.option] (dropdown) and free-form arguments use
/// [SnippetVarKind.text] (textfield). Variables marked `optional: true`
/// are collapsed (along with their preceding space) when left blank,
/// so `/compact {{focus}}` with empty focus becomes `/compact` rather
/// than `/compact ` with a trailing space — see [Snippet.resolve].
///
/// These are derived from the current Claude Code and Codex CLI slash
/// command references and are shown in the snippet picker when the
/// matching profile is active. They are NOT stored in SharedPreferences
/// — they're static data.
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
    // -----------------------------------------------------------------------
    // Claude Code — reference: code.claude.com/docs/en/commands
    // -----------------------------------------------------------------------
    ActionBarPresets.claudeCodeId: const [
      // Conversation lifecycle
      Snippet(id: 'preset-cc-clear', name: '/clear', content: '/clear', category: 'claude-code', sendImmediately: true),
      Snippet(
        id: 'preset-cc-compact',
        name: '/compact',
        content: '/compact {{focus}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'focus', hint: 'keep-these-notes (optional)', optional: true),
        ],
      ),
      Snippet(id: 'preset-cc-context', name: '/context', content: '/context', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-rewind', name: '/rewind', content: '/rewind', category: 'claude-code', sendImmediately: true),
      Snippet(
        id: 'preset-cc-resume',
        name: '/resume',
        content: '/resume {{session}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'session', hint: 'session id (optional)', optional: true),
        ],
      ),
      Snippet(id: 'preset-cc-exit', name: '/exit', content: '/exit', category: 'claude-code', sendImmediately: true),

      // Model / effort / mode
      Snippet(
        id: 'preset-cc-model',
        name: '/model',
        content: '/model {{model}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'model',
            kind: SnippetVarKind.option,
            defaultValue: 'default',
            options: ['default', 'opus', 'sonnet', 'haiku'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cc-effort',
        name: '/effort',
        content: '/effort {{level}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'level',
            kind: SnippetVarKind.option,
            defaultValue: 'auto',
            options: ['low', 'medium', 'high', 'max', 'auto'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cc-fast',
        name: '/fast',
        content: '/fast {{state}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'state',
            kind: SnippetVarKind.option,
            defaultValue: 'on',
            options: ['on', 'off'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cc-plan',
        name: '/plan',
        content: '/plan {{description}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'description', hint: 'what to plan (optional)', optional: true),
        ],
      ),

      // Code intel
      Snippet(
        id: 'preset-cc-add-dir',
        name: '/add-dir',
        content: '/add-dir {{path}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'path', hint: 'e.g. packages/api'),
        ],
      ),
      Snippet(id: 'preset-cc-agents', name: '/agents', content: '/agents', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-skills', name: '/skills', content: '/skills', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-hooks', name: '/hooks', content: '/hooks', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-mcp', name: '/mcp', content: '/mcp', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-memory', name: '/memory', content: '/memory', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-init', name: '/init', content: '/init', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-ide', name: '/ide', content: '/ide', category: 'claude-code', sendImmediately: true),

      // Inspection
      Snippet(id: 'preset-cc-diff', name: '/diff', content: '/diff', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-status', name: '/status', content: '/status', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-cost', name: '/cost', content: '/cost', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-doctor', name: '/doctor', content: '/doctor', category: 'claude-code', sendImmediately: true),
      Snippet(
        id: 'preset-cc-copy',
        name: '/copy',
        content: '/copy {{n}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'n', hint: 'entries (optional)', optional: true),
        ],
      ),
      Snippet(
        id: 'preset-cc-export',
        name: '/export',
        content: '/export {{filename}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'filename', hint: 'out.md (optional)', optional: true),
        ],
      ),

      // Settings
      Snippet(id: 'preset-cc-config', name: '/config', content: '/config', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-permissions', name: '/permissions', content: '/permissions', category: 'claude-code', sendImmediately: true),
      Snippet(id: 'preset-cc-theme', name: '/theme', content: '/theme', category: 'claude-code', sendImmediately: true),
      Snippet(
        id: 'preset-cc-color',
        name: '/color',
        content: '/color {{color}}',
        category: 'claude-code',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'color',
            kind: SnippetVarKind.option,
            defaultValue: 'default',
            options: [
              'default',
              'red',
              'blue',
              'green',
              'yellow',
              'purple',
              'orange',
              'pink',
              'cyan',
            ],
          ),
        ],
      ),
      Snippet(id: 'preset-cc-help', name: '/help', content: '/help', category: 'claude-code', sendImmediately: true),
    ],

    // -----------------------------------------------------------------------
    // Steward overlay — starter chips for the always-on team concierge.
    // Surface as preset snippets so the user can edit-in-place via
    // `SnippetsScreen` (override stored in `presetOverrides`) and
    // restore the built-in via swipe-to-reset. Shown on the overlay
    // chip strip (steward_overlay_chips.dart) alongside any user
    // snippets tagged `category: 'steward'`.
    // -----------------------------------------------------------------------
    'steward': const [
      Snippet(
        id: 'preset-stw-insights',
        name: 'Show insights',
        content: 'Show me the insights view',
        category: 'steward',
      ),
      Snippet(
        id: 'preset-stw-blocked',
        name: "What's blocked?",
        content: "What's blocked right now?",
        category: 'steward',
      ),
      Snippet(
        id: 'preset-stw-projects',
        name: 'My projects',
        content: 'Open my projects',
        category: 'steward',
      ),
    ],

    // -----------------------------------------------------------------------
    // Codex — reference: developers.openai.com/codex/guides/slash-commands
    // -----------------------------------------------------------------------
    ActionBarPresets.codexId: const [
      // Conversation lifecycle
      Snippet(id: 'preset-cx-new', name: '/new', content: '/new', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-clear', name: '/clear', content: '/clear', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-compact', name: '/compact', content: '/compact', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-fork', name: '/fork', content: '/fork', category: 'codex', sendImmediately: true),
      Snippet(
        id: 'preset-cx-resume',
        name: '/resume',
        content: '/resume {{session}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'session', hint: 'session id (optional)', optional: true),
        ],
      ),
      Snippet(id: 'preset-cx-quit', name: '/quit', content: '/quit', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-exit', name: '/exit', content: '/exit', category: 'codex', sendImmediately: true),

      // Model / mode
      Snippet(id: 'preset-cx-model', name: '/model', content: '/model', category: 'codex', sendImmediately: true),
      Snippet(
        id: 'preset-cx-fast',
        name: '/fast',
        content: '/fast {{state}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'state',
            kind: SnippetVarKind.option,
            defaultValue: 'status',
            options: ['on', 'off', 'status'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cx-personality',
        name: '/personality',
        content: '/personality {{style}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'style',
            kind: SnippetVarKind.option,
            defaultValue: 'pragmatic',
            options: ['friendly', 'pragmatic', 'none'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cx-permissions',
        name: '/permissions',
        content: '/permissions {{level}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(
            name: 'level',
            kind: SnippetVarKind.option,
            defaultValue: 'Auto',
            options: ['Auto', 'Read Only', 'Full Access'],
          ),
        ],
      ),
      Snippet(
        id: 'preset-cx-plan',
        name: '/plan',
        content: '/plan {{description}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'description', hint: 'what to plan (optional)', optional: true),
        ],
      ),

      // Code intel
      Snippet(
        id: 'preset-cx-mention',
        name: '/mention',
        content: '/mention {{file}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'file', hint: 'path/to/file'),
        ],
      ),
      Snippet(
        id: 'preset-cx-review',
        name: '/review',
        content: '/review {{target}}',
        category: 'codex',
        sendImmediately: true,
        variables: [
          SnippetVariable(name: 'target', hint: 'file or range (optional)', optional: true),
        ],
      ),
      Snippet(id: 'preset-cx-agent', name: '/agent', content: '/agent', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-apps', name: '/apps', content: '/apps', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-init', name: '/init', content: '/init', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-mcp', name: '/mcp', content: '/mcp', category: 'codex', sendImmediately: true),

      // Inspection
      Snippet(id: 'preset-cx-diff', name: '/diff', content: '/diff', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-copy', name: '/copy', content: '/copy', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-status', name: '/status', content: '/status', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-statusline', name: '/statusline', content: '/statusline', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-ps', name: '/ps', content: '/ps', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-debug-config', name: '/debug-config', content: '/debug-config', category: 'codex', sendImmediately: true),

      // Account
      Snippet(id: 'preset-cx-feedback', name: '/feedback', content: '/feedback', category: 'codex', sendImmediately: true),
      Snippet(id: 'preset-cx-logout', name: '/logout', content: '/logout', category: 'codex', sendImmediately: true),
    ],
  };

  /// Human-readable category label for display.
  static String categoryLabel(String category) {
    return switch (category) {
      'claude-code' => 'Claude Code',
      'codex' => 'Codex',
      'steward' => 'Steward',
      'general' => 'General',
      'tmux' => 'Tmux',
      'drafts' => 'Drafts',
      _ => category,
    };
  }
}
