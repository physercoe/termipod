import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../models/action_bar_config.dart';
import '../providers/action_bar_provider.dart';
import '../theme/design_colors.dart';

/// Tmux cheat sheet entry
class _TmuxEntry {
  final String key;
  final String description;
  const _TmuxEntry(this.key, this.description);
}

/// Resolve the text the action-bar cheat sheet should render for a
/// given button. Prefers the button's own [ActionBarButton.description]
/// when set, then the shared [ActionBarButton.defaultDescriptions]
/// lookup (also used by the settings picker), and finally falls back
/// to the button's raw value so nothing ever renders blank.
String _describeButton(ActionBarButton button) {
  final own = button.description;
  if (own != null && own.trim().isNotEmpty) return own;
  final mapped = ActionBarButton.defaultDescriptions[button.value];
  if (mapped != null && mapped.isNotEmpty) return mapped;
  return button.value;
}

/// Tmux cheat sheet categories
const _tmuxCheatSheet = <String, List<_TmuxEntry>>{
  'Windows': [
    _TmuxEntry('C-b c', 'New window'),
    _TmuxEntry('C-b n', 'Next window'),
    _TmuxEntry('C-b p', 'Previous window'),
    _TmuxEntry('C-b 0-9', 'Select window by number'),
    _TmuxEntry('C-b w', 'List windows'),
    _TmuxEntry('C-b ,', 'Rename window'),
    _TmuxEntry('C-b &', 'Kill window'),
  ],
  'Panes': [
    _TmuxEntry('C-b %', 'Split vertical'),
    _TmuxEntry('C-b "', 'Split horizontal'),
    _TmuxEntry('C-b o', 'Next pane'),
    _TmuxEntry('C-b ;', 'Last active pane'),
    _TmuxEntry('C-b arrows', 'Navigate panes'),
    _TmuxEntry('C-b x', 'Kill pane'),
    _TmuxEntry('C-b z', 'Toggle zoom'),
    _TmuxEntry('C-b {', 'Move pane left'),
    _TmuxEntry('C-b }', 'Move pane right'),
  ],
  'Sessions': [
    _TmuxEntry('C-b d', 'Detach'),
    _TmuxEntry('C-b s', 'List sessions'),
    _TmuxEntry('C-b \$', 'Rename session'),
    _TmuxEntry('C-b (', 'Previous session'),
    _TmuxEntry('C-b )', 'Next session'),
  ],
  'Copy Mode': [
    _TmuxEntry('C-b [', 'Enter copy mode'),
    _TmuxEntry('q', 'Exit copy mode'),
    _TmuxEntry('Space', 'Start selection'),
    _TmuxEntry('Enter', 'Copy selection'),
    _TmuxEntry('C-b ]', 'Paste buffer'),
  ],
  'Misc': [
    _TmuxEntry('C-b :', 'Command prompt'),
    _TmuxEntry('C-b t', 'Show clock'),
    _TmuxEntry('C-b ?', 'List key bindings'),
    _TmuxEntry('C-b i', 'Display info'),
  ],
};

/// Show the help bottom sheet
void showHelpSheet(BuildContext context, WidgetRef ref) {
  final isDark = Theme.of(context).brightness == Brightness.dark;
  final bgColor = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;

  showModalBottomSheet(
    context: context,
    backgroundColor: bgColor,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (context) => DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.7,
      minChildSize: 0.4,
      maxChildSize: 0.9,
      builder: (context, scrollController) => _HelpSheetContent(
        scrollController: scrollController,
        ref: ref,
      ),
    ),
  );
}

class _HelpSheetContent extends StatelessWidget {
  final ScrollController scrollController;
  final WidgetRef ref;

  const _HelpSheetContent({
    required this.scrollController,
    required this.ref,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;
    final mutedColor = isDark ? Colors.white38 : Colors.black38;

    return DefaultTabController(
      length: 3,
      child: Column(
        children: [
          // Handle
          Center(
            child: Container(
              margin: const EdgeInsets.only(top: 8),
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: mutedColor,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          // Title
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Icon(Icons.help_outline, color: DesignColors.primary),
                const SizedBox(width: 8),
                Text(
                  'Help',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 18,
                    fontWeight: FontWeight.w700,
                    color: textColor,
                  ),
                ),
              ],
            ),
          ),
          // Tabs
          TabBar(
            labelColor: DesignColors.primary,
            unselectedLabelColor: mutedColor,
            indicatorColor: DesignColors.primary,
            labelStyle: GoogleFonts.spaceGrotesk(
              fontSize: 13,
              fontWeight: FontWeight.w700,
            ),
            tabs: const [
              Tab(text: 'ACTION BAR'),
              Tab(text: 'CONTROLS'),
              Tab(text: 'TMUX'),
            ],
          ),
          // Tab content
          Expanded(
            child: TabBarView(
              children: [
                _buildActionBarTab(context),
                _buildGesturesTab(context),
                _buildTmuxTab(context),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildActionBarTab(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;
    final mutedColor = isDark ? Colors.white54 : Colors.black54;
    final actionBarState = ref.read(actionBarProvider);
    final groups = actionBarState.activeGroups;

    return ListView.builder(
      controller: scrollController,
      padding: const EdgeInsets.all(16),
      itemCount: groups.length,
      itemBuilder: (context, groupIndex) {
        final group = groups[groupIndex];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (groupIndex > 0) const SizedBox(height: 16),
            Text(
              group.name.toUpperCase(),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 1.5,
                color: DesignColors.primary,
              ),
            ),
            const SizedBox(height: 8),
            ...group.buttons.map((button) => _buildButtonRow(
              button,
              textColor,
              mutedColor,
            )),
          ],
        );
      },
    );
  }

  Widget _buildButtonRow(
    ActionBarButton button,
    Color textColor,
    Color mutedColor,
  ) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          Container(
            width: 48,
            padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
            decoration: BoxDecoration(
              color: DesignColors.primary.withValues(alpha: 0.15),
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              button.label,
              textAlign: TextAlign.center,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 12,
                fontWeight: FontWeight.w600,
                color: DesignColors.primary,
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              _describeButton(button),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 13,
                color: textColor,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildGesturesTab(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;

    const gestureCheatSheet = <String, List<_TmuxEntry>>{
      'Terminal (Normal Mode)': [
        _TmuxEntry('Scroll', 'Scroll terminal output up/down'),
        _TmuxEntry('Pinch', 'Zoom in/out (adjusts font size)'),
        _TmuxEntry('2-Finger Swipe', 'Navigate between panes'),
        _TmuxEntry('Hold + Drag', 'Arrow keys (repeatable)'),
        _TmuxEntry('Double Tap', 'Toggle direct input mode'),
        _TmuxEntry('Tap', 'Focus terminal / show scroll button'),
      ],
      'Gesture Mode': [
        _TmuxEntry('Swipe', 'Arrow keys (left/right/up/down)'),
        _TmuxEntry('Double Tap', 'Tab key'),
        _TmuxEntry('2-Finger Tap', 'Enter key'),
        _TmuxEntry('3-Finger Tap', 'Escape key'),
        _TmuxEntry('Long Press', 'Paste from clipboard'),
      ],
      'Navigation Pad': [
        _TmuxEntry('D-pad / Joystick', 'Arrow keys (hold to repeat)'),
        _TmuxEntry('Action Buttons', '4 customizable keys (default: ESC, TAB, C-C, ENT)'),
        _TmuxEntry('Chevron ›', 'Cycle: compact > off > compact'),
      ],
      'Floating Joystick': [
        _TmuxEntry('Tap Zone', 'Arrow key (Up/Down/Left/Right)'),
        _TmuxEntry('Tap Center', 'Enter key'),
        _TmuxEntry('Long Press', 'Auto-repeat arrow or Enter'),
        _TmuxEntry('Drag', 'Reposition on screen'),
      ],
      'Compose Bar': [
        _TmuxEntry('[+] Tap', 'Insert menu (files, images, input mode)'),
        _TmuxEntry('[+] Long Press', 'Snippet picker & command history'),
        _TmuxEntry('Send Button', 'Execute command (Enter)'),
      ],
      'Scroll Mode': [
        _TmuxEntry('Swipe Up/Down', 'Scroll terminal output (enters tmux copy mode)'),
        _TmuxEntry('Toggle', 'Terminal menu or bottom bar indicator'),
      ],
    };

    final categories = gestureCheatSheet.entries.toList();

    return ListView.builder(
      controller: scrollController,
      padding: const EdgeInsets.all(16),
      itemCount: categories.length,
      itemBuilder: (context, index) {
        final category = categories[index];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (index > 0) const SizedBox(height: 16),
            Text(
              category.key.toUpperCase(),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 1.5,
                color: DesignColors.primary,
              ),
            ),
            const SizedBox(height: 8),
            ...category.value.map((entry) => Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                children: [
                  Container(
                    width: 110,
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
                    decoration: BoxDecoration(
                      color: DesignColors.primary.withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(
                      entry.key,
                      textAlign: TextAlign.center,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: DesignColors.primary,
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      entry.description,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        color: textColor,
                      ),
                    ),
                  ),
                ],
              ),
            )),
          ],
        );
      },
    );
  }

  Widget _buildTmuxTab(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;

    final categories = _tmuxCheatSheet.entries.toList();

    return ListView.builder(
      controller: scrollController,
      padding: const EdgeInsets.all(16),
      itemCount: categories.length,
      itemBuilder: (context, index) {
        final category = categories[index];
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (index > 0) const SizedBox(height: 16),
            Text(
              category.key.toUpperCase(),
              style: GoogleFonts.spaceGrotesk(
                fontSize: 11,
                fontWeight: FontWeight.w700,
                letterSpacing: 1.5,
                color: DesignColors.primary,
              ),
            ),
            const SizedBox(height: 8),
            ...category.value.map((entry) => Padding(
              padding: const EdgeInsets.symmetric(vertical: 4),
              child: Row(
                children: [
                  Container(
                    width: 80,
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 3),
                    decoration: BoxDecoration(
                      color: DesignColors.primary.withValues(alpha: 0.15),
                      borderRadius: BorderRadius.circular(4),
                    ),
                    child: Text(
                      entry.key,
                      textAlign: TextAlign.center,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: DesignColors.primary,
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(
                      entry.description,
                      style: GoogleFonts.spaceGrotesk(
                        fontSize: 13,
                        color: textColor,
                      ),
                    ),
                  ),
                ],
              ),
            )),
          ],
        );
      },
    );
  }
}
