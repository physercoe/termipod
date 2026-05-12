import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:pdfrx/pdfrx.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Renders a `pdf`-kind artifact (wave 2 W2 of artifact-type-registry).
///
/// Resolves the artifact URI to bytes via the hub blob endpoint
/// (`blob:sha256/<sha>` → `/v1/blobs/<sha>` with bearer auth +
/// content-addressed disk cache) and feeds them into pdfrx for
/// rendering. URIs in non-`blob:sha256/` schemes (mock seed data,
/// external HTTPS, etc.) show an explicit "cannot load" message.
///
/// v1.0.514 added a diagnostic strip at the bottom showing the load
/// pipeline state (bytes → pdfium → pages) — four prior releases
/// chased "white page" with speculative fixes (encoding, page geometry,
/// init call) and none moved the needle because we had no signal as to
/// which stage was failing. The strip puts that signal on screen so
/// the next tester screenshot tells us where pdfium stalls instead of
/// requiring another guess-and-ship loop.
class ArtifactPdfViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;
  final int? expectedSize;

  const ArtifactPdfViewer({
    super.key,
    required this.uri,
    this.title,
    this.expectedSize,
  });

  @override
  ConsumerState<ArtifactPdfViewer> createState() => _ArtifactPdfViewerState();
}

class _ArtifactPdfViewerState extends ConsumerState<ArtifactPdfViewer> {
  Uint8List? _bytes;
  String? _error;
  bool _loading = true;
  // pdfium-side state (populated from PdfViewerParams callbacks).
  // `null` = not yet reported, `true` = success, `false` = failure.
  bool? _pdfiumLoadOk;
  String? _pdfiumError;
  int? _pageCount;

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
      setState(() {
        _bytes = Uint8List.fromList(bytes);
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _PdfLoadError(message: _error!, uri: widget.uri);
    }
    final bytes = _bytes;
    if (bytes == null) {
      return _PdfLoadError(message: 'no bytes', uri: widget.uri);
    }
    // White viewport so any transparent page-fill in a minimal PDF
    // doesn't blend into the Scaffold's gray. ColoredBox + the params'
    // backgroundColor are belt-and-suspenders for pdfium builds that
    // skip painting the page background.
    //
    // `useProgressiveLoading: false` — our seed + uploaded PDFs are
    // small (a few KB to a few MB) and the progressive-loading path
    // has been the source of "white page" rendering bugs in the past
    // (pdfrx#617, merged 2026-05-08). Plain-load is cheaper and more
    // reliable for sub-25 MiB blobs.
    return Column(
      children: [
        Expanded(
          child: ColoredBox(
            color: Colors.white,
            child: PdfViewer.data(
              bytes,
              sourceName: widget.title ?? widget.uri,
              useProgressiveLoading: false,
              params: PdfViewerParams(
                backgroundColor: Colors.white,
                onDocumentLoadFinished: _onLoadFinished,
                onDocumentChanged: _onDocumentChanged,
              ),
            ),
          ),
        ),
        _PdfDiagnosticStrip(
          byteCount: bytes.length,
          pdfiumLoadOk: _pdfiumLoadOk,
          pdfiumError: _pdfiumError,
          pageCount: _pageCount,
          uri: widget.uri,
        ),
      ],
    );
  }

  void _onLoadFinished(PdfDocumentRef docRef, bool ok) {
    if (!mounted) return;
    final listenable = docRef.resolveListenable();
    setState(() {
      _pdfiumLoadOk = ok;
      _pdfiumError = ok ? null : (listenable.error?.toString() ?? 'unknown');
      _pageCount = listenable.document?.pages.length;
    });
  }

  void _onDocumentChanged(PdfDocument? document) {
    if (!mounted) return;
    setState(() {
      _pageCount = document?.pages.length;
    });
  }
}

class _PdfDiagnosticStrip extends StatelessWidget {
  final int byteCount;
  final bool? pdfiumLoadOk;
  final String? pdfiumError;
  final int? pageCount;
  final String uri;

  const _PdfDiagnosticStrip({
    required this.byteCount,
    required this.pdfiumLoadOk,
    required this.pdfiumError,
    required this.pageCount,
    required this.uri,
  });

  @override
  Widget build(BuildContext context) {
    final String status;
    final Color color;
    if (pdfiumLoadOk == null) {
      status = 'pdfium: loading…';
      color = DesignColors.textMuted;
    } else if (pdfiumLoadOk == true) {
      status = 'pdfium: ok · ${pageCount ?? "?"} pages';
      color = Colors.greenAccent.shade700;
    } else {
      status = 'pdfium: failed';
      color = DesignColors.error;
    }
    final kib = (byteCount / 1024).toStringAsFixed(byteCount < 10240 ? 1 : 0);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: const BoxDecoration(
        color: DesignColors.surfaceDark,
        border: Border(
          top: BorderSide(color: DesignColors.borderDark, width: 0.5),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Icon(Icons.bug_report_outlined, size: 12, color: color),
              const SizedBox(width: 6),
              Text(
                'bytes: ${kib}KiB · $status',
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 10.5,
                  color: color,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          if (pdfiumError != null && pdfiumError!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 2, left: 18),
              child: Text(
                pdfiumError!,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 9.5,
                  color: DesignColors.error,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _PdfLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _PdfLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.picture_as_pdf_outlined,
              size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render PDF',
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

/// Fullscreen route for the PDF viewer. Lifts the artifact detail sheet
/// out of the way so pinch-zoom + page navigation aren't fighting the
/// DraggableScrollableSheet for vertical drag gestures.
class ArtifactPdfViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactPdfViewerScreen({
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
      body: ArtifactPdfViewer(uri: uri, title: title),
    );
  }
}
