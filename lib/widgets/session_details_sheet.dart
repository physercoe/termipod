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
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

// ---------------------------------------------------------------------------
// v1.0.706 polish — top-level statusLine accessors for the SESSION STATE
// section of showSessionDetailsSheet. Mirrors the agent_feed.dart pattern of
// @visibleForTesting reducers — keeps the sheet's branch coverage pinable
// without spinning the full widget tree.
//
// Each helper is defensive: claude's statusLine ships these as nested maps
// or bare scalars depending on the field, and older binary versions omit
// them. Empty string / null sentinel collapses the row at the caller cleanly.
// ---------------------------------------------------------------------------

@visibleForTesting
String statusLineEffortLevel(Map<String, dynamic>? statusLine) {
  if (statusLine == null) return '';
  final e = statusLine['effort'];
  if (e is Map) {
    final lvl = e['level'];
    if (lvl is String) return lvl;
  }
  // Older versions emit a bare string `effort: "xhigh"` rather than
  // `{level: "xhigh"}`; accept both shapes.
  if (e is String) return e;
  return '';
}

@visibleForTesting
String statusLineOutputStyleName(Map<String, dynamic>? statusLine) {
  if (statusLine == null) return '';
  final v = statusLine['output_style'];
  if (v is Map) {
    final name = v['name'];
    if (name is String) return name;
  }
  if (v is String) return v;
  return '';
}

/// Returns null when the field is absent (so the row hides); true / false
/// when the wire frame is explicit. The "absent vs explicit false" distinction
/// matters for the "thinking" row — older versions don't ship the field at
/// all, and rendering "thinking: off" on those would be a guess.
@visibleForTesting
bool? statusLineThinkingEnabled(Map<String, dynamic>? statusLine) {
  if (statusLine == null) return null;
  final t = statusLine['thinking'];
  if (t is Map) {
    final en = t['enabled'];
    if (en is bool) return en;
  }
  if (t is bool) return t;
  return null;
}

@visibleForTesting
bool? statusLineFastMode(Map<String, dynamic>? statusLine) {
  if (statusLine == null) return null;
  final v = statusLine['fast_mode'];
  if (v is bool) return v;
  return null;
}

/// Open the session.init details bottom sheet for [payload]. Public so
/// SessionChatScreen can wire its AppBar chip to the same drawer the
/// inline header used to use. [agentKind] surfaces the engine
/// (claude-code, codex, ...) which session.init doesn't carry.
///
/// [statusLine] is the latest status_line frame's payload (v1.0.706
/// polish). The sheet uses it to surface fields that are
/// mutable mid-session and therefore NOT captured by session.init
/// alone: `effort.level`, `output_style.name` (newer than
/// session.init when the user has run `/style`), `thinking.enabled`,
/// `fast_mode`. Nullable for cold-open and engines without
/// statusLine.
void showSessionDetailsSheet(
  BuildContext context,
  Map<String, dynamic> payload, {
  String? agentKind,
  ModeModelPickerData? modeModel,
  Map<String, dynamic>? statusLine,
}) {
  showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (ctx) => _SessionDetailsSheet(
      payload: payload,
      agentKind: agentKind,
      modeModel: modeModel,
      statusLine: statusLine,
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
  // v1.0.706 polish — latest status_line frame payload. The chip
  // itself doesn't render any status_line fields (the AppBar real
  // estate is precious; the dynamic chips live in the telemetry
  // strip below). It's passed through to the sheet on tap so the
  // SESSION STATE section can show live effort / output_style /
  // thinking / fast_mode.
  final Map<String, dynamic>? statusLine;
  const SessionInitChip({
    super.key,
    required this.payload,
    this.agentKind,
    this.modeModel,
    this.statusLine,
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
          agentKind: agentKind, modeModel: modeModel,
          statusLine: statusLine),
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
  final Map<String, dynamic>? statusLine;
  const _SessionDetailsSheet({
    required this.payload,
    this.agentKind,
    this.modeModel,
    this.statusLine,
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
    //
    // Compact pill layout: the previous one-row-per-option list ate
    // ~50px × N (gemini ships 8 models, so the picker drowned the
    // sheet). Wrap of ChoiceChip surfaces every option at a glance,
    // wraps long model names naturally, and is one tap to switch.
    // Description text is parked in the chip's tooltip so power users
    // can long-press for the engine's prose without paying the line
    // cost up front.
    if (modeModel != null && modeModel!.hasMode) {
      section(
        'MODE',
        _ModeModelPicker(
          options: modeModel!.availableModes,
          currentId: modeModel!.currentMode,
          onPick: modeModel!.onPickMode,
        ),
      );
    }
    if (modeModel != null && modeModel!.hasModel) {
      section(
        'MODEL',
        _ModeModelPicker(
          options: modeModel!.availableModels,
          currentId: modeModel!.currentModel,
          onPick: modeModel!.onPickModel,
        ),
      );
    }

    final model = payload['model']?.toString() ?? '';
    final version = payload['version']?.toString() ?? '';
    final permMode = payload['permission_mode']?.toString() ?? '';
    // session.init carries `output_style` as a flat string (the
    // hostrunner stdio driver pulls `frame["output_style"]` → typed
    // event payload). statusLine ships it as a {name: "default"} map
    // and is mid-session-mutable; use statusLine when available so
    // a `/style` toggle surfaces in the sheet.
    final outputStyleFromInit = payload['output_style']?.toString() ?? '';
    final outputStyleFromStatus = _statusLineOutputStyle();
    final outputStyle = outputStyleFromStatus.isNotEmpty
        ? outputStyleFromStatus
        : outputStyleFromInit;
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

    // v1.0.706 polish — live mutable state from claude's statusLine.
    // Section gates on at least one extracted value (a non-claude
    // engine or a cold-open session keeps it hidden). Each row
    // gates independently — claude versions that drop a field still
    // surface the others.
    final effort = _statusLineEffort();
    final thinking = _statusLineThinking();
    final fastMode = _statusLineFastMode();
    final hasStateSection = effort.isNotEmpty ||
        thinking != null ||
        fastMode != null;
    if (hasStateSection) {
      section(
        'SESSION STATE',
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (effort.isNotEmpty)
              _kvLine(context, 'effort', effort,
                  valueColor: _effortColor(effort)),
            if (thinking != null)
              _kvLine(context, 'thinking', thinking ? 'on' : 'off',
                  valueColor: thinking
                      ? DesignColors.success
                      : mutedColor),
            if (fastMode != null)
              _kvLine(context, 'fast mode', fastMode ? 'on' : 'off',
                  valueColor: fastMode
                      ? DesignColors.warning
                      : mutedColor),
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

  // v1.0.706 polish — accessor wrappers delegate to top-level
  // @visibleForTesting helpers (statusLineEffortLevel etc.) so the
  // reducer branch-coverage can be pinned without spinning the full
  // sheet widget tree.
  String _statusLineEffort() => statusLineEffortLevel(statusLine);
  String _statusLineOutputStyle() => statusLineOutputStyleName(statusLine);
  bool? _statusLineThinking() => statusLineThinkingEnabled(statusLine);
  bool? _statusLineFastMode() => statusLineFastMode(statusLine);

  // Effort levels are a small enum claude uses to advertise the
  // depth-of-thought knob: "low" / "medium" / "high" / "xhigh". Tint
  // higher values warmer so the operator can spot an unusually
  // expensive run at a glance without reading the label.
  Color _effortColor(String level) {
    switch (level.toLowerCase()) {
      case 'low':
        return DesignColors.textMuted;
      case 'medium':
        return DesignColors.success;
      case 'high':
        return DesignColors.warning;
      case 'xhigh':
        return DesignColors.error;
      default:
        return DesignColors.textMuted;
    }
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
    isScrollControlled: true,
    showDragHandle: true,
    builder: (sheetCtx) {
      return SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (data.hasMode) ...[
                Padding(
                  padding: const EdgeInsets.fromLTRB(0, 4, 0, 6),
                  child: Text(
                    'Mode',
                    style: Theme.of(sheetCtx).textTheme.titleSmall,
                  ),
                ),
                _ModeModelPicker(
                  options: data.availableModes,
                  currentId: data.currentMode,
                  onPick: data.onPickMode,
                ),
              ],
              if (data.hasMode && data.hasModel)
                const SizedBox(height: 12),
              if (data.hasModel) ...[
                Padding(
                  padding: const EdgeInsets.fromLTRB(0, 4, 0, 6),
                  child: Text(
                    'Model',
                    style: Theme.of(sheetCtx).textTheme.titleSmall,
                  ),
                ),
                _ModeModelPicker(
                  options: data.availableModels,
                  currentId: data.currentModel,
                  onPick: data.onPickModel,
                ),
              ],
            ],
          ),
        ),
      );
    },
  );
}

/// Compact pill picker for ACP availableModes / availableModels.
/// Renders every option as a ChoiceChip in a Wrap so the selector
/// stays under ~50-100px even when the engine ships 8+ models.
/// Selected chip has a filled background; unselected chips are
/// outlined. Tap dismisses the parent sheet AND fires onPick (only
/// when the id actually changes — same behavior as the old row).
/// Long-press / hover surfaces the engine's `description` via the
/// chip tooltip; the ids that already include human-friendly names
/// (e.g. "Auto (Gemini 3)") need no extra prose, so the field is
/// optional in the source data.
class _ModeModelPicker extends StatelessWidget {
  final List<Map<String, dynamic>> options;
  final String? currentId;
  final ValueChanged<String> onPick;
  const _ModeModelPicker({
    required this.options,
    required this.currentId,
    required this.onPick,
  });

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 6,
      runSpacing: 4,
      children: [
        for (final opt in options)
          _buildChip(context, opt),
      ],
    );
  }

  Widget _buildChip(BuildContext context, Map<String, dynamic> opt) {
    // ACP spec: model entries carry `modelId`, mode entries carry `id`.
    // Read modelId first, fall back to id. Without this, kimi-cli's
    // models (which ship only `modelId`) all collapse to an empty id
    // and the tap-handler's `id.isEmpty` guard silently swallows every
    // chip press — no hub-side log, picker looks unresponsive (W7).
    final id = (opt['modelId'] ?? opt['id'] ?? '').toString();
    final label = (opt['name'] ?? opt['modelId'] ?? opt['id'] ?? '').toString();
    final desc = opt['description']?.toString() ?? '';
    final selected = id == currentId;
    final chip = ChoiceChip(
      label: Text(label,
          style: GoogleFonts.jetBrainsMono(fontSize: 12)),
      selected: selected,
      onSelected: (_) {
        Navigator.of(context).pop();
        if (id.isNotEmpty && id != currentId) {
          onPick(id);
        }
      },
      visualDensity: VisualDensity.compact,
      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 0),
    );
    if (desc.isEmpty) return chip;
    return Tooltip(
      message: desc,
      preferBelow: true,
      waitDuration: const Duration(milliseconds: 400),
      child: chip,
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

// Permission-mode color signal — restrictive policies are green,
// open-permission policies are amber/red so the operator notices at
// a glance. Two engines today:
//
//   claude-code values: default / plan / acceptEdits / bypassPermissions
//   codex values:       on-request / on-failure / untrusted / never /
//                       granular (experimental)
//
// Color mapping (semantic equivalence between engines):
//   green  = approval gated for every action (codex on-request,
//            untrusted; claude default, plan)
//   amber  = file edits auto-approved, exec gated (codex on-failure
//            ≈ "ask only when something breaks"; claude acceptEdits)
//   red    = everything auto-approved (codex never; claude
//            bypassPermissions). Operator should see this in red
//            because the agent can edit + run anything unattended.
//   muted  = unknown / granular / engine variants we haven't mapped.
Color _permModeColor(String mode) {
  switch (mode) {
    // claude-code
    case 'default':
    case 'plan':
    // codex
    case 'on-request':
    case 'untrusted':
      return DesignColors.success;
    // claude-code
    case 'acceptEdits':
    // codex
    case 'on-failure':
      return DesignColors.warning;
    // claude-code
    case 'bypassPermissions':
    // codex
    case 'never':
      return DesignColors.error;
    default:
      // granular (codex experimental) + anything new lands here —
      // muted, no color signal, until we explicitly classify.
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
