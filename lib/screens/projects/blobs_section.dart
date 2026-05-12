import 'dart:convert';
import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:path_provider/path_provider.dart';
import 'package:share_plus/share_plus.dart';

import '../../providers/hub_provider.dart';
import '../../services/hub/blob_cache.dart';
import '../../theme/design_colors.dart';
import '../../widgets/artifact_viewers/image_viewer.dart';
import '../../widgets/artifact_viewers/pdf_viewer.dart';

/// Blobs section inside ProjectDetail's PageView. Blobs are content-addressed
/// and team-global — the records shown here are a device-local cache of
/// every blob this device has touched (uploads + attachments we've
/// downloaded). The actual bytes live on the hub; [BlobCache] is just a
/// convenience index so the UI can list what you've seen without replaying
/// every chat.
class BlobsSection extends ConsumerStatefulWidget {
  const BlobsSection({super.key});

  @override
  ConsumerState<BlobsSection> createState() => _BlobsSectionState();
}

class _BlobsSectionState extends ConsumerState<BlobsSection> {
  bool _loading = true;
  bool _uploading = false;
  List<BlobRecord> _records = const [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final rows = await BlobCache.instance.list();
    if (!mounted) return;
    setState(() {
      _records = rows;
      _loading = false;
    });
  }

  /// Routes a blob row to its mime-appropriate viewer. PDFs go to the
  /// shared `ArtifactPdfViewerScreen`; markdown + plain text get a small
  /// inline reader; images go to the image viewer. Anything else falls
  /// back to the system share/save flow via [_download] (v1.0.509 — the
  /// tile previously always called `_download`, so testers couldn't
  /// preview the assets they uploaded).
  ///
  /// v1.0.517: falls back to filename-extension detection when mime is
  /// ambiguous. `file_picker` sometimes hands us a null extension or
  /// the server stores `application/octet-stream` for files where
  /// content-sniffing isn't conclusive — a PDF uploaded with bad mime
  /// would otherwise drop straight to the share sheet.
  Future<void> _preview(BlobRecord rec) async {
    final mime = rec.mime;
    final lowerName = rec.name.toLowerCase();
    final blobUri = 'blob:sha256/${rec.sha}';
    final isPdf = mime == 'application/pdf' || lowerName.endsWith('.pdf');
    if (isPdf) {
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => ArtifactPdfViewerScreen(uri: blobUri, title: rec.name),
        ),
      );
      return;
    }
    final isImage = mime.startsWith('image/') ||
        lowerName.endsWith('.png') ||
        lowerName.endsWith('.jpg') ||
        lowerName.endsWith('.jpeg') ||
        lowerName.endsWith('.gif') ||
        lowerName.endsWith('.webp');
    if (isImage) {
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) =>
              ArtifactImageViewerScreen(uri: blobUri, title: rec.name),
        ),
      );
      return;
    }
    final isText = mime == 'text/markdown' ||
        mime == 'text/plain' ||
        mime == 'application/json' ||
        mime == 'application/yaml' ||
        lowerName.endsWith('.md') ||
        lowerName.endsWith('.txt') ||
        lowerName.endsWith('.json') ||
        lowerName.endsWith('.yaml') ||
        lowerName.endsWith('.yml');
    if (isText) {
      // Pick a best-effort mime when the server didn't give us one;
      // BlobTextViewerScreen renders markdown for `text/markdown`.
      final viewerMime = mime.startsWith('text/') ||
              mime == 'application/json' ||
              mime == 'application/yaml'
          ? mime
          : lowerName.endsWith('.md')
              ? 'text/markdown'
              : 'text/plain';
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) =>
              BlobTextViewerScreen(sha: rec.sha, mime: viewerMime, name: rec.name),
        ),
      );
      return;
    }
    // Fall-through: not a previewable mime — preserve the legacy
    // download/share path so the row stays useful.
    await _download(rec);
  }

  Future<void> _download(BlobRecord rec) async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    try {
      final bytes = await client.downloadBlobCached(rec.sha);
      final dir = await getTemporaryDirectory();
      final path = '${dir.path}/${rec.name}';
      final file = File(path);
      await file.writeAsBytes(bytes);
      await Share.shareXFiles([XFile(path)], text: rec.name);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Download failed: $e')),
      );
    }
  }

  Future<void> _confirmRemove(BlobRecord rec) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Remove from cache?'),
        content: const Text('The blob stays on the server.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: DesignColors.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await BlobCache.instance.remove(rec.sha);
    await _load();
  }

  Future<void> _upload() async {
    if (_uploading) return;
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) return;
    final picked = await FilePicker.platform.pickFiles(
      type: FileType.any,
      allowMultiple: false,
      withData: true,
    );
    if (picked == null || picked.files.isEmpty) return;
    final f = picked.files.single;
    setState(() => _uploading = true);
    try {
      List<int> bytes;
      if (f.bytes != null) {
        bytes = f.bytes!;
      } else if (f.path != null) {
        bytes = await File(f.path!).readAsBytes();
      } else {
        throw StateError('No bytes or path for picked file');
      }
      final mime = _guessMime(f.extension);
      final out = await client.uploadBlob(bytes, mime: mime);
      final sha = (out['sha256'] ?? '').toString();
      final size = (out['size'] is int)
          ? out['size'] as int
          : int.tryParse('${out['size']}') ?? bytes.length;
      final outMime = (out['mime'] ?? mime).toString();
      await BlobCache.instance.add(BlobRecord(
        sha: sha,
        name: f.name,
        mime: outMime,
        size: size,
        uploadedAt: DateTime.now().toUtc().toIso8601String(),
      ));
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Upload failed: $e')),
      );
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final list = _loading
        ? const Center(child: CircularProgressIndicator())
        : _records.isEmpty
            ? _buildEmpty(context)
            : RefreshIndicator(
                onRefresh: _load,
                child: ListView.separated(
                  padding: const EdgeInsets.fromLTRB(8, 0, 8, 96),
                  itemCount: _records.length,
                  separatorBuilder: (_, __) => const SizedBox(height: 4),
                  itemBuilder: (_, i) => _BlobTile(
                    rec: _records[i],
                    onTap: () => _preview(_records[i]),
                    onLongPress: () => _confirmRemove(_records[i]),
                    onDownload: () => _download(_records[i]),
                  ),
                ),
              );
    return Stack(
      children: [
        Positioned.fill(
          child: Column(
            children: [
              const _AssetsGuidance(),
              Expanded(child: list),
            ],
          ),
        ),
        Positioned(
          right: 16,
          bottom: 16,
          child: FloatingActionButton.extended(
            heroTag: 'hub-blobs-fab',
            onPressed: _uploading ? null : _upload,
            icon: _uploading
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Icon(Icons.add),
            label: const Text('Upload'),
          ),
        ),
      ],
    );
  }

  Widget _buildEmpty(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted = isDark
        ? DesignColors.textMuted
        : DesignColors.textMutedLight;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.perm_media_outlined, size: 48, color: muted),
            const SizedBox(height: 12),
            Text(
              'No assets yet on this device.',
              textAlign: TextAlign.center,
              style: GoogleFonts.spaceGrotesk(
                  fontSize: 13, fontWeight: FontWeight.w600, color: muted),
            ),
            const SizedBox(height: 6),
            Text(
              'Attachments you view in channels appear here. '
              'Use + to upload a standalone reference.',
              textAlign: TextAlign.center,
              style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
            ),
          ],
        ),
      ),
    );
  }
}

/// Role banner for the Assets/Blobs section. Mirrors the Files banner so
/// a user landing on either surface sees the same decision map: agents
/// read Files by path, humans browse Assets by content.
class _AssetsGuidance extends StatelessWidget {
  const _AssetsGuidance();

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bg = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;
    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.perm_media_outlined,
              size: 18, color: DesignColors.primary),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Assets humans browse by content',
                  style: GoogleFonts.spaceGrotesk(
                      fontSize: 12, fontWeight: FontWeight.w700),
                ),
                const SizedBox(height: 2),
                Text(
                  'Screenshots, audio, PDFs, notes. Tap a row to preview '
                  '(PDF / markdown / text / images), or use the download '
                  'button to share. For files an agent reads by path, use Files.',
                  style: GoogleFonts.spaceGrotesk(fontSize: 11, color: muted),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _BlobTile extends StatelessWidget {
  final BlobRecord rec;
  final VoidCallback onTap;
  final VoidCallback onLongPress;
  final VoidCallback onDownload;
  const _BlobTile({
    required this.rec,
    required this.onTap,
    required this.onLongPress,
    required this.onDownload,
  });

  @override
  Widget build(BuildContext context) {
    final shaShort = rec.sha.length >= 12 ? rec.sha.substring(0, 12) : rec.sha;
    return ListTile(
      leading: Icon(_iconFor(rec.mime), color: DesignColors.primary),
      title: Text(
        rec.name,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: GoogleFonts.spaceGrotesk(
            fontSize: 13, fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '${rec.size}B • $shaShort…',
        style: GoogleFonts.jetBrainsMono(fontSize: 11),
      ),
      trailing: IconButton(
        icon: const Icon(Icons.download),
        onPressed: onDownload,
      ),
      onTap: onTap,
      onLongPress: onLongPress,
    );
  }

  IconData _iconFor(String mime) {
    if (mime.startsWith('image/')) return Icons.image;
    if (mime.startsWith('text/')) return Icons.description;
    return Icons.insert_drive_file;
  }
}

/// Fullscreen reader for text-shaped blobs (markdown, plain text,
/// JSON, YAML). Decoded as UTF-8; markdown renders via flutter_markdown,
/// everything else falls back to a monospace `SelectableText` so the
/// raw content stays copyable. Keeps the assets surface useful for the
/// notes/configs testers upload to verify behavior without needing the
/// full DocViewerScreen (which is wired to `getProjectDoc`, not blobs).
class BlobTextViewerScreen extends ConsumerStatefulWidget {
  final String sha;
  final String mime;
  final String name;
  const BlobTextViewerScreen({
    super.key,
    required this.sha,
    required this.mime,
    required this.name,
  });

  @override
  ConsumerState<BlobTextViewerScreen> createState() =>
      _BlobTextViewerScreenState();
}

class _BlobTextViewerScreenState extends ConsumerState<BlobTextViewerScreen> {
  bool _loading = true;
  String _content = '';
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'hub not connected';
      });
      return;
    }
    try {
      final bytes = await client.downloadBlobCached(widget.sha);
      if (!mounted) return;
      setState(() {
        _content = utf8.decode(bytes, allowMalformed: true);
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
    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.name,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text(
                    _error!,
                    style: GoogleFonts.jetBrainsMono(
                      color: DesignColors.error,
                      fontSize: 12,
                    ),
                  ),
                )
              : _body(),
    );
  }

  Widget _body() {
    if (widget.mime == 'text/markdown') {
      return Markdown(
        data: _content,
        padding: const EdgeInsets.all(16),
        selectable: true,
      );
    }
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: SelectableText(
        _content,
        style: GoogleFonts.jetBrainsMono(fontSize: 12, height: 1.4),
      ),
    );
  }
}

/// Maps common file extensions onto MIME types. Reused verbatim by the
/// chat composer attachment flow — keep the two in sync if the table
/// grows. Unknown extensions fall through to `application/octet-stream`.
String _guessMime(String? ext) {
  if (ext == null || ext.isEmpty) return 'application/octet-stream';
  final e = ext.toLowerCase();
  if (e == 'png' || e == 'jpg' || e == 'jpeg' || e == 'gif' || e == 'webp') {
    return 'image/$e';
  }
  if (e == 'md' || e == 'markdown') return 'text/markdown';
  if (e == 'txt') return 'text/plain';
  if (e == 'json') return 'application/json';
  if (e == 'yaml' || e == 'yml') return 'application/yaml';
  if (e == 'pdf') return 'application/pdf';
  return 'application/octet-stream';
}
