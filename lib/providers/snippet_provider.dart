import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// スニペット（再利用可能なコマンド/テキスト）
class Snippet {
  final String id;
  final String name;
  final String content;

  /// スニペットカテゴリ: 'general', 'tmux', 'cli-agent' 等
  final String category;

  const Snippet({
    required this.id,
    required this.name,
    required this.content,
    this.category = 'general',
  });

  Snippet copyWith({
    String? name,
    String? content,
    String? category,
  }) {
    return Snippet(
      id: id,
      name: name ?? this.name,
      content: content ?? this.content,
      category: category ?? this.category,
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'content': content,
        'category': category,
      };

  factory Snippet.fromJson(Map<String, dynamic> json) => Snippet(
        id: json['id'] as String,
        name: json['name'] as String,
        content: json['content'] as String,
        category: json['category'] as String? ?? 'general',
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
  }) async {
    final snippet = Snippet(
      id: DateTime.now().millisecondsSinceEpoch.toString(),
      name: name,
      content: content,
      category: category,
    );
    state = state.copyWith(snippets: [...state.snippets, snippet]);
    await _save();
  }

  Future<void> updateSnippet(String id, {String? name, String? content, String? category}) async {
    final updated = state.snippets.map((s) {
      if (s.id == id) return s.copyWith(name: name, content: content, category: category);
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

  List<Snippet> getByCategory(String category) {
    return state.snippets.where((s) => s.category == category).toList();
  }
}

final snippetsProvider = NotifierProvider<SnippetsNotifier, SnippetsState>(
  SnippetsNotifier.new,
);
