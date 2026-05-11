import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:path_provider/path_provider.dart';
import 'package:video_player/video_player.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Renders a `video`-kind artifact (wave 2 W6 of artifact-type-registry).
///
/// Same staging pattern as the audio viewer — video_player wants a
/// file path; bytes from `downloadBlobCached` are written into temp
/// before the controller is initialised. Temp file is best-effort
/// cleaned on dispose.
class ArtifactVideoViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;

  const ArtifactVideoViewer({
    super.key,
    required this.uri,
    this.title,
  });

  @override
  ConsumerState<ArtifactVideoViewer> createState() =>
      _ArtifactVideoViewerState();
}

class _ArtifactVideoViewerState extends ConsumerState<ArtifactVideoViewer> {
  VideoPlayerController? _controller;
  File? _temp;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _controller?.dispose();
    final t = _temp;
    if (t != null) {
      t.delete().catchError((_) => t);
    }
    super.dispose();
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
      final dir = await getTemporaryDirectory();
      final file = File('${dir.path}/video-$sha');
      await file.writeAsBytes(bytes, flush: true);
      if (!mounted) return;
      _temp = file;
      final controller = VideoPlayerController.file(file);
      await controller.initialize();
      if (!mounted) {
        await controller.dispose();
        return;
      }
      setState(() {
        _controller = controller;
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
      return _VideoLoadError(message: _error!, uri: widget.uri);
    }
    final controller = _controller;
    if (controller == null || !controller.value.isInitialized) {
      return _VideoLoadError(message: 'controller not ready', uri: widget.uri);
    }
    return Center(
      child: AspectRatio(
        aspectRatio: controller.value.aspectRatio,
        child: Stack(
          alignment: Alignment.bottomCenter,
          children: [
            VideoPlayer(controller),
            VideoProgressIndicator(controller, allowScrubbing: true),
            Positioned.fill(
              child: Center(
                child: ValueListenableBuilder<VideoPlayerValue>(
                  valueListenable: controller,
                  builder: (_, v, __) => IconButton(
                    iconSize: 56,
                    icon: Icon(
                      v.isPlaying ? Icons.pause_circle : Icons.play_circle,
                      color: Colors.white.withValues(alpha: 0.85),
                    ),
                    onPressed: () => v.isPlaying
                        ? controller.pause()
                        : controller.play(),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _VideoLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _VideoLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.movie_outlined, size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot play video',
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

/// Fullscreen route for the video viewer.
class ArtifactVideoViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactVideoViewerScreen({
    super.key,
    required this.uri,
    required this.title,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: ArtifactVideoViewer(uri: uri, title: title),
    );
  }
}
