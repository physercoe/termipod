import 'dart:io';
import 'dart:typed_data';

/// Content-addressed on-disk cache for `/v1/blobs/{sha}` payloads so that
/// run images, artifact previews, and any blob-linked content keep
/// rendering when the hub is unreachable. Blob bytes are binary and can
/// get large (up to the hub's 25 MiB per-blob cap), so they live on the
/// filesystem rather than SQLite — the JSON snapshot cache stays lean
/// and each blob is a straight file read.
///
/// Keyed purely by sha; the content is immutable under SHA, so there's
/// no per-hub partitioning — the same sha from two hubs is the same
/// bytes. Eviction is simple LRU by access mtime, capped by total byte
/// budget so the cache can't grow unbounded on a phone.
class BlobBytesCache {
  BlobBytesCache({
    required String rootDir,
    int maxBytes = 200 * 1024 * 1024,
  })  : _rootDir = rootDir,
        _maxBytes = maxBytes;

  final String _rootDir;
  final int _maxBytes;

  Directory get _dir => Directory(_rootDir);

  Future<void> _ensureDir() async {
    final d = _dir;
    if (!await d.exists()) {
      await d.create(recursive: true);
    }
  }

  File _file(String sha) => File('$_rootDir/$sha.bin');

  /// Read cached bytes for [sha] or null if nothing is on disk. Updates
  /// the file's access time so LRU eviction treats it as recently used.
  Future<Uint8List?> get(String sha) async {
    if (sha.isEmpty) return null;
    final f = _file(sha);
    if (!await f.exists()) return null;
    final bytes = await f.readAsBytes();
    try {
      await f.setLastModified(DateTime.now());
    } catch (_) {
      // Some filesystems / sandboxes reject setLastModified; LRU
      // degrades to creation-order, which is still bounded by _maxBytes.
    }
    return bytes;
  }

  /// Persist [bytes] under [sha]. Triggers an eviction pass so the total
  /// footprint stays within [_maxBytes]. No-op on empty sha.
  Future<void> put(String sha, List<int> bytes) async {
    if (sha.isEmpty) return;
    await _ensureDir();
    final f = _file(sha);
    await f.writeAsBytes(bytes, flush: true);
    await _evictIfNeeded();
  }

  /// Wipe every cached blob. Surfaced to the Settings "Clear offline
  /// cache" action so one tap covers both JSON snapshots and binaries.
  /// Returns the number of files removed.
  Future<int> wipeAll() async {
    final d = _dir;
    if (!await d.exists()) return 0;
    var removed = 0;
    await for (final entry in d.list()) {
      if (entry is File) {
        try {
          await entry.delete();
          removed++;
        } catch (_) {
          // best-effort; a file we can't delete was probably already gone.
        }
      }
    }
    return removed;
  }

  /// Sum of all cached blob file sizes in bytes. Used by the Settings
  /// row to report footprint ("Clear offline cache · 12.4 MB").
  Future<int> totalBytes() async {
    final d = _dir;
    if (!await d.exists()) return 0;
    var total = 0;
    await for (final entry in d.list()) {
      if (entry is File) {
        try {
          total += await entry.length();
        } catch (_) {
          // entry may have been deleted concurrently — ignore.
        }
      }
    }
    return total;
  }

  Future<void> _evictIfNeeded() async {
    final d = _dir;
    if (!await d.exists()) return;
    final files = <_Entry>[];
    await for (final entry in d.list()) {
      if (entry is File) {
        try {
          final stat = await entry.stat();
          files.add(_Entry(entry, stat.size, stat.modified));
        } catch (_) {}
      }
    }
    var total = files.fold<int>(0, (acc, e) => acc + e.size);
    if (total <= _maxBytes) return;
    files.sort((a, b) => a.modified.compareTo(b.modified));
    for (final e in files) {
      if (total <= _maxBytes) break;
      try {
        await e.file.delete();
        total -= e.size;
      } catch (_) {}
    }
  }
}

class _Entry {
  final File file;
  final int size;
  final DateTime modified;
  _Entry(this.file, this.size, this.modified);
}
