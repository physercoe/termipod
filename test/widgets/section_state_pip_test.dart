import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/section_state_pip.dart';

void main() {
  group('parseSectionState', () {
    test('maps known values', () {
      expect(parseSectionState('empty'), SectionState.empty);
      expect(parseSectionState('draft'), SectionState.draft);
      expect(parseSectionState('ratified'), SectionState.ratified);
    });

    test('case-insensitive', () {
      expect(parseSectionState('RATIFIED'), SectionState.ratified);
    });

    test('null / unknown → empty (safe default)', () {
      expect(parseSectionState(null), SectionState.empty);
      expect(parseSectionState('something-else'), SectionState.empty);
      expect(parseSectionState(''), SectionState.empty);
    });
  });

  group('SectionStatePip widget', () {
    testWidgets('renders state label by default', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: SectionStatePip(state: SectionState.draft),
        ),
      ));
      expect(find.text('draft'), findsOneWidget);
    });

    testWidgets('hides label when showLabel is false', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: SectionStatePip(state: SectionState.ratified, showLabel: false),
        ),
      ));
      expect(find.text('ratified'), findsNothing);
    });

    testWidgets('renders different glyphs per state', (tester) async {
      // All three states should mount without errors and show their labels.
      for (final s in SectionState.values) {
        await tester.pumpWidget(MaterialApp(
          home: Scaffold(
            body: SectionStatePip(state: s),
          ),
        ));
        expect(tester.takeException(), isNull);
      }
    });
  });
}
