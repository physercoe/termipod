import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../providers/file_transfer_provider.dart';
import '../providers/settings_provider.dart';

/// File transfer confirm dialog
///
/// Shows the list of picked files with sizes and allows the user to
/// configure remote directory, path format, and injection options.
class FileTransferConfirmDialog extends StatefulWidget {
  final List<PickedFile> files;
  final String remoteDir;
  final AppSettings settings;

  const FileTransferConfirmDialog({
    super.key,
    required this.files,
    required this.remoteDir,
    required this.settings,
  });

  static Future<FileTransferOptions?> show(
    BuildContext context, {
    required List<PickedFile> files,
    required String remoteDir,
    required AppSettings settings,
  }) {
    return showDialog<FileTransferOptions>(
      context: context,
      barrierDismissible: true,
      builder: (_) => FileTransferConfirmDialog(
        files: files,
        remoteDir: remoteDir,
        settings: settings,
      ),
    );
  }

  @override
  State<FileTransferConfirmDialog> createState() =>
      _FileTransferConfirmDialogState();
}

class _FileTransferConfirmDialogState extends State<FileTransferConfirmDialog> {
  late final TextEditingController _remoteDirController;
  late final TextEditingController _pathFormatController;
  late bool _autoEnter;
  late bool _bracketedPaste;

  @override
  void initState() {
    super.initState();
    _remoteDirController = TextEditingController(text: widget.remoteDir);
    _pathFormatController =
        TextEditingController(text: widget.settings.filePathFormat);
    _autoEnter = widget.settings.fileAutoEnter;
    _bracketedPaste = widget.settings.fileBracketedPaste;
  }

  @override
  void dispose() {
    _remoteDirController.dispose();
    _pathFormatController.dispose();
    super.dispose();
  }

  String _formatSize(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }

  FileTransferOptions _buildOptions() {
    return FileTransferOptions(
      remoteDir: _remoteDirController.text.trim(),
      pathFormat: _pathFormatController.text.trim(),
      autoEnter: _autoEnter,
      bracketedPaste: _bracketedPaste,
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;
    final totalSize =
        widget.files.fold<int>(0, (sum, f) => sum + f.size);

    return AlertDialog(
      backgroundColor: theme.colorScheme.surfaceContainerHigh,
      title: Text(l10n.uploadFileTitle),
      content: SizedBox(
        width: double.maxFinite,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // File list
              Container(
                constraints: const BoxConstraints(maxHeight: 150),
                decoration: BoxDecoration(
                  color: theme.colorScheme.surfaceContainerLow,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: ListView.separated(
                  shrinkWrap: true,
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  itemCount: widget.files.length,
                  separatorBuilder: (_, _) =>
                      const Divider(height: 1, indent: 12, endIndent: 12),
                  itemBuilder: (context, index) {
                    final file = widget.files[index];
                    return ListTile(
                      dense: true,
                      visualDensity: VisualDensity.compact,
                      leading: Icon(
                        _iconForExtension(file.name),
                        size: 20,
                        color: Colors.white70,
                      ),
                      title: Text(
                        file.name,
                        style: theme.textTheme.bodySmall,
                        overflow: TextOverflow.ellipsis,
                      ),
                      trailing: Text(
                        _formatSize(file.size),
                        style: theme.textTheme.bodySmall
                            ?.copyWith(color: Colors.grey),
                      ),
                    );
                  },
                ),
              ),
              const SizedBox(height: 8),
              Text(
                '${widget.files.length} file${widget.files.length > 1 ? 's' : ''} — ${_formatSize(totalSize)}',
                style:
                    theme.textTheme.bodySmall?.copyWith(color: Colors.grey),
              ),
              const SizedBox(height: 16),

              // Remote directory
              _label(l10n.fileRemoteDirLabel),
              const SizedBox(height: 4),
              TextField(
                controller: _remoteDirController,
                style: const TextStyle(fontSize: 13, fontFamily: 'monospace'),
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  isDense: true,
                  contentPadding:
                      EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                ),
              ),
              const SizedBox(height: 12),

              // Bracketed paste
              SwitchListTile(
                dense: true,
                contentPadding: EdgeInsets.zero,
                title:
                    Text('Bracketed Paste', style: theme.textTheme.bodyMedium),
                value: _bracketedPaste,
                onChanged: (v) => setState(() => _bracketedPaste = v),
              ),

              // Advanced
              ExpansionTile(
                tilePadding: EdgeInsets.zero,
                childrenPadding: const EdgeInsets.only(bottom: 8),
                title: Text('Advanced',
                    style: theme.textTheme.bodyMedium
                        ?.copyWith(color: Colors.grey)),
                children: [
                  // Path format
                  _label(l10n.filePathFormatLabel),
                  const SizedBox(height: 2),
                  Text(
                    'Use {path} as placeholder. e.g. @{path}',
                    style: theme.textTheme.bodySmall
                        ?.copyWith(color: Colors.grey, fontSize: 11),
                  ),
                  const SizedBox(height: 4),
                  TextField(
                    controller: _pathFormatController,
                    style:
                        const TextStyle(fontSize: 13, fontFamily: 'monospace'),
                    decoration: const InputDecoration(
                      border: OutlineInputBorder(),
                      isDense: true,
                      contentPadding:
                          EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                    ),
                  ),
                  const SizedBox(height: 12),

                  // Auto enter
                  SwitchListTile(
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    title: Text('Auto Enter',
                        style: theme.textTheme.bodyMedium),
                    subtitle: Text('Send Enter after path injection',
                        style: theme.textTheme.bodySmall
                            ?.copyWith(color: Colors.grey)),
                    value: _autoEnter,
                    onChanged: (v) => setState(() => _autoEnter = v),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, null),
          child: Text(l10n.buttonCancel),
        ),
        FilledButton(
          onPressed: () {
            final dir = _remoteDirController.text.trim();
            if (dir.isNotEmpty) {
              Navigator.pop(context, _buildOptions());
            }
          },
          child: Text(l10n.buttonUpload),
        ),
      ],
    );
  }

  Widget _label(String text) {
    return Text(
      text,
      style: Theme.of(context)
          .textTheme
          .bodyMedium
          ?.copyWith(fontWeight: FontWeight.w500),
    );
  }

  IconData _iconForExtension(String filename) {
    final ext = filename.contains('.')
        ? filename.substring(filename.lastIndexOf('.') + 1).toLowerCase()
        : '';
    switch (ext) {
      case 'txt':
      case 'log':
      case 'md':
        return Icons.description;
      case 'sh':
      case 'bash':
      case 'zsh':
      case 'py':
      case 'js':
      case 'ts':
      case 'dart':
      case 'rb':
      case 'go':
      case 'rs':
        return Icons.code;
      case 'json':
      case 'yaml':
      case 'yml':
      case 'toml':
      case 'xml':
      case 'ini':
      case 'conf':
      case 'cfg':
        return Icons.settings;
      case 'zip':
      case 'tar':
      case 'gz':
      case 'bz2':
      case 'xz':
      case '7z':
      case 'rar':
        return Icons.archive;
      case 'png':
      case 'jpg':
      case 'jpeg':
      case 'gif':
      case 'bmp':
      case 'svg':
      case 'webp':
        return Icons.image;
      case 'pdf':
        return Icons.picture_as_pdf;
      default:
        return Icons.insert_drive_file;
    }
  }
}
