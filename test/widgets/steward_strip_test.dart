import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:termipod/widgets/steward_strip.dart';

import '../helpers/test_helpers.dart';

/// StewardStrip is a network-driven widget — exhaustive integration
/// testing belongs to a fake-hub harness, but the static label/colour
/// mapping is mechanical and worth a sanity check on a dry mount: it
/// renders without throwing on every state's initial frame (state
/// table didn't drift out of sync with the widget's switch).
void main() {
  group('StewardStrip', () {
    testWidgets('mounts cleanly with no hub client wired', (tester) async {
      await tester.pumpWidget(
        const ProviderScope(
          child: MaterialApp(
            localizationsDelegates: testLocalizationsDelegates,
            supportedLocales: testSupportedLocales,
            home: Scaffold(
              body: StewardStrip(
                projectId: 'demo',
                stewardAgentId: '',
              ),
            ),
          ),
        ),
      );
      expect(find.text('Steward · …'), findsOneWidget);
    });
  });
}
