import 'package:flutter/material.dart';
import 'package:termipod/l10n/app_localizations.dart';

import '../services/sftp/sftp_service.dart';

/// Remote file browser dialog
///
/// Shows a directory listing with navigation. User can browse directories
/// and select a file to download. Returns the selected remote file path.
class RemoteFileBrowserDialog extends StatefulWidget {
  final String initialPath;
  final Future<List<SftpFileEntry>?> Function(String path) onListDir;

  const RemoteFileBrowserDialog({
    super.key,
    required this.initialPath,
    required this.onListDir,
  });

  static Future<String?> show(
    BuildContext context, {
    required String initialPath,
    required Future<List<SftpFileEntry>?> Function(String path) onListDir,
  }) {
    return showDialog<String>(
      context: context,
      barrierDismissible: true,
      builder: (_) => RemoteFileBrowserDialog(
        initialPath: initialPath,
        onListDir: onListDir,
      ),
    );
  }

  @override
  State<RemoteFileBrowserDialog> createState() =>
      _RemoteFileBrowserDialogState();
}

class _RemoteFileBrowserDialogState extends State<RemoteFileBrowserDialog> {
  late String _currentPath;
  late TextEditingController _pathController;
  List<SftpFileEntry>? _entries;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _currentPath = widget.initialPath;
    _pathController = TextEditingController(text: _currentPath);
    _loadDirectory(_currentPath);
  }

  @override
  void dispose() {
    _pathController.dispose();
    super.dispose();
  }

  Future<void> _loadDirectory(String path) async {
    setState(() {
      _isLoading = true;
      _error = null;
    });

    final entries = await widget.onListDir(path);

    if (!mounted) return;

    if (entries != null) {
      setState(() {
        _currentPath = path;
        _pathController.text = path;
        _entries = entries;
        _isLoading = false;
      });
    } else {
      setState(() {
        _error = 'Failed to list directory';
        _isLoading = false;
      });
    }
  }

  void _navigateUp() {
    if (_currentPath == '/') return;
    final parent = _currentPath.endsWith('/')
        ? _currentPath.substring(0, _currentPath.length - 1)
        : _currentPath;
    final lastSlash = parent.lastIndexOf('/');
    final parentPath = lastSlash <= 0 ? '/' : parent.substring(0, lastSlash);
    _loadDirectory(parentPath);
  }

  void _navigateInto(String dirName) {
    final path = _currentPath.endsWith('/')
        ? '$_currentPath$dirName'
        : '$_currentPath/$dirName';
    _loadDirectory(path);
  }

  void _navigateToPath() {
    final path = _pathController.text.trim();
    if (path.isNotEmpty) {
      _loadDirectory(path);
    }
  }

  String _formatSize(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;

    return AlertDialog(
      backgroundColor: theme.colorScheme.surfaceContainerHigh,
      title: Text(l10n.remoteFileBrowser),
      content: SizedBox(
        width: double.maxFinite,
        height: 400,
        child: Column(
          children: [
            // Path bar
            Row(
              children: [
                IconButton(
                  icon: const Icon(Icons.arrow_upward, size: 20),
                  onPressed: _currentPath != '/' ? _navigateUp : null,
                  tooltip: l10n.parentDirectory,
                  padding: EdgeInsets.zero,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
                const SizedBox(width: 4),
                Expanded(
                  child: TextField(
                    controller: _pathController,
                    style:
                        const TextStyle(fontSize: 13, fontFamily: 'monospace'),
                    decoration: const InputDecoration(
                      border: OutlineInputBorder(),
                      isDense: true,
                      contentPadding:
                          EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                    ),
                    onSubmitted: (_) => _navigateToPath(),
                  ),
                ),
                const SizedBox(width: 4),
                IconButton(
                  icon: const Icon(Icons.arrow_forward, size: 20),
                  onPressed: _navigateToPath,
                  padding: EdgeInsets.zero,
                  constraints:
                      const BoxConstraints(minWidth: 36, minHeight: 36),
                ),
              ],
            ),
            const SizedBox(height: 8),
            const Divider(height: 1),
            // Directory listing
            Expanded(
              child: _isLoading
                  ? const Center(child: CircularProgressIndicator())
                  : _error != null
                      ? Center(
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Icon(Icons.error_outline,
                                  color: Colors.redAccent, size: 32),
                              const SizedBox(height: 8),
                              Text(_error!,
                                  style: theme.textTheme.bodySmall
                                      ?.copyWith(color: Colors.redAccent)),
                              const SizedBox(height: 8),
                              TextButton(
                                onPressed: () =>
                                    _loadDirectory(_currentPath),
                                child: const Text('Retry'),
                              ),
                            ],
                          ),
                        )
                      : _entries == null || _entries!.isEmpty
                          ? Center(
                              child: Text(l10n.emptyDirectory,
                                  style: theme.textTheme.bodySmall
                                      ?.copyWith(color: Colors.grey)),
                            )
                          : ListView.builder(
                              itemCount: _entries!.length,
                              itemBuilder: (context, index) {
                                final entry = _entries![index];
                                return ListTile(
                                  dense: true,
                                  visualDensity: VisualDensity.compact,
                                  leading: Icon(
                                    entry.isDirectory
                                        ? Icons.folder
                                        : _iconForFilename(entry.name),
                                    size: 20,
                                    color: entry.isDirectory
                                        ? Colors.amber
                                        : Colors.white70,
                                  ),
                                  title: Text(
                                    entry.name,
                                    style: theme.textTheme.bodySmall,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  trailing: entry.isDirectory
                                      ? const Icon(Icons.chevron_right,
                                          size: 16)
                                      : Text(
                                          _formatSize(entry.size),
                                          style: theme.textTheme.bodySmall
                                              ?.copyWith(color: Colors.grey),
                                        ),
                                  onTap: () {
                                    if (entry.isDirectory) {
                                      _navigateInto(entry.name);
                                    } else {
                                      // Select file for download
                                      final filePath =
                                          _currentPath.endsWith('/')
                                              ? '$_currentPath${entry.name}'
                                              : '$_currentPath/${entry.name}';
                                      Navigator.pop(context, filePath);
                                    }
                                  },
                                );
                              },
                            ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, null),
          child: Text(l10n.buttonCancel),
        ),
      ],
    );
  }

  IconData _iconForFilename(String filename) {
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
      case 'py':
      case 'js':
      case 'ts':
      case 'dart':
      case 'go':
      case 'rs':
        return Icons.code;
      case 'json':
      case 'yaml':
      case 'yml':
      case 'toml':
      case 'xml':
      case 'conf':
        return Icons.settings;
      case 'zip':
      case 'tar':
      case 'gz':
      case 'bz2':
      case '7z':
        return Icons.archive;
      case 'png':
      case 'jpg':
      case 'jpeg':
      case 'gif':
      case 'svg':
        return Icons.image;
      case 'pdf':
        return Icons.picture_as_pdf;
      default:
        return Icons.insert_drive_file;
    }
  }
}
