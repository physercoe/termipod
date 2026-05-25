import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/agent_compose.dart';

// Tests for v1.0.707 polish — the slash-command shape gate that
// drives `raw: true` on postAgentInput. Inputs matching this gate
// bypass the hub's principal-directive envelope wrap; everything
// else gets the existing envelope (prose, A2A, etc).
//
// The gate must:
//   - accept the canonical control commands (`/clear`, `/compact`,
//     `/model claude-sonnet-4`, `/effort xhigh`)
//   - reject prose that happens to start with `/` (markdown lists,
//     path-like values)
//   - reject empty / pure-whitespace / non-slash bodies
//
// False positives are worse than false negatives: a misfire makes a
// real directive look like a slash command (engine ignores it). A
// miss makes a slash command look like a directive (engine reads
// the directive header, replies in prose). Both bad, but the false
// negative is recoverable on the user's next try.

void main() {
  group('isSlashCommandBody', () {
    test('accepts canonical slash commands with no args', () {
      expect(isSlashCommandBody('/clear'), isTrue);
      expect(isSlashCommandBody('/compact'), isTrue);
      expect(isSlashCommandBody('/cost'), isTrue);
      expect(isSlashCommandBody('/exit'), isTrue);
      expect(isSlashCommandBody('/status'), isTrue);
    });

    test('accepts slash commands with simple args', () {
      // The shape allows arbitrary content after the command token —
      // claude parses the rest itself.
      expect(isSlashCommandBody('/model claude-sonnet-4-6'), isTrue);
      expect(isSlashCommandBody('/effort xhigh'), isTrue);
      expect(isSlashCommandBody('/compact focus the conclusion'), isTrue);
      expect(isSlashCommandBody('/add-dir /home/user/proj'), isTrue);
    });

    test('accepts multi-line bodies starting with a slash command', () {
      // /compact takes a multiline focus block in some workflows.
      // The first-line gate accepts this — the rest of the body is
      // claude's problem to parse.
      const body = '/compact\nPlease focus on the architecture decisions\n'
          'made in the last 5 turns';
      expect(isSlashCommandBody(body), isTrue);
    });

    test('strips leading/trailing whitespace before checking shape', () {
      // A trailing newline (TextField submit adds one in some
      // configurations) must not break the gate. Same for leading
      // whitespace on a stray paste.
      expect(isSlashCommandBody('/clear\n'), isTrue);
      expect(isSlashCommandBody('  /clear  '), isTrue);
      expect(isSlashCommandBody('\n\t/compact\n'), isTrue);
    });

    test('rejects empty / whitespace-only bodies', () {
      expect(isSlashCommandBody(''), isFalse);
      expect(isSlashCommandBody('   '), isFalse);
      expect(isSlashCommandBody('\n\n'), isFalse);
    });

    test('rejects bodies that do not start with a slash', () {
      expect(isSlashCommandBody('hello'), isFalse);
      expect(isSlashCommandBody('please /clear'), isFalse);
      expect(
        isSlashCommandBody('Hello! Could you /clear the conversation?'),
        isFalse,
      );
    });

    test('rejects slash followed by non-letter (path-like, bullets)', () {
      // Path-like leading slashes must NOT trip the gate — the user
      // may be referencing a UNIX path in a prompt.
      expect(isSlashCommandBody('/etc/hosts'), isFalse);
      expect(isSlashCommandBody('/usr/local/bin/foo'), isFalse);
      // Markdown list markers
      expect(isSlashCommandBody('/ - item'), isFalse);
      expect(isSlashCommandBody('/ first'), isFalse);
      // Digits-first or symbol-first command tokens — claude doesn't
      // mint these and accepting them would widen the false-positive
      // surface for no gain.
      expect(isSlashCommandBody('/123abc'), isFalse);
      expect(isSlashCommandBody('/-flag'), isFalse);
    });

    test('rejects standalone slash', () {
      // A bare slash isn't a command; the regex requires at least one
      // letter after the slash.
      expect(isSlashCommandBody('/'), isFalse);
      expect(isSlashCommandBody('/ '), isFalse);
    });

    test('accepts underscored/dashed command names', () {
      // Some engines use kebab-case (/add-dir) or snake_case names;
      // both pass the shape gate.
      expect(isSlashCommandBody('/add-dir'), isTrue);
      expect(isSlashCommandBody('/foo_bar'), isTrue);
    });
  });
}
