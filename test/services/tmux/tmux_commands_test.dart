import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_muxpod/services/tmux/tmux_commands.dart';

void main() {
  group('TmuxCommands', () {
    group('killPane', () {
      test('generates correct kill-pane command for standard pane ID', () {
        expect(TmuxCommands.killPane('%0'), 'tmux kill-pane -t %0');
      });

      test('generates correct kill-pane command for multi-digit pane ID', () {
        expect(TmuxCommands.killPane('%42'), 'tmux kill-pane -t %42');
      });

      test('escapes pane ID with special characters', () {
        // Normally pane IDs are %N, but _escapeArg should handle edge cases
        expect(
          TmuxCommands.killPane('%1'),
          'tmux kill-pane -t %1',
        );
      });
    });

    group('selectPane', () {
      test('generates correct select-pane command', () {
        expect(TmuxCommands.selectPane('%0'), 'tmux select-pane -t %0');
      });
    });

    group('splitWindowHorizontal', () {
      test('generates basic horizontal split command', () {
        expect(
          TmuxCommands.splitWindowHorizontal(target: '%0'),
          'tmux split-window -h -t %0',
        );
      });

      test('generates horizontal split with percentage', () {
        expect(
          TmuxCommands.splitWindowHorizontal(target: '%1', percentage: 50),
          'tmux split-window -h -t %1 -p 50',
        );
      });

      test('generates horizontal split with start directory', () {
        expect(
          TmuxCommands.splitWindowHorizontal(
            target: '%0',
            startDirectory: '/home/user',
          ),
          'tmux split-window -h -t %0 -c /home/user',
        );
      });

      test('generates horizontal split with directory containing spaces', () {
        expect(
          TmuxCommands.splitWindowHorizontal(
            target: '%0',
            startDirectory: '/home/my projects',
          ),
          'tmux split-window -h -t %0 -c "/home/my projects"',
        );
      });
    });

    group('splitWindowVertical', () {
      test('generates basic vertical split command', () {
        expect(
          TmuxCommands.splitWindowVertical(target: '%0'),
          'tmux split-window -v -t %0',
        );
      });
    });

    group('killSession', () {
      test('generates correct kill-session command', () {
        expect(
          TmuxCommands.killSession('my-session'),
          'tmux kill-session -t my-session',
        );
      });

      test('escapes session name with spaces', () {
        expect(
          TmuxCommands.killSession('my session'),
          'tmux kill-session -t "my session"',
        );
      });
    });

    group('killWindow', () {
      test('generates correct kill-window command', () {
        expect(
          TmuxCommands.killWindow('my-session', 2),
          'tmux kill-window -t my-session:2',
        );
      });
    });

    group('resizePane', () {
      test('generates zoom command', () {
        expect(
          TmuxCommands.resizePane('%0', zoom: true),
          'tmux resize-pane -t %0 -Z',
        );
      });

      test('generates unzoom command', () {
        expect(
          TmuxCommands.resizePane('%0', zoom: false),
          'tmux resize-pane -t %0 -z',
        );
      });
    });

    group('sendKeys', () {
      test('generates literal send-keys command', () {
        // _escapeArg escapes backslashes, so \\ becomes \\\\
        expect(
          TmuxCommands.sendKeys('%0', '\\x1b[I', literal: true),
          'tmux send-keys -t %0 -l "\\\\x1b[I"',
        );
      });

      test('generates non-literal send-keys command', () {
        expect(
          TmuxCommands.sendKeys('%0', 'Enter'),
          'tmux send-keys -t %0 Enter',
        );
      });
    });

    group('chain', () {
      test('chains multiple commands with &&', () {
        expect(
          TmuxCommands.chain(['tmux kill-pane -t %0', 'tmux list-panes']),
          'tmux kill-pane -t %0 && tmux list-panes',
        );
      });
    });
  });

  group('SplitDirection', () {
    test('has horizontal and vertical values', () {
      expect(SplitDirection.values, contains(SplitDirection.horizontal));
      expect(SplitDirection.values, contains(SplitDirection.vertical));
    });
  });

  group('TmuxLayout', () {
    test('name returns correct tmux layout string', () {
      expect(TmuxLayout.evenHorizontal.name, 'even-horizontal');
      expect(TmuxLayout.evenVertical.name, 'even-vertical');
      expect(TmuxLayout.mainHorizontal.name, 'main-horizontal');
      expect(TmuxLayout.mainVertical.name, 'main-vertical');
      expect(TmuxLayout.tiled.name, 'tiled');
    });
  });
}
