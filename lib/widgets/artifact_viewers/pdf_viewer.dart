import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:pdfrx/pdfrx.dart';
import 'package:url_launcher/url_launcher.dart';

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
  // v1.0.524 (recovery step 2): a local PdfViewerController so we
  // can route internal page-ref taps via goToDest. v1.0.523 confirmed
  // linkHandlerParams alone doesn't regress rendering; v1.0.524
  // confirmed `controller:` passing is also fine. Kept local to this
  // State (not threaded from the screen) so it can't interact with
  // any other surface.
  late final PdfViewerController _controller;
  // v1.0.525 (recovery step 3): track current page via onPageChanged.
  // 0 means "not reported yet" — the badge is hidden until pdfrx
  // emits the first page-change event. No total page count yet (that
  // would need onViewerReady, the next bisect step).
  int _currentPage = 0;

  @override
  void initState() {
    super.initState();
    _controller = PdfViewerController();
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
    // ColoredBox + the params' backgroundColor white-paint the
    // viewport so a transparent page-fill in a minimal PDF doesn't
    // blend into the Scaffold's gray.
    //
    // The v1.0.514 diagnostic strip + 10 s watchdog were removed in
    // v1.0.517 once the pdfrx 2.2.24 pin confirmed rendering works
    // — the strip's "TIMEOUT" message was misleading on the happy
    // path because `onDocumentLoadFinished` doesn't fire reliably in
    // 2.2.x even when rendering succeeds. If a future regression
    // calls for diagnostics again, resurrect from git history at
    // commit 6dc5614.
    // v1.0.525 (recovery step 3): add the onPageChanged callback
    // alone — no onViewerReady, no pagePaintCallbacks, no overlay
    // builder. If rendering stays OK, that callback is innocent and
    // we move to onViewerReady as the next bisect.
    return Stack(
      children: [
        ColoredBox(
          color: Colors.white,
          child: PdfViewer.data(
            bytes,
            sourceName: widget.title ?? widget.uri,
            controller: _controller,
            params: PdfViewerParams(
              backgroundColor: Colors.white,
              linkHandlerParams: PdfLinkHandlerParams(
                onLinkTap: (link) async {
                  final url = link.url;
                  if (url != null) {
                    await launchUrl(url,
                        mode: LaunchMode.externalApplication);
                    return;
                  }
                  final dest = link.dest;
                  if (dest != null) {
                    await _controller.goToDest(dest);
                  }
                },
              ),
              onPageChanged: (page) {
                if (!mounted) return;
                setState(() => _currentPage = page ?? 0);
              },
            ),
          ),
        ),
        if (_currentPage > 0)
          Positioned(
            bottom: 8,
            left: 0,
            right: 0,
            child: IgnorePointer(
              child: Center(
                child: Container(
                  padding: const EdgeInsets.symmetric(
                      horizontal: 10, vertical: 4),
                  decoration: BoxDecoration(
                    color: Colors.black.withValues(alpha: 0.55),
                    borderRadius: BorderRadius.circular(12),
                  ),
                  child: Text(
                    '$_currentPage',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10.5,
                      color: Colors.white,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ),
            ),
          ),
      ],
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
