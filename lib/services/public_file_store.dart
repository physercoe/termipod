import 'dart:io';

import 'package:media_store_plus/media_store_plus.dart';
import 'package:path_provider/path_provider.dart';

/// User-visible file store for exports + SFTP downloads.
///
/// Android 10+ → writes via [MediaStore] into public `Download/TermiPod/`
/// with no runtime permission. Files appear in the system Files app.
/// iOS → writes into the app Documents directory; exposed to the Files
/// app via `UIFileSharingEnabled` + `LSSupportsOpeningDocumentsInPlace`
/// in Info.plist (Files → On My iPhone → TermiPod).
///
/// Design notes:
/// - [moveFile] is the primary entry for SFTP: the download finishes to
///   an app-private temp path (existing `.part` resume untouched), and
///   the completed file is promoted into the public store.
/// - [writeBytes] is the entry for in-memory payloads like backup JSON.
/// - On Android the plugin returns a content URI; we report a display
///   path string like `Download/TermiPod/filename` for user messaging.
///   Listing / deletion of public files is deliberately out of scope —
///   the system Files app is the management surface.
class PublicFileStore {
  /// App-specific subfolder within `Download/` (Android) — set once in
  /// `main()` via `MediaStore.appFolder`. Centralised here so the init
  /// site and the runtime API reference the same literal.
  static const String appFolderName = 'TermiPod';

  /// Promote a completed local file into the public store.
  ///
  /// Returns a display path on success, null on failure. On success the
  /// source temp file is deleted. On failure the temp file is left in
  /// place so the caller can retry or expose it to the user.
  static Future<String?> moveFile(String tempPath, String filename) async {
    final src = File(tempPath);
    if (!src.existsSync()) return null;

    if (Platform.isAndroid) {
      final info = await MediaStore().saveFile(
        tempFilePath: tempPath,
        dirType: DirType.download,
        dirName: DirName.download,
      );
      // info == null is the only true failure. SaveStatus.duplicated still
      // wrote the file — Android just appended " (1)" because the plugin's
      // pre-insert delete couldn't clear the prior copy. Use info.name so
      // the path we report matches what's actually on disk.
      if (info == null) return null;
      // saveFile copies; the temp source stays until we delete it.
      try {
        src.deleteSync();
      } catch (_) {
        // Leave the temp file on failure — Android cache-evicts it later.
      }
      return 'Download/$appFolderName/${info.name}';
    }

    if (Platform.isIOS) {
      final docs = await getApplicationDocumentsDirectory();
      final dest = File('${docs.path}/$filename');
      try {
        await src.rename(dest.path);
      } on FileSystemException {
        // Cross-device rename (e.g. temp on a different volume) — fall
        // back to copy + delete.
        await src.copy(dest.path);
        try {
          src.deleteSync();
        } catch (_) {}
      }
      return dest.path;
    }

    // Desktop / other: write alongside the app docs directory.
    final docs = await getApplicationDocumentsDirectory();
    final dest = File('${docs.path}/$filename');
    await src.copy(dest.path);
    try {
      src.deleteSync();
    } catch (_) {}
    return dest.path;
  }

  /// Write an in-memory payload as a new public file.
  ///
  /// On Android the plugin only accepts a temp-file path, so we stage
  /// the bytes in the system temp dir first, then promote via
  /// [MediaStore.saveFile]. The staging file is always cleaned up.
  static Future<String?> writeBytes(
    String filename,
    List<int> bytes,
  ) async {
    if (Platform.isAndroid) {
      final tempDir = await getTemporaryDirectory();
      final staging = File('${tempDir.path}/$filename');
      await staging.writeAsBytes(bytes, flush: true);
      try {
        return await moveFile(staging.path, filename);
      } finally {
        if (staging.existsSync()) {
          try {
            staging.deleteSync();
          } catch (_) {}
        }
      }
    }

    if (Platform.isIOS) {
      final docs = await getApplicationDocumentsDirectory();
      final dest = File('${docs.path}/$filename');
      await dest.writeAsBytes(bytes, flush: true);
      return dest.path;
    }

    final docs = await getApplicationDocumentsDirectory();
    final dest = File('${docs.path}/$filename');
    await dest.writeAsBytes(bytes, flush: true);
    return dest.path;
  }
}
