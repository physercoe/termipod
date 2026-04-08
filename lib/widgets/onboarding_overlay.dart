import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../theme/design_colors.dart';

/// Key for SharedPreferences to track if terminal onboarding was shown
const _onboardingShownKey = 'onboarding_terminal_shown';

/// Onboarding card data
class _OnboardingCard {
  final IconData icon;
  final String title;
  final String description;
  const _OnboardingCard(this.icon, this.title, this.description);
}

const _cards = [
  _OnboardingCard(
    Icons.edit_note,
    'Compose Bar',
    'Type commands in the text field at the bottom. Press send or Enter to execute.',
  ),
  _OnboardingCard(
    Icons.swipe,
    'Action Bar',
    'Swipe left/right on the toolbar buttons to see more groups. Tap a button to send its key.',
  ),
  _OnboardingCard(
    Icons.add_circle_outline,
    'Insert Menu',
    'Tap [+] to insert snippets, commands from history, or switch input mode.',
  ),
  _OnboardingCard(
    Icons.more_vert,
    'Terminal Menu',
    'Tap the menu icon for scroll mode, zoom, help, settings, and disconnect.',
  ),
];

/// Check if onboarding should be shown and display it if needed.
///
/// Call from terminal screen's initState (via post-frame callback).
Future<void> maybeShowOnboarding(BuildContext context) async {
  final prefs = await SharedPreferences.getInstance();
  if (prefs.getBool(_onboardingShownKey) == true) return;

  if (!context.mounted) return;

  await showDialog(
    context: context,
    barrierDismissible: true,
    barrierColor: Colors.black87,
    builder: (context) => const _OnboardingDialog(),
  );

  await prefs.setBool(_onboardingShownKey, true);
}

class _OnboardingDialog extends StatefulWidget {
  const _OnboardingDialog();

  @override
  State<_OnboardingDialog> createState() => _OnboardingDialogState();
}

class _OnboardingDialogState extends State<_OnboardingDialog> {
  int _currentIndex = 0;
  final _pageController = PageController();

  @override
  void dispose() {
    _pageController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Container(
        margin: const EdgeInsets.symmetric(horizontal: 32),
        constraints: const BoxConstraints(maxWidth: 400),
        decoration: BoxDecoration(
          color: DesignColors.surfaceDark,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: DesignColors.primary.withValues(alpha: 0.3),
          ),
          boxShadow: [
            BoxShadow(
              color: DesignColors.primary.withValues(alpha: 0.2),
              blurRadius: 30,
              spreadRadius: 2,
            ),
          ],
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Header
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 24, 24, 0),
              child: Row(
                children: [
                  Icon(Icons.waving_hand, color: DesignColors.primary, size: 24),
                  const SizedBox(width: 10),
                  Text(
                    'Welcome to MuxPod',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 18,
                      fontWeight: FontWeight.w700,
                      color: Colors.white,
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 8),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: Text(
                'Quick tour of the terminal interface',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 13,
                  color: Colors.white54,
                ),
              ),
            ),
            const SizedBox(height: 16),
            // Page view
            SizedBox(
              height: 160,
              child: PageView.builder(
                controller: _pageController,
                itemCount: _cards.length,
                onPageChanged: (i) => setState(() => _currentIndex = i),
                itemBuilder: (context, index) {
                  final card = _cards[index];
                  return Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 24),
                    child: Column(
                      children: [
                        Container(
                          width: 56,
                          height: 56,
                          decoration: BoxDecoration(
                            color: DesignColors.primary.withValues(alpha: 0.15),
                            borderRadius: BorderRadius.circular(16),
                          ),
                          child: Icon(
                            card.icon,
                            color: DesignColors.primary,
                            size: 28,
                          ),
                        ),
                        const SizedBox(height: 16),
                        Text(
                          card.title,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 16,
                            fontWeight: FontWeight.w700,
                            color: Colors.white,
                          ),
                        ),
                        const SizedBox(height: 8),
                        Text(
                          card.description,
                          textAlign: TextAlign.center,
                          style: GoogleFonts.spaceGrotesk(
                            fontSize: 13,
                            color: Colors.white70,
                            height: 1.4,
                          ),
                        ),
                      ],
                    ),
                  );
                },
              ),
            ),
            // Dots
            Row(
              mainAxisAlignment: MainAxisAlignment.center,
              children: List.generate(_cards.length, (i) {
                return Container(
                  margin: const EdgeInsets.symmetric(horizontal: 4),
                  width: _currentIndex == i ? 20 : 8,
                  height: 8,
                  decoration: BoxDecoration(
                    color: _currentIndex == i
                        ? DesignColors.primary
                        : Colors.white24,
                    borderRadius: BorderRadius.circular(4),
                  ),
                );
              }),
            ),
            const SizedBox(height: 20),
            // Button
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
              child: SizedBox(
                width: double.infinity,
                child: ElevatedButton(
                  onPressed: () {
                    if (_currentIndex < _cards.length - 1) {
                      _pageController.nextPage(
                        duration: const Duration(milliseconds: 300),
                        curve: Curves.easeInOut,
                      );
                    } else {
                      Navigator.of(context).pop();
                    }
                  },
                  style: ElevatedButton.styleFrom(
                    backgroundColor: DesignColors.primary,
                    foregroundColor: Colors.white,
                    padding: const EdgeInsets.symmetric(vertical: 14),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  child: Text(
                    _currentIndex < _cards.length - 1 ? 'Next' : 'Got it!',
                    style: GoogleFonts.spaceGrotesk(
                      fontSize: 15,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
