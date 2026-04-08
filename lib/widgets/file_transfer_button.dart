import 'package:flutter/material.dart';
import 'package:flutter_muxpod/l10n/app_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/file_transfer_provider.dart';

/// File transfer button
///
/// 36x36 icon button placed next to ImageTransferButton in the terminal bar.
/// Tapping opens the system file picker directly.
class FileTransferButton extends ConsumerWidget {
  final String connectionId;

  const FileTransferButton({
    super.key,
    required this.connectionId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final transferState = ref.watch(fileTransferProvider(connectionId));
    final isUploading = transferState.phase == FileTransferPhase.uploading;
    final isIdle = transferState.canPick;

    return SizedBox(
      width: 36,
      height: 36,
      child: isUploading
          ? Padding(
              padding: const EdgeInsets.all(8.0),
              child: CircularProgressIndicator(
                value: transferState.uploadProgress > 0
                    ? transferState.uploadProgress
                    : null,
                strokeWidth: 2.0,
                color: Theme.of(context).colorScheme.primary,
              ),
            )
          : IconButton(
              onPressed: isIdle
                  ? () => ref.read(fileTransferProvider(connectionId).notifier).pickFiles()
                  : null,
              icon: Icon(
                _iconForPhase(transferState.phase),
                size: 20,
                color: isIdle
                    ? Colors.white70
                    : transferState.phase == FileTransferPhase.error
                        ? Colors.redAccent
                        : Colors.green,
              ),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(
                minWidth: 36,
                minHeight: 36,
              ),
              tooltip: AppLocalizations.of(context)!.sendFileTooltip,
            ),
    );
  }

  IconData _iconForPhase(FileTransferPhase phase) {
    switch (phase) {
      case FileTransferPhase.completed:
        return Icons.check_circle_outline;
      case FileTransferPhase.error:
        return Icons.error_outline;
      default:
        return Icons.upload_file;
    }
  }
}
