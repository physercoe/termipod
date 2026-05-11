import 'dart:convert';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

/// Shared text-file attach helpers for compose surfaces (full AgentFeed
/// + steward overlay chat). Wave 2 W7.1 of artifact-type-registry —
/// markdown/code/.txt bytes are inlined into the prompt body as a
/// fenced code block so every engine (Claude / Gemini / Codex) sees
/// them as text in the user message. No hub/driver work; no
/// multimodal content block on the wire.
///
/// Distinct from `composer_image_attach.dart` because the wire
/// representation differs (text-as-text vs base64 image block) and the
/// capability gate is different (none vs `prompt_image[mode]`).
const int kMaxTextAttachBytes = 256 * 1024; // 256 KiB

/// File extensions the picker accepts. Kept conservative — narrower
/// than the system file picker would surface — because the failure
/// mode for binary-as-text is a screenful of garbage in the prompt.
const Set<String> kTextAttachExtensions = {
  'md',
  'markdown',
  'txt',
  'text',
  'log',
  'csv',
  'tsv',
  'json',
  'jsonl',
  'yaml',
  'yml',
  'toml',
  'ini',
  'xml',
  'html',
  'htm',
  'css',
  'scss',
  'py',
  'js',
  'mjs',
  'cjs',
  'jsx',
  'ts',
  'tsx',
  'go',
  'rs',
  'java',
  'kt',
  'swift',
  'rb',
  'php',
  'c',
  'h',
  'cc',
  'cpp',
  'cxx',
  'hpp',
  'cs',
  'sh',
  'bash',
  'zsh',
  'sql',
  'dart',
  'tex',
  'r',
  'lua',
  'pl',
  'env',
  'dockerfile',
  'makefile',
  'gitignore',
};

/// Resolves a markdown fence-tag from a file extension. Unknown
/// extensions return the empty string (renders as `` ``` `` with no
/// language tag — still a valid code fence, just uncoloured by
/// downstream renderers that key off the language).
@visibleForTesting
String fenceLanguageForExtension(String ext) {
  final e = ext.toLowerCase();
  const map = <String, String>{
    'md': 'markdown',
    'markdown': 'markdown',
    'txt': '',
    'text': '',
    'log': '',
    'csv': 'csv',
    'tsv': 'tsv',
    'json': 'json',
    'jsonl': 'json',
    'yaml': 'yaml',
    'yml': 'yaml',
    'toml': 'toml',
    'ini': 'ini',
    'xml': 'xml',
    'html': 'html',
    'htm': 'html',
    'css': 'css',
    'scss': 'scss',
    'py': 'python',
    'js': 'javascript',
    'mjs': 'javascript',
    'cjs': 'javascript',
    'jsx': 'jsx',
    'ts': 'typescript',
    'tsx': 'tsx',
    'go': 'go',
    'rs': 'rust',
    'java': 'java',
    'kt': 'kotlin',
    'swift': 'swift',
    'rb': 'ruby',
    'php': 'php',
    'c': 'c',
    'h': 'cpp',
    'cc': 'cpp',
    'cpp': 'cpp',
    'cxx': 'cpp',
    'hpp': 'cpp',
    'cs': 'cs',
    'sh': 'bash',
    'bash': 'bash',
    'zsh': 'bash',
    'sql': 'sql',
    'dart': 'dart',
    'tex': 'latex',
    'r': 'r',
    'lua': 'lua',
    'pl': 'perl',
    'dockerfile': 'dockerfile',
    'makefile': 'makefile',
  };
  return map[e] ?? '';
}

/// Builds the fenced code block that gets appended to the composer
/// text. Public for tests; called by [pickAndInlineTextFile] with the
/// picked bytes. Picks the fence delimiter (` ``` ` or longer) by
/// scanning the content for backtick runs — required because flutter
/// agents echo backticks inside fences sometimes and a clean parse
/// expects the closing fence to be longer than any internal run.
@visibleForTesting
String buildFencedBlock({
  required String filename,
  required String content,
  required String language,
}) {
  // Find the longest run of backticks in the content; the fence must
  // be at least one longer than that. Standard CommonMark behaviour;
  // the rare case that triggers is files containing fenced examples.
  var longest = 0;
  var current = 0;
  for (final c in content.codeUnits) {
    if (c == 0x60) {
      current++;
      if (current > longest) longest = current;
    } else {
      current = 0;
    }
  }
  final fence = '`' * (longest >= 3 ? longest + 1 : 3);
  final tag = language.isEmpty ? '' : language;
  // Trailing newline before the closing fence keeps the prompt
  // readable. Leading marker line names the source so the agent can
  // address the file by name in its reply.
  return '$fence$tag\n// $filename\n${content.trimRight()}\n$fence\n';
}

/// Result of a successful pick+read. `markdown` is the fenced code
/// block ready to splice into the composer text field; `path` is the
/// original filename (kept around so the composer can show a brief
/// toast on attach).
class TextAttachment {
  final String filename;
  final String markdown;
  final int bytes;
  const TextAttachment({
    required this.filename,
    required this.markdown,
    required this.bytes,
  });
}

class TextAttachError implements Exception {
  final String message;
  TextAttachError(this.message);
  @override
  String toString() => message;
}

/// Opens the system file picker, reads the picked file's bytes (capped
/// at [kMaxTextAttachBytes]), and returns a [TextAttachment] whose
/// `markdown` is a fenced code block ready to insert into the
/// composer. Returns null if the user cancels. Throws
/// [TextAttachError] for size-cap / unsupported-extension / decode
/// failures so the composer can show a banner.
Future<TextAttachment?> pickAndInlineTextFile({FilePicker? picker}) async {
  final p = picker ?? FilePicker.platform;
  final result = await p.pickFiles(
    type: FileType.any,
    allowMultiple: false,
    withData: true,
  );
  if (result == null || result.files.isEmpty) return null;
  final f = result.files.first;
  final raw = f.bytes;
  if (raw == null) {
    throw TextAttachError('Could not read file bytes');
  }
  if (raw.length > kMaxTextAttachBytes) {
    final kib = (raw.length / 1024).toStringAsFixed(1);
    throw TextAttachError(
      'File too large ($kib KiB > ${kMaxTextAttachBytes ~/ 1024} KiB)',
    );
  }
  final name = f.name;
  final ext = _extensionOf(name);
  if (ext.isNotEmpty && !kTextAttachExtensions.contains(ext.toLowerCase())) {
    throw TextAttachError('Unsupported file type: .$ext');
  }
  String text;
  try {
    text = utf8.decode(raw, allowMalformed: false);
  } catch (_) {
    throw TextAttachError('File is not valid UTF-8 text');
  }
  final language = fenceLanguageForExtension(ext);
  final markdown = buildFencedBlock(
    filename: name,
    content: text,
    language: language,
  );
  return TextAttachment(filename: name, markdown: markdown, bytes: raw.length);
}

String _extensionOf(String filename) {
  final dot = filename.lastIndexOf('.');
  if (dot < 0 || dot == filename.length - 1) return '';
  return filename.substring(dot + 1);
}
