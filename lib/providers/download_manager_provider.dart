import 'dart:io';

import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Status of a single download entry.
enum DownloadStatus {
  downloading,
  completed,
  failed,
  cancelled,
}

/// A single download entry tracked by the download manager.
class DownloadEntry {
  final String id;
  final String remotePath;
  final String? localPath;
  final String connectionId;
  final String connectionName;
  final DownloadStatus status;
  final double progress;
  final int bytesReceived;
  final int totalBytes;
  final String? error;
  final DateTime startTime;
  final DateTime? endTime;

  const DownloadEntry({
    required this.id,
    required this.remotePath,
    this.localPath,
    required this.connectionId,
    required this.connectionName,
    required this.status,
    this.progress = 0.0,
    this.bytesReceived = 0,
    this.totalBytes = 0,
    this.error,
    required this.startTime,
    this.endTime,
  });

  String get filename => remotePath.contains('/')
      ? remotePath.substring(remotePath.lastIndexOf('/') + 1)
      : remotePath;

  bool get isActive => status == DownloadStatus.downloading;

  DownloadEntry copyWith({
    String? localPath,
    DownloadStatus? status,
    double? progress,
    int? bytesReceived,
    int? totalBytes,
    String? error,
    DateTime? endTime,
  }) {
    return DownloadEntry(
      id: id,
      remotePath: remotePath,
      localPath: localPath ?? this.localPath,
      connectionId: connectionId,
      connectionName: connectionName,
      status: status ?? this.status,
      progress: progress ?? this.progress,
      bytesReceived: bytesReceived ?? this.bytesReceived,
      totalBytes: totalBytes ?? this.totalBytes,
      error: error ?? this.error,
      startTime: startTime,
      endTime: endTime ?? this.endTime,
    );
  }
}

/// Download manager state — list of all tracked downloads.
class DownloadManagerState {
  final List<DownloadEntry> entries;

  const DownloadManagerState({this.entries = const []});

  int get activeCount => entries.where((e) => e.isActive).length;

  DownloadManagerState copyWithEntry(DownloadEntry updated) {
    final newEntries = entries.map((e) => e.id == updated.id ? updated : e).toList();
    if (!newEntries.any((e) => e.id == updated.id)) {
      newEntries.insert(0, updated);
    }
    return DownloadManagerState(entries: newEntries);
  }

  DownloadManagerState withoutEntry(String id) {
    return DownloadManagerState(entries: entries.where((e) => e.id != id).toList());
  }
}

/// Global download manager — tracks all downloads across connections.
class DownloadManagerNotifier extends Notifier<DownloadManagerState> {
  @override
  DownloadManagerState build() => const DownloadManagerState();

  /// Add a new download entry (called when download starts).
  void addEntry(DownloadEntry entry) {
    state = state.copyWithEntry(entry);
  }

  /// Update progress for a download.
  void updateProgress(String id, double progress, int bytesReceived, int totalBytes) {
    final entry = state.entries.where((e) => e.id == id).firstOrNull;
    if (entry == null) return;
    state = state.copyWithEntry(entry.copyWith(
      progress: progress,
      bytesReceived: bytesReceived,
      totalBytes: totalBytes,
    ));
  }

  /// Mark download as completed.
  void markCompleted(String id, String localPath) {
    final entry = state.entries.where((e) => e.id == id).firstOrNull;
    if (entry == null) return;
    state = state.copyWithEntry(entry.copyWith(
      status: DownloadStatus.completed,
      localPath: localPath,
      progress: 1.0,
      endTime: DateTime.now(),
    ));
  }

  /// Mark download as failed.
  void markFailed(String id, String error) {
    final entry = state.entries.where((e) => e.id == id).firstOrNull;
    if (entry == null) return;
    state = state.copyWithEntry(entry.copyWith(
      status: DownloadStatus.failed,
      error: error,
      endTime: DateTime.now(),
    ));
  }

  /// Remove a single entry.
  void removeEntry(String id) {
    state = state.withoutEntry(id);
  }

  /// Clear all completed/failed entries.
  void clearFinished() {
    state = DownloadManagerState(
      entries: state.entries.where((e) => e.isActive).toList(),
    );
  }

  /// Delete local file and remove entry.
  void deleteAndRemove(String id) {
    final entry = state.entries.where((e) => e.id == id).firstOrNull;
    if (entry?.localPath != null) {
      final file = File(entry!.localPath!);
      if (file.existsSync()) file.deleteSync();
    }
    removeEntry(id);
  }
}

final downloadManagerProvider =
    NotifierProvider<DownloadManagerNotifier, DownloadManagerState>(
  DownloadManagerNotifier.new,
);
