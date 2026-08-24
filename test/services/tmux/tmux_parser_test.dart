import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/tmux/tmux_parser.dart';

void main() {
  group('TmuxParser.isServerRunning', () {
    test('recognizes all known tmux connection failures', () {
      const failures = [
        'no server running on /tmp/tmux-1000/default',
        'error connecting to /tmp/tmux-1000/default',
        'failed to connect to server',
        'tmux: command not found',
        'No such file or directory',
        'Permission denied',
      ];

      for (final failure in failures) {
        expect(
          TmuxParser.isServerRunning(failure),
          isFalse,
          reason: failure,
        );
      }
    });

    test('accepts ordinary command output', () {
      expect(TmuxParser.isServerRunning(r'work|||$0|||0|||%1'), isTrue);
    });
  });
}
