import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

/// Device-local record of a blob the user has interacted with. The blob
/// itself lives on the hub (content-addressed by sha256); this struct only
/// carries the metadata we need to display the row and round-trip the sha
/// into chat attachments. Persisted as a JSON array under
/// [BlobCache._prefsKey] in SharedPreferences.
class BlobRecord {
  final String sha;
  final String name;
  final String mime;
  final int size;
  final String uploadedAt;

  const BlobRecord({
    required this.sha,
    required this.name,
    required this.mime,
    required this.size,
    required this.uploadedAt,
  });

  factory BlobRecord.fromJson(Map<String, dynamic> json) => BlobRecord(
        sha: (json['sha'] ?? '').toString(),
        name: (json['name'] ?? '').toString(),
        mime: (json['mime'] ?? '').toString(),
        size: (json['size'] is int)
            ? json['size'] as int
            : int.tryParse('${json['size']}') ?? 0,
        uploadedAt: (json['uploadedAt'] ?? '').toString(),
      );

  Map<String, dynamic> toJson() => <String, dynamic>{
        'sha': sha,
        'name': name,
        'mime': mime,
        'size': size,
        'uploadedAt': uploadedAt,
      };
}

/// Singleton cache of [BlobRecord]s backed by SharedPreferences. Records
/// are kept newest-first; [add] dedups by sha (existing row is removed
/// before the new one is prepended). The hub-side blob is never touched.
class BlobCache {
  BlobCache._();
  static final BlobCache instance = BlobCache._();

  static const _prefsKey = 'hub_blob_cache';

  Future<List<BlobRecord>> list() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_prefsKey);
    if (raw == null || raw.isEmpty) return const [];
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! List) return const [];
      return decoded
          .whereType<Map>()
          .map((m) => BlobRecord.fromJson(m.cast<String, dynamic>()))
          .toList();
    } catch (_) {
      return const [];
    }
  }

  Future<void> add(BlobRecord rec) async {
    final current = await list();
    current.removeWhere((r) => r.sha == rec.sha);
    current.insert(0, rec);
    await _save(current);
  }

  Future<void> remove(String sha) async {
    final current = await list();
    current.removeWhere((r) => r.sha == sha);
    await _save(current);
  }

  Future<void> _save(List<BlobRecord> rows) async {
    final prefs = await SharedPreferences.getInstance();
    final encoded = jsonEncode(rows.map((r) => r.toJson()).toList());
    await prefs.setString(_prefsKey, encoded);
  }
}
