import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/artifact_manifest/artifact_manifest.dart';
import 'package:termipod/widgets/artifact_viewers/canvas_viewer.dart';
import 'package:webview_flutter/webview_flutter.dart';

void main() {
  group('inlineCanvasBundle', () {
    test('inlines script + link + img from manifest', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'entry': 'index.html',
        'files': [
          {
            'path': 'index.html',
            'content': '<!doctype html>\n'
                '<link rel="stylesheet" href="style.css">\n'
                '<script src="./chart.js"></script>\n'
                '<img src="logo.svg" alt="logo">',
          },
          {'path': 'chart.js', 'content': 'console.log(1);'},
          {'path': 'style.css', 'content': '.x { color: red; }'},
          {'path': 'logo.svg', 'content': '<svg/>'},
        ],
      })!;
      final html = inlineCanvasBundle(m);
      expect(html, contains('<style>.x { color: red; }</style>'));
      expect(html, contains('<script>console.log(1);</script>'));
      expect(html, contains('data:image/svg+xml;base64,'));
      expect(html, isNot(contains('href="style.css"')));
      expect(html, isNot(contains('src="./chart.js"')));
    });

    test('passes CDN urls through untouched', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'files': [
          {
            'path': 'index.html',
            'content':
                '<script src="https://cdn.jsdelivr.net/npm/d3@7"></script>',
          },
        ],
      })!;
      final html = inlineCanvasBundle(m);
      expect(
        html,
        contains('<script src="https://cdn.jsdelivr.net/npm/d3@7"></script>'),
      );
    });

    test('rejects parent-dir traversal', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'files': [
          {
            'path': 'index.html',
            'content': '<script src="../secret.js"></script>',
          },
          {'path': 'secret.js', 'content': 'leaked'},
        ],
      })!;
      final html = inlineCanvasBundle(m);
      expect(html, contains('src="../secret.js"'));
      expect(html, isNot(contains('leaked')));
    });

    test('non-stylesheet <link> tags are left alone', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'files': [
          {
            'path': 'index.html',
            'content':
                '<link rel="icon" href="favicon.ico">',
          },
          {'path': 'favicon.ico', 'content': 'x'},
        ],
      })!;
      final html = inlineCanvasBundle(m);
      expect(html, contains('<link rel="icon" href="favicon.ico">'));
    });

    test('throws on missing entry', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'lib.js', 'content': 'x'},
        ],
      })!;
      expect(() => inlineCanvasBundle(m), throwsA(isA<FormatException>()));
    });
  });

  group('decideCanvasNavigation', () {
    test('allows about:blank, data:, and CDN HTTPS', () {
      expect(decideCanvasNavigation('about:blank'),
          NavigationDecision.navigate);
      expect(decideCanvasNavigation('data:image/png;base64,AAAA'),
          NavigationDecision.navigate);
      expect(decideCanvasNavigation('https://cdn.jsdelivr.net/npm/d3@7'),
          NavigationDecision.navigate);
      expect(decideCanvasNavigation('https://unpkg.com/three@latest'),
          NavigationDecision.navigate);
      expect(decideCanvasNavigation('https://cdnjs.cloudflare.com/x.js'),
          NavigationDecision.navigate);
      expect(decideCanvasNavigation('https://esm.sh/lodash'),
          NavigationDecision.navigate);
    });

    test('blocks HTTP and non-allowlisted HTTPS', () {
      expect(decideCanvasNavigation('http://cdn.jsdelivr.net/x'),
          NavigationDecision.prevent);
      expect(decideCanvasNavigation('https://evil.example.com/x'),
          NavigationDecision.prevent);
      expect(decideCanvasNavigation('ftp://server/x'),
          NavigationDecision.prevent);
    });
  });

  group('resolveManifestPath', () {
    test('strips ./ and exact-matches', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'a.js', 'content': 'x'},
        ],
      })!;
      final byPath = {for (final f in m.files) f.path: f};
      expect(resolveManifestPath('a.js', byPath)?.content, 'x');
      expect(resolveManifestPath('./a.js', byPath)?.content, 'x');
    });

    test('rejects http/https/data/absolute/traversal/protocol-relative', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'a.js', 'content': 'x'},
        ],
      })!;
      final byPath = {for (final f in m.files) f.path: f};
      expect(resolveManifestPath('https://x/a.js', byPath), isNull);
      expect(resolveManifestPath('http://x/a.js', byPath), isNull);
      expect(resolveManifestPath('data:image/png,X', byPath), isNull);
      expect(resolveManifestPath('/a.js', byPath), isNull);
      expect(resolveManifestPath('//other.host/a.js', byPath), isNull);
      expect(resolveManifestPath('../a.js', byPath), isNull);
      expect(resolveManifestPath('sub/../a.js', byPath), isNull);
    });
  });

  group('ArtifactCanvasViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactCanvasViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot render canvas'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactCanvasViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with refresh action',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactCanvasViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'Eval curve',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Eval curve'), findsOneWidget);
      expect(find.byTooltip('Reload canvas'), findsOneWidget);
    });
  });
}
