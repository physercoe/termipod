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
/// The widget is the leaf — its parent [ArtifactPdfViewerScreen]
/// owns the `PdfViewerController` and `PdfTextSearcher` so the
/// AppBar can drive outline + search actions. v1.0.518 added these
/// integrations on top of the v1.0.515 native-assets backout.
class ArtifactPdfViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;
  final int? expectedSize;
  final PdfViewerController? controller;
  final PdfTextSearcher? searcher;
  final ValueChanged<List<PdfOutlineNode>?>? onOutlineLoaded;

  const ArtifactPdfViewer({
    super.key,
    required this.uri,
    this.title,
    this.expectedSize,
    this.controller,
    this.searcher,
    this.onOutlineLoaded,
  });

  @override
  ConsumerState<ArtifactPdfViewer> createState() => _ArtifactPdfViewerState();
}

class _ArtifactPdfViewerState extends ConsumerState<ArtifactPdfViewer> {
  Uint8List? _bytes;
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
    final searcher = widget.searcher;
    final paintCallbacks = searcher != null
        ? <PdfViewerPagePaintCallback>[searcher.pageTextMatchPaintCallback]
        : null;
    return ColoredBox(
      color: Colors.white,
      child: PdfViewer.data(
        bytes,
        sourceName: widget.title ?? widget.uri,
        controller: widget.controller,
        params: PdfViewerParams(
          backgroundColor: Colors.white,
          pagePaintCallbacks: paintCallbacks,
          onViewerReady: (document, controller) async {
            final outline = await document.loadOutline();
            widget.onOutlineLoaded?.call(outline);
          },
        ),
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
///
/// Owns the `PdfViewerController`, `PdfTextSearcher`, and outline state
/// so the AppBar can drive find-in-PDF and the end-drawer outline
/// jump-list. The viewer widget (`ArtifactPdfViewer`) is a leaf.
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
  late final PdfTextSearcher _searcher;
  late final TextEditingController _searchInput;
  final GlobalKey<ScaffoldState> _scaffoldKey = GlobalKey<ScaffoldState>();
  List<PdfOutlineNode>? _outline;
  bool _searchMode = false;

  @override
  void initState() {
    super.initState();
    _controller = PdfViewerController();
    _searcher = PdfTextSearcher(_controller)..addListener(_onSearcherUpdate);
    _searchInput = TextEditingController();
  }

  @override
  void dispose() {
    _searchInput.dispose();
    _searcher
      ..removeListener(_onSearcherUpdate)
      ..dispose();
    super.dispose();
  }

  void _onSearcherUpdate() {
    if (mounted) setState(() {});
  }

  void _submitSearch() {
    final q = _searchInput.text.trim();
    if (q.isEmpty) {
      _searcher.resetTextSearch();
    } else {
      _searcher.startTextSearch(q, caseInsensitive: true);
    }
  }

  void _toggleSearch() {
    setState(() {
      _searchMode = !_searchMode;
      if (!_searchMode) {
        _searchInput.clear();
        _searcher.resetTextSearch();
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final hasOutline = _outline != null && _outline!.isNotEmpty;
    return Scaffold(
      key: _scaffoldKey,
      appBar: _searchMode
          ? _buildSearchAppBar()
          : _buildDefaultAppBar(hasOutline),
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
        searcher: _searcher,
        onOutlineLoaded: (outline) {
          if (!mounted) return;
          setState(() => _outline = outline);
        },
      ),
    );
  }

  PreferredSizeWidget _buildDefaultAppBar(bool hasOutline) {
    return AppBar(
      title: Text(
        widget.title,
        style: GoogleFonts.spaceGrotesk(
            fontSize: 14, fontWeight: FontWeight.w700),
        overflow: TextOverflow.ellipsis,
      ),
      actions: [
        IconButton(
          icon: const Icon(Icons.search),
          tooltip: 'Find in PDF',
          onPressed: _toggleSearch,
        ),
        if (hasOutline)
          IconButton(
            icon: const Icon(Icons.menu_book_outlined),
            tooltip: 'Outline',
            onPressed: () => _scaffoldKey.currentState?.openEndDrawer(),
          ),
      ],
    );
  }

  PreferredSizeWidget _buildSearchAppBar() {
    final matches = _searcher.matches;
    final n = matches.length;
    final i = _searcher.currentIndex ?? -1;
    final label = n == 0
        ? (_searcher.isSearching ? 'searching…' : '—')
        : '${i + 1}/$n';
    return AppBar(
      leading: IconButton(
        icon: const Icon(Icons.close),
        tooltip: 'Close search',
        onPressed: _toggleSearch,
      ),
      title: TextField(
        controller: _searchInput,
        autofocus: true,
        textInputAction: TextInputAction.search,
        decoration: const InputDecoration(
          hintText: 'Find in PDF',
          border: InputBorder.none,
          isDense: true,
        ),
        onSubmitted: (_) => _submitSearch(),
        onChanged: (_) {
          // Reset match state if user clears the field mid-search;
          // otherwise wait for explicit submit (live-search would
          // re-scan every keystroke on multi-page PDFs).
          if (_searchInput.text.isEmpty) {
            _searcher.resetTextSearch();
          }
        },
        style: GoogleFonts.jetBrainsMono(fontSize: 13),
      ),
      actions: [
        Center(
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Text(
              label,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ),
        IconButton(
          icon: const Icon(Icons.keyboard_arrow_up),
          tooltip: 'Previous match',
          onPressed: n > 0 ? () => _searcher.goToPrevMatch() : null,
        ),
        IconButton(
          icon: const Icon(Icons.keyboard_arrow_down),
          tooltip: 'Next match',
          onPressed: n > 0 ? () => _searcher.goToNextMatch() : null,
        ),
      ],
    );
  }
}

/// End drawer listing the PDF's outline / bookmarks / table-of-
/// contents. Tap an entry to jump the viewer to its destination.
/// Hidden when the PDF carries no outline (most synthetic PDFs).
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
              child: list.isEmpty
                  ? Center(
                      child: Text(
                        'No outline',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 12,
                          color: DesignColors.textMuted,
                        ),
                      ),
                    )
                  : ListView.builder(
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
