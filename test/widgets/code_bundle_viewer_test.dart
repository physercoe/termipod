import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/artifact_viewers/code_bundle_viewer.dart';

void main() {
  group('parseCodeBundle', () {
    test('files-object shape parses paths/content/language', () {
      final files = parseCodeBundle({
        'files': [
          {'path': 'src/train.py', 'content': 'print(1)\n'},
          {'path': 'README.md', 'content': '# Hello'},
        ],
      });
      expect(files, hasLength(2));
      expect(files[0].path, 'src/train.py');
      expect(files[0].language, 'python');
      expect(files[0].content, 'print(1)\n');
      expect(files[1].language, 'markdown');
    });

    test('flat list shape parses', () {
      final files = parseCodeBundle([
        {'path': 'main.go', 'content': 'package main\n'},
      ]);
      expect(files, hasLength(1));
      expect(files[0].language, 'go');
    });

    test('single-file degenerate shape parses', () {
      final files = parseCodeBundle({
        'path': 'a.rs',
        'content': 'fn main() {}',
      });
      expect(files, hasLength(1));
      expect(files[0].language, 'rust');
    });

    test('unsupported shape returns empty', () {
      expect(parseCodeBundle('not a bundle'), isEmpty);
      expect(parseCodeBundle(42), isEmpty);
      expect(parseCodeBundle({'foo': 'bar'}), isEmpty);
    });

    test('drops entries missing path or content', () {
      final files = parseCodeBundle({
        'files': [
          {'path': 'ok.py', 'content': 'x'},
          {'path': 'no-content.py'},
          {'content': 'no path'},
          'not even a map',
        ],
      });
      expect(files, hasLength(1));
      expect(files[0].path, 'ok.py');
    });
  });

  group('languageForPath', () {
    test('common extensions map to highlight ids', () {
      expect(languageForPath('foo/bar.py'), 'python');
      expect(languageForPath('foo.TS'), 'typescript');
      expect(languageForPath('a.tsx'), 'typescript');
      expect(languageForPath('Dockerfile.dockerfile'), 'dockerfile');
      expect(languageForPath('script.sh'), 'bash');
      expect(languageForPath('cfg.yml'), 'yaml');
    });

    test('unknown or missing extension falls back to plaintext', () {
      expect(languageForPath('Makefile'), 'plaintext');
      expect(languageForPath('foo.weirdext'), 'plaintext');
      expect(languageForPath('trailing.'), 'plaintext');
    });
  });

  group('ArtifactCodeBundleViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactCodeBundleViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot render code bundle'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactCodeBundleViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with title in the AppBar',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactCodeBundleViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'Run script',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Run script'), findsOneWidget);
    });
  });
}
