import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/l10n/app_localizations.dart';
import 'package:termipod/screens/terminal/widgets/connection_failure_notice.dart';

void main() {
  testWidgets('retry diagnostic is passive, readable on demand and copyable', (
    tester,
  ) async {
    const error =
        'Connection failed during jump-host forwarding: connection refused';
    final clipboard = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          clipboard.add((call.arguments as Map)['text'] as String);
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    await tester.pumpWidget(
      MaterialApp(
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: Scaffold(
          body: SizedBox(
            width: 320,
            child: Column(
              children: [
                const ConnectionFailureNotice(error: error),
                TextButton(
                  onPressed: () {},
                  child: const Text('Terminal remains usable'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.byType(SelectableText), findsNothing);
    await tester.tap(find.text('Terminal remains usable'));
    expect(tester.takeException(), isNull);
    await tester.tap(find.byTooltip('Details'));
    await tester.pumpAndSettle();
    expect(find.byType(SelectableText), findsOneWidget);
    await tester.tap(find.text('Copy'));
    await tester.pump();
    expect(clipboard, [error]);
    expect(tester.takeException(), isNull);
  });
}
