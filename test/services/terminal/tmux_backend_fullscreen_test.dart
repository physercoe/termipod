import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/terminal/tmux_backend.dart';

/// Locks in the fullscreen-TUI detection used to suppress tmux scrollback
/// when the active pane is running an editor / pager.
///
/// This is a *regression* test — earlier alpha builds (1.0.4, 1.0.5)
/// shipped fixes that did not actually take effect because (a) the
/// detection set was too narrow and (b) a parallel polling path in
/// `terminal_screen.dart` ignored it. If the symptom returns ("vi
/// shows shell scrollback above the editor"), start by checking these
/// cases.
void main() {
  group('TmuxBackend.isFullscreenCommandName', () {
    test('plain editors and pagers are detected', () {
      const cases = [
        'vi', 'vim', 'nvim', 'neovim', 'view',
        'nano', 'pico',
        'less', 'more', 'most', 'man', 'info',
        'htop', 'top', 'btop', 'btm', 'glances',
        'fzf', 'ranger', 'mc', 'lf',
        'tig', 'lazygit', 'gitui', 'emacs',
      ];
      for (final c in cases) {
        expect(
          TmuxBackend.isFullscreenCommandName(c),
          isTrue,
          reason: '"$c" should be detected as fullscreen',
        );
      }
    });

    test('Debian/Ubuntu vim alternatives are detected', () {
      // /usr/bin/vi → /etc/alternatives/vi → /usr/bin/vim.basic etc.
      // pane_current_command shows the *resolved* binary name, so the
      // fix has to handle the variant suffixes.
      const variants = [
        'vim.basic', 'vim.tiny', 'vim.gtk3', 'vim.nox',
        'vimdiff', 'vimtutor',
        'nvim-qt',
      ];
      for (final v in variants) {
        expect(
          TmuxBackend.isFullscreenCommandName(v),
          isTrue,
          reason: '"$v" should be detected as fullscreen',
        );
      }
    });

    test('case-insensitive and whitespace-tolerant', () {
      expect(TmuxBackend.isFullscreenCommandName('VIM'), isTrue);
      expect(TmuxBackend.isFullscreenCommandName('  htop  '), isTrue);
      expect(TmuxBackend.isFullscreenCommandName('Vim.Basic'), isTrue);
    });

    test('shells and ordinary commands are NOT detected', () {
      const negatives = [
        'bash', 'zsh', 'fish', 'sh', 'dash',
        'ls', 'cat', 'grep', 'cargo', 'node', 'python',
        'ssh', 'git', 'docker',
        // False-positive guards: don't match arbitrary words starting
        // with `vi`/`nvim`.
        'vis',     // a real shell tool, not vim
        'vipw',    // vipw(8), not fullscreen
        'visudo',  // vipw cousin
        'nvimage', // hypothetical, must not match
      ];
      for (final c in negatives) {
        expect(
          TmuxBackend.isFullscreenCommandName(c),
          isFalse,
          reason: '"$c" should NOT be detected as fullscreen',
        );
      }
    });

    test('null and empty inputs return false', () {
      expect(TmuxBackend.isFullscreenCommandName(null), isFalse);
      expect(TmuxBackend.isFullscreenCommandName(''), isFalse);
      expect(TmuxBackend.isFullscreenCommandName('   '), isFalse);
    });
  });
}
