import 'dart:async';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/sftp/sftp_service.dart';
import 'settings_provider.dart';
import 'ssh_provider.dart';

/// File transfer phase
enum FileTransferPhase {
  idle,
  picking,
  confirming,
  uploading,
  injecting,
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

  const FileTransferState({
    this.phase = FileTransferPhase.idle,
    this.uploadProgress = 0.0,
    this.lastUploadedPaths,
    this.errorMessage,
    this.pickedFiles,
    this.pendingRemoteDir,
  });

  bool get canPick =>
      phase == FileTransferPhase.idle || phase == FileTransferPhase.completed;

  FileTransferState copyWith({
    FileTransferPhase? phase,
    double? uploadProgress,
    List<String>? lastUploadedPaths,
    String? errorMessage,
    List<PickedFile>? pickedFiles,
    String? pendingRemoteDir,
  }) {
    return FileTransferState(
      phase: phase ?? this.phase,
      uploadProgress: uploadProgress ?? this.uploadProgress,
      lastUploadedPaths: lastUploadedPaths ?? this.lastUploadedPaths,
      errorMessage: errorMessage ?? this.errorMessage,
      pickedFiles: pickedFiles ?? this.pickedFiles,
      pendingRemoteDir: pendingRemoteDir ?? this.pendingRemoteDir,
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

/// File transfer notifier
class FileTransferNotifier extends Notifier<FileTransferState> {
  final _sftpService = SftpService();
  StreamSubscription? _connectionSub;

  @override
  FileTransferState build() {
    ref.onDispose(() {
      _connectionSub?.cancel();
    });
    return const FileTransferState();
  }

  /// Pick files using system file picker
  Future<void> pickFiles() async {
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
      state = FileTransferState(
        phase: FileTransferPhase.confirming,
        pickedFiles: pickedFiles,
        pendingRemoteDir: settings.fileRemotePath,
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

    final sshClient = ref.read(sshProvider.notifier).client;
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

/// File transfer provider
final fileTransferProvider =
    NotifierProvider<FileTransferNotifier, FileTransferState>(() {
  return FileTransferNotifier();
});
