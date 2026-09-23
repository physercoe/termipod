import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/providers/ssh_provider.dart';
import 'package:termipod/screens/web_services/web_services_screen.dart';

import '../helpers/test_helpers.dart';

class _DisconnectedSsh extends SshNotifier {
  _DisconnectedSsh() : super('test');
  @override
  SshState build() => const SshState();
}

void main() {
  testWidgets(
    'service form defaults to HTTP port 8080 and rejects invalid ports',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      await tester.pumpWidget(
        ProviderScope(
          overrides: [sshProvider('test').overrideWith(_DisconnectedSsh.new)],
          child: MaterialApp(
            localizationsDelegates: testLocalizationsDelegates,
            supportedLocales: testSupportedLocales,
            home: const WebServicesScreen(connectionId: 'test'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(
        find.text('No tunnels running. Start a service to view it here.'),
        findsOneWidget,
      );
      await tester.tap(find.text('Add service'));
      await tester.pumpAndSettle();
      expect(find.text('8080'), findsOneWidget);
      await tester.enterText(find.byType(TextFormField).first, '65536');
      await tester.tap(find.text('Start & Open'));
      await tester.pumpAndSettle();
      expect(find.text('Enter a port from 1 to 65535.'), findsOneWidget);
      expect(find.byType(AlertDialog), findsOneWidget);
      expect(find.byType(SnackBar), findsNothing);
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
    },
  );
}
