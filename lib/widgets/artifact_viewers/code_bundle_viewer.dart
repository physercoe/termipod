import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../services/artifact_manifest/artifact_manifest.dart';
import '../../theme/design_colors.dart';

/// Renders a `code-bundle`-kind artifact (wave 2 W5 of
/// artifact-type-registry). Read-only file tree with syntax highlighting.
///
/// Wire shape: JSON resolved from a `blob:sha256/<sha>` URI. Two
/// accepted shapes — picked because the registry plan calls for "agent
/// emitted scaffolds, ML run-script snapshots, paper LaTeX sources" and
/// the wild produces both flat lists and labelled manifests:
///
///   1. `{files: [{path: "src/foo.py", content: "..."}, …]}` — preferred.
///   2. `[{path: ..., content: ...}, …]` — flat-list shorthand.
///
/// Single-file degenerate shape `{path: "...", content: "..."}` also
/// works (treated as a one-entry bundle).
///
/// Language detection runs off the file extension; unknown extensions
/// drop to `plaintext` (still themed/padded, just uncoloured).
class ArtifactCodeBundleViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;

  const ArtifactCodeBundleViewer({
    super.key,
    required this.uri,
    this.title,
  });

  @override
  ConsumerState<ArtifactCodeBundleViewer> createState() =>
      _ArtifactCodeBundleViewerState();
}

class _ArtifactCodeBundleViewerState
    extends ConsumerState<ArtifactCodeBundleViewer> {
  List<CodeBundleFile>? _files;
  int _selected = 0;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final uri = widget.uri;
    if (!uri.startsWith('blob:sha256/')) {
      setState(() {
        _loading = false;
        _error = 'unsupported uri scheme — only hub-served blobs '
            '(blob:sha256/…) render today';
      });
      return;
    }
    final sha = uri.substring('blob:sha256/'.length);
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'hub not connected';
      });
      return;
    }
    try {
      final bytes = await client.downloadBlobCached(sha);
      if (!mounted) return;
      _parse(Uint8List.fromList(bytes));
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  void _parse(Uint8List bytes) {
    try {
      final text = utf8.decode(bytes);
      final parsed = jsonDecode(text);
      final files = parseCodeBundle(parsed);
      if (files.isEmpty) {
        setState(() {
          _loading = false;
          _error = 'bundle has no files';
        });
        return;
      }
      setState(() {
        _files = files;
        _selected = 0;
        _loading = false;
      });
    } catch (e) {
      setState(() {
        _loading = false;
        _error = 'parse error: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _CodeBundleLoadError(message: _error!, uri: widget.uri);
    }
    final files = _files;
    if (files == null || files.isEmpty) {
      return _CodeBundleLoadError(message: 'no files', uri: widget.uri);
    }
    final selected = files[_selected.clamp(0, files.length - 1)];
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _BundleFileBar(
          files: files,
          selected: _selected,
          onPick: (i) => setState(() => _selected = i),
        ),
        _BundleFileHeader(file: selected),
        Expanded(
          child: SingleChildScrollView(
            scrollDirection: Axis.vertical,
            child: SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 12),
              child: HighlightView(
                selected.content,
                language: selected.language,
                theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
                padding: const EdgeInsets.all(8),
                textStyle: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  height: 1.35,
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

/// One file inside a code-bundle. `language` is the highlight.js id the
/// view should pass to `HighlightView`; resolved from the path's
/// extension via [languageForPath]. Public so widget tests can read
/// the parser's output by field; not part of any other public surface.
class CodeBundleFile {
  final String path;
  final String content;
  final String language;
  const CodeBundleFile({
    required this.path,
    required this.content,
    required this.language,
  });
}

/// Parse a decoded JSON value into a list of [CodeBundleFile]s. Thin
/// adapter over the shared [parseArtifactFileManifest] (AFM-V1) — the
/// viewer keeps its own value type because it needs a highlight.js
/// language id alongside the path, which the shared manifest doesn't
/// carry. Returns an empty list for shapes the viewer cannot render
/// rather than throwing — the build() error path renders "bundle has
/// no files" with the original URI.
@visibleForTesting
List<CodeBundleFile> parseCodeBundle(dynamic decoded) {
  final manifest = parseArtifactFileManifest(decoded);
  if (manifest == null) return const [];
  return manifest.files
      .map((f) => CodeBundleFile(
            path: f.path,
            content: f.content,
            language: languageForPath(f.path),
          ))
      .toList(growable: false);
}

/// Resolves a highlight.js language id from a file path. Falls back to
/// `plaintext` for unknown extensions (still themed/padded, just
/// uncoloured). Kept separate from the markdown fence-class normaliser
/// in `markdown_builders.dart` because the input domain differs —
/// fences carry a free-form language tag, paths carry an extension.
@visibleForTesting
String languageForPath(String path) {
  final dot = path.lastIndexOf('.');
  if (dot < 0 || dot == path.length - 1) return 'plaintext';
  final ext = path.substring(dot + 1).toLowerCase();
  const map = <String, String>{
    'py': 'python',
    'js': 'javascript',
    'mjs': 'javascript',
    'cjs': 'javascript',
    'jsx': 'javascript',
    'ts': 'typescript',
    'tsx': 'typescript',
    'go': 'go',
    'rs': 'rust',
    'java': 'java',
    'kt': 'kotlin',
    'swift': 'swift',
    'rb': 'ruby',
    'php': 'php',
    'c': 'c',
    'h': 'cpp',
    'cc': 'cpp',
    'cpp': 'cpp',
    'cxx': 'cpp',
    'hpp': 'cpp',
    'cs': 'cs',
    'm': 'objectivec',
    'mm': 'objectivec',
    'sh': 'bash',
    'bash': 'bash',
    'zsh': 'bash',
    'sql': 'sql',
    'yaml': 'yaml',
    'yml': 'yaml',
    'json': 'json',
    'toml': 'ini',
    'ini': 'ini',
    'xml': 'xml',
    'html': 'xml',
    'css': 'css',
    'scss': 'scss',
    'md': 'markdown',
    'tex': 'latex',
    'dart': 'dart',
    'dockerfile': 'dockerfile',
    'makefile': 'makefile',
  };
  return map[ext] ?? 'plaintext';
}

class _BundleFileBar extends StatelessWidget {
  final List<CodeBundleFile> files;
  final int selected;
  final ValueChanged<int> onPick;
  const _BundleFileBar({
    required this.files,
    required this.selected,
    required this.onPick,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 40,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        itemCount: files.length,
        separatorBuilder: (_, __) => const SizedBox(width: 6),
        itemBuilder: (_, i) {
          final isSel = i == selected;
          return InkWell(
            onTap: () => onPick(i),
            borderRadius: BorderRadius.circular(14),
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              decoration: BoxDecoration(
                color: isSel
                    ? DesignColors.primary.withValues(alpha: 0.2)
                    : Colors.transparent,
                borderRadius: BorderRadius.circular(14),
                border: Border.all(
                  color: isSel
                      ? DesignColors.primary
                      : DesignColors.borderDark,
                ),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    Icons.insert_drive_file_outlined,
                    size: 11,
                    color: isSel ? DesignColors.primary : DesignColors.textMuted,
                  ),
                  const SizedBox(width: 6),
                  Text(
                    files[i].path,
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 11,
                      fontWeight: isSel ? FontWeight.w700 : FontWeight.w500,
                      color: isSel
                          ? DesignColors.primary
                          : DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

class _BundleFileHeader extends StatelessWidget {
  final CodeBundleFile file;
  const _BundleFileHeader({required this.file});

  @override
  Widget build(BuildContext context) {
    final lines = '\n'.allMatches(file.content).length + 1;
    final bytes = utf8.encode(file.content).length;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 6),
      child: Row(
        children: [
          Icon(Icons.code, size: 14, color: DesignColors.textMuted),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              '${file.path} · ${file.language} · $lines lines · ${_formatBytes(bytes)}',
              overflow: TextOverflow.ellipsis,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _formatBytes(int b) {
    if (b < 1024) return '${b}B';
    final kb = b / 1024;
    if (kb < 1024) return '${kb.toStringAsFixed(1)}KB';
    final mb = kb / 1024;
    return '${mb.toStringAsFixed(1)}MB';
  }
}

class _CodeBundleLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _CodeBundleLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.code, size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render code bundle',
            style: GoogleFonts.spaceGrotesk(
                fontSize: 14, fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 6),
          Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 11, color: DesignColors.textMuted),
          ),
          const SizedBox(height: 8),
          SelectableText(
            uri,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 10, color: DesignColors.textMuted),
          ),
        ],
      ),
    );
  }
}

/// Fullscreen route for the code-bundle viewer. Mirrors the other
/// wave-2 viewer screens so the kind dispatcher in
/// `artifacts_screen.dart` routes all three the same way.
class ArtifactCodeBundleViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactCodeBundleViewerScreen({
    super.key,
    required this.uri,
    required this.title,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: ArtifactCodeBundleViewer(uri: uri, title: title),
    );
  }
}
