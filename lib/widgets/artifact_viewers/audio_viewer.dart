import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:just_audio/just_audio.dart';
import 'package:path_provider/path_provider.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Renders an `audio`-kind artifact (wave 2 W6 of artifact-type-registry).
///
/// just_audio cannot take raw bytes — it wants a URI, file, or asset.
/// We resolve the artifact's `blob:sha256/<sha>` URI via
/// `HubClient.downloadBlobCached`, stage the bytes into the app's
/// temporary directory, then hand the path to `setFilePath`. The temp
/// file is best-effort cleaned on widget disposal; the OS evicts the
/// directory on its own schedule if disposal is skipped.
class ArtifactAudioViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;

  const ArtifactAudioViewer({
    super.key,
    required this.uri,
    this.title,
  });

  @override
  ConsumerState<ArtifactAudioViewer> createState() =>
      _ArtifactAudioViewerState();
}

class _ArtifactAudioViewerState extends ConsumerState<ArtifactAudioViewer> {
  // Lazy so the error path (unsupported URI) doesn't touch the
  // just_audio platform channel — keeps widget tests free of the
  // MissingPluginException that the channel raises in flutter_test.
  AudioPlayer? _player;
  File? _temp;
  String? _error;
  bool _loading = true;
  Duration _duration = Duration.zero;
  Duration _position = Duration.zero;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _player?.dispose();
    final t = _temp;
    if (t != null) {
      // Fire-and-forget. Failures here are fine — temp dir gets evicted
      // by the OS on its own schedule, and a leaked file doesn't break
      // anything load-bearing.
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
      final file = File('${dir.path}/audio-$sha');
      await file.writeAsBytes(bytes, flush: true);
      if (!mounted) return;
      _temp = file;
      final player = AudioPlayer();
      _player = player;
      player.positionStream.listen((p) {
        if (mounted) setState(() => _position = p);
      });
      player.durationStream.listen((d) {
        if (mounted && d != null) setState(() => _duration = d);
      });
      await player.setFilePath(file.path);
      if (!mounted) return;
      setState(() => _loading = false);
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
      return _MediaLoadError(
        icon: Icons.audiotrack,
        title: 'Cannot play audio',
        message: _error!,
        uri: widget.uri,
      );
    }
    final player = _player;
    if (player == null) {
      return _MediaLoadError(
        icon: Icons.audiotrack,
        title: 'Cannot play audio',
        message: 'player not initialised',
        uri: widget.uri,
      );
    }
    final duration = _duration;
    final durationMs = duration.inMilliseconds;
    final positionMs = _position.inMilliseconds.clamp(
      0,
      durationMs == 0 ? 0 : durationMs,
    );
    final clampedPosition = Duration(milliseconds: positionMs);
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.audiotrack, size: 48, color: DesignColors.primary),
          const SizedBox(height: 12),
          Text(
            widget.title ?? 'Audio',
            style: GoogleFonts.spaceGrotesk(
              fontSize: 14,
              fontWeight: FontWeight.w700,
            ),
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 24),
          StreamBuilder<PlayerState>(
            stream: player.playerStateStream,
            builder: (_, snap) {
              final playing = snap.data?.playing ?? false;
              return IconButton(
                iconSize: 56,
                icon: Icon(
                  playing ? Icons.pause_circle : Icons.play_circle,
                  color: DesignColors.primary,
                ),
                onPressed: () =>
                    playing ? player.pause() : player.play(),
              );
            },
          ),
          const SizedBox(height: 12),
          Slider(
            value: positionMs.toDouble(),
            min: 0,
            max: durationMs == 0 ? 1 : durationMs.toDouble(),
            onChanged: durationMs == 0
                ? null
                : (v) => player.seek(Duration(milliseconds: v.round())),
          ),
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                _fmt(clampedPosition),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
              Text(
                _fmt(duration),
                style: GoogleFonts.jetBrainsMono(
                  fontSize: 11,
                  color: DesignColors.textMuted,
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// Formats a duration as `m:ss` (or `h:mm:ss` past one hour). Public so
/// the test can assert formatting without instantiating the player.
@visibleForTesting
String formatAudioDuration(Duration d) => _fmt(d);

String _fmt(Duration d) {
  final h = d.inHours;
  final m = d.inMinutes.remainder(60);
  final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
  if (h > 0) {
    return '$h:${m.toString().padLeft(2, '0')}:$s';
  }
  return '$m:$s';
}

class _MediaLoadError extends StatelessWidget {
  final IconData icon;
  final String title;
  final String message;
  final String uri;
  const _MediaLoadError({
    required this.icon,
    required this.title,
    required this.message,
    required this.uri,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(icon, size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            title,
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

/// Fullscreen route for the audio viewer.
class ArtifactAudioViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactAudioViewerScreen({
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
      body: ArtifactAudioViewer(uri: uri, title: title),
    );
  }
}
