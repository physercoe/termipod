import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/ssh_provider.dart';

/// `activeSshConnectionIdsProvider` is the visibility signal that
/// `_HostTile` watches to render its green live-dot indicator. The
/// `_ActiveSshConnectionIds` notifier itself is private, so we exercise
/// it via the public provider + the methods exposed by `.notifier`.
///
/// The actual add/remove calls happen from inside `SshNotifier` on
/// connect/disconnect; those paths are network-dependent and covered
/// by widget/integration tests. Here we just lock the pure provider
/// semantics so neither the Hosts row nor the steward sees a stale or
/// duplicated entry.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('activeSshConnectionIdsProvider', () {
    test('starts empty', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      expect(container.read(activeSshConnectionIdsProvider), isEmpty);
    });

    test('add records the id and is reflected in state', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      container
          .read(activeSshConnectionIdsProvider.notifier)
          .add('conn-1');

      expect(
        container.read(activeSshConnectionIdsProvider),
        equals({'conn-1'}),
      );
    });

    test('add is idempotent — same id twice stays a singleton', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final n = container.read(activeSshConnectionIdsProvider.notifier);
      n.add('conn-1');
      final first = container.read(activeSshConnectionIdsProvider);
      n.add('conn-1');
      final second = container.read(activeSshConnectionIdsProvider);

      expect(second, equals({'conn-1'}));
      // Identity unchanged on a redundant add — no spurious rebuilds.
      expect(identical(first, second), isTrue);
    });

    test('remove drops the id', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final n = container.read(activeSshConnectionIdsProvider.notifier);
      n.add('conn-1');
      n.add('conn-2');
      n.remove('conn-1');

      expect(
        container.read(activeSshConnectionIdsProvider),
        equals({'conn-2'}),
      );
    });

    test('remove of an unknown id is a no-op (no rebuild)', () {
      final container = ProviderContainer();
      addTearDown(container.dispose);

      final n = container.read(activeSshConnectionIdsProvider.notifier);
      n.add('conn-1');
      final first = container.read(activeSshConnectionIdsProvider);
      n.remove('conn-2');
      final second = container.read(activeSshConnectionIdsProvider);

      expect(second, equals({'conn-1'}));
      expect(identical(first, second), isTrue);
    });
  });
}
