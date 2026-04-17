import 'dart:io';

import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:share_plus/share_plus.dart';
import 'package:termipod/l10n/app_localizations.dart';

/// Simple read-only file browser over the app's local storage locations.
///
/// Surfaces three roots: Documents (persistent app files), Downloads
/// (virtual info card — actual files live in public `Download/TermiPod`
/// on Android / `Files → TermiPod` on iOS, outside this app's reach),
/// and Temp (staging dir for exports + in-flight SFTP downloads).
/// Drill into subfolders, share files, delete files. No rename/copy —
/// this is a lightweight utility, not a full file manager.
class FileBrowserScreen extends StatefulWidget {
  const FileBrowserScreen({super.key});

  @override
  State<FileBrowserScreen> createState() => _FileBrowserScreenState();
}

enum _Location { documents, downloads, temp }

class _FileBrowserScreenState extends State<FileBrowserScreen> {
  _Location _location = _Location.downloads;
  Directory? _root;
  Directory? _current;
  List<FileSystemEntity> _entries = const [];
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadRoot();
  }

  Future<void> _loadRoot() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final dir = await _resolveRoot(_location);
      if (dir != null && !dir.existsSync()) {
        dir.createSync(recursive: true);
      }
      if (!mounted) return;
      setState(() {
        _root = dir;
        _current = dir;
      });
      await _refreshEntries();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _loading = false;
      });
    }
  }

  Future<Directory?> _resolveRoot(_Location loc) async {
    switch (loc) {
      case _Location.documents:
        return await getApplicationDocumentsDirectory();
      case _Location.downloads:
        // No in-app directory — downloads land in the system-level
        // Download/TermiPod folder (Android MediaStore) or the Files
        // app (iOS). The body renders an info card instead.
        return null;
      case _Location.temp:
        return await getTemporaryDirectory();
    }
  }

  Future<void> _refreshEntries() async {
    final dir = _current;
    if (dir == null) {
      setState(() {
        _entries = const [];
        _loading = false;
      });
      return;
    }
    try {
      final list = dir.listSync(followLinks: false)
        ..sort((a, b) {
          final aIsDir = a is Directory;
          final bIsDir = b is Directory;
          if (aIsDir != bIsDir) return aIsDir ? -1 : 1;
          return a.path.toLowerCase().compareTo(b.path.toLowerCase());
        });
      if (!mounted) return;
      setState(() {
        _entries = list;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _entries = const [];
        _loading = false;
      });
    }
  }

  void _enterDir(Directory dir) {
    setState(() {
      _current = dir;
      _loading = true;
    });
    _refreshEntries();
  }

  bool _canGoUp() {
    final cur = _current;
    final root = _root;
    return cur != null && root != null && cur.path != root.path;
  }

  void _goUp() {
    final cur = _current;
    if (cur == null) return;
    final parent = cur.parent;
    setState(() {
      _current = parent;
      _loading = true;
    });
    _refreshEntries();
  }

  Future<void> _onEntryTap(FileSystemEntity e) async {
    if (e is Directory) {
      _enterDir(e);
      return;
    }
    await _showFileActions(e as File);
  }

  Future<void> _showFileActions(File file) async {
    final l10n = AppLocalizations.of(context)!;
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                child: Row(
                  children: [
                    const Icon(Icons.insert_drive_file_outlined, size: 20),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(
                        file.uri.pathSegments.last,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                    ),
                  ],
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(48, 0, 16, 8),
                child: Row(
                  children: [
                    Text(
                      _formatBytes(file.lengthSync()),
                      style: TextStyle(color: Theme.of(ctx).hintColor, fontSize: 12),
                    ),
                    const SizedBox(width: 12),
                    Text(
                      _formatTime(file.lastModifiedSync()),
                      style: TextStyle(color: Theme.of(ctx).hintColor, fontSize: 12),
                    ),
                  ],
                ),
              ),
              const Divider(height: 1),
              ListTile(
                leading: const Icon(Icons.share),
                title: Text(l10n.fileActionShare),
                onTap: () async {
                  Navigator.pop(ctx);
                  await Share.shareXFiles([XFile(file.path)]);
                },
              ),
              ListTile(
                leading: Icon(Icons.delete, color: Theme.of(ctx).colorScheme.error),
                title: Text(
                  l10n.fileActionDelete,
                  style: TextStyle(color: Theme.of(ctx).colorScheme.error),
                ),
                onTap: () async {
                  Navigator.pop(ctx);
                  await _confirmDelete(file);
                },
              ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _confirmDelete(File file) async {
    final l10n = AppLocalizations.of(context)!;
    final name = file.uri.pathSegments.last;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        content: Text(l10n.fileActionDeleteConfirm(name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: Text(MaterialLocalizations.of(ctx).cancelButtonLabel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(foregroundColor: Theme.of(ctx).colorScheme.error),
            child: Text(l10n.fileActionDelete),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await file.delete();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(l10n.fileDeleted)),
      );
      await _refreshEntries();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(l10n.fileDeleteFailed(e.toString())),
          backgroundColor: Theme.of(context).colorScheme.error,
        ),
      );
    }
  }

  String _formatBytes(int n) {
    if (n < 1024) return '$n B';
    if (n < 1024 * 1024) return '${(n / 1024).toStringAsFixed(1)} KB';
    if (n < 1024 * 1024 * 1024) return '${(n / 1024 / 1024).toStringAsFixed(1)} MB';
    return '${(n / 1024 / 1024 / 1024).toStringAsFixed(2)} GB';
  }

  String _formatTime(DateTime t) {
    final y = t.year.toString().padLeft(4, '0');
    final m = t.month.toString().padLeft(2, '0');
    final d = t.day.toString().padLeft(2, '0');
    final hh = t.hour.toString().padLeft(2, '0');
    final mm = t.minute.toString().padLeft(2, '0');
    return '$y-$m-$d $hh:$mm';
  }

  String _relativePath() {
    final cur = _current;
    final root = _root;
    if (cur == null || root == null) return '';
    if (cur.path == root.path) return '/';
    final rel = cur.path.substring(root.path.length);
    return rel.isEmpty ? '/' : rel;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.fileBrowserTitle),
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(8),
            child: SegmentedButton<_Location>(
              segments: [
                ButtonSegment(
                  value: _Location.documents,
                  label: Text(l10n.fileBrowserLocDocuments),
                  icon: const Icon(Icons.folder_special_outlined),
                ),
                ButtonSegment(
                  value: _Location.downloads,
                  label: Text(l10n.fileBrowserLocDownloads),
                  icon: const Icon(Icons.download_outlined),
                ),
                ButtonSegment(
                  value: _Location.temp,
                  label: Text(l10n.fileBrowserLocTemp),
                  icon: const Icon(Icons.cached_outlined),
                ),
              ],
              selected: {_location},
              onSelectionChanged: (s) {
                setState(() => _location = s.first);
                _loadRoot();
              },
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
            child: Row(
              children: [
                if (_canGoUp())
                  IconButton(
                    icon: const Icon(Icons.arrow_upward),
                    onPressed: _goUp,
                    tooltip: 'Up',
                  ),
                Expanded(
                  child: Text(
                    _relativePath(),
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(color: Theme.of(context).hintColor, fontSize: 13),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.refresh),
                  onPressed: _refreshEntries,
                  tooltip: 'Refresh',
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(child: _buildBody(l10n)),
        ],
      ),
    );
  }

  Widget _buildBody(AppLocalizations l10n) {
    if (_location == _Location.downloads) {
      return _buildDownloadsInfo(l10n);
    }
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
        ),
      );
    }
    if (_entries.isEmpty) {
      return Center(
        child: Text(l10n.fileBrowserEmpty, style: TextStyle(color: Theme.of(context).hintColor)),
      );
    }
    return ListView.builder(
      itemCount: _entries.length,
      itemBuilder: (ctx, i) {
        final e = _entries[i];
        final name = e.uri.pathSegments.isNotEmpty && e.uri.pathSegments.last.isEmpty
            ? e.uri.pathSegments[e.uri.pathSegments.length - 2]
            : e.uri.pathSegments.last;
        if (e is Directory) {
          return ListTile(
            leading: const Icon(Icons.folder),
            title: Text(name),
            onTap: () => _enterDir(e),
          );
        }
        final f = e as File;
        int size = 0;
        DateTime? mtime;
        try {
          size = f.lengthSync();
          mtime = f.lastModifiedSync();
        } catch (_) {}
        return ListTile(
          leading: const Icon(Icons.insert_drive_file_outlined),
          title: Text(name, overflow: TextOverflow.ellipsis),
          subtitle: Text(
            mtime != null
                ? '${_formatBytes(size)} • ${_formatTime(mtime)}'
                : _formatBytes(size),
            style: const TextStyle(fontSize: 12),
          ),
          onTap: () => _onEntryTap(e),
        );
      },
    );
  }

  /// Info card shown when the "Downloads" tab is selected. Public
  /// downloads live outside the app sandbox now — we can't list them
  /// here, so we tell the user where to look instead.
  Widget _buildDownloadsInfo(AppLocalizations l10n) {
    final theme = Theme.of(context);
    final body = Platform.isAndroid
        ? l10n.fileBrowserDownloadsInfoAndroid
        : Platform.isIOS
            ? l10n.fileBrowserDownloadsInfoIos
            : l10n.fileBrowserDownloadsInfoOther;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.folder_shared_outlined,
              size: 56,
              color: theme.colorScheme.primary,
            ),
            const SizedBox(height: 16),
            Text(
              body,
              textAlign: TextAlign.center,
              style: theme.textTheme.bodyMedium,
            ),
          ],
        ),
      ),
    );
  }
}
