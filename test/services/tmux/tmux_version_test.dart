import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/tmux/tmux_version.dart';

void main() {
  group('TmuxVersionInfo', () {
    group('parse', () {
      test('標準的なバージョン文字列をパースできる', () {
        final result = TmuxVersionInfo.parse('tmux 3.4');
        expect(result, isNotNull);
        expect(result!.major, 3);
        expect(result.minor, 4);
      });

      test('サフィックス付きバージョンをパースできる', () {
        final result = TmuxVersionInfo.parse('tmux 2.9a');
        expect(result, isNotNull);
        expect(result!.major, 2);
        expect(result.minor, 9);
      });

      test('古いバージョンをパースできる', () {
        final result = TmuxVersionInfo.parse('tmux 1.8');
        expect(result, isNotNull);
        expect(result!.major, 1);
        expect(result.minor, 8);
      });

      test('サフィックスなしのバージョンをパースできる', () {
        final result = TmuxVersionInfo.parse('tmux 2.9');
        expect(result, isNotNull);
        expect(result!.major, 2);
        expect(result.minor, 9);
      });

      test('空文字列はnullを返す', () {
        expect(TmuxVersionInfo.parse(''), isNull);
      });

      test('tmuxを含まない文字列はnullを返す', () {
        expect(TmuxVersionInfo.parse('not-tmux'), isNull);
      });

      test('バージョン番号なしはnullを返す', () {
        expect(TmuxVersionInfo.parse('tmux'), isNull);
      });

      test('不正なバージョン番号はnullを返す', () {
        expect(TmuxVersionInfo.parse('tmux abc'), isNull);
      });
    });

    group('supportsResizeWindow', () {
      test('2.9はサポートする', () {
        expect(const TmuxVersionInfo(2, 9).supportsResizeWindow, isTrue);
      });

      test('2.8はサポートしない', () {
        expect(const TmuxVersionInfo(2, 8).supportsResizeWindow, isFalse);
      });

      test('3.0はサポートする', () {
        expect(const TmuxVersionInfo(3, 0).supportsResizeWindow, isTrue);
      });

      test('1.8はサポートしない', () {
        expect(const TmuxVersionInfo(1, 8).supportsResizeWindow, isFalse);
      });
    });

    group('supportsResizePaneToSize', () {
      test('1.7はサポートする', () {
        expect(const TmuxVersionInfo(1, 7).supportsResizePaneToSize, isTrue);
      });

      test('1.6はサポートしない', () {
        expect(const TmuxVersionInfo(1, 6).supportsResizePaneToSize, isFalse);
      });

      test('2.0はサポートする', () {
        expect(const TmuxVersionInfo(2, 0).supportsResizePaneToSize, isTrue);
      });
    });

    group('toString', () {
      test('正しいフォーマットで文字列化される', () {
        expect(const TmuxVersionInfo(3, 4).toString(), 'tmux 3.4');
        expect(const TmuxVersionInfo(2, 9).toString(), 'tmux 2.9');
      });
    });

    group('equality', () {
      test('同じバージョンは等しい', () {
        expect(const TmuxVersionInfo(3, 4), const TmuxVersionInfo(3, 4));
      });

      test('異なるバージョンは等しくない', () {
        expect(
          const TmuxVersionInfo(3, 4) == const TmuxVersionInfo(3, 5),
          isFalse,
        );
        expect(
          const TmuxVersionInfo(2, 4) == const TmuxVersionInfo(3, 4),
          isFalse,
        );
      });

      test('同じバージョンのhashCodeは一致する', () {
        expect(
          const TmuxVersionInfo(3, 4).hashCode,
          const TmuxVersionInfo(3, 4).hashCode,
        );
      });
    });
  });
}
