import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/artifact_manifest/artifact_manifest.dart';

void main() {
  group('parseArtifactFileManifest', () {
    test('AFM-V1 explicit form parses version + entry + files', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'entry': 'index.html',
        'files': [
          {'path': 'index.html', 'content': '<!doctype html>'},
          {'path': 'chart.js', 'content': 'const x = 1'},
        ],
      });
      expect(m, isNotNull);
      expect(m!.version, 1);
      expect(m.entry, 'index.html');
      expect(m.files, hasLength(2));
      expect(m.files[0].path, 'index.html');
      expect(m.files[0].mime, 'text/html');
      expect(m.files[1].mime, 'text/javascript');
    });

    test('legacy {files: [...]} shape parses with version=1 default', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'train.py', 'content': 'print(1)'},
        ],
      });
      expect(m, isNotNull);
      expect(m!.version, 1);
      expect(m.entry, isNull);
      expect(m.files, hasLength(1));
      expect(m.files[0].mime, 'text/x-python');
    });

    test('flat list shape parses with version=1 default', () {
      final m = parseArtifactFileManifest([
        {'path': 'main.go', 'content': 'package main'},
      ]);
      expect(m, isNotNull);
      expect(m!.files, hasLength(1));
      expect(m.files[0].mime, 'text/x-go');
    });

    test('single-file degenerate shape parses', () {
      final m = parseArtifactFileManifest({
        'path': 'a.rs',
        'content': 'fn main() {}',
      });
      expect(m, isNotNull);
      expect(m!.files, hasLength(1));
      expect(m.files[0].path, 'a.rs');
      expect(m.files[0].mime, 'text/rust');
    });

    test('declared mime is preserved verbatim', () {
      final m = parseArtifactFileManifest({
        'files': [
          {
            'path': 'data.bin',
            'content': '...',
            'mime': 'application/x-custom',
          },
        ],
      });
      expect(m!.files[0].mime, 'application/x-custom');
    });

    test('unsupported shapes return null', () {
      expect(parseArtifactFileManifest('not a manifest'), isNull);
      expect(parseArtifactFileManifest(42), isNull);
      expect(parseArtifactFileManifest({'foo': 'bar'}), isNull);
    });

    test('unknown version is rejected', () {
      expect(
        parseArtifactFileManifest({
          'version': 2,
          'files': [
            {'path': 'a.py', 'content': 'x'}
          ],
        }),
        isNull,
      );
    });

    test('non-int version is rejected', () {
      expect(
        parseArtifactFileManifest({
          'version': '1',
          'files': [
            {'path': 'a.py', 'content': 'x'}
          ],
        }),
        isNull,
      );
    });

    test('drops entries missing path or content; empty list yields null', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'ok.py', 'content': 'x'},
          {'path': 'no-content.py'},
          {'content': 'no path'},
          'not even a map',
        ],
      });
      expect(m, isNotNull);
      expect(m!.files, hasLength(1));
      expect(m.files[0].path, 'ok.py');

      expect(
        parseArtifactFileManifest({
          'files': [
            {'path': 'no-content.py'},
          ],
        }),
        isNull,
      );
    });
  });

  group('resolveCanvasEntry', () {
    test('explicit entry wins', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'entry': 'app.html',
        'files': [
          {'path': 'index.html', 'content': 'a'},
          {'path': 'app.html', 'content': 'b'},
        ],
      })!;
      expect(resolveCanvasEntry(m)?.path, 'app.html');
    });

    test('falls back to index.html', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'lib.js', 'content': 'a'},
          {'path': 'index.html', 'content': 'b'},
        ],
      })!;
      expect(resolveCanvasEntry(m)?.path, 'index.html');
    });

    test('falls back to first .html/.htm in declaration order', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'lib.js', 'content': 'a'},
          {'path': 'page.htm', 'content': 'b'},
          {'path': 'other.html', 'content': 'c'},
        ],
      })!;
      expect(resolveCanvasEntry(m)?.path, 'page.htm');
    });

    test('returns null when no HTML present', () {
      final m = parseArtifactFileManifest({
        'files': [
          {'path': 'a.py', 'content': 'x'},
        ],
      })!;
      expect(resolveCanvasEntry(m), isNull);
    });

    test('explicit entry that does not match falls through to fallbacks', () {
      final m = parseArtifactFileManifest({
        'version': 1,
        'entry': 'missing.html',
        'files': [
          {'path': 'index.html', 'content': 'b'},
        ],
      })!;
      expect(resolveCanvasEntry(m)?.path, 'index.html');
    });
  });

  group('mimeForPath', () {
    test('common extensions map to IANA types', () {
      expect(mimeForPath('index.html'), 'text/html');
      expect(mimeForPath('style.CSS'), 'text/css');
      expect(mimeForPath('asset.svg'), 'image/svg+xml');
      expect(mimeForPath('app.js'), 'text/javascript');
      expect(mimeForPath('train.py'), 'text/x-python');
      expect(mimeForPath('photo.PNG'), 'image/png');
    });

    test('unknown or missing extension falls back to text/plain', () {
      expect(mimeForPath('Makefile'), 'text/plain');
      expect(mimeForPath('foo.weirdext'), 'text/plain');
      expect(mimeForPath('trailing.'), 'text/plain');
    });
  });
}
