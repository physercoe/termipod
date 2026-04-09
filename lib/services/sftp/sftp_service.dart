import 'dart:io';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';
import 'package:uuid/uuid.dart';

/// SFTP upload result
class SftpUploadResult {
  final String remotePath;
  final int bytesWritten;

  const SftpUploadResult({
    required this.remotePath,
    required this.bytesWritten,
  });
}

/// SFTP download result
class SftpDownloadResult {
  final Uint8List bytes;
  final String remotePath;
  final int size;

  const SftpDownloadResult({
    required this.bytes,
    required this.remotePath,
    required this.size,
  });
}

/// Remote file/directory entry
class SftpFileEntry {
  final String name;
  final bool isDirectory;
  final int size;

  const SftpFileEntry({
    required this.name,
    required this.isDirectory,
    required this.size,
  });
}

/// SFTPアップロードサービス
class SftpService {
  static const _uuid = Uuid();
  static final _safeCharsRegex = RegExp(r'[^a-zA-Z0-9._-]');

  /// ファイル名をサニタイズ（安全な文字のみ許可）
  ///
  /// [a-zA-Z0-9._-] 以外の文字は `_` に置換する。
  static String sanitizeFilename(String raw) {
    if (raw.isEmpty) return 'unnamed';
    return raw.replaceAll(_safeCharsRegex, '_');
  }

  /// タイムスタンプ + UUID短縮でユニークファイル名を生成
  ///
  /// 例: img_20260403_143025_a3f2.png
  static String generateFilename(String prefix, String extension) {
    final now = DateTime.now();
    final timestamp =
        '${now.year}${_pad(now.month)}${_pad(now.day)}_${_pad(now.hour)}${_pad(now.minute)}${_pad(now.second)}';
    final shortUuid = _uuid.v4().substring(0, 4);
    final sanitizedExt = extension.startsWith('.') ? extension.substring(1) : extension;
    return '${sanitizeFilename(prefix)}${timestamp}_$shortUuid.$sanitizedExt';
  }

  /// リモートディレクトリの存在確認・作成
  Future<void> ensureDirectory(SftpClient sftp, String remotePath) async {
    try {
      await sftp.stat(remotePath);
    } on SftpStatusError {
      await sftp.mkdir(remotePath);
    }
  }

  /// ファイルアップロード
  ///
  /// [sftp] SFTPクライアント
  /// [remoteDir] リモートディレクトリパス（末尾/なし可）
  /// [filename] ファイル名
  /// [bytes] アップロードするバイトデータ
  /// [onProgress] 進捗コールバック (0.0 ~ 1.0)
  Future<SftpUploadResult> upload({
    required SftpClient sftp,
    required String remoteDir,
    required String filename,
    required Uint8List bytes,
    void Function(double progress)? onProgress,
  }) async {
    final dir = remoteDir.endsWith('/') ? remoteDir.substring(0, remoteDir.length - 1) : remoteDir;
    final remotePath = '$dir/$filename';

    await ensureDirectory(sftp, dir);

    SftpFile? file;
    try {
      file = await sftp.open(
        remotePath,
        mode: SftpFileOpenMode.create | SftpFileOpenMode.write | SftpFileOpenMode.truncate,
      );

      final totalBytes = bytes.length;
      var written = 0;

      // チャンク分割でストリーム書き込み（進捗追跡用）
      const chunkSize = 32 * 1024; // 32KB
      final chunks = <Uint8List>[];
      for (var offset = 0; offset < totalBytes; offset += chunkSize) {
        final end = (offset + chunkSize > totalBytes) ? totalBytes : offset + chunkSize;
        chunks.add(bytes.sublist(offset, end));
      }

      final stream = Stream.fromIterable(chunks).map((chunk) {
        written += chunk.length;
        onProgress?.call(totalBytes > 0 ? written / totalBytes : 1.0);
        return chunk;
      });

      final writer = file.write(stream);
      await writer.done;

      return SftpUploadResult(remotePath: remotePath, bytesWritten: totalBytes);
    } catch (e) {
      // 部分ファイルのクリーンアップ試行
      try {
        await sftp.remove(remotePath);
      } catch (_) {
        // クリーンアップ失敗は無視
      }
      rethrow;
    } finally {
      await file?.close();
    }
  }

  /// File download
  ///
  /// [sftp] SFTP client
  /// [remotePath] Full remote file path
  /// [onProgress] Progress callback (0.0 ~ 1.0)
  Future<SftpDownloadResult> download({
    required SftpClient sftp,
    required String remotePath,
    void Function(double progress)? onProgress,
  }) async {
    final stat = await sftp.stat(remotePath);
    final totalBytes = stat.size ?? 0;

    final file = await sftp.open(
      remotePath,
      mode: SftpFileOpenMode.read,
    );

    try {
      final chunks = <Uint8List>[];
      var readBytes = 0;

      await for (final chunk in file.read()) {
        chunks.add(chunk);
        readBytes += chunk.length;
        if (totalBytes > 0) {
          onProgress?.call(readBytes / totalBytes);
        }
      }

      final result = Uint8List(readBytes);
      var offset = 0;
      for (final chunk in chunks) {
        result.setRange(offset, offset + chunk.length, chunk);
        offset += chunk.length;
      }

      return SftpDownloadResult(
        bytes: result,
        remotePath: remotePath,
        size: readBytes,
      );
    } finally {
      await file.close();
    }
  }

  /// Download a remote file directly to a local file path.
  ///
  /// Supports resume: if [localPath] already exists with partial content,
  /// continues from the last byte received. Uses `.part` suffix during
  /// download, renamed on completion.
  Future<String> downloadToFile({
    required SftpClient sftp,
    required String remotePath,
    required String localPath,
    void Function(double progress, int bytesReceived, int totalBytes)? onProgress,
  }) async {
    final stat = await sftp.stat(remotePath);
    final totalBytes = stat.size ?? 0;

    final partPath = '$localPath.part';
    final partFile = File(partPath);
    int startOffset = 0;

    // Resume: check for existing partial file
    if (partFile.existsSync()) {
      startOffset = partFile.lengthSync();
      if (startOffset >= totalBytes && totalBytes > 0) {
        // Already complete — just rename
        partFile.renameSync(localPath);
        onProgress?.call(1.0, totalBytes, totalBytes);
        return localPath;
      }
    }

    final file = await sftp.open(remotePath, mode: SftpFileOpenMode.read);

    try {
      final sink = partFile.openWrite(mode: startOffset > 0 ? FileMode.append : FileMode.write);
      var receivedBytes = startOffset;

      try {
        // dartssh2 SftpFile.read() streams from current position.
        // For resume, we skip already-received bytes.
        var skippedBytes = 0;
        await for (final chunk in file.read()) {
          // Skip bytes we already have from a previous partial download
          if (skippedBytes < startOffset) {
            final remaining = startOffset - skippedBytes;
            if (chunk.length <= remaining) {
              skippedBytes += chunk.length;
              continue;
            }
            // Partial skip: take only the new part of this chunk
            final newBytes = chunk.sublist(remaining);
            skippedBytes = startOffset;
            sink.add(newBytes);
            receivedBytes += newBytes.length;
            if (totalBytes > 0) {
              onProgress?.call(receivedBytes / totalBytes, receivedBytes, totalBytes);
            }
            continue;
          }
          sink.add(chunk);
          receivedBytes += chunk.length;
          if (totalBytes > 0) {
            onProgress?.call(receivedBytes / totalBytes, receivedBytes, totalBytes);
          }
        }
      } finally {
        await sink.flush();
        await sink.close();
      }

      // Rename .part to final name
      final finalFile = File(partPath);
      finalFile.renameSync(localPath);

      return localPath;
    } finally {
      await file.close();
    }
  }

  /// List remote directory contents
  ///
  /// Returns sorted list: directories first, then files, alphabetically.
  Future<List<SftpFileEntry>> listDir({
    required SftpClient sftp,
    required String path,
  }) async {
    final items = await sftp.listdir(path);
    final entries = <SftpFileEntry>[];

    for (final item in items) {
      final name = item.filename;
      if (name == '.' || name == '..') continue;

      final isDir = item.attr.isDirectory;
      final size = item.attr.size ?? 0;

      entries.add(SftpFileEntry(
        name: name,
        isDirectory: isDir,
        size: size,
      ));
    }

    // Sort: directories first, then alphabetically
    entries.sort((a, b) {
      if (a.isDirectory != b.isDirectory) {
        return a.isDirectory ? -1 : 1;
      }
      return a.name.compareTo(b.name);
    });

    return entries;
  }

  static String _pad(int value) => value.toString().padLeft(2, '0');
}
