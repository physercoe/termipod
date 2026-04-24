import 'package:flutter/material.dart';

import '../../../widgets/sweep_scatter.dart';
import 'registry.dart';

/// `sweep_compare` hero. Thin wrapper around the existing [SweepScatter]
/// chart — W4 pulls the template selection into YAML without redesigning
/// the chart itself (the ML demo's Overview look is locked).
class SweepCompareHero extends StatelessWidget {
  final OverviewContext ctx;
  const SweepCompareHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    if (ctx.projectId.isEmpty) return const SizedBox.shrink();
    return SweepScatter(projectId: ctx.projectId);
  }
}
