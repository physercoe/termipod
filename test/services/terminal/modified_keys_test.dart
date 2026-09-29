import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/ssh/ssh_client.dart';
import 'package:termipod/services/terminal/raw_pty_backend.dart';
import 'package:termipod/services/terminal/tmux_backend.dart';

class _RecordingClient extends SshClient {
  final writes = <String>[];
  final commands = <String>[];
  @override
  bool get isConnected => true;
  @override
  void write(String data) => writes.add(data);
  @override
  Future<String> exec(String command, {Duration? timeout}) async {
    commands.add(command);
    return '';
  }
}

void main() {
  test('raw PTY encodes navigation modifier combinations', () async {
    final client = _RecordingClient();
    final backend = RawPtyBackend(sshClient: client);
    addTearDown(backend.dispose);
    for (final key in [
      'S-Left',
      'C-Left',
      'M-Left',
      'C-S-Right',
      'C-M-S-Up',
      'S-Home',
      'C-NPage',
      'S-F1',
      'M-F5',
      'S-Tab',
      'S-Enter',
      'Left',
    ]) {
      await backend.sendSpecialKey(key);
    }
    expect(client.writes, [
      '\x1b[1;2D',
      '\x1b[1;5D',
      '\x1b[1;3D',
      '\x1b[1;6C',
      '\x1b[1;8A',
      '\x1b[1;2H',
      '\x1b[6;5~',
      '\x1b[1;2P',
      '\x1b[15;3~',
      '\x1b[Z',
      '\n',
      '\x1b[D',
    ]);
  });

  test(
    'tmux receives modifier names as single keys, not literal text',
    () async {
      final client = _RecordingClient();
      final backend = TmuxBackend(
        sshClient: client,
        getCurrentTarget: () => '%1',
      );
      addTearDown(backend.dispose);
      await backend.sendSpecialKey('S-Left');
      await backend.sendSpecialKey('C-S-Left');
      expect(client.commands, [
        'tmux send-keys -t %1 S-Left',
        'tmux send-keys -t %1 C-S-Left',
      ]);
    },
  );
}
