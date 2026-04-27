import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:termipod/l10n/app_localizations.dart';

import '../../providers/hub_provider.dart';
import '../../services/steward_handle.dart';
import '../../theme/design_colors.dart';

/// Team Settings → Steward Config per `docs/ia-redesign.md` §11 Wedge 7.
///
/// Surfaces the steward's director-controlled knobs: autonomy level,
/// budget cap, scope allowlist, and model selection. Values are
/// persisted to SharedPreferences so the form has meaningful state
/// today; the hub API for mutating steward config lands in a follow-up
/// (values here will round-trip via that endpoint once it exists).
///
/// The "Current steward" panel at the top is live — reads from
/// `hubProvider.agents` and shows which steward (if any) is running.
class StewardConfigScreen extends ConsumerStatefulWidget {
  const StewardConfigScreen({super.key});

  @override
  ConsumerState<StewardConfigScreen> createState() =>
      _StewardConfigScreenState();
}

class _StewardConfigScreenState extends ConsumerState<StewardConfigScreen> {
  static const _kAutonomy = 'steward.autonomy';
  static const _kBudgetCap = 'steward.budget_cap_usd';
  static const _kScope = 'steward.scope_allowlist';
  static const _kModel = 'steward.model';

  static const _autonomyLevels = <String>[
    'observe',
    'suggest',
    'act_with_approval',
    'autonomous',
  ];

  static const _modelOptions = <String>[
    'claude-opus-4-7',
    'claude-sonnet-4-6',
    'claude-haiku-4-5',
  ];

  String _autonomy = 'suggest';
  double _budgetCap = 10.0;
  String _model = 'claude-sonnet-4-6';
  final _scopeCtrl = TextEditingController();
  bool _loaded = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _scopeCtrl.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _autonomy = prefs.getString(_kAutonomy) ?? 'suggest';
      _budgetCap = prefs.getDouble(_kBudgetCap) ?? 10.0;
      _model = prefs.getString(_kModel) ?? 'claude-sonnet-4-6';
      _scopeCtrl.text = prefs.getString(_kScope) ?? '';
      _loaded = true;
    });
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_kAutonomy, _autonomy);
    await prefs.setDouble(_kBudgetCap, _budgetCap);
    await prefs.setString(_kModel, _model);
    await prefs.setString(_kScope, _scopeCtrl.text);
    if (!mounted) return;
    final l10n = AppLocalizations.of(context)!;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(l10n.stewardConfigSaved)),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    if (!_loaded) {
      return const Scaffold(
        body: Center(child: CircularProgressIndicator()),
      );
    }

    final hub = ref.watch(hubProvider).value;
    final steward = _findSteward(hub?.agents ?? const []);

    return Scaffold(
      appBar: AppBar(
        title: Text(
          l10n.stewardConfigTitle,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          TextButton(
            onPressed: _save,
            child: Text(l10n.stewardConfigSave),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _CurrentStewardCard(steward: steward),
          const SizedBox(height: 20),
          _SectionLabel(text: l10n.stewardConfigAutonomyLabel),
          const SizedBox(height: 6),
          _AutonomyPicker(
            selected: _autonomy,
            levels: _autonomyLevels,
            onChanged: (v) => setState(() => _autonomy = v),
          ),
          const SizedBox(height: 20),
          _SectionLabel(text: l10n.stewardConfigBudgetLabel),
          const SizedBox(height: 6),
          Row(
            children: [
              Expanded(
                child: Slider(
                  value: _budgetCap.clamp(0, 100),
                  min: 0,
                  max: 100,
                  divisions: 20,
                  label: '\$${_budgetCap.toStringAsFixed(0)}',
                  onChanged: (v) => setState(() => _budgetCap = v),
                ),
              ),
              SizedBox(
                width: 64,
                child: Text(
                  '\$${_budgetCap.toStringAsFixed(0)}',
                  textAlign: TextAlign.right,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 20),
          _SectionLabel(text: l10n.stewardConfigScopeLabel),
          const SizedBox(height: 6),
          TextField(
            controller: _scopeCtrl,
            minLines: 3,
            maxLines: 6,
            style: GoogleFonts.jetBrainsMono(fontSize: 12),
            decoration: InputDecoration(
              hintText: l10n.stewardConfigScopeHint,
              border: const OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 20),
          _SectionLabel(text: l10n.stewardConfigModelLabel),
          const SizedBox(height: 6),
          DropdownButtonFormField<String>(
            initialValue: _model,
            items: [
              for (final m in _modelOptions)
                DropdownMenuItem(value: m, child: Text(m)),
            ],
            onChanged: (v) {
              if (v != null) setState(() => _model = v);
            },
            decoration: const InputDecoration(
              border: OutlineInputBorder(),
              isDense: true,
            ),
          ),
          const SizedBox(height: 24),
          Text(
            l10n.stewardConfigLocalOnlyNote,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 11,
              color: DesignColors.textMuted,
              fontStyle: FontStyle.italic,
            ),
          ),
        ],
      ),
    );
  }

  /// Picks the steward most likely to be the *current* one. The agents
  /// list is ordered by created_at, so a previously-terminated steward
  /// row sits before a freshly-spawned pending one — returning the
  /// first match would leave this screen showing "No steward is running"
  /// while the AppBar chip (which iterates with state-aware logic)
  /// already shows "starting". Prefer running > pending > everything
  /// else, then falls back to the most recent row by handle.
  static Map<String, dynamic>? _findSteward(
      List<Map<String, dynamic>> agents) {
    Map<String, dynamic>? running;
    Map<String, dynamic>? pending;
    Map<String, dynamic>? other;
    for (final a in agents) {
      if (!isStewardHandle((a['handle'] ?? '').toString())) continue;
      final status = (a['status'] ?? '').toString();
      if (status == 'running') {
        running = a;
      } else if (status == 'pending') {
        pending ??= a;
      } else {
        other = a; // keep the latest non-live match as a fallback
      }
    }
    return running ?? pending ?? other;
  }
}

class _CurrentStewardCard extends StatelessWidget {
  final Map<String, dynamic>? steward;
  const _CurrentStewardCard({this.steward});

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final status = (steward?['status'] ?? 'none').toString();
    final hostId = (steward?['host_id'] ?? '').toString();
    final running =
        steward != null && (status == 'running' || status == 'pending');

    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: border),
      ),
      child: Row(
        children: [
          Icon(
            Icons.smart_toy_outlined,
            size: 28,
            color: running ? DesignColors.primary : DesignColors.textMuted,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  running
                      ? l10n.stewardConfigRunning
                      : l10n.stewardConfigNotRunning,
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (steward != null)
                  Text(
                    'status=$status${hostId.isEmpty ? '' : '  host=$hostId'}',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      color: DesignColors.textMuted,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel({required this.text});

  @override
  Widget build(BuildContext context) {
    return Text(
      text.toUpperCase(),
      style: GoogleFonts.spaceGrotesk(
        fontSize: 11,
        fontWeight: FontWeight.w700,
        letterSpacing: 0.8,
        color: DesignColors.textMuted,
      ),
    );
  }
}

class _AutonomyPicker extends StatelessWidget {
  final String selected;
  final List<String> levels;
  final ValueChanged<String> onChanged;
  const _AutonomyPicker({
    required this.selected,
    required this.levels,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final lvl in levels)
          ChoiceChip(
            label: Text(lvl),
            selected: selected == lvl,
            onSelected: (_) => onChanged(lvl),
          ),
      ],
    );
  }
}
