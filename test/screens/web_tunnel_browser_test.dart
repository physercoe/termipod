import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/ssh_provider.dart';
import 'package:termipod/providers/web_tunnel_provider.dart';
import 'package:termipod/screens/web_services/web_tunnel_browser.dart';
import 'package:termipod/services/ssh/local_forward.dart';
import 'package:termipod/services/ssh/ssh_client.dart';

import '../helpers/test_helpers.dart';

class _Forward implements LocalForward {
  @override
  int get port => 32123;
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Ssh extends SshNotifier {
  _Ssh() : super('test');
  @override
  SshState build() =>
      const SshState(connectionState: SshConnectionState.connected);
}

class _Tunnels extends WebTunnelNotifier {
  _Tunnels(this.tunnel) : super('test');
  final WebTunnel tunnel;
  @override
  List<WebTunnel> build() => [tunnel];
}

void main() {
  const channel = MethodChannel('termipod/web_browser');
  TestWidgetsFlutterBinding.ensureInitialized();
  late WebTunnel tunnel;
  setUp(() {
    tunnel = WebTunnel(
      id: '1',
      remoteHost: 'localhost',
      remotePort: 8080,
      path: '/',
      name: 'Test',
      forward: _Forward(),
    );
  });
  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });
  Future<void> mount(WidgetTester tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          sshProvider('test').overrideWith(_Ssh.new),
          webTunnelProvider('test').overrideWith(() => _Tunnels(tunnel)),
        ],
        child: MaterialApp(
          localizationsDelegates: testLocalizationsDelegates,
          supportedLocales: testSupportedLocales,
          home: WebTunnelBrowser(connectionId: 'test', tunnel: tunnel),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets(
    'WebView 114 uses process fallback instead of unsupported screen',
    (tester) async {
      final opened = <MethodCall>[];
      final close = Completer<void>();
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
            if (call.method == 'capabilities')
              return {
                'mode': 'process',
                'provider': 'com.android.webview',
                'version': '114',
              };
            opened.add(call);
            await close.future;
            return null;
          });
      await mount(tester);
      expect(opened.single.method, 'openCompat');
      expect(opened.single.arguments['url'], 'http://127.0.0.1:32123/');
      expect(
        find.textContaining('cannot create an isolated browser'),
        findsNothing,
      );
      expect(find.byType(AndroidView), findsNothing);
      await tester.pumpWidget(const SizedBox());
      close.complete();
      await tester.pump();
    },
    variant: TargetPlatformVariant({TargetPlatform.android}),
  );

  testWidgets(
    'native initialization error is not reported as unsupported WebView',
    (tester) async {
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (_) async {
            throw PlatformException(code: 'browserInitFailed');
          });
      await mount(tester);
      expect(
        find.textContaining('The browser could not start.'),
        findsOneWidget,
      );
      expect(
        find.textContaining('cannot create an isolated browser'),
        findsNothing,
      );
      expect(find.text('Retry'), findsOneWidget);
    },
    variant: TargetPlatformVariant({TargetPlatform.android}),
  );
}
