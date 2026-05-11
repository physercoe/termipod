import 'package:flutter/foundation.dart';

/// Artifact File Manifest V1 (AFM-V1) — the shared multi-file body
/// schema used by `code-bundle` and `canvas-app` artifacts.
///
/// Schema locked in `docs/plans/canvas-viewer.md` (2026-05-11).
/// Canonicalises the multi-file body the code-bundle viewer already
/// accepted (v1.0.494, W5 of artifact-type-registry) so the canvas
/// viewer can consume the same parser without two viewers drifting.
///
/// Wire shape:
///
///   {
///     "version": 1,                      // optional in legacy inputs
///     "entry": "index.html",             // optional; canvas-app only
///     "files": [
///       {"path": "...", "content": "...", "mime": "text/html"},
///       ...
///     ]
///   }
///
/// Name disambiguation: "manifest" is also taken by ADR-016's
/// operation-scope governance manifest (`roles.yaml`); the two
/// concepts never appear in the same context, but the "Artifact File"
/// qualifier keeps search legible.
class ArtifactFileManifest {
  final int version;
  final String? entry;
  final List<ArtifactFile> files;

  const ArtifactFileManifest({
    required this.version,
    this.entry,
    required this.files,
  });
}

class ArtifactFile {
  final String path;
  final String content;
  final String mime;

  const ArtifactFile({
    required this.path,
    required this.content,
    required this.mime,
  });
}

/// Parse a decoded JSON value into an [ArtifactFileManifest].
///
/// Accepts (in order of explicitness):
///
/// 1. AFM-V1 explicit: `{version: 1, entry?, files: [...]}`.
/// 2. Bare bundle (pre-V1 code-bundle): `{files: [...]}` — version
///    defaults to 1.
/// 3. Flat list: `[{path, content, ...}, …]` — version defaults to 1.
/// 4. Single-file degenerate: `{path, content}` — version defaults to 1.
///
/// Returns null for unrecognisable input or unsupported versions. Each
/// returned [ArtifactFile.mime] is non-empty — when the entry omits
/// `mime`, it is derived via [mimeForPath].
ArtifactFileManifest? parseArtifactFileManifest(dynamic decoded) {
  var version = 1;
  String? entry;
  List<dynamic>? rawFiles;

  if (decoded is Map) {
    if (decoded.containsKey('version')) {
      final v = decoded['version'];
      if (v is int) {
        version = v;
      } else {
        return null;
      }
    }
    if (version != 1) return null;
    if (decoded['entry'] is String) {
      entry = decoded['entry'] as String;
    }
    if (decoded['files'] is List) {
      rawFiles = decoded['files'] as List;
    } else if (decoded['path'] is String && decoded['content'] is String) {
      rawFiles = [decoded];
    } else {
      return null;
    }
  } else if (decoded is List) {
    rawFiles = decoded;
  } else {
    return null;
  }

  final files = <ArtifactFile>[];
  for (final item in rawFiles) {
    if (item is! Map) continue;
    final path = item['path'];
    final content = item['content'];
    if (path is! String || content is! String) continue;
    final declaredMime = item['mime'];
    final mime = declaredMime is String && declaredMime.isNotEmpty
        ? declaredMime
        : mimeForPath(path);
    files.add(ArtifactFile(path: path, content: content, mime: mime));
  }

  if (files.isEmpty) return null;
  return ArtifactFileManifest(version: version, entry: entry, files: files);
}

/// Resolves the canvas-app entry HTML per AFM-V1 rules:
///
/// 1. If `manifest.entry` is set and matches a file path, use it.
/// 2. Otherwise pick the file named `index.html`.
/// 3. Otherwise pick the first `.html`/`.htm` file in declaration order.
/// 4. Otherwise return null (caller surfaces the error).
ArtifactFile? resolveCanvasEntry(ArtifactFileManifest manifest) {
  final declared = manifest.entry;
  if (declared != null) {
    for (final f in manifest.files) {
      if (f.path == declared) return f;
    }
  }
  for (final f in manifest.files) {
    if (f.path == 'index.html') return f;
  }
  for (final f in manifest.files) {
    final lower = f.path.toLowerCase();
    if (lower.endsWith('.html') || lower.endsWith('.htm')) return f;
  }
  return null;
}

/// Derives an IANA MIME type from a POSIX path's extension. Falls back
/// to `text/plain` so callers always get a non-empty mime. Used by
/// [parseArtifactFileManifest] when a file entry omits `mime`; safe
/// for canvas inliner consumers that need an explicit type for
/// `data:` URI synthesis.
@visibleForTesting
String mimeForPath(String path) {
  final dot = path.lastIndexOf('.');
  if (dot < 0 || dot == path.length - 1) return 'text/plain';
  final ext = path.substring(dot + 1).toLowerCase();
  const map = <String, String>{
    'html': 'text/html',
    'htm': 'text/html',
    'css': 'text/css',
    'svg': 'image/svg+xml',
    'js': 'text/javascript',
    'mjs': 'text/javascript',
    'cjs': 'text/javascript',
    'json': 'application/json',
    'md': 'text/markdown',
    'txt': 'text/plain',
    'py': 'text/x-python',
    'ts': 'text/typescript',
    'tsx': 'text/typescript',
    'jsx': 'text/javascript',
    'go': 'text/x-go',
    'rs': 'text/rust',
    'java': 'text/x-java',
    'kt': 'text/x-kotlin',
    'swift': 'text/x-swift',
    'rb': 'text/x-ruby',
    'php': 'text/x-php',
    'c': 'text/x-c',
    'h': 'text/x-c',
    'cc': 'text/x-c++',
    'cpp': 'text/x-c++',
    'hpp': 'text/x-c++',
    'sh': 'application/x-sh',
    'bash': 'application/x-sh',
    'yaml': 'text/yaml',
    'yml': 'text/yaml',
    'toml': 'text/toml',
    'xml': 'text/xml',
    'scss': 'text/css',
    'tex': 'text/x-tex',
    'dart': 'text/x-dart',
    'png': 'image/png',
    'jpg': 'image/jpeg',
    'jpeg': 'image/jpeg',
    'gif': 'image/gif',
    'webp': 'image/webp',
  };
  return map[ext] ?? 'text/plain';
}
