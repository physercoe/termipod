import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/tmux_provider.dart';
import 'package:termipod/services/tmux/tmux_parser.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  TmuxSession tree({
    required int windowIndex,
    required int paneIndex,
    required String paneId,
  }) {
    return TmuxSession(
      name: 'work',
      windows: [
        TmuxWindow(
          index: windowIndex,
          name: 'shell',
          active: true,
          panes: [
            TmuxPane(index: paneIndex, id: paneId, active: true),
          ],
        ),
      ],
    );
  }

  group('TmuxNotifier tree refresh', () {
    test('keeps the active pane by stable ID after indexes are renumbered', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final provider = tmuxProvider('connection-1');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);

      notifier.updateSessions([
        tree(windowIndex: 1, paneIndex: 0, paneId: '%7'),
      ]);
      notifier.setActive(
        sessionName: 'work',
        windowIndex: 1,
        paneIndex: 0,
        paneId: '%7',
      );

      notifier.updateSessions([
        tree(windowIndex: 4, paneIndex: 3, paneId: '%7'),
      ]);

      final state = container.read(provider);
      expect(state.activeWindowIndex, 4);
      expect(state.activePaneIndex, 3);
      expect(state.activePaneId, '%7');
      expect(notifier.currentTarget, '%7');
    });

    test('malformed non-empty output preserves the last good tree', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final provider = tmuxProvider('connection-2');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);
      final original = tree(windowIndex: 0, paneIndex: 0, paneId: '%1');
      notifier.updateSessions([original]);

      notifier.parseAndUpdateFullTree(
        'unexpected command output',
        serverConfirmed: true,
      );

      final state = container.read(provider);
      expect(state.sessions, [original]);
      expect(state.error, contains('Malformed tmux session tree output'));
    });

    test('parses confirmed tree when a window name resembles an error', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final provider = tmuxProvider('connection-error-like-name');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);
      final output = [
        'work',
        r'$0',
        '0',
        '@0',
        'Permission denied',
        '1',
        '0',
        '%1',
        '1',
        '80',
        '24',
        '0',
        '0',
        'shell',
        'bash',
        '0',
        '0',
        '*',
      ].join(TmuxParser.defaultDelimiter);

      notifier.parseAndUpdateFullTree(output, serverConfirmed: true);

      final state = container.read(provider);
      expect(state.error, isNull);
      expect(state.sessions, hasLength(1));
      expect(state.sessions.single.windows.single.name, 'Permission denied');
      expect(state.activePaneId, '%1');
      expect(notifier.currentTarget, '%1');
    });

    test('list-only session updates preserve the active pane target', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final provider = tmuxProvider('connection-session-list');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);
      notifier.updateSessions([
        tree(windowIndex: 0, paneIndex: 0, paneId: '%1'),
      ]);

      notifier.parseAndUpdateSessions(
        ['work', '0', '1', '1', r'$0'].join(TmuxParser.defaultDelimiter),
      );

      final state = container.read(provider);
      expect(state.sessions.single.name, 'work');
      expect(state.sessions.single.windows, isEmpty);
      expect(state.activePaneId, '%1');
      expect(notifier.currentTarget, '%1');
    });

    test('confirmed empty tree clears stale selection', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);
      final provider = tmuxProvider('connection-3');
      final subscription = container.listen(provider, (previous, next) {});
      addTearDown(subscription.close);
      final notifier = container.read(provider.notifier);
      notifier.updateSessions([
        tree(windowIndex: 0, paneIndex: 0, paneId: '%1'),
      ]);

      notifier.updateSessions(const []);

      final state = container.read(provider);
      expect(state.sessions, isEmpty);
      expect(state.activeSessionName, isNull);
      expect(state.activeWindowIndex, isNull);
      expect(state.activePaneId, isNull);
      expect(notifier.currentTarget, isNull);
    });
  });
}
