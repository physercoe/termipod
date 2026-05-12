import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Renders an `image`-kind artifact (wave 2 W4 — view-on-tap).
///
/// Resolves the artifact URI to bytes via the hub blob endpoint
/// (`blob:sha256/<sha>` → `/v1/blobs/<sha>`) and renders the result
/// with `Image.memory` inside an `InteractiveViewer` so the user can
/// pinch-zoom and pan. Non-`blob:sha256/` URI schemes (mock seed
/// data, external HTTPS, etc.) show an explicit "cannot load" card —
/// matches the PdfViewer error surface.
class ArtifactImageViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;
  const ArtifactImageViewer({super.key, required this.uri, this.title});

  @override
  ConsumerState<ArtifactImageViewer> createState() =>
      _ArtifactImageViewerState();
}

class _ArtifactImageViewerState extends ConsumerState<ArtifactImageViewer> {
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
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return _ImageLoadError(message: _error!, uri: widget.uri);
    }
    final bytes = _bytes;
    if (bytes == null) {
      return _ImageLoadError(message: 'no bytes', uri: widget.uri);
    }
    // InteractiveViewer feeds tight constraints to its child (default
    // `constrained: true`). A `Center` wrapper would then re-loosen the
    // constraints handed to `Image.memory`, which collapses to its
    // intrinsic size — so a 4000×3000 photo rendered at 4000×3000 and
    // ran off-screen on smaller devices (v1.0.510 tester report). Drop
    // the `Center` so `Image.memory` receives the viewport's tight box
    // directly and `BoxFit.contain` can scale-to-fit on first paint.
    // Pinch-zoom + pan still work because that's `InteractiveViewer`'s
    // job, not the child's.
    return InteractiveViewer(
      minScale: 0.5,
      maxScale: 8.0,
      child: SizedBox.expand(
        child: Image.memory(
          bytes,
          fit: BoxFit.contain,
          gaplessPlayback: true,
        ),
      ),
    );
  }
}

class _ImageLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _ImageLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.broken_image_outlined,
              size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render image',
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

class ArtifactImageViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactImageViewerScreen({
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
      body: ArtifactImageViewer(uri: uri, title: title),
    );
  }
}
