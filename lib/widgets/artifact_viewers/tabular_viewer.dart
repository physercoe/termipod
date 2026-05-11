import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Renders a `tabular`-kind artifact (wave 2 W3 of artifact-type-registry).
///
/// Wire shape: JSON body resolved from a `blob:sha256/<sha>` URI via the
/// hub blob endpoint. Body is either a top-level list-of-objects
/// (`[{author: ..., year: ...}, …]`) or `{rows: [...]}` — both are
/// accepted because seed data and agent-emitted citations both occur
/// in the wild.
///
/// Schema discovery follows Q6 option (a): the MIME's `schema` param
/// (`application/json; schema=citation`) is the discriminator. Known
/// schemas pick a canonical column order; unknown schemas fall back
/// to the union of keys in the first 8 rows. No `_schema.json`
/// sibling artifact lookup today — escalate to option (c) only if
/// domain viewers proliferate.
class ArtifactTabularViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? mime;
  final String? title;

  const ArtifactTabularViewer({
    super.key,
    required this.uri,
    this.mime,
    this.title,
  });

  @override
  ConsumerState<ArtifactTabularViewer> createState() =>
      _ArtifactTabularViewerState();
}

class _ArtifactTabularViewerState extends ConsumerState<ArtifactTabularViewer> {
  List<Map<String, dynamic>>? _rows;
  List<String>? _columns;
  String? _schema;
  String? _error;
  bool _loading = true;

  static const _knownSchemaColumns = <String, List<String>>{
    'citation': ['author', 'year', 'title', 'venue', 'doi', 'notes'],
  };

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
      List<Map<String, dynamic>> rows;
      if (parsed is List) {
        rows = parsed
            .whereType<Map>()
            .map((m) => m.cast<String, dynamic>())
            .toList(growable: false);
      } else if (parsed is Map && parsed['rows'] is List) {
        rows = (parsed['rows'] as List)
            .whereType<Map>()
            .map((m) => m.cast<String, dynamic>())
            .toList(growable: false);
      } else {
        setState(() {
          _loading = false;
          _error = 'expected JSON list-of-objects or {rows: [...]}';
        });
        return;
      }
      final schema = _schemaFromMime(widget.mime);
      final columns = _columnsFor(schema, rows);
      setState(() {
        _rows = rows;
        _columns = columns;
        _schema = schema;
        _loading = false;
      });
    } catch (e) {
      setState(() {
        _loading = false;
        _error = 'parse error: $e';
      });
    }
  }

  static String? _schemaFromMime(String? mime) {
    if (mime == null || mime.isEmpty) return null;
    for (final part in mime.split(';')) {
      final trimmed = part.trim();
      if (trimmed.startsWith('schema=')) {
        return trimmed.substring('schema='.length).toLowerCase();
      }
    }
    return null;
  }

  static List<String> _columnsFor(
    String? schema,
    List<Map<String, dynamic>> rows,
  ) {
    if (schema != null) {
      final known = _knownSchemaColumns[schema];
      if (known != null) return known;
    }
    final seen = <String>{};
    final ordered = <String>[];
    for (final r in rows.take(8)) {
      for (final k in r.keys) {
        if (seen.add(k)) ordered.add(k);
      }
    }
    return ordered;
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _TabularLoadError(message: _error!, uri: widget.uri);
    }
    final rows = _rows;
    final cols = _columns;
    if (rows == null || cols == null) {
      return _TabularLoadError(message: 'no rows', uri: widget.uri);
    }
    if (rows.isEmpty) {
      return _TabularLoadError(
        message: 'table is empty (0 rows)',
        uri: widget.uri,
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _TabularHeader(
          rowCount: rows.length,
          columnCount: cols.length,
          schema: _schema,
        ),
        Expanded(
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: SingleChildScrollView(
              scrollDirection: Axis.vertical,
              child: DataTable(
                headingTextStyle: GoogleFonts.spaceGrotesk(
                    fontSize: 12, fontWeight: FontWeight.w700),
                dataTextStyle: GoogleFonts.jetBrainsMono(fontSize: 11),
                columnSpacing: 16,
                dataRowMinHeight: 28,
                dataRowMaxHeight: 64,
                columns: [
                  for (final c in cols) DataColumn(label: Text(c)),
                ],
                rows: [
                  for (final r in rows)
                    DataRow(
                      cells: [
                        for (final c in cols)
                          DataCell(_cellText(r[c])),
                      ],
                    ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }

  Widget _cellText(dynamic v) {
    final s = v == null ? '' : v.toString();
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 280),
      child: Text(
        s,
        overflow: TextOverflow.ellipsis,
        maxLines: 3,
      ),
    );
  }
}

class _TabularHeader extends StatelessWidget {
  final int rowCount;
  final int columnCount;
  final String? schema;
  const _TabularHeader({
    required this.rowCount,
    required this.columnCount,
    this.schema,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      child: Row(
        children: [
          Icon(Icons.table_chart_outlined,
              size: 14, color: DesignColors.textMuted),
          const SizedBox(width: 6),
          Text(
            '$rowCount rows · $columnCount cols'
            '${schema == null ? '' : ' · schema=$schema'}',
            style: GoogleFonts.jetBrainsMono(
                fontSize: 11, color: DesignColors.textMuted),
          ),
        ],
      ),
    );
  }
}

class _TabularLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _TabularLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.table_chart_outlined,
              size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render table',
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

/// Fullscreen route for the tabular viewer. Mirrors
/// `ArtifactPdfViewerScreen` so the kind dispatcher in
/// `artifacts_screen.dart` can route both the same way.
class ArtifactTabularViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  final String? mime;
  const ArtifactTabularViewerScreen({
    super.key,
    required this.uri,
    required this.title,
    this.mime,
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
      body: ArtifactTabularViewer(uri: uri, mime: mime, title: title),
    );
  }
}
