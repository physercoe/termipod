import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';
import '../../theme/tokens.dart';

/// ADR-030 W21 — read-only viewer for the `kinds:` block in
/// `<dataRoot>/team/policy.yaml`. Reached via team-settings →
/// Governance → "Governed action policy".
///
/// Read-only by design (per plan §4.3 W21): the policy file is
/// authored by hand and PUT through the existing Policies tab.
/// This screen exists so the principal can VIEW how the system will
/// route propose-decisions (default tier, override allowed, commits)
/// per kind without parsing YAML themselves.
///
/// Hub side returns parsed JSON (via `getPolicyKinds()`) so the
/// Flutter binary doesn't need a YAML parser. Empty result renders
/// an explanatory empty-state pointing the operator at the editable
/// Policies tab.
class GovernedActionsPolicyScreen extends ConsumerStatefulWidget {
  const GovernedActionsPolicyScreen({super.key});

  @override
  ConsumerState<GovernedActionsPolicyScreen> createState() =>
      _GovernedActionsPolicyScreenState();
}

class _GovernedActionsPolicyScreenState
    extends ConsumerState<GovernedActionsPolicyScreen> {
  Map<String, dynamic>? _kinds;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'No active hub connection';
      });
      return;
    }
    try {
      final kinds = await client.getPolicyKinds();
      if (!mounted) return;
      setState(() {
        _kinds = kinds;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Governed action policy',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            tooltip: 'Reload',
            icon: const Icon(Icons.refresh, size: 20),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _body(context),
    );
  }

  Widget _body(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _Empty(
        icon: Icons.error_outline,
        title: 'Could not load policy',
        message: _error!,
      );
    }
    final kinds = _kinds ?? const {};
    if (kinds.isEmpty) {
      return const _Empty(
        icon: Icons.policy_outlined,
        title: 'No `kinds:` block in policy.yaml',
        message:
            'The hub falls back to a permissive default: every governed '
            'action lands on the principal with quorum=1 and override '
            'allowed. To pin per-kind routing, add a `kinds:` block to '
            'team/policy.yaml under the Policies tab.',
      );
    }
    // Stable key ordering so the table doesn't reshuffle on each reload.
    final keys = kinds.keys.toList()..sort();
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      children: [
        _Header(),
        const SizedBox(height: 4),
        for (final k in keys) _KindRow(name: k, policy: kinds[k] as Map),
        const SizedBox(height: 16),
        _Footnote(),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(0, 0, 0, 8),
      child: Row(
        children: [
          Expanded(
              flex: 5,
              child: Text('kind',
                  style: _headerStyle(mutedColor))),
          Expanded(
              flex: 3,
              child: Text('default tier',
                  style: _headerStyle(mutedColor))),
          Expanded(
              flex: 2,
              child:
                  Text('commits', style: _headerStyle(mutedColor))),
          Expanded(
              flex: 2,
              child: Text('override',
                  style: _headerStyle(mutedColor))),
        ],
      ),
    );
  }

  TextStyle _headerStyle(Color color) {
    return GoogleFonts.jetBrainsMono(
      fontSize: FontSizes.label,
      fontWeight: FontWeight.w700,
      color: color,
    );
  }
}

class _KindRow extends StatelessWidget {
  final String name;
  final Map policy;
  const _KindRow({required this.name, required this.policy});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final defaultTier = (policy['default_tier'] ?? '—').toString();
    final commits = policy['commits'] == true;
    final overrideAllowed = policy['override_allowed'] == true;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      padding: const EdgeInsets.symmetric(vertical: 8),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(
            color: mutedColor.withValues(alpha: 0.2),
          ),
        ),
      ),
      child: Row(
        children: [
          Expanded(
            flex: 5,
            child: Text(
              name,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: isDark ? Colors.white : Colors.black87,
              ),
            ),
          ),
          Expanded(
            flex: 3,
            child: Text(
              defaultTier,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: mutedColor,
              ),
            ),
          ),
          Expanded(
            flex: 2,
            child: _BoolDot(value: commits, label: 'yes'),
          ),
          Expanded(
            flex: 2,
            child: _BoolDot(value: overrideAllowed, label: 'allow'),
          ),
        ],
      ),
    );
  }
}

class _BoolDot extends StatelessWidget {
  final bool value;
  final String label;
  const _BoolDot({required this.value, required this.label});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final fg = value
        ? (isDark ? DesignColors.success : DesignColors.successOnLight)
        : (isDark ? DesignColors.textMuted : DesignColors.textMutedLight);
    return Row(
      children: [
        Icon(
          value ? Icons.check_circle : Icons.remove_circle_outline,
          size: 13,
          color: fg,
        ),
        const SizedBox(width: 4),
        Text(
          value ? label : '—',
          style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: fg),
        ),
      ],
    );
  }
}

class _Footnote extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Text(
      'Edit policy.yaml under team settings → Policies. Kinds without '
      'a `default_tier` fall back to "principal" with quorum=1.',
      style: GoogleFonts.jetBrainsMono(fontSize: FontSizes.label, color: mutedColor),
    );
  }
}

class _Empty extends StatelessWidget {
  final IconData icon;
  final String title;
  final String message;
  const _Empty(
      {required this.icon, required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final mutedColor =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(icon, size: 32, color: mutedColor),
          const SizedBox(height: 12),
          Text(
            title,
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(fontSize: 11, color: mutedColor),
          ),
        ],
      ),
    );
  }
}
