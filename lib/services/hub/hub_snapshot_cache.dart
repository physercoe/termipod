import 'dart:convert';

import 'package:sqflite/sqflite.dart';

/// Last-known-good snapshot of a hub response. [fetchedAt] is the moment
/// the row was written — the UI shows it as "Last updated X" when serving
/// from cache because the hub was unreachable.
class HubSnapshot {
  final Object body;
  final DateTime fetchedAt;
  const HubSnapshot({required this.body, required this.fetchedAt});
}

/// SQLite-backed cache of successful hub list/get responses so a user can
/// still see the last seen runs / reviews / activity / documents when the
/// hub is offline. Content is mutable server data, so this sits in SQLite
/// rather than SharedPreferences (which is reserved for stable config).
///
/// Rows are scoped by [hubCacheKey] = baseUrl+teamId so switching hubs or
/// teams never exposes another partition's data. 4xx responses must never
/// be cached — they're authoritative (auth/not-found) and should not be
/// replayed on later failures.
class HubSnapshotCache {
  HubSnapshotCache({
    required String dbPath,
    DatabaseFactory? dbFactory,
    int maxRowsPerHub = 500,
    Duration ttl = const Duration(days: 7),
  })  : _dbPath = dbPath,
        _dbFactory = dbFactory ?? databaseFactory,
        _maxRowsPerHub = maxRowsPerHub,
        _ttl = ttl;

  final String _dbPath;
  final DatabaseFactory _dbFactory;
  final int _maxRowsPerHub;
  final Duration _ttl;

  Database? _db;

  static const _table = 'hub_snapshots';

  Future<Database> _open() async {
    final existing = _db;
    if (existing != null) return existing;
    final db = await _dbFactory.openDatabase(
      _dbPath,
      options: OpenDatabaseOptions(
        version: 1,
        onCreate: (db, _) async {
          await db.execute('''
            CREATE TABLE $_table (
              hub_key TEXT NOT NULL,
              endpoint TEXT NOT NULL,
              body_json TEXT NOT NULL,
              etag TEXT,
              fetched_at INTEGER NOT NULL,
              PRIMARY KEY (hub_key, endpoint)
            )
          ''');
          await db.execute(
            'CREATE INDEX idx_fetched ON $_table(hub_key, fetched_at)',
          );
        },
      ),
    );
    _db = db;
    return db;
  }

  /// Persist a fresh response body. Overwrites any prior row for
  /// (hubKey, endpoint) so the newest successful fetch always wins.
  Future<void> put(
    String hubKey,
    String endpoint,
    Object body, {
    String? etag,
  }) async {
    final db = await _open();
    await db.insert(
      _table,
      <String, Object?>{
        'hub_key': hubKey,
        'endpoint': endpoint,
        'body_json': jsonEncode(body),
        'etag': etag,
        'fetched_at': DateTime.now().millisecondsSinceEpoch,
      },
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
    await _evictIfNeeded(db, hubKey);
  }

  /// Return the cached snapshot if one exists and is not past [_ttl];
  /// expired rows are deleted on read so disk pressure stays bounded
  /// even for endpoints the caller never revisits.
  Future<HubSnapshot?> get(String hubKey, String endpoint) async {
    final db = await _open();
    final rows = await db.query(
      _table,
      where: 'hub_key = ? AND endpoint = ?',
      whereArgs: [hubKey, endpoint],
      limit: 1,
    );
    if (rows.isEmpty) return null;
    final row = rows.first;
    final fetched =
        DateTime.fromMillisecondsSinceEpoch(row['fetched_at'] as int);
    if (DateTime.now().difference(fetched) > _ttl) {
      await db.delete(
        _table,
        where: 'hub_key = ? AND endpoint = ?',
        whereArgs: [hubKey, endpoint],
      );
      return null;
    }
    return HubSnapshot(
      body: jsonDecode(row['body_json'] as String) as Object,
      fetchedAt: fetched,
    );
  }

  /// Drop every endpoint under [prefix] — called after a mutation that
  /// could have changed a list or a specific resource (e.g. POST /runs
  /// invalidates "/v1/teams/t/projects/p/runs").
  Future<void> invalidatePrefix(String hubKey, String prefix) async {
    final db = await _open();
    await db.delete(
      _table,
      where: 'hub_key = ? AND endpoint LIKE ?',
      whereArgs: [hubKey, '$prefix%'],
    );
  }

  /// Drop every row for a hub partition. Used by clearConfig and any
  /// "switched hubs" path so data from one deployment never leaks into
  /// another.
  Future<void> wipeHub(String hubKey) async {
    final db = await _open();
    await db.delete(_table, where: 'hub_key = ?', whereArgs: [hubKey]);
  }

  /// Drop every row across every hub partition. Exposed for the Settings
  /// "Clear offline cache" action, which isn't tied to any one hub and
  /// shouldn't have to enumerate partitions itself.
  Future<int> wipeAll() async {
    final db = await _open();
    return db.delete(_table);
  }

  /// Total row count across every hub partition. Used by the Settings
  /// "Clear offline cache" row to show how much is currently stashed
  /// before the user taps.
  Future<int> countAll() async {
    final db = await _open();
    final rows = await db.rawQuery('SELECT COUNT(*) AS n FROM $_table');
    return (rows.first['n'] as int?) ?? 0;
  }

  /// Row count for a hub partition. Exposed for tests and for a future
  /// "Clear offline cache" settings row that wants to show the size.
  Future<int> countFor(String hubKey) async {
    final db = await _open();
    final rows = await db.rawQuery(
      'SELECT COUNT(*) AS n FROM $_table WHERE hub_key = ?',
      [hubKey],
    );
    return (rows.first['n'] as int?) ?? 0;
  }

  Future<void> close() async {
    final db = _db;
    if (db != null) {
      await db.close();
      _db = null;
    }
  }

  Future<void> _evictIfNeeded(Database db, String hubKey) async {
    final count = await countFor(hubKey);
    if (count <= _maxRowsPerHub) return;
    final over = count - _maxRowsPerHub;
    final victims = await db.query(
      _table,
      columns: ['endpoint'],
      where: 'hub_key = ?',
      whereArgs: [hubKey],
      orderBy: 'fetched_at ASC',
      limit: over,
    );
    if (victims.isEmpty) return;
    final placeholders = List.filled(victims.length, '?').join(',');
    await db.delete(
      _table,
      where: 'hub_key = ? AND endpoint IN ($placeholders)',
      whereArgs: [
        hubKey,
        for (final v in victims) v['endpoint'] as String,
      ],
    );
  }
}

/// Partition key for cache rows. baseUrl + teamId is coarse enough that
/// a token rotation doesn't invalidate snapshots, but fine enough that
/// switching teams on the same hub never surfaces another team's data.
String hubCacheKey({required String baseUrl, required String teamId}) =>
    '$baseUrl#$teamId';
