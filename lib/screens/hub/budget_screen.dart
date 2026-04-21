import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Aggregated view of agent budgets and spend. Pulls rows from
/// `listAgents()` and rolls them up client-side — there is no dedicated
/// server endpoint for budget aggregation today.
class BudgetScreen extends ConsumerStatefulWidget {
  const BudgetScreen({super.key});

  @override
  ConsumerState<BudgetScreen> createState() => _BudgetScreenState();
}

class _BudgetScreenState extends ConsumerState<BudgetScreen> {
  List<Map<String, dynamic>>? _rows;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final rows = await client.listAgents();
      if (!mounted) return;
      setState(() {
        _rows = rows;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          'Usage',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 18, fontWeight: FontWeight.w700),
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _loading ? null : _load,
          ),
        ],
      ),
      body: _loading && _rows == null
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text(
                    _error!,
                    style: GoogleFonts.jetBrainsMono(
                        color: DesignColors.error, fontSize: 12),
                  ),
                )
              : RefreshIndicator(
                  onRefresh: _load,
                  child: _body(),
                ),
    );
  }

  Widget _body() {
    final rows = _rows ?? const <Map<String, dynamic>>[];
    if (rows.isEmpty) {
      final isDark = Theme.of(context).brightness == Brightness.dark;
      return ListView(
        children: [
          const SizedBox(height: 96),
          Icon(
            Icons.account_balance_wallet,
            size: 48,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
          const SizedBox(height: 12),
          Center(
            child: Text(
              'No agent spend yet.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ),
        ],
      );
    }

    // Totals.
    var totalSpent = 0;
    var totalBudget = 0;
    var anyBudget = false;
    for (final r in rows) {
      totalSpent += (r['spent_cents'] as num?)?.toInt() ?? 0;
      final b = (r['budget_cents'] as num?)?.toInt();
      if (b != null) {
        totalBudget += b;
        anyBudget = true;
      }
    }

    // Per-project rollup.
    final perProject = <String, _ProjectAgg>{};
    for (final r in rows) {
      final pid = (r['project_id'] ?? '').toString();
      final agg = perProject.putIfAbsent(pid, _ProjectAgg.new);
      agg.count += 1;
      agg.spent += (r['spent_cents'] as num?)?.toInt() ?? 0;
      final b = (r['budget_cents'] as num?)?.toInt();
      if (b != null) {
        agg.budget = (agg.budget ?? 0) + b;
      }
    }

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      children: [
        _SummaryCard(
          totalSpent: totalSpent,
          totalBudget: totalBudget,
          anyBudget: anyBudget,
        ),
        const SizedBox(height: 20),
        Text(
          'By project',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 4),
        for (final entry in perProject.entries)
          _ProjectRow(
            projectId: entry.key.isEmpty ? '(none)' : entry.key,
            agg: entry.value,
          ),
        const SizedBox(height: 20),
        Text(
          'By agent',
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 4),
        for (final r in rows) _AgentRow(row: r),
      ],
    );
  }
}

class _ProjectAgg {
  int count = 0;
  int spent = 0;
  int? budget;
}

String _fmtDollars(int cents) => '\$${(cents / 100).toStringAsFixed(2)}';

class _SummaryCard extends StatelessWidget {
  final int totalSpent;
  final int totalBudget;
  final bool anyBudget;
  const _SummaryCard({
    required this.totalSpent,
    required this.totalBudget,
    required this.anyBudget,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final pct = anyBudget && totalBudget > 0
        ? (totalSpent / totalBudget).clamp(0.0, 1.0)
        : 0.0;
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: isDark ? DesignColors.borderDark : DesignColors.borderLight,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(child: _Stat(label: 'Spent', value: _fmtDollars(totalSpent))),
              Expanded(
                child: _Stat(
                  label: 'Budget',
                  value: anyBudget ? _fmtDollars(totalBudget) : '—',
                ),
              ),
            ],
          ),
          const SizedBox(height: 14),
          if (anyBudget) ...[
            ClipRRect(
              borderRadius: BorderRadius.circular(4),
              child: LinearProgressIndicator(
                value: pct.toDouble(),
                minHeight: 6,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              '${(pct * 100).round()}% of budget',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
          ] else
            Text(
              'No budgets set',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
            ),
        ],
      ),
    );
  }
}

class _Stat extends StatelessWidget {
  final String label;
  final String value;
  const _Stat({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: GoogleFonts.spaceGrotesk(
            fontSize: 11,
            fontWeight: FontWeight.w600,
            color: isDark
                ? DesignColors.textMuted
                : DesignColors.textMutedLight,
          ),
        ),
        const SizedBox(height: 2),
        Text(
          value,
          style: GoogleFonts.jetBrainsMono(
            fontSize: 20,
            fontWeight: FontWeight.w700,
          ),
        ),
      ],
    );
  }
}

class _ProjectRow extends StatelessWidget {
  final String projectId;
  final _ProjectAgg agg;
  const _ProjectRow({required this.projectId, required this.agg});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final budget = agg.budget;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      title: Text(
        projectId,
        style: GoogleFonts.spaceGrotesk(
            fontSize: 13, fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '${agg.count} agent${agg.count == 1 ? '' : 's'} · ${_fmtDollars(agg.spent)} spent',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
      ),
      trailing: budget != null
          ? Text(
              _fmtDollars(budget),
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            )
          : null,
    );
  }
}

class _AgentRow extends StatelessWidget {
  final Map<String, dynamic> row;
  const _AgentRow({required this.row});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final handle = (row['handle'] ?? '').toString();
    final kind = (row['kind'] ?? '').toString();
    final status = (row['status'] ?? '').toString();
    final spent = (row['spent_cents'] as num?)?.toInt() ?? 0;
    final budget = (row['budget_cents'] as num?)?.toInt();

    IconData icon;
    Color? iconColor;
    if (status == 'active') {
      icon = Icons.play_circle;
      iconColor = Colors.green;
    } else if (status == 'terminated' || status == 'stopped') {
      icon = Icons.stop_circle;
      iconColor =
          isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    } else {
      icon = Icons.circle_outlined;
      iconColor =
          isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    }

    Widget trailing;
    if (budget != null) {
      final pct = budget > 0 ? (spent / budget).clamp(0.0, 1.0) : 0.0;
      trailing = SizedBox(
        width: 80,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: pct.toDouble(),
                minHeight: 4,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              '$spent/$budget¢',
              style: GoogleFonts.jetBrainsMono(
                fontSize: 10,
                color: isDark
                    ? DesignColors.textMuted
                    : DesignColors.textMutedLight,
              ),
              textAlign: TextAlign.end,
            ),
          ],
        ),
      );
    } else {
      trailing = Text(
        'no limit',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 10,
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
      );
    }

    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Icon(icon, color: iconColor),
      title: Text(
        '@$handle',
        style: GoogleFonts.spaceGrotesk(
            fontSize: 13, fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '$kind · ${_fmtDollars(spent)}',
        style: GoogleFonts.jetBrainsMono(
          fontSize: 11,
          color: isDark
              ? DesignColors.textMuted
              : DesignColors.textMutedLight,
        ),
      ),
      trailing: trailing,
    );
  }
}
