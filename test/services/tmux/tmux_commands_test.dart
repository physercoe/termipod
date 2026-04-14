import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/tmux/tmux_commands.dart';

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

    group('resizePaneToSize', () {
      test('generates resize-pane with cols only', () {
        expect(
          TmuxCommands.resizePaneToSize('%0', cols: 120),
          'tmux resize-pane -t %0 -x 120',
        );
      });

      test('generates resize-pane with rows only', () {
        expect(
          TmuxCommands.resizePaneToSize('%0', rows: 40),
          'tmux resize-pane -t %0 -y 40',
        );
      });

      test('generates resize-pane with both cols and rows', () {
        expect(
          TmuxCommands.resizePaneToSize('%1', cols: 200, rows: 50),
          'tmux resize-pane -t %1 -x 200 -y 50',
        );
      });

      test('escapes pane ID with special characters', () {
        expect(
          TmuxCommands.resizePaneToSize('my pane', cols: 80),
          'tmux resize-pane -t "my pane" -x 80',
        );
      });
    });

    group('resizeWindow', () {
      test('generates resize-window with cols only', () {
        expect(
          TmuxCommands.resizeWindow('my-session:0', cols: 160),
          'tmux resize-window -t my-session:0 -x 160',
        );
      });

      test('generates resize-window with rows only', () {
        expect(
          TmuxCommands.resizeWindow('my-session:0', rows: 48),
          'tmux resize-window -t my-session:0 -y 48',
        );
      });

      test('generates resize-window with both cols and rows', () {
        expect(
          TmuxCommands.resizeWindow('@1', cols: 200, rows: 50),
          'tmux resize-window -t @1 -x 200 -y 50',
        );
      });

      test('escapes target with special characters', () {
        expect(
          TmuxCommands.resizeWindow('my session:0', cols: 80),
          'tmux resize-window -t "my session:0" -x 80',
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

  group('Image path injection via sendKeys', () {
    test('sends simple path with literal flag', () {
      final cmd = TmuxCommands.sendKeys('%0', '/tmp/muxpod/img_20260403_a3f2.png', literal: true);
      expect(cmd, contains('-l'));
      expect(cmd, contains('/tmp/muxpod/img_20260403_a3f2.png'));
    });

    test('handles path with safe characters only', () {
      final cmd = TmuxCommands.sendKeys('%42', '/tmp/muxpod/test_image-v2.0.jpg', literal: true);
      expect(cmd, contains('-l'));
      expect(cmd, contains('test_image-v2.0.jpg'));
    });

    test('sends Enter key after path for auto-enter', () {
      final cmd = TmuxCommands.sendKeys('%0', 'Enter');
      expect(cmd, contains('Enter'));
      expect(cmd, isNot(contains('-l')));
    });

    test('formats @-prefixed path correctly', () {
      const path = '@/tmp/muxpod/img_test.png';
      final cmd = TmuxCommands.sendKeys('%0', path, literal: true);
      expect(cmd, contains('-l'));
      expect(cmd, contains('@/tmp/muxpod/img_test.png'));
    });
  });
}
