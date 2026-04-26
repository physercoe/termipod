import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/notes_provider.dart';
import '../../services/notes/notes_db.dart';

/// Markdown-ish note/todo editor. Auto-saves on back so users never
/// hit a save button. Empty-on-back is treated as a discard.
class NoteEditorScreen extends ConsumerStatefulWidget {
  final Note? note;
  const NoteEditorScreen({super.key, this.note});

  @override
  ConsumerState<NoteEditorScreen> createState() => _NoteEditorScreenState();
}

class _NoteEditorScreenState extends ConsumerState<NoteEditorScreen> {
  late TextEditingController _titleCtrl;
  late TextEditingController _bodyCtrl;
  late NoteKind _kind;
  late bool _done;
  late bool _pinned;
  String? _id;
  bool _dirty = false;
  bool _saved = false; // flips to true once _saveOnExit runs, unblocks pop

  @override
  void initState() {
    super.initState();
    final n = widget.note;
    _id = n?.id;
    _titleCtrl = TextEditingController(text: n?.title ?? '');
    _bodyCtrl = TextEditingController(text: n?.body ?? '');
    _kind = n?.kind ?? NoteKind.note;
    _done = n?.done ?? false;
    _pinned = n?.pinned ?? false;
    _titleCtrl.addListener(() => _dirty = true);
    _bodyCtrl.addListener(() => _dirty = true);
  }

  @override
  void dispose() {
    _titleCtrl.dispose();
    _bodyCtrl.dispose();
    super.dispose();
  }

  Future<bool> _saveOnExit() async {
    final title = _titleCtrl.text.trim();
    final body = _bodyCtrl.text.trim();
    final notifier = ref.read(notesProvider.notifier);
    if (_id == null) {
      // Brand-new note: only save if there's content.
      if (title.isEmpty && body.isEmpty) return true;
      await notifier.create(title: title, body: body, kind: _kind);
      return true;
    }
    if (!_dirty &&
        _kind == widget.note?.kind &&
        _done == widget.note?.done &&
        _pinned == widget.note?.pinned) {
      return true;
    }
    if (title.isEmpty && body.isEmpty) {
      // User emptied an existing note — interpret as delete.
      await notifier.delete(_id!);
      return true;
    }
    final updated = (widget.note ?? Note(
      id: _id!,
      title: '',
      body: '',
      kind: _kind,
      done: _done,
      pinned: _pinned,
      createdAt: DateTime.now(),
      updatedAt: DateTime.now(),
    ))
        .copyWith(
      title: title,
      body: body,
      kind: _kind,
      done: _done,
      pinned: _pinned,
    );
    await notifier.save(updated);
    return true;
  }

  Future<void> _confirmDelete() async {
    if (_id == null) {
      Navigator.of(context).pop();
      return;
    }
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete note?'),
        content: const Text('This cannot be undone.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    await ref.read(notesProvider.notifier).delete(_id!);
    _saved = true; // prevent _saveOnExit from re-creating the deleted note
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    final colorScheme = Theme.of(context).colorScheme;
    return PopScope<Object?>(
      canPop: _saved,
      onPopInvokedWithResult: (didPop, _) async {
        if (didPop) return;
        await _saveOnExit();
        _saved = true;
        if (mounted) {
          // Trigger a rebuild so canPop=true takes effect, then pop. The
          // user already requested back; we just deferred it for the save.
          setState(() {});
          Navigator.of(context).pop();
        }
      },
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            _id == null ? 'New note' : 'Note',
            style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
          ),
          actions: [
            IconButton(
              tooltip: _pinned ? 'Unpin' : 'Pin',
              icon: Icon(_pinned ? Icons.push_pin : Icons.push_pin_outlined),
              onPressed: () => setState(() {
                _pinned = !_pinned;
                _dirty = true;
              }),
            ),
            if (_id != null)
              IconButton(
                tooltip: 'Delete',
                icon: const Icon(Icons.delete_outline),
                onPressed: _confirmDelete,
              ),
          ],
        ),
        body: Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  ChoiceChip(
                    label: const Text('Note'),
                    selected: _kind == NoteKind.note,
                    onSelected: (_) => setState(() {
                      _kind = NoteKind.note;
                      _dirty = true;
                    }),
                  ),
                  const SizedBox(width: 8),
                  ChoiceChip(
                    label: const Text('Todo'),
                    selected: _kind == NoteKind.todo,
                    onSelected: (_) => setState(() {
                      _kind = NoteKind.todo;
                      _dirty = true;
                    }),
                  ),
                  const Spacer(),
                  if (_kind == NoteKind.todo)
                    Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Text('Done'),
                        Checkbox(
                          value: _done,
                          onChanged: (v) => setState(() {
                            _done = v ?? false;
                            _dirty = true;
                          }),
                        ),
                      ],
                    ),
                ],
              ),
              const SizedBox(height: 8),
              TextField(
                controller: _titleCtrl,
                maxLength: 120,
                style: GoogleFonts.spaceGrotesk(
                    fontSize: 18, fontWeight: FontWeight.w700),
                decoration: const InputDecoration(
                  border: InputBorder.none,
                  hintText: 'Title',
                  counterText: '',
                ),
              ),
              Divider(color: colorScheme.outlineVariant),
              Expanded(
                child: TextField(
                  controller: _bodyCtrl,
                  maxLines: null,
                  expands: true,
                  textAlignVertical: TextAlignVertical.top,
                  style: GoogleFonts.jetBrainsMono(fontSize: 14),
                  decoration: const InputDecoration(
                    border: InputBorder.none,
                    hintText: 'Body (markdown allowed)',
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
