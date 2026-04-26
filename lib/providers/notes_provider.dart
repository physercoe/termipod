import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path_provider/path_provider.dart';
import 'package:uuid/uuid.dart';

import '../services/notes/notes_db.dart';

const _uuid = Uuid();

/// State for the Notes list — local-only, sqflite-backed (see NotesDb).
class NotesState {
  final List<Note> notes;
  final bool loading;
  const NotesState({this.notes = const [], this.loading = false});

  NotesState copyWith({List<Note>? notes, bool? loading}) =>
      NotesState(
        notes: notes ?? this.notes,
        loading: loading ?? this.loading,
      );
}

class NotesNotifier extends AsyncNotifier<NotesState> {
  NotesDb? _db;

  Future<NotesDb> _ensureDb() async {
    final existing = _db;
    if (existing != null) return existing;
    final dir = await getApplicationDocumentsDirectory();
    final db = NotesDb(dbPath: '${dir.path}/notes.db');
    _db = db;
    return db;
  }

  @override
  Future<NotesState> build() async {
    final db = await _ensureDb();
    final notes = await db.list();
    return NotesState(notes: notes);
  }

  Future<Note> create({
    String title = '',
    String body = '',
    NoteKind kind = NoteKind.note,
  }) async {
    final db = await _ensureDb();
    final now = DateTime.now();
    final note = Note(
      id: _uuid.v4(),
      title: title,
      body: body,
      kind: kind,
      done: false,
      pinned: false,
      createdAt: now,
      updatedAt: now,
    );
    await db.upsert(note);
    await _refresh();
    return note;
  }

  Future<void> save(Note note) async {
    final db = await _ensureDb();
    await db.upsert(note.copyWith(updatedAt: DateTime.now()));
    await _refresh();
  }

  Future<void> toggleDone(String id) async {
    final db = await _ensureDb();
    final n = await db.get(id);
    if (n == null) return;
    await db.upsert(n.copyWith(done: !n.done, updatedAt: DateTime.now()));
    await _refresh();
  }

  Future<void> togglePinned(String id) async {
    final db = await _ensureDb();
    final n = await db.get(id);
    if (n == null) return;
    await db.upsert(n.copyWith(pinned: !n.pinned, updatedAt: DateTime.now()));
    await _refresh();
  }

  Future<void> delete(String id) async {
    final db = await _ensureDb();
    await db.delete(id);
    await _refresh();
  }

  Future<void> _refresh() async {
    final db = await _ensureDb();
    final notes = await db.list();
    state = AsyncData(NotesState(notes: notes));
  }
}

final notesProvider =
    AsyncNotifierProvider<NotesNotifier, NotesState>(NotesNotifier.new);
