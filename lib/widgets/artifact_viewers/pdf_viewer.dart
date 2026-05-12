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
  // v1.0.527: controller threaded down from the screen so the screen
  // can host the TOC drawer + (later) text-search affordances. If
  // null, the leaf creates its own — preserves the v1.0.524 self-
  // contained mode for any callers that want a bare viewer.
  final PdfViewerController? controller;
  // Outline loaded callback — fires (once, deferred to post-frame)
  // when pdfrx reports the document's outline tree. Null on PDFs
  // with no outline; non-null even when empty so the screen can
  // distinguish "no outline" from "still loading."
  final ValueChanged<List<PdfOutlineNode>?>? onOutlineLoaded;

  const ArtifactPdfViewer({
    super.key,
    required this.uri,
    this.title,
    this.expectedSize,
    this.controller,
    this.onOutlineLoaded,
  });

  @override
  ConsumerState<ArtifactPdfViewer> createState() => _ArtifactPdfViewerState();
}

class _ArtifactPdfViewerState extends ConsumerState<ArtifactPdfViewer> {
  Uint8List? _bytes;
  String? _error;
  bool _loading = true;
  // v1.0.524 (recovery step 2): a local PdfViewerController so we
  // can route internal page-ref taps via goToDest. v1.0.527: now
  // optional — the screen can pass its own to host the TOC drawer.
  // Falls back to an internally-created controller if the caller
  // didn't supply one.
  PdfViewerController? _ownedController;
  PdfViewerController get _controller =>
      widget.controller ?? _ownedController!;
  // v1.0.525 (recovery step 3): track current page via onPageChanged.
  // 0 means "not reported yet" — the badge is hidden until pdfrx
  // emits the first page-change event.
  int _currentPage = 0;
  // v1.0.526 (recovery step 4): total page count via onViewerReady.
  // The setState is deferred to a post-frame callback because
  // onViewerReady may fire during pdfrx's first build pass — a
  // synchronous setState there triggers Flutter's "setState during
  // build" assertion which renders ErrorWidget (full-screen gray).
  // Wrapping with addPostFrameCallback bounces the state update to
  // the next frame. This is the prime hypothesis for v1.0.518's
  // gray-screen since that version's onViewerReady called
  // widget.onOutlineLoaded synchronously.
  int _pageCount = 0;

  @override
  void initState() {
    super.initState();
    if (widget.controller == null) {
      _ownedController = PdfViewerController();
    }
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
              onViewerReady: (document, _) {
                // Defer the setState — onViewerReady can fire during
                // pdfrx's build pass and a synchronous setState there
                // would trigger Flutter's "called during build"
                // assertion and gray the viewport.
                WidgetsBinding.instance.addPostFrameCallback((_) async {
                  if (!mounted) return;
                  setState(() => _pageCount = document.pages.length);
                  // v1.0.527: load the outline tree on the same
                  // deferred frame and bubble up to the screen. PDFs
                  // without an outline return null/empty; the screen
                  // hides the drawer in that case.
                  final cb = widget.onOutlineLoaded;
                  if (cb != null) {
                    try {
                      final outline = await document.loadOutline();
                      if (!mounted) return;
                      cb(outline);
                    } catch (_) {
                      // loadOutline failures (malformed outline, etc.)
                      // just leave the drawer hidden; not fatal.
                    }
                  }
                });
              },
            ),
          ),
        ),
        if (_currentPage > 0)
          Positioned(
            bottom: 8,
            left: 0,
            right: 0,
            child: Center(
              // v1.0.530: tappable page badge → "Go to page" dialog.
              // Only tappable when pdfrx has reported the total page
              // count (otherwise the validator can't bound the input).
              // Below the badge the rest of the viewport is unblocked
              // because Positioned + Center sizes to content.
              child: Material(
                color: Colors.transparent,
                child: InkWell(
                  borderRadius: BorderRadius.circular(12),
                  onTap: _pageCount > 0 ? _showGoToPageDialog : null,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                        horizontal: 10, vertical: 4),
                    decoration: BoxDecoration(
                      color: Colors.black.withValues(alpha: 0.55),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Text(
                      _pageCount > 0
                          ? '$_currentPage / $_pageCount'
                          : '$_currentPage',
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
          ),
      ],
    );
  }

  Future<void> _showGoToPageDialog() async {
    final controller = TextEditingController(text: '$_currentPage');
    final formKey = GlobalKey<FormState>();
    final target = await showDialog<int>(
      context: context,
      builder: (ctx) {
        return AlertDialog(
          title: Text(
            'Go to page',
            style: GoogleFonts.spaceGrotesk(
                fontSize: 14, fontWeight: FontWeight.w700),
          ),
          content: Form(
            key: formKey,
            child: TextFormField(
              controller: controller,
              autofocus: true,
              keyboardType: TextInputType.number,
              textInputAction: TextInputAction.go,
              decoration: InputDecoration(
                hintText: '1 – $_pageCount',
                border: const OutlineInputBorder(),
              ),
              style: GoogleFonts.jetBrainsMono(fontSize: 13),
              validator: (s) {
                if (s == null || s.isEmpty) return 'enter a page number';
                final n = int.tryParse(s);
                if (n == null) return 'not a number';
                if (n < 1 || n > _pageCount) return '1 – $_pageCount';
                return null;
              },
              onFieldSubmitted: (_) {
                if (formKey.currentState!.validate()) {
                  Navigator.of(ctx).pop(int.parse(controller.text));
                }
              },
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                if (formKey.currentState!.validate()) {
                  Navigator.of(ctx).pop(int.parse(controller.text));
                }
              },
              child: const Text('Go'),
            ),
          ],
        );
      },
    );
    controller.dispose();
    if (target != null) {
      await _controller.goToPage(pageNumber: target);
    }
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
///
/// v1.0.527: owns the `PdfViewerController` and the outline state so
/// the AppBar can host an "Outline" icon → end-drawer with the
/// document's table-of-contents. The leaf viewer (`ArtifactPdfViewer`)
/// is given the controller via constructor and bubbles the outline up
/// through `onOutlineLoaded`. AppBar icon is hidden until the outline
/// resolves and is non-empty (so synthetic PDFs without an outline
/// don't tease an empty drawer).
class ArtifactPdfViewerScreen extends StatefulWidget {
  final String uri;
  final String title;
  const ArtifactPdfViewerScreen({
    super.key,
    required this.uri,
    required this.title,
  });

  @override
  State<ArtifactPdfViewerScreen> createState() =>
      _ArtifactPdfViewerScreenState();
}

class _ArtifactPdfViewerScreenState extends State<ArtifactPdfViewerScreen> {
  late final PdfViewerController _controller;
  final GlobalKey<ScaffoldState> _scaffoldKey = GlobalKey<ScaffoldState>();
  List<PdfOutlineNode>? _outline;

  @override
  void initState() {
    super.initState();
    _controller = PdfViewerController();
  }

  @override
  Widget build(BuildContext context) {
    final hasOutline = _outline != null && _outline!.isNotEmpty;
    return Scaffold(
      key: _scaffoldKey,
      appBar: AppBar(
        title: Text(
          widget.title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          if (hasOutline)
            IconButton(
              icon: const Icon(Icons.menu_book_outlined),
              tooltip: 'Outline',
              onPressed: () => _scaffoldKey.currentState?.openEndDrawer(),
            ),
        ],
      ),
      endDrawer: hasOutline
          ? _OutlineDrawer(
              outline: _outline!,
              controller: _controller,
              onTap: () => Navigator.of(context).maybePop(),
            )
          : null,
      body: ArtifactPdfViewer(
        uri: widget.uri,
        title: widget.title,
        controller: _controller,
        onOutlineLoaded: (outline) {
          if (!mounted) return;
          setState(() => _outline = outline);
        },
      ),
    );
  }
}

/// End drawer listing the PDF's outline / bookmarks / TOC. Tap an
/// entry to jump the viewer to its destination. The leaf viewer
/// owns the heavy lifting; this widget is just presentation.
class _OutlineDrawer extends StatelessWidget {
  final List<PdfOutlineNode> outline;
  final PdfViewerController controller;
  final VoidCallback? onTap;

  const _OutlineDrawer({
    required this.outline,
    required this.controller,
    this.onTap,
  });

  Iterable<({PdfOutlineNode node, int level})> _flatten(
    List<PdfOutlineNode>? nodes,
    int level,
  ) sync* {
    if (nodes == null) return;
    for (final n in nodes) {
      yield (node: n, level: level);
      yield* _flatten(n.children, level + 1);
    }
  }

  @override
  Widget build(BuildContext context) {
    final list = _flatten(outline, 0).toList();
    return Drawer(
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Text(
                'Outline',
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView.builder(
                itemCount: list.length,
                itemBuilder: (ctx, i) {
                  final item = list[i];
                  final dest = item.node.dest;
                  return InkWell(
                    onTap: dest == null
                        ? null
                        : () {
                            controller.goToDest(dest);
                            onTap?.call();
                          },
                    child: Padding(
                      padding: EdgeInsets.fromLTRB(
                        item.level * 16.0 + 12,
                        10,
                        12,
                        10,
                      ),
                      child: Text(
                        item.node.title,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: GoogleFonts.spaceGrotesk(
                          fontSize: 13,
                          fontWeight: item.level == 0
                              ? FontWeight.w600
                              : FontWeight.w400,
                          color: dest == null
                              ? DesignColors.textMuted
                              : null,
                        ),
                      ),
                    ),
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}
