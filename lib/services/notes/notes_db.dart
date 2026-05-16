import 'package:sqflite/sqflite.dart';

/// A personal note or reminder. Local-only (MVP) — stored in a
/// sqflite DB next to the hub snapshot cache, scoped to this device.
/// The intent is the user's personal scratch space on the Me page:
/// thoughts, reminders, todos that don't belong on a project doc.
///
/// ADR-029: the on-device "reminder" kind is renamed from "todo" to
/// resolve the collision with the hub-side `tasks.status='todo'`
/// primitive. Existing on-device rows are migrated transparently on
/// first open of v1.0.610 or newer (see [NotesDb._open]'s onOpen hook).
///
/// Sync to the hub is intentionally out of scope for v1. If we add
/// it later, principals can be keyed by handle and rows merged by
/// (id, updated_at).
class Note {
  final String id;
  final String title;
  final String body;
  final NoteKind kind;
  final bool done;
  final bool pinned;
  final DateTime createdAt;
  final DateTime updatedAt;

  const Note({
    required this.id,
    required this.title,
    required this.body,
    required this.kind,
    required this.done,
    required this.pinned,
    required this.createdAt,
    required this.updatedAt,
  });

  Note copyWith({
    String? title,
    String? body,
    NoteKind? kind,
    bool? done,
    bool? pinned,
    DateTime? updatedAt,
  }) =>
      Note(
        id: id,
        title: title ?? this.title,
        body: body ?? this.body,
        kind: kind ?? this.kind,
        done: done ?? this.done,
        pinned: pinned ?? this.pinned,
        createdAt: createdAt,
        updatedAt: updatedAt ?? this.updatedAt,
      );
}

enum NoteKind { note, reminder }

NoteKind _kindFromString(String s) {
  // Legacy on-device rows wrote `todo`; v1.0.610+ writes `reminder`.
  // _open() runs a one-shot UPDATE to migrate old rows, but a defensive
  // read mapping here keeps things safe if a row slipped through.
  if (s == 'reminder' || s == 'todo') return NoteKind.reminder;
  return NoteKind.note;
}

String _kindToString(NoteKind k) =>
    k == NoteKind.reminder ? 'reminder' : 'note';

/// SQLite-backed CRUD for [Note]. Sits in its own DB file (separate
/// from the hub snapshot cache) so notes survive a "Clear cache"
/// action and don't pollute hub-scoped snapshot rows.
class NotesDb {
  NotesDb({
    required String dbPath,
    DatabaseFactory? dbFactory,
  })  : _dbPath = dbPath,
        _dbFactory = dbFactory ?? databaseFactory;

  final String _dbPath;
  final DatabaseFactory _dbFactory;

  Database? _db;
  static const _table = 'notes';

  Future<Database> _open() async {
    final existing = _db;
    if (existing != null) return existing;
    final db = await _dbFactory.openDatabase(
      _dbPath,
      options: OpenDatabaseOptions(
        version: 2,
        onCreate: (db, _) async {
          await db.execute('''
            CREATE TABLE $_table (
              id TEXT PRIMARY KEY,
              title TEXT NOT NULL,
              body TEXT NOT NULL,
              kind TEXT NOT NULL CHECK (kind IN ('note','reminder')),
              done INTEGER NOT NULL DEFAULT 0,
              pinned INTEGER NOT NULL DEFAULT 0,
              created_at INTEGER NOT NULL,
              updated_at INTEGER NOT NULL
            )
          ''');
          await db.execute(
            'CREATE INDEX idx_notes_updated ON $_table(updated_at DESC)',
          );
        },
        // v1 → v2 (ADR-029 W5): the kind enum's "todo" value is
        // renamed to "reminder" to free up the "todo" label for the
        // hub-side task status. SQLite can't ALTER a CHECK constraint
        // in place, so we rebuild the table: copy rows with kind
        // rewritten, drop the old, rename the new.
        onUpgrade: (db, oldVersion, newVersion) async {
          if (oldVersion < 2) {
            await db.execute('''
              CREATE TABLE ${_table}_v2 (
                id TEXT PRIMARY KEY,
                title TEXT NOT NULL,
                body TEXT NOT NULL,
                kind TEXT NOT NULL CHECK (kind IN ('note','reminder')),
                done INTEGER NOT NULL DEFAULT 0,
                pinned INTEGER NOT NULL DEFAULT 0,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL
              )
            ''');
            await db.execute('''
              INSERT INTO ${_table}_v2
                (id, title, body, kind, done, pinned, created_at, updated_at)
              SELECT id, title, body,
                     CASE WHEN kind = 'todo' THEN 'reminder' ELSE kind END,
                     done, pinned, created_at, updated_at
                FROM $_table
            ''');
            await db.execute('DROP TABLE $_table');
            await db.execute('ALTER TABLE ${_table}_v2 RENAME TO $_table');
            await db.execute(
              'CREATE INDEX idx_notes_updated ON $_table(updated_at DESC)',
            );
          }
        },
      ),
    );
    _db = db;
    return db;
  }

  Future<List<Note>> list() async {
    final db = await _open();
    final rows = await db.query(
      _table,
      orderBy: 'pinned DESC, updated_at DESC',
    );
    return rows.map(_fromRow).toList(growable: false);
  }

  Future<Note?> get(String id) async {
    final db = await _open();
    final rows = await db.query(_table, where: 'id = ?', whereArgs: [id], limit: 1);
    if (rows.isEmpty) return null;
    return _fromRow(rows.first);
  }

  Future<void> upsert(Note n) async {
    final db = await _open();
    await db.insert(
      _table,
      <String, Object?>{
        'id': n.id,
        'title': n.title,
        'body': n.body,
        'kind': _kindToString(n.kind),
        'done': n.done ? 1 : 0,
        'pinned': n.pinned ? 1 : 0,
        'created_at': n.createdAt.millisecondsSinceEpoch,
        'updated_at': n.updatedAt.millisecondsSinceEpoch,
      },
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
  }

  Future<void> delete(String id) async {
    final db = await _open();
    await db.delete(_table, where: 'id = ?', whereArgs: [id]);
  }

  Future<void> close() async {
    await _db?.close();
    _db = null;
  }

  Note _fromRow(Map<String, Object?> row) => Note(
        id: row['id'] as String,
        title: row['title'] as String,
        body: row['body'] as String,
        kind: _kindFromString(row['kind'] as String),
        done: (row['done'] as int) == 1,
        pinned: (row['pinned'] as int) == 1,
        createdAt:
            DateTime.fromMillisecondsSinceEpoch(row['created_at'] as int),
        updatedAt:
            DateTime.fromMillisecondsSinceEpoch(row['updated_at'] as int),
      );
}
