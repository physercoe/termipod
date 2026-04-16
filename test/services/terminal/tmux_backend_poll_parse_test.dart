import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/terminal/tmux_backend.dart';

/// Regression tests for the META-delimited poll output parser.
///
/// The bug this guards against: earlier alpha builds shipped a fallback
/// that, when the `\x01META\x01` delimiter went missing from one poll
/// iteration's combined output, dumped the entire blob — including the
/// trailing cursor-metadata line — into the terminal content area. The
/// user briefly saw lines like `33,0,56,44,0,0,bash` on the screen
/// before the next poll cleaned it up.
///
/// The parser MUST return null in any situation that would otherwise
/// surface metadata as content. Callers treat null as a skip signal
/// (leave previous frame alone, let the next poll recover).
void main() {
  group('TmuxBackend.parsePollOutput — happy path', () {
    test('splits content / cursor / pane-mode at the delimiter', () {
      const out =
          'line1\n'
          'line2\n'
          '${TmuxBackend.pollMetaDelimiter}\n'
          '12,7,80,24,500,0,bash\n'
          '\n';
      final r = TmuxBackend.parsePollOutput(out)!;
      expect(r.content, 'line1\nline2');
      expect(r.cursorLine, '12,7,80,24,500,0,bash');
      expect(r.paneModeLine, '');
    });

    test('captures pane-mode line when tmux is in copy-mode', () {
      const out =
          'visible\n'
          '${TmuxBackend.pollMetaDelimiter}\n'
          '0,0,80,24,0,0,bash\n'
          'copy-mode\n';
      final r = TmuxBackend.parsePollOutput(out)!;
      expect(r.content, 'visible');
      expect(r.cursorLine, '0,0,80,24,0,0,bash');
      expect(r.paneModeLine, 'copy-mode');
    });

    test('empty pane content is allowed', () {
      const out =
          '${TmuxBackend.pollMetaDelimiter}\n'
          '0,0,80,24,0,0,bash\n';
      final r = TmuxBackend.parsePollOutput(out)!;
      expect(r.content, '');
      expect(r.cursorLine, '0,0,80,24,0,0,bash');
    });

    test('uses the LAST META marker when multiple appear', () {
      // Defensive: capture-pane content could in principle contain
      // a literal SOH-META-SOH byte sequence. The actual delimiter is
      // always the printf at the end, so lastIndexOf is required.
      const out =
          'pane has ${TmuxBackend.pollMetaDelimiter} weirdly in it\n'
          '${TmuxBackend.pollMetaDelimiter}\n'
          '5,5,80,24,0,0,bash\n';
      final r = TmuxBackend.parsePollOutput(out)!;
      expect(r.content, 'pane has ${TmuxBackend.pollMetaDelimiter} weirdly in it');
      expect(r.cursorLine, '5,5,80,24,0,0,bash');
    });
  });

  group('TmuxBackend.parsePollOutput — leak guards (return null)', () {
    test('missing META delimiter — would leak metadata as content', () {
      // Symptom from user report: occasional "33,0,56,44,0,0,bash"
      // visible on the terminal screen.
      const leaked =
          'line1\n'
          'line2\n'
          '33,0,56,44,0,0,bash\n';
      expect(
        TmuxBackend.parsePollOutput(leaked),
        isNull,
        reason:
            'No META delimiter — must skip this frame, NOT dump cursor '
            'metadata onto the terminal screen.',
      );
    });

    test('META present but trailing content line LOOKS like metadata', () {
      // Defense in depth: if a future change ever splits at the wrong
      // position, the trailing cursor-metadata line would surface.
      // Detecting the regex on the last content line catches it before
      // it reaches the user.
      const out =
          'line1\n'
          '46,53,198,54,1988,1,vim\n'
          '${TmuxBackend.pollMetaDelimiter}\n'
          '0,0,80,24,0,0,bash\n';
      expect(
        TmuxBackend.parsePollOutput(out),
        isNull,
        reason:
            'Trailing line of content looks like a cursor-metadata leak; '
            'parser must reject the frame.',
      );
    });

    test('empty input returns null (no META present)', () {
      expect(TmuxBackend.parsePollOutput(''), isNull);
    });

    test('arbitrary garbage without META returns null', () {
      expect(
        TmuxBackend.parsePollOutput('completely unrelated text'),
        isNull,
      );
    });
  });

  group('TmuxBackend.parsePollOutput — pattern boundary checks', () {
    test('lines that LOOK numeric but lack the right shape are content', () {
      // Counter-cases the leak guard regex must NOT trip on:
      //   - timestamps, IPs, version numbers, log levels, etc.
      const cases = [
        'time: 12:34:56',
        'ip 10.0.0.1',
        'v1.2.3',
        '1,2,3',                  // too few fields
        '1,2,3,4,5,6',            // 6 fields, missing command
        '1,2,3,4,5,2,bash',       // alternate_on must be 0 or 1
        'a,b,c,d,e,f,g',          // not numeric
      ];
      for (final c in cases) {
        final out = '$c\n${TmuxBackend.pollMetaDelimiter}\n0,0,80,24,0,0,bash\n';
        expect(
          TmuxBackend.parsePollOutput(out),
          isNotNull,
          reason: 'last content line "$c" should NOT be flagged as a leak',
        );
      }
    });

    test('genuine cursor-metadata variants are flagged', () {
      // Empty pane_current_command happens immediately after a pane is
      // created and before any process attaches.
      const variants = [
        '0,0,80,24,0,0,',           // empty current command
        '0,0,80,24,0,1,vi',         // alternate_on=1 (vi)
        '999,999,999,999,9999,0,htop',
      ];
      for (final v in variants) {
        final out = 'shell\n$v\n${TmuxBackend.pollMetaDelimiter}\n0,0,80,24,0,0,bash\n';
        expect(
          TmuxBackend.parsePollOutput(out),
          isNull,
          reason: '"$v" matches cursor-metadata format and must be flagged',
        );
      }
    });
  });
}
