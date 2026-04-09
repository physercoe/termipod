import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';
import 'package:share_plus/share_plus.dart';

import '../providers/download_manager_provider.dart';
import '../theme/design_colors.dart';

/// Show the download manager bottom sheet.
void showDownloadManagerSheet(BuildContext context) {
  final isDark = Theme.of(context).brightness == Brightness.dark;
  final bgColor = isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;

  showModalBottomSheet(
    context: context,
    backgroundColor: bgColor,
    isScrollControlled: true,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
    ),
    builder: (context) => DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.5,
      minChildSize: 0.3,
      maxChildSize: 0.85,
      builder: (context, scrollController) => _DownloadManagerContent(
        scrollController: scrollController,
      ),
    ),
  );
}

class _DownloadManagerContent extends ConsumerWidget {
  final ScrollController scrollController;

  const _DownloadManagerContent({required this.scrollController});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;
    final mutedColor = isDark ? Colors.white38 : Colors.black38;
    final dmState = ref.watch(downloadManagerProvider);
    final entries = dmState.entries;

    return Column(
      children: [
        // Handle
        Center(
          child: Container(
            margin: const EdgeInsets.only(top: 8),
            width: 40,
            height: 4,
            decoration: BoxDecoration(
              color: mutedColor,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        ),
        // Header
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 8, 8),
          child: Row(
            children: [
              Icon(Icons.download, color: DesignColors.primary),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Downloads',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 18,
                    fontWeight: FontWeight.w700,
                    color: textColor,
                  ),
                ),
              ),
              if (entries.any((e) => !e.isActive))
                TextButton(
                  onPressed: () =>
                      ref.read(downloadManagerProvider.notifier).clearFinished(),
                  child: Text(
                    'Clear all',
                    style: TextStyle(color: DesignColors.primary, fontSize: 13),
                  ),
                ),
            ],
          ),
        ),
        const Divider(height: 1),
        // List
        Expanded(
          child: entries.isEmpty
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(Icons.cloud_download_outlined, size: 48, color: mutedColor),
                      const SizedBox(height: 8),
                      Text(
                        'No downloads yet',
                        style: TextStyle(color: mutedColor, fontSize: 14),
                      ),
                    ],
                  ),
                )
              : ListView.builder(
                  controller: scrollController,
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  itemCount: entries.length,
                  itemBuilder: (context, index) =>
                      _DownloadEntryTile(entry: entries[index]),
                ),
        ),
      ],
    );
  }
}

class _DownloadEntryTile extends ConsumerWidget {
  final DownloadEntry entry;

  const _DownloadEntryTile({required this.entry});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : Colors.black87;
    final mutedColor = isDark ? Colors.white54 : Colors.black54;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Status icon
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: _buildStatusIcon(),
          ),
          const SizedBox(width: 12),
          // File info
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  entry.filename,
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: textColor,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  _buildSubtitle(),
                  style: TextStyle(fontSize: 12, color: mutedColor),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                if (entry.isActive) ...[
                  const SizedBox(height: 4),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(2),
                    child: LinearProgressIndicator(
                      value: entry.progress > 0 ? entry.progress : null,
                      minHeight: 3,
                      backgroundColor: isDark ? Colors.white12 : Colors.black12,
                      color: DesignColors.primary,
                    ),
                  ),
                ],
                if (entry.status == DownloadStatus.failed && entry.error != null) ...[
                  const SizedBox(height: 2),
                  Text(
                    entry.error!,
                    style: const TextStyle(fontSize: 11, color: Colors.red),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ],
            ),
          ),
          const SizedBox(width: 8),
          // Action buttons
          ..._buildActions(context, ref),
        ],
      ),
    );
  }

  Widget _buildStatusIcon() {
    switch (entry.status) {
      case DownloadStatus.downloading:
        return SizedBox(
          width: 20,
          height: 20,
          child: CircularProgressIndicator(
            strokeWidth: 2,
            value: entry.progress > 0 ? entry.progress : null,
            color: DesignColors.primary,
          ),
        );
      case DownloadStatus.completed:
        return const Icon(Icons.check_circle, color: Colors.green, size: 20);
      case DownloadStatus.failed:
        return const Icon(Icons.error, color: Colors.red, size: 20);
      case DownloadStatus.cancelled:
        return const Icon(Icons.cancel, color: Colors.grey, size: 20);
    }
  }

  String _buildSubtitle() {
    switch (entry.status) {
      case DownloadStatus.downloading:
        if (entry.totalBytes > 0) {
          return '${_formatBytes(entry.bytesReceived)} / ${_formatBytes(entry.totalBytes)} — ${entry.connectionName}';
        }
        return 'Downloading... — ${entry.connectionName}';
      case DownloadStatus.completed:
        return '${_formatBytes(entry.totalBytes)} — ${entry.connectionName}';
      case DownloadStatus.failed:
        return 'Failed — ${entry.connectionName}';
      case DownloadStatus.cancelled:
        return 'Cancelled — ${entry.connectionName}';
    }
  }

  List<Widget> _buildActions(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(downloadManagerProvider.notifier);

    switch (entry.status) {
      case DownloadStatus.completed:
        return [
          // Open/share
          IconButton(
            icon: const Icon(Icons.share, size: 20),
            tooltip: 'Share',
            onPressed: () {
              if (entry.localPath != null && File(entry.localPath!).existsSync()) {
                Share.shareXFiles([XFile(entry.localPath!)]);
              }
            },
          ),
          // Open in file manager
          IconButton(
            icon: const Icon(Icons.folder_open, size: 20),
            tooltip: 'Open folder',
            onPressed: () {
              if (entry.localPath != null) {
                ScaffoldMessenger.of(context).showSnackBar(
                  SnackBar(
                    content: Text('Saved: ${entry.localPath}'),
                    duration: const Duration(seconds: 3),
                  ),
                );
              }
            },
          ),
          // Remove from list
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            tooltip: 'Remove',
            onPressed: () => notifier.removeEntry(entry.id),
          ),
        ];
      case DownloadStatus.failed:
      case DownloadStatus.cancelled:
        return [
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            tooltip: 'Remove',
            onPressed: () => notifier.removeEntry(entry.id),
          ),
        ];
      case DownloadStatus.downloading:
        return []; // No actions while downloading
    }
  }

  static String _formatBytes(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    if (bytes < 1024 * 1024 * 1024) return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
    return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} GB';
  }
}
