// Session-details bottom sheet + the AppBar chip that opens it,
// extracted from agent_feed.dart. The chip renders engine + model +
// permission mode + tools/mcp counts from a session.init payload; tap
// opens a sectioned drawer with the full payload, plus mode/model
// selection rows when the engine advertised them.
//
// Public surface:
//   - SessionInitChip           → the AppBar tile
//   - showSessionDetailsSheet   → opens the drawer (used by the chip and
//                                  by callers that already have the payload)
//   - ModeModelPickerData       → captured mode/model + pick callbacks
//   - showModeModelPickerSheet  → fallback drawer when there's no
//                                  session.init payload but mode/model are
//                                  still advertised
//
// All other widgets / helpers are file-private.
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// Open the session.init details bottom sheet for [payload]. Public so
/// SessionChatScreen can wire its AppBar chip to the same drawer the
/// inline header used to use. [agentKind] surfaces the engine
/// (claude-code, codex, ...) which session.init doesn't carry.
void showSessionDetailsSheet(
  BuildContext context,
  Map<String, dynamic> payload, {
  String? agentKind,
  ModeModelPickerData? modeModel,
}) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (ctx) => _SessionDetailsSheet(
      payload: payload,
      agentKind: agentKind,
      modeModel: modeModel,
    ),
  );
}

/// Compact AppBar chip rendering engine + model + permission mode +
/// tools/mcp counts from a session.init payload. Tap → details sheet.
///
/// [agentKind] is the agent's runtime (claude-code, codex, ...) from
/// the agents table. session.init carries the model (LLM weights) but
/// not the engine that's hosting it; surfacing both lets the operator
/// tell at a glance "this is claude-code running opus 4.7" rather
/// than guessing from the model string.
class SessionInitChip extends StatelessWidget {
  final Map<String, dynamic> payload;
  final String? agentKind;
  // When provided, the details sheet (opened on tap) renders Mode +
  // Model selection sections at the top. This consolidates what used
  // to live behind a separate tune AppBar icon — one entry, one sheet,
  // one cognitive surface for "what is this agent and how is it
  // configured." Pass null when the engine doesn't advertise modes
  // or models and the chip should stay read-only.
  final ModeModelPickerData? modeModel;
  const SessionInitChip({
    super.key,
    required this.payload,
    this.agentKind,
    this.modeModel,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final model = payload['model']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    final tools = _payloadToList(payload['tools']);
    final mcpServers = _payloadToMapList(payload['mcp_servers']);
    return InkWell(
      onTap: () => showSessionDetailsSheet(context, payload,
          agentKind: agentKind, modeModel: modeModel),
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (agentKind != null && agentKind!.isNotEmpty) ...[
              _Pill(
                label: _shortKind(agentKind!),
                color: DesignColors.primary,
              ),
              const SizedBox(width: 4),
            ],
            if (model.isNotEmpty)
              _Pill(
                label: _shortModel(model),
                color: DesignColors.secondary,
              ),
            if (permMode.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(
                label: permMode,
                color: _permModeColor(permMode),
              ),
            ],
            if (tools.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(label: '${tools.length}t', color: mutedColor),
            ],
            if (mcpServers.isNotEmpty) ...[
              const SizedBox(width: 4),
              _Pill(
                label: '${mcpServers.length}mcp',
                color: _mcpAggregateColor(mcpServers, mutedColor),
              ),
            ],
            const SizedBox(width: 2),
            Icon(Icons.expand_more, size: 14, color: mutedColor),
          ],
        ),
      ),
    );
  }

  // Trim the long claude/codex model strings (e.g.
  // "claude-opus-4-7-20260101") down to the family + version so the
  // pill stays readable in the AppBar. Unknown shapes pass through.
  static String _shortModel(String raw) {
    if (raw.startsWith('claude-')) {
      final parts = raw.split('-');
      if (parts.length >= 4) return '${parts[1]} ${parts[2]}.${parts[3]}';
    }
    return raw;
  }

  // Engine names ship as "claude-code" / "codex" / etc. The pill is
  // narrow, so drop the "-code" suffix where it adds no signal.
  static String _shortKind(String raw) {
    if (raw == 'claude-code') return 'claude';
    return raw;
  }
}

/// Bottom-sheet drawer shown when the operator taps the session header
/// chip. Sectioned view of every field session.init exposes; sections
/// absent from the payload (e.g. plugins on a stripped-down driver)
/// just don't render, so the drawer adapts to whatever the driver
/// surfaces. When a [ModeModelPickerData] is supplied, the MODE / MODEL
/// selection sections render above the read-only sections so the
/// "do something" controls reach the user in one scroll.
class _SessionDetailsSheet extends StatelessWidget {
  final Map<String, dynamic> payload;
  final String? agentKind;
  final ModeModelPickerData? modeModel;
  const _SessionDetailsSheet({
    required this.payload,
    this.agentKind,
    this.modeModel,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    final children = <Widget>[];

    void section(String title, Widget body) {
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
        child: Text(
          title,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            fontWeight: FontWeight.w700,
            color: mutedColor,
            letterSpacing: 0.5,
          ),
        ),
      ));
      children.add(Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
        child: body,
      ));
    }

    // Mode + Model sections render at the top so the controls the user
    // most often comes here to change are reachable in one scroll. The
    // subsequent AGENT/WORKDIR/TOOLS/... sections are read-only state;
    // mode/model is the "do something" surface, hence the prominence.
    if (modeModel != null && modeModel!.hasMode) {
      section(
        'MODE',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final opt in modeModel!.availableModes)
              _ModeModelOptionRow(
                label: (opt['name'] ?? opt['id'] ?? '').toString(),
                description: opt['description']?.toString(),
                selected: opt['id']?.toString() == modeModel!.currentMode,
                leading: Icons.tune,
                onTap: () {
                  final id = opt['id']?.toString() ?? '';
                  Navigator.of(context).pop();
                  if (id.isNotEmpty && id != modeModel!.currentMode) {
                    modeModel!.onPickMode(id);
                  }
                },
              ),
          ],
        ),
      );
    }
    if (modeModel != null && modeModel!.hasModel) {
      section(
        'MODEL',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final opt in modeModel!.availableModels)
              _ModeModelOptionRow(
                label: (opt['name'] ?? opt['id'] ?? '').toString(),
                description: opt['description']?.toString(),
                selected: opt['id']?.toString() == modeModel!.currentModel,
                leading: Icons.psychology_alt,
                onTap: () {
                  final id = opt['id']?.toString() ?? '';
                  Navigator.of(context).pop();
                  if (id.isNotEmpty && id != modeModel!.currentModel) {
                    modeModel!.onPickModel(id);
                  }
                },
              ),
          ],
        ),
      );
    }

    final model = payload['model']?.toString() ?? '';
    final version = payload['version']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    final outputStyle = payload['output_style']?.toString() ?? '';
    final cwd = payload['cwd']?.toString() ?? '';
    final sessionId = payload['session_id']?.toString() ?? '';

    final modelLine = [
      if (model.isNotEmpty) model,
      if (version.isNotEmpty) 'v$version',
    ].join(' · ');
    final hasAgentSection = (agentKind != null && agentKind!.isNotEmpty) ||
        modelLine.isNotEmpty ||
        permMode.isNotEmpty ||
        outputStyle.isNotEmpty;
    if (hasAgentSection) {
      section(
        'AGENT',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (agentKind != null && agentKind!.isNotEmpty)
              _kvLine(context, 'engine', agentKind!),
            if (modelLine.isNotEmpty) _kvLine(context, 'model', modelLine),
            if (permMode.isNotEmpty)
              _kvLine(context, 'permission', permMode,
                  valueColor: _permModeColor(permMode)),
            if (outputStyle.isNotEmpty) _kvLine(context, 'style', outputStyle),
            if (sessionId.isNotEmpty) _kvLine(context, 'session', sessionId),
          ],
        ),
      );
    }

    if (cwd.isNotEmpty) {
      section('WORKDIR', _kvLine(context, 'cwd', cwd));
    }

    final tools = _payloadToList(payload['tools']);
    if (tools.isNotEmpty) {
      section('TOOLS · ${tools.length}', _ChipWrap(items: tools));
    }

    final mcp = _payloadToMapList(payload['mcp_servers']);
    if (mcp.isNotEmpty) {
      section(
        'MCP SERVERS · ${mcp.length}',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final s in mcp) _McpRow(server: s),
          ],
        ),
      );
    }

    final slash = _payloadToList(payload['slash_commands']);
    if (slash.isNotEmpty) {
      section('SLASH · ${slash.length}', _ChipWrap(items: slash));
    }

    final agents = _payloadToList(payload['agents']);
    if (agents.isNotEmpty) {
      section('AGENTS · ${agents.length}', _ChipWrap(items: agents));
    }

    final skills = _payloadToList(payload['skills']);
    if (skills.isNotEmpty) {
      section('SKILLS · ${skills.length}', _ChipWrap(items: skills));
    }

    final plugins = _payloadToList(payload['plugins']);
    if (plugins.isNotEmpty) {
      section('PLUGINS · ${plugins.length}', _ChipWrap(items: plugins));
    }

    return SafeArea(
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.of(context).size.height * 0.85,
        ),
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: children,
          ),
        ),
      ),
    );
  }

  Widget _kvLine(BuildContext ctx, String k, String v, {Color? valueColor}) {
    final isDark = Theme.of(ctx).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: RichText(
        text: TextSpan(
          style: GoogleFonts.jetBrainsMono(
            fontSize: 12,
            color: isDark
                ? DesignColors.textPrimary
                : DesignColors.textPrimaryLight,
          ),
          children: [
            TextSpan(text: '$k: ', style: TextStyle(color: muted)),
            TextSpan(
              text: v,
              style: valueColor == null ? null : TextStyle(color: valueColor),
            ),
          ],
        ),
      ),
    );
  }
}

class _ChipWrap extends StatelessWidget {
  final List<String> items;
  const _ChipWrap({required this.items});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final fg = isDark
        ? DesignColors.textSecondary
        : DesignColors.textSecondaryLight;
    final border = isDark
        ? DesignColors.borderDark
        : DesignColors.borderLight;
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [
        for (final it in items)
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(4),
              border: Border.all(color: border),
            ),
            child: Text(
              it,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: fg),
            ),
          ),
      ],
    );
  }
}

class _McpRow extends StatelessWidget {
  final Map<String, dynamic> server;
  const _McpRow({required this.server});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final fg = isDark
        ? DesignColors.textPrimary
        : DesignColors.textPrimaryLight;
    final name = (server['name'] ?? '?').toString();
    final status = (server['status'] ?? '').toString();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Expanded(
            child: Text(
              name,
              style: GoogleFonts.jetBrainsMono(fontSize: 12, color: fg),
            ),
          ),
          if (status.isNotEmpty)
            _Pill(label: status, color: _mcpStatusColor(status)),
        ],
      ),
    );
  }

  static Color _mcpStatusColor(String status) {
    switch (status.toLowerCase()) {
      case 'connected':
      case 'ok':
        return DesignColors.success;
      case 'needs-auth':
      case 'pending-auth':
        return DesignColors.warning;
      case 'failed':
      case 'error':
        return DesignColors.error;
      default:
        return DesignColors.textMuted;
    }
  }
}

class _Pill extends StatelessWidget {
  final String label;
  final Color color;
  const _Pill({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        label,
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

/// ADR-021 W2.5 — captured mode + model state advertised by the agent
/// plus the bound picker callbacks. Lifted out of [AgentFeed] so the
/// SessionChatScreen AppBar can host the picker icon — without it the
/// chip strip cost a row of vertical space above every transcript even
/// for engines that never re-advertise mode/model after handshake.
///
/// `currentMode` / `currentModel` are nullable because some engines
/// only advertise one of the two (e.g. claude exposes model but not a
/// runtime "mode" concept). availableModes / availableModels each list
/// `{id, name, description?}` maps mirroring the ACP shape.
class ModeModelPickerData {
  final String? currentMode;
  final List<Map<String, dynamic>> availableModes;
  final String? currentModel;
  final List<Map<String, dynamic>> availableModels;
  final Future<void> Function(String modeId) onPickMode;
  final Future<void> Function(String modelId) onPickModel;
  const ModeModelPickerData({
    required this.currentMode,
    required this.availableModes,
    required this.currentModel,
    required this.availableModels,
    required this.onPickMode,
    required this.onPickModel,
  });
  bool get hasMode => currentMode != null && availableModes.isNotEmpty;
  bool get hasModel => currentModel != null && availableModels.isNotEmpty;
  bool get hasAny => hasMode || hasModel;

  // Friendly label for the AppBar icon's tooltip / chip subtitle.
  // Falls back to the id when no `name` is advertised.
  String? _labelFor(String? currentId, List<Map<String, dynamic>> options) {
    if (currentId == null) return null;
    for (final o in options) {
      if (o['id']?.toString() == currentId) {
        final name = (o['name'] ?? '').toString();
        return name.isNotEmpty ? name : currentId;
      }
    }
    return currentId;
  }

  String? get currentModeLabel => _labelFor(currentMode, availableModes);
  String? get currentModelLabel => _labelFor(currentModel, availableModels);
}

/// Opens a single bottom-sheet listing both mode and model options
/// (whichever the agent advertises) so the SessionChatScreen AppBar
/// icon collapses to one tap. Each section header is suppressed when
/// that capability is absent. Selecting a row pops the sheet and fires
/// the matching `onPick*` callback — caller's responsibility to surface
/// any error via SnackBar.
///
/// Now a fallback path: when a session.init payload is present, the
/// mode/model sections render inline inside [showSessionDetailsSheet]
/// so users see a single consolidated sheet from the engine chip. This
/// standalone sheet stays for the rare case where the agent advertises
/// modes/models but no session.init has landed yet.
Future<void> showModeModelPickerSheet(
  BuildContext context,
  ModeModelPickerData data,
) {
  return showModalBottomSheet<void>(
    context: context,
    // Without isScrollControlled the sheet's height is capped at half
    // the screen; a long model list (claude has 6+) overflows and the
    // bottom rows are unreachable. With it, the SingleChildScrollView
    // below can grow to fit and the user can scroll.
    isScrollControlled: true,
    showDragHandle: true,
    builder: (sheetCtx) {
      return SafeArea(
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.of(sheetCtx).size.height * 0.85,
          ),
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (data.hasMode) ...[
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
                    child: Text(
                      'Mode',
                      style: Theme.of(sheetCtx).textTheme.titleSmall,
                    ),
                  ),
                  for (final opt in data.availableModes)
                    ListTile(
                      leading: const Icon(Icons.tune, size: 18),
                      title: Text((opt['name'] ?? opt['id'] ?? '').toString()),
                      subtitle: opt['description'] != null
                          ? Text((opt['description']).toString())
                          : null,
                      trailing: (opt['id']?.toString() == data.currentMode)
                          ? const Icon(Icons.check, size: 18)
                          : null,
                      onTap: () {
                        final id = opt['id']?.toString() ?? '';
                        Navigator.of(sheetCtx).pop();
                        if (id.isNotEmpty && id != data.currentMode) {
                          data.onPickMode(id);
                        }
                      },
                    ),
                ],
                if (data.hasMode && data.hasModel) const Divider(height: 1),
                if (data.hasModel) ...[
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
                    child: Text(
                      'Model',
                      style: Theme.of(sheetCtx).textTheme.titleSmall,
                    ),
                  ),
                  for (final opt in data.availableModels)
                    ListTile(
                      leading: const Icon(Icons.psychology_alt, size: 18),
                      title: Text((opt['name'] ?? opt['id'] ?? '').toString()),
                      subtitle: opt['description'] != null
                          ? Text((opt['description']).toString())
                          : null,
                      trailing: (opt['id']?.toString() == data.currentModel)
                          ? const Icon(Icons.check, size: 18)
                          : null,
                      onTap: () {
                        final id = opt['id']?.toString() ?? '';
                        Navigator.of(sheetCtx).pop();
                        if (id.isNotEmpty && id != data.currentModel) {
                          data.onPickModel(id);
                        }
                      },
                    ),
                ],
                const SizedBox(height: 4),
              ],
            ),
          ),
        ),
      );
    },
  );
}

/// One row in the consolidated session-details sheet's MODE / MODEL
/// sections. Shape mirrors a ListTile but with a smaller leading icon
/// + subtle selected-state ring so the row reads as "tap to switch"
/// rather than "permanent label." Subtitle is the engine's optional
/// `description` string; absent means the engine offered an id without
/// human-friendly prose.
class _ModeModelOptionRow extends StatelessWidget {
  final String label;
  final String? description;
  final bool selected;
  final IconData leading;
  final VoidCallback onTap;
  const _ModeModelOptionRow({
    required this.label,
    required this.description,
    required this.selected,
    required this.leading,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return ListTile(
      dense: true,
      contentPadding: EdgeInsets.zero,
      leading: Icon(leading, size: 18),
      title: Text(label),
      subtitle: (description != null && description!.isNotEmpty)
          ? Text(description!)
          : null,
      trailing: selected ? const Icon(Icons.check, size: 18) : null,
      onTap: onTap,
    );
  }
}

// Coerce a JSON-decoded value into a list of strings. Lists of mixed
// types are rendered via toString(); non-list values become an empty
// list. Used to normalize session.init's `tools`, `slash_commands`,
// `agents`, `skills`, `plugins` fields.
List<String> _payloadToList(Object? v) {
  if (v is! List) return const [];
  return [for (final e in v) e.toString()];
}

// Coerce a JSON-decoded value into a list of `Map<String, dynamic>`
// rows. Mixed-type lists drop non-map entries silently; non-list
// values become empty. Used for `mcp_servers`.
List<Map<String, dynamic>> _payloadToMapList(Object? v) {
  if (v is! List) return const [];
  return [
    for (final e in v)
      if (e is Map) e.cast<String, dynamic>(),
  ];
}

// bypassPermissions / acceptEdits / default / plan: only "default"
// and "plan" are restrictive; the others let the agent edit/run
// without prompting and deserve an amber/red pill so the operator
// notices.
Color _permModeColor(String mode) {
  switch (mode) {
    case 'default':
    case 'plan':
      return DesignColors.success;
    case 'acceptEdits':
      return DesignColors.warning;
    case 'bypassPermissions':
      return DesignColors.error;
    default:
      return DesignColors.textMuted;
  }
}

// Aggregate color for the mcp pill: red if any server is in error,
// amber if any needs auth, green if all connected, fallback otherwise.
Color _mcpAggregateColor(
    List<Map<String, dynamic>> servers, Color fallback) {
  var hasError = false;
  var hasNeedsAuth = false;
  var allConnected = true;
  for (final s in servers) {
    final status = (s['status'] ?? '').toString().toLowerCase();
    if (status == 'failed' || status == 'error') {
      hasError = true;
    } else if (status == 'needs-auth' || status == 'pending-auth') {
      hasNeedsAuth = true;
    } else if (status != 'connected' && status != 'ok') {
      allConnected = false;
    }
  }
  if (hasError) return DesignColors.error;
  if (hasNeedsAuth) return DesignColors.warning;
  if (allConnected) return DesignColors.success;
  return fallback;
}
