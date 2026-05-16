import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Type-prefixed short form of an entity id.
///
/// Every hub primary key is a 26-char Crockford-base32 ULID, so visually
/// a session id, an agent id, and a project id are indistinguishable
/// (e.g. `01KRNVJTYMQDHNA5ZBSCRJN4M0`). We don't prefix the stored id
/// (would force a schema migration across thousands of FK columns), but
/// we DO disambiguate at display time so audit rows, agent rows, and
/// session rows are unambiguous at a glance.
///
/// Format: `<kind>-<first8>…<last4>`, e.g. `prj-01KRNVJT…N4M0`.
///
/// `kind` should be the entity type token — `prj` / `sess` / `agt` /
/// `att` / `audit` / `evt` — three or four letters keeps the row tight.
/// Pass the same token across surfaces so a user learns the shorthand.
String formatId(String kind, String? id) {
  if (id == null || id.isEmpty) return '';
  if (id.length <= 12) {
    // Short ids (custom test seeds, legacy short strings) — show whole.
    return kind.isEmpty ? id : '$kind-$id';
  }
  final head = id.substring(0, 8);
  final tail = id.substring(id.length - 4);
  return kind.isEmpty ? '$head…$tail' : '$kind-$head…$tail';
}

/// Maps target_kind / scope_kind strings to display tokens. Centralised
/// so the audit row, session row, and agent row converge on the same
/// shorthand — `target_kind=project` and `scope_kind=project` both
/// render as `prj`.
String idKindFor(String entityKind) {
  switch (entityKind) {
    case 'project':
      return 'prj';
    case 'session':
      return 'sess';
    case 'agent':
      return 'agt';
    case 'attention':
      return 'att';
    case 'audit':
      return 'audit';
    case 'event':
      return 'evt';
    case 'run':
      return 'run';
    case 'plan':
      return 'plan';
    case 'document':
      return 'doc';
    case 'artifact':
      return 'art';
    case 'channel':
      return 'ch';
    case 'host':
      return 'host';
    case 'task':
      return 'task';
    default:
      return entityKind;
  }
}

/// Long-press a formatted-id widget to copy the FULL id to the clipboard
/// + show a SnackBar. Tap is preserved for the parent widget's primary
/// action; copy is the secondary affordance.
Future<void> copyIdToClipboard(BuildContext context, String id) async {
  if (id.isEmpty) return;
  await Clipboard.setData(ClipboardData(text: id));
  if (!context.mounted) return;
  // Imported as needed at call sites — we keep this file UI-light so
  // it doesn't drag in a theme dependency.
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(
      content: Text('Copied $id'),
      duration: const Duration(seconds: 2),
    ),
  );
}
