import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/session_turns_provider.dart';

// P2 (agent-run-analysis-mode): the turn-index provider backing the analysis
// surface's "Turns" structure index. The empty-id fast path must resolve to an
// empty list WITHOUT touching the hub client, so a caller can `watch` it
// unconditionally (e.g. before a session id is known) and the disclosure
// self-gates blank.
void main() {
  test('empty session id resolves to an empty list (no client needed)',
      () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final out = await container.read(sessionTurnsProvider('').future);
    expect(out, isEmpty);
  });
}
