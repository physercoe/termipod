import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/models/action_bar_presets.dart';
import 'package:termipod/services/ssh/ssh_client.dart';
import 'package:termipod/services/terminal/tmux_backend.dart';

class _ConnectedSshClient extends SshClient {
  @override
  bool get isConnected => true;
}

void main() {
  test('native tmux action is dispatched instead of sent to the pane', () async {
    String? dispatched;
    final backend = TmuxBackend(
      sshClient: _ConnectedSshClient(),
      getCurrentTarget: () => '%1',
      onTmuxAction: (action) async {
        dispatched = action;
      },
    );
    addTearDown(backend.dispose);

    await backend.sendSpecialKey('termipod:tmux:kill-pane');

    expect(dispatched, 'termipod:tmux:kill-pane');
  });

  test('tmux preset contains native actions rather than prefix chords', () {
    final values = ActionBarPresets.tmux.groups
        .expand((group) => group.buttons)
        .map((button) => button.value)
        .toList();

    expect(values, isNotEmpty);
    expect(values, everyElement(startsWith('termipod:tmux:')));
    expect(values, isNot(contains(startsWith('C-b '))));
  });
}
