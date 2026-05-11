import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/text_attach/composer_text_attach.dart';

void main() {
  group('fenceLanguageForExtension', () {
    test('common code extensions map to fence tags', () {
      expect(fenceLanguageForExtension('py'), 'python');
      expect(fenceLanguageForExtension('PY'), 'python');
      expect(fenceLanguageForExtension('ts'), 'typescript');
      expect(fenceLanguageForExtension('tsx'), 'tsx');
      expect(fenceLanguageForExtension('go'), 'go');
      expect(fenceLanguageForExtension('md'), 'markdown');
      expect(fenceLanguageForExtension('json'), 'json');
      expect(fenceLanguageForExtension('yml'), 'yaml');
    });

    test('plain text returns empty tag', () {
      expect(fenceLanguageForExtension('txt'), '');
      expect(fenceLanguageForExtension('log'), '');
    });

    test('unknown extensions return empty tag', () {
      expect(fenceLanguageForExtension('weirdext'), '');
      expect(fenceLanguageForExtension(''), '');
    });
  });

  group('buildFencedBlock', () {
    test('wraps content with default triple-backtick fence', () {
      final out = buildFencedBlock(
        filename: 'hello.py',
        content: 'print("hi")\n',
        language: 'python',
      );
      expect(out, startsWith('```python\n// hello.py\nprint("hi")\n```'));
      expect(out, endsWith('```\n'));
    });

    test('empty language yields untagged fence', () {
      final out = buildFencedBlock(
        filename: 'notes.txt',
        content: 'just a note',
        language: '',
      );
      expect(out, startsWith('```\n// notes.txt\njust a note\n```'));
    });

    test('escalates fence length when content has triple-backticks', () {
      final out = buildFencedBlock(
        filename: 'README.md',
        content: '```dart\nprint(1);\n```',
        language: 'markdown',
      );
      // Inner backtick run is 3, fence must be at least 4.
      expect(out, startsWith('````markdown\n'));
      expect(out, endsWith('````\n'));
    });

    test('escalates further when content has 4-backtick run', () {
      final out = buildFencedBlock(
        filename: 'tricky.md',
        content: '````x````',
        language: 'markdown',
      );
      expect(out, startsWith('`````markdown\n'));
      expect(out, endsWith('`````\n'));
    });

    test('trims trailing whitespace from content', () {
      final out = buildFencedBlock(
        filename: 'a.txt',
        content: 'x\n\n\n',
        language: '',
      );
      // One trailing newline before closing fence; the helper trimRights
      // the input then adds a single `\n`.
      expect(out, contains('x\n```'));
    });
  });
}
