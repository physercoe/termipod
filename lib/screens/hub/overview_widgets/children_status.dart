import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../../theme/design_colors.dart';
import 'registry.dart';

/// `children_status` placeholder. The real sub-project aggregation is
/// W5's scope; W4 only wires the registry entry so a template can opt in
/// now and light up automatically when W5 ships.
class ChildrenStatusHero extends StatelessWidget {
  final OverviewContext ctx;
  const ChildrenStatusHero({super.key, required this.ctx});

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 16),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: border),
      ),
      child: Row(
        children: [
          const Icon(Icons.account_tree_outlined,
              size: 18, color: DesignColors.textMuted),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Sub-projects will appear here after W5 ships.',
              style: GoogleFonts.spaceGrotesk(
                fontSize: 12,
                color: DesignColors.textMuted,
                fontStyle: FontStyle.italic,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
