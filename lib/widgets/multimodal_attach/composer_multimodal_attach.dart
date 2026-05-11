import 'dart:convert';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

/// Per-modality picker helpers for the PDF / audio / video attach
/// surfaces (artifact-type-registry W7.2). Mirrors the
/// `composer_image_attach.dart` pattern — caller picks once, validates
/// against the hub's per-modality caps before send, surfaces user-
/// readable errors on rejection.
///
/// Image attach stays in `composer_image_attach.dart` because that one
/// also runs the compress/recode pipeline; the modalities here are
/// pass-through (no decode/resize).

/// Caps mirror `hub/internal/server/handlers_agent_input.go` so the
/// composer can clamp before the request lands instead of round-trip
/// 400. Counts are conservative — 1 attachment of each modality per
/// turn is enough for the demo arc.
const int kMaxPdfsPerTurn = 1;
const int kMaxPdfBytes = 32 * 1024 * 1024;
const int kMaxAudiosPerTurn = 1;
const int kMaxAudioBytes = 20 * 1024 * 1024;
const int kMaxVideosPerTurn = 1;
const int kMaxVideoBytes = 20 * 1024 * 1024;

/// MIME allowlists mirror the hub-side allowedPdfMimes / allowedAudio
/// Mimes / allowedVideoMimes maps. Kept narrow on purpose — broader
/// formats route through the artifact upload path where conversion
/// happens out-of-band.
const Set<String> kPdfMimes = {'application/pdf'};
const Set<String> kAudioMimes = {
  'audio/mpeg',
  'audio/mp4',
  'audio/wav',
  'audio/webm',
  'audio/ogg',
  'audio/aac',
  'audio/flac',
};
const Set<String> kVideoMimes = {
  'video/mp4',
  'video/webm',
  'video/quicktime',
};

/// Three modalities share a wire shape: {mime_type, data, filename}.
/// One struct keeps the composer code DRY across modalities.
class MultimodalAttachment {
  final String mimeType;
  final String data;
  final String filename;
  const MultimodalAttachment({
    required this.mimeType,
    required this.data,
    required this.filename,
  });

  Map<String, String> toJson() => {
        'mime_type': mimeType,
        'data': data,
        'filename': filename,
      };
}

class MultimodalAttachError implements Exception {
  final String message;
  MultimodalAttachError(this.message);
  @override
  String toString() => message;
}

enum MultimodalKind { pdf, audio, video }

extension MultimodalKindX on MultimodalKind {
  String get label {
    switch (this) {
      case MultimodalKind.pdf:
        return 'PDF';
      case MultimodalKind.audio:
        return 'audio';
      case MultimodalKind.video:
        return 'video';
    }
  }

  int get maxBytes {
    switch (this) {
      case MultimodalKind.pdf:
        return kMaxPdfBytes;
      case MultimodalKind.audio:
        return kMaxAudioBytes;
      case MultimodalKind.video:
        return kMaxVideoBytes;
    }
  }

  Set<String> get mimes {
    switch (this) {
      case MultimodalKind.pdf:
        return kPdfMimes;
      case MultimodalKind.audio:
        return kAudioMimes;
      case MultimodalKind.video:
        return kVideoMimes;
    }
  }

  List<String> get extensions {
    switch (this) {
      case MultimodalKind.pdf:
        return ['pdf'];
      case MultimodalKind.audio:
        return ['mp3', 'm4a', 'wav', 'webm', 'ogg', 'aac', 'flac'];
      case MultimodalKind.video:
        return ['mp4', 'webm', 'mov'];
    }
  }
}

/// Resolves a MIME type from an extension. Falls back to `null` when
/// the picker reports a file whose extension isn't on the modality's
/// allowlist. Pulled out so widget tests can verify the mapping
/// without driving the file picker.
@visibleForTesting
String? mimeForExtension(String ext, MultimodalKind kind) {
  switch (ext.toLowerCase()) {
    case 'pdf':
      return 'application/pdf';
    case 'mp3':
      return 'audio/mpeg';
    case 'm4a':
      return 'audio/mp4';
    case 'wav':
      return 'audio/wav';
    case 'webm':
      return kind == MultimodalKind.video ? 'video/webm' : 'audio/webm';
    case 'ogg':
      return 'audio/ogg';
    case 'aac':
      return 'audio/aac';
    case 'flac':
      return 'audio/flac';
    case 'mp4':
      return 'video/mp4';
    case 'mov':
      return 'video/quicktime';
  }
  return null;
}

/// Opens the system file picker filtered to [kind]'s extension
/// allowlist, reads bytes (capped at `kind.maxBytes`), validates the
/// resolved MIME against `kind.mimes`, and returns the resulting
/// [MultimodalAttachment] base64-encoded. Returns null if the user
/// cancels. Throws [MultimodalAttachError] for size/format failures so
/// the composer can show a banner.
Future<MultimodalAttachment?> pickMultimodalFile(
  MultimodalKind kind, {
  FilePicker? picker,
}) async {
  final p = picker ?? FilePicker.platform;
  final result = await p.pickFiles(
    type: FileType.custom,
    allowedExtensions: kind.extensions,
    allowMultiple: false,
    withData: true,
  );
  if (result == null || result.files.isEmpty) return null;
  final f = result.files.first;
  final bytes = f.bytes;
  if (bytes == null) {
    throw MultimodalAttachError('Could not read file bytes');
  }
  if (bytes.length > kind.maxBytes) {
    final mib = (bytes.length / 1024 / 1024).toStringAsFixed(1);
    final capMib = (kind.maxBytes / 1024 / 1024).toStringAsFixed(0);
    throw MultimodalAttachError(
      '${kind.label} too large ($mib MiB > $capMib MiB)',
    );
  }
  final name = f.name;
  final dot = name.lastIndexOf('.');
  final ext = dot < 0 ? '' : name.substring(dot + 1);
  final mime = mimeForExtension(ext, kind);
  if (mime == null || !kind.mimes.contains(mime)) {
    throw MultimodalAttachError(
      'Unsupported ${kind.label} format: .$ext',
    );
  }
  return MultimodalAttachment(
    mimeType: mime,
    data: base64Encode(bytes),
    filename: name,
  );
}
