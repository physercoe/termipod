import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Per-connection compose-bar draft storage.
///
/// In-memory only — drafts survive navigation between the terminal screen
/// and the connections list (so the user doesn't lose typed text when they
/// pop back to reconnect), but are cleared on app restart. Keyed by
/// `connectionId` so every connection has its own draft.
///
/// Not auto-disposed: the whole point is to outlive the terminal screen.
class ComposeDraftNotifier extends Notifier<String> {
  final String connectionId;

  ComposeDraftNotifier(this.connectionId);

  @override
  String build() => '';

  void set(String text) {
    if (state != text) state = text;
  }

  void clear() {
    if (state.isNotEmpty) state = '';
  }
}

final composeDraftProvider =
    NotifierProvider.family<ComposeDraftNotifier, String, String>(
  (connectionId) => ComposeDraftNotifier(connectionId),
);
