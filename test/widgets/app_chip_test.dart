import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/theme/app_theme.dart';
import 'package:termipod/theme/design_colors.dart';
import 'package:termipod/theme/tokens.dart';
import 'package:termipod/widgets/app_chip.dart';

Widget _host(Widget child) => MaterialApp(
      theme: AppTheme.dark,
      home: Scaffold(body: Center(child: child)),
    );

void main() {
  group('AppStatusChip', () {
    testWidgets('renders the label', (tester) async {
      await tester.pumpWidget(_host(
        const AppStatusChip(label: 'active', color: DesignColors.success),
      ));
      expect(find.text('active'), findsOneWidget);
      expect(find.byIcon(Icons.flag), findsNothing);
    });

    testWidgets('renders a leading icon when provided', (tester) async {
      await tester.pumpWidget(_host(
        const AppStatusChip(
          label: 'review',
          color: DesignColors.warning,
          icon: Icons.flag,
        ),
      ));
      expect(find.text('review'), findsOneWidget);
      expect(find.byIcon(Icons.flag), findsOneWidget);
    });
  });

  group('AppChoiceChip', () {
    testWidgets('fires onTap', (tester) async {
      var taps = 0;
      await tester.pumpWidget(_host(
        AppChoiceChip(label: 'all', selected: false, onTap: () => taps++),
      ));
      await tester.tap(find.text('all'));
      expect(taps, 1);
    });

    testWidgets('selected uses a bolder weight than unselected',
        (tester) async {
      await tester.pumpWidget(_host(
        Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            AppChoiceChip(label: 'on', selected: true, onTap: () {}),
            AppChoiceChip(label: 'off', selected: false, onTap: () {}),
          ],
        ),
      ));
      final on = tester.widget<Text>(find.text('on'));
      final off = tester.widget<Text>(find.text('off'));
      expect(on.style!.fontWeight, FontWeight.w700);
      expect(off.style!.fontWeight, FontWeight.w500);
      // Both compose from the label token, not an ad-hoc size.
      expect(on.style!.fontSize, FontSizes.label);
    });
  });
}
