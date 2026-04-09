import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path_provider/path_provider.dart';

import 'package:uuid/uuid.dart';

import '../services/sftp/sftp_service.dart';
import 'connection_provider.dart';
import 'download_manager_provider.dart';
import 'settings_provider.dart';
import 'ssh_provider.dart';

const _uuid = Uuid();

/// File transfer phase
enum FileTransferPhase {
  idle,
  picking,
  confirming,
  uploading,
  injecting,
  browsing,
  downloading,
  completed,
  error,
}

/// A picked file ready for upload
class PickedFile {
  final String name;
  final Uint8List bytes;
  final int size;

  const PickedFile({
    required this.name,
    required this.bytes,
    required this.size,
  });
}

/// File transfer state
class FileTransferState {
  final FileTransferPhase phase;
  final double uploadProgress;
  final List<String>? lastUploadedPaths;
  final String? errorMessage;
  final List<PickedFile>? pickedFiles;
  final String? pendingRemoteDir;
  // Download-specific fields
  final double downloadProgress;
  final String? lastDownloadedLocalPath;
  final List<SftpFileEntry>? remoteEntries;
  final String? currentRemotePath;

  const FileTransferState({
    this.phase = FileTransferPhase.idle,
    this.uploadProgress = 0.0,
    this.lastUploadedPaths,
    this.errorMessage,
    this.pickedFiles,
    this.pendingRemoteDir,
    this.downloadProgress = 0.0,
    this.lastDownloadedLocalPath,
    this.remoteEntries,
    this.currentRemotePath,
  });

  bool get canPick =>
      phase == FileTransferPhase.idle || phase == FileTransferPhase.completed;

  bool get canBrowse =>
      phase == FileTransferPhase.idle || phase == FileTransferPhase.completed;

  FileTransferState copyWith({
    FileTransferPhase? phase,
    double? uploadProgress,
    List<String>? lastUploadedPaths,
    String? errorMessage,
    List<PickedFile>? pickedFiles,
    String? pendingRemoteDir,
    double? downloadProgress,
    String? lastDownloadedLocalPath,
    List<SftpFileEntry>? remoteEntries,
    String? currentRemotePath,
  }) {
    return FileTransferState(
      phase: phase ?? this.phase,
      uploadProgress: uploadProgress ?? this.uploadProgress,
      lastUploadedPaths: lastUploadedPaths ?? this.lastUploadedPaths,
      errorMessage: errorMessage ?? this.errorMessage,
      pickedFiles: pickedFiles ?? this.pickedFiles,
      pendingRemoteDir: pendingRemoteDir ?? this.pendingRemoteDir,
      downloadProgress: downloadProgress ?? this.downloadProgress,
      lastDownloadedLocalPath: lastDownloadedLocalPath ?? this.lastDownloadedLocalPath,
      remoteEntries: remoteEntries ?? this.remoteEntries,
      currentRemotePath: currentRemotePath ?? this.currentRemotePath,
    );
  }
}

/// File transfer options from confirm dialog
class FileTransferOptions {
  final String remoteDir;
  final String pathFormat;
  final bool autoEnter;
  final bool bracketedPaste;

  const FileTransferOptions({
    required this.remoteDir,
    required this.pathFormat,
    required this.autoEnter,
    required this.bracketedPaste,
  });
}

/// File transfer notifier — one instance per connectionId via .family provider.
class FileTransferNotifier extends Notifier<FileTransferState> {
  final String connectionId;
  final _sftpService = SftpService();
  StreamSubscription? _connectionSub;

  FileTransferNotifier(this.connectionId);

  @override
  FileTransferState build() {
    ref.onDispose(() {
      _connectionSub?.cancel();
    });
    return const FileTransferState();
  }

  /// Pick files using system file picker.
  ///
  /// [initialRemoteDir] — optional CWD from the terminal backend. When
  /// provided (and non-empty), it's used as the pending remote directory
  /// instead of `settings.fileRemotePath`. Callers should pass the result
  /// of `backend.getCurrentPath()` here.
  Future<void> pickFiles({String? initialRemoteDir}) async {
    if (!state.canPick) return;

    state = const FileTransferState(phase: FileTransferPhase.picking);

    try {
      final result = await FilePicker.platform.pickFiles(
        allowMultiple: true,
        withData: true,
      );

      if (result == null || result.files.isEmpty) {
        state = const FileTransferState(phase: FileTransferPhase.idle);
        return;
      }

      final pickedFiles = <PickedFile>[];
      for (final file in result.files) {
        if (file.bytes != null) {
          pickedFiles.add(PickedFile(
            name: file.name,
            bytes: file.bytes!,
            size: file.size,
          ));
        }
      }

      if (pickedFiles.isEmpty) {
        state = const FileTransferState(
          phase: FileTransferPhase.error,
          errorMessage: 'Failed to read file data',
        );
        return;
      }

      final settings = ref.read(settingsProvider);
      final remoteDir = (initialRemoteDir != null && initialRemoteDir.isNotEmpty)
          ? initialRemoteDir
          : settings.fileRemotePath;
      state = FileTransferState(
        phase: FileTransferPhase.confirming,
        pickedFiles: pickedFiles,
        pendingRemoteDir: remoteDir,
      );
    } catch (e) {
      state = FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'Failed to pick files: $e',
      );
    }
  }

  /// Upload files after confirmation
  Future<List<String>?> confirmAndUpload({
    required FileTransferOptions options,
  }) async {
    if (state.phase != FileTransferPhase.confirming ||
        state.pickedFiles == null ||
        state.pickedFiles!.isEmpty) {
      return null;
    }

    final sshClient = ref.read(sshProvider(connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      state = const FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'SSH connection not available',
      );
      return null;
    }

    // Monitor SSH disconnection
    _connectionSub?.cancel();
    _connectionSub = sshClient.connectionStateStream.listen((connState) {
      if (state.phase == FileTransferPhase.uploading) {
        state = const FileTransferState(
          phase: FileTransferPhase.error,
          errorMessage: 'SSH connection lost during upload',
        );
      }
    });

    final files = state.pickedFiles!;
    final remoteDir = options.remoteDir.endsWith('/')
        ? options.remoteDir.substring(0, options.remoteDir.length - 1)
        : options.remoteDir;

    try {
      state = state.copyWith(
        phase: FileTransferPhase.uploading,
        uploadProgress: 0.0,
      );

      final sftp = await sshClient.openSftp();
      try {
        final uploadedPaths = <String>[];
        final totalBytes =
            files.fold<int>(0, (sum, f) => sum + f.size);
        var uploadedBytes = 0;

        for (final file in files) {
          final sanitizedName = SftpService.sanitizeFilename(file.name);
          final result = await _sftpService.upload(
            sftp: sftp,
            remoteDir: remoteDir,
            filename: sanitizedName,
            bytes: file.bytes,
            onProgress: (fileProgress) {
              final fileBytes = (file.size * fileProgress).round();
              final totalProgress = totalBytes > 0
                  ? (uploadedBytes + fileBytes) / totalBytes
                  : 1.0;
              state = state.copyWith(uploadProgress: totalProgress);
            },
          );
          uploadedBytes += file.size;
          uploadedPaths.add(result.remotePath);
        }

        state = FileTransferState(
          phase: FileTransferPhase.completed,
          lastUploadedPaths: uploadedPaths,
          uploadProgress: 1.0,
        );

        return uploadedPaths;
      } finally {
        sftp.close();
      }
    } catch (e) {
      state = FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'Upload failed: $e',
      );
      return null;
    } finally {
      _connectionSub?.cancel();
      _connectionSub = null;
    }
  }

  /// Browse remote directory
  Future<List<SftpFileEntry>?> browseRemote(String path) async {
    final sshClient = ref.read(sshProvider(connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      state = const FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'SSH connection not available',
      );
      return null;
    }

    try {
      final sftp = await sshClient.openSftp();
      try {
        final entries = await _sftpService.listDir(sftp: sftp, path: path);
        state = FileTransferState(
          phase: FileTransferPhase.browsing,
          remoteEntries: entries,
          currentRemotePath: path,
        );
        return entries;
      } finally {
        sftp.close();
      }
    } catch (e) {
      state = FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'Failed to list directory: $e',
      );
      return null;
    }
  }

  /// Get the effective download directory.
  /// Uses configured path if set, otherwise app external storage + TermiPod/.
  Future<String> _getDownloadDir() async {
    final settings = ref.read(settingsProvider);
    if (settings.fileDownloadPath.isNotEmpty) {
      return settings.fileDownloadPath;
    }
    // Default: app external storage / TermiPod
    final extDir = await getExternalStorageDirectory();
    if (extDir != null) {
      final dlDir = Directory('${extDir.path}/TermiPod');
      if (!dlDir.existsSync()) {
        dlDir.createSync(recursive: true);
      }
      return dlDir.path;
    }
    // Fallback to temp
    final tempDir = await getTemporaryDirectory();
    return tempDir.path;
  }

  /// Download a remote file to configurable local directory.
  /// Supports resume via .part files if connection drops mid-transfer.
  Future<String?> downloadFile(String remotePath) async {
    final sshClient = ref.read(sshProvider(connectionId).notifier).client;
    if (sshClient == null || !sshClient.isConnected) {
      state = const FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'SSH connection not available',
      );
      return null;
    }

    // Monitor SSH disconnection
    _connectionSub?.cancel();
    _connectionSub = sshClient.connectionStateStream.listen((connState) {
      if (state.phase == FileTransferPhase.downloading) {
        state = const FileTransferState(
          phase: FileTransferPhase.error,
          errorMessage: 'SSH connection lost during download',
        );
      }
    });

    // Register with download manager
    final downloadId = _uuid.v4();
    final connections = ref.read(connectionsProvider);
    final connName = connections.connections
        .where((c) => c.id == connectionId)
        .firstOrNull
        ?.name ?? connectionId;
    final dm = ref.read(downloadManagerProvider.notifier);

    dm.addEntry(DownloadEntry(
      id: downloadId,
      remotePath: remotePath,
      connectionId: connectionId,
      connectionName: connName,
      status: DownloadStatus.downloading,
      startTime: DateTime.now(),
    ));

    try {
      state = state.copyWith(
        phase: FileTransferPhase.downloading,
        downloadProgress: 0.0,
      );

      final sftp = await sshClient.openSftp();
      try {
        final downloadDir = await _getDownloadDir();
        final filename = remotePath.contains('/')
            ? remotePath.substring(remotePath.lastIndexOf('/') + 1)
            : remotePath;
        final localPath = '$downloadDir/$filename';

        // Ensure download directory exists
        final dir = Directory(downloadDir);
        if (!dir.existsSync()) {
          dir.createSync(recursive: true);
        }

        final resultPath = await _sftpService.downloadToFile(
          sftp: sftp,
          remotePath: remotePath,
          localPath: localPath,
          onProgress: (progress, received, total) {
            state = state.copyWith(downloadProgress: progress);
            dm.updateProgress(downloadId, progress, received, total);
          },
        );

        dm.markCompleted(downloadId, resultPath);

        state = FileTransferState(
          phase: FileTransferPhase.completed,
          lastDownloadedLocalPath: resultPath,
          downloadProgress: 1.0,
        );

        return resultPath;
      } finally {
        sftp.close();
      }
    } catch (e) {
      dm.markFailed(downloadId, '$e');

      state = FileTransferState(
        phase: FileTransferPhase.error,
        errorMessage: 'Download failed: $e',
      );
      return null;
    } finally {
      _connectionSub?.cancel();
      _connectionSub = null;
    }
  }

  /// Cancel transfer
  void cancel() {
    _connectionSub?.cancel();
    _connectionSub = null;
    state = const FileTransferState(phase: FileTransferPhase.idle);
  }

  /// Reset to idle
  void reset() {
    state = const FileTransferState(phase: FileTransferPhase.idle);
  }
}

/// File transfer provider — keyed by connectionId.
final fileTransferProvider =
    NotifierProvider.autoDispose.family<FileTransferNotifier, FileTransferState, String>(
  (connectionId) => FileTransferNotifier(connectionId),
);
