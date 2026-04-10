import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// How a [SnippetVariable] should be rendered in the fill-in dialog.
///
/// - [text]: free-form [TextField]. Used for paths, PR numbers,
///   prompts, etc.
/// - [option]: fixed-choice [DropdownButtonFormField]. Used for
///   enumerated arguments like `/model {opus,sonnet,haiku,default}`
///   or `/permissions {auto,read-only,full-access}`.
enum SnippetVarKind { text, option }

/// A variable placeholder in a snippet (`{{name}}` in content).
class SnippetVariable {
  final String name;
  final String defaultValue;

  /// How to render the input for this variable. Default: [SnippetVarKind.text].
  final SnippetVarKind kind;

  /// Allowed values when [kind] == [SnippetVarKind.option]. Ignored
  /// when [kind] == [SnippetVarKind.text].
  final List<String> options;

  /// Optional hint text shown inside the [TextField] for text-kind
  /// variables. Ignored for option-kind.
  final String? hint;

  /// Whether the variable may be left blank. Text-kind only (options
  /// are always required to a concrete choice). When true, empty
  /// input resolves to an empty string and the surrounding space is
  /// trimmed by [Snippet.resolve] at call sites that care.
  final bool optional;

  const SnippetVariable({
    required this.name,
    this.defaultValue = '',
    this.kind = SnippetVarKind.text,
    this.options = const [],
    this.hint,
    this.optional = false,
  });

  Map<String, dynamic> toJson() => {
        'name': name,
        'defaultValue': defaultValue,
        if (kind != SnippetVarKind.text) 'kind': kind.name,
        if (options.isNotEmpty) 'options': options,
        if (hint != null) 'hint': hint,
        if (optional) 'optional': optional,
      };

  factory SnippetVariable.fromJson(Map<String, dynamic> json) {
    final rawKind = json['kind'] as String?;
    final kind = switch (rawKind) {
      'option' => SnippetVarKind.option,
      _ => SnippetVarKind.text,
    };
    return SnippetVariable(
      name: json['name'] as String,
      defaultValue: json['defaultValue'] as String? ?? '',
      kind: kind,
      options: (json['options'] as List?)?.cast<String>() ?? const [],
      hint: json['hint'] as String?,
      optional: json['optional'] as bool? ?? false,
    );
  }
}

/// Saved snippet (reusable command/text)
class Snippet {
  final String id;
  final String name;

  /// Content text, may contain {{varname}} placeholders
  final String content;

  /// Category: 'general', 'tmux', 'cli-agent', etc.
  final String category;

  /// Variable placeholders in content
  final List<SnippetVariable> variables;

  /// If true, send directly to terminal on tap; otherwise insert into compose
  final bool sendImmediately;

  const Snippet({
    required this.id,
    required this.name,
    required this.content,
    this.category = 'general',
    this.variables = const [],
    this.sendImmediately = false,
  });

  Snippet copyWith({
    String? name,
    String? content,
    String? category,
    List<SnippetVariable>? variables,
    bool? sendImmediately,
  }) {
    return Snippet(
      id: id,
      name: name ?? this.name,
      content: content ?? this.content,
      category: category ?? this.category,
      variables: variables ?? this.variables,
      sendImmediately: sendImmediately ?? this.sendImmediately,
    );
  }

  /// Resolve content by substituting variable values. Optional text
  /// variables that resolve to empty string get their surrounding
  /// single space collapsed so `/compact {{focus}}` with empty focus
  /// sends `/compact` rather than `/compact ` with a trailing space.
  String resolve(Map<String, String> values) {
    var result = content;
    for (final v in variables) {
      final replacement = values[v.name] ?? v.defaultValue;
      final placeholder = '{{${v.name}}}';
      if (replacement.isEmpty && v.optional) {
        // Collapse " {{name}}" → "" and "{{name}} " → "".
        result = result
            .replaceAll(' $placeholder', '')
            .replaceAll('$placeholder ', '')
            .replaceAll(placeholder, '');
      } else {
        result = result.replaceAll(placeholder, replacement);
      }
    }
    return result;
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'content': content,
        'category': category,
        if (variables.isNotEmpty)
          'variables': variables.map((v) => v.toJson()).toList(),
        if (sendImmediately) 'sendImmediately': sendImmediately,
      };

  factory Snippet.fromJson(Map<String, dynamic> json) => Snippet(
        id: json['id'] as String,
        name: json['name'] as String,
        content: json['content'] as String,
        category: json['category'] as String? ?? 'general',
        variables: (json['variables'] as List?)
                ?.map((v) =>
                    SnippetVariable.fromJson(v as Map<String, dynamic>))
                .toList() ??
            [],
        sendImmediately: json['sendImmediately'] as bool? ?? false,
      );
}

/// スニペット一覧の状態
class SnippetsState {
  final List<Snippet> snippets;
  final bool isLoading;

  const SnippetsState({
    this.snippets = const [],
    this.isLoading = false,
  });

  SnippetsState copyWith({
    List<Snippet>? snippets,
    bool? isLoading,
  }) {
    return SnippetsState(
      snippets: snippets ?? this.snippets,
      isLoading: isLoading ?? this.isLoading,
    );
  }
}

class SnippetsNotifier extends Notifier<SnippetsState> {
  static const _storageKey = 'snippets';

  @override
  SnippetsState build() {
    _loadSnippets();
    return const SnippetsState(isLoading: true);
  }

  Future<void> _loadSnippets() async {
    final prefs = await SharedPreferences.getInstance();
    final jsonStr = prefs.getString(_storageKey);
    if (jsonStr != null) {
      final list = (jsonDecode(jsonStr) as List)
          .map((e) => Snippet.fromJson(e as Map<String, dynamic>))
          .toList();
      state = SnippetsState(snippets: list);
    } else {
      state = const SnippetsState();
    }
  }

  Future<void> _save() async {
    final prefs = await SharedPreferences.getInstance();
    final jsonStr = jsonEncode(state.snippets.map((s) => s.toJson()).toList());
    await prefs.setString(_storageKey, jsonStr);
  }

  Future<void> addSnippet({
    required String name,
    required String content,
    String category = 'general',
    List<SnippetVariable> variables = const [],
    bool sendImmediately = false,
  }) async {
    final snippet = Snippet(
      id: DateTime.now().millisecondsSinceEpoch.toString(),
      name: name,
      content: content,
      category: category,
      variables: variables,
      sendImmediately: sendImmediately,
    );
    state = state.copyWith(snippets: [...state.snippets, snippet]);
    await _save();
  }

  Future<void> updateSnippet(
    String id, {
    String? name,
    String? content,
    String? category,
    List<SnippetVariable>? variables,
    bool? sendImmediately,
  }) async {
    final updated = state.snippets.map((s) {
      if (s.id == id) {
        return s.copyWith(
          name: name,
          content: content,
          category: category,
          variables: variables,
          sendImmediately: sendImmediately,
        );
      }
      return s;
    }).toList();
    state = state.copyWith(snippets: updated);
    await _save();
  }

  Future<void> deleteSnippet(String id) async {
    state = state.copyWith(
      snippets: state.snippets.where((s) => s.id != id).toList(),
    );
    await _save();
  }

  Future<void> reorderSnippets(int oldIndex, int newIndex) async {
    final items = [...state.snippets];
    final item = items.removeAt(oldIndex);
    items.insert(newIndex, item);
    state = state.copyWith(snippets: items);
    await _save();
  }

  List<Snippet> getByCategory(String category) {
    return state.snippets.where((s) => s.category == category).toList();
  }
}

final snippetsProvider = NotifierProvider<SnippetsNotifier, SnippetsState>(
  SnippetsNotifier.new,
);
