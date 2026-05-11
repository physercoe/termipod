import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';
import 'package:image_picker/image_picker.dart';

import '../../services/image/image_converter.dart';
import '../../theme/design_colors.dart';

/// Shared image-attach helpers for compose surfaces (full AgentFeed
/// + steward overlay chat). Caps mirror the hub validator so the
/// composer can clamp before sending instead of round-tripping a 400.
/// See `docs/plans/artifact-type-registry.md` W4 + ADR-021 W4.
const int kMaxImagesPerTurn = 3;
const int kMaxImageBytes = 5 * 1024 * 1024; // 5 MiB decoded
const int kComposeImageMaxEdge = 1024;
const int kComposeImageJpegQuality = 70;

/// Result of a successful pick+compress. `mimeType` lands on the
/// hub validator's allowlist; `data` is base64-encoded bytes.
class ComposerImageAttachment {
  final String mimeType;
  final String data;
  const ComposerImageAttachment({
    required this.mimeType,
    required this.data,
  });

  Map<String, String> toJson() => {'mime_type': mimeType, 'data': data};
}

/// `pickAndCompressImage` opens the gallery picker, compresses to JPEG
/// (1024px max edge / 70% quality), and base64-encodes the result.
/// Returns `null` if the user cancels; throws [ComposerImageAttachError]
/// with a user-facing message for size-cap or unsupported-format
/// failures so the composer can show a banner.
Future<ComposerImageAttachment?> pickAndCompressImage({
  ImagePicker? picker,
}) async {
  final p = picker ?? ImagePicker();
  final picked = await p.pickImage(source: ImageSource.gallery);
  if (picked == null) return null;
  final raw = await picked.readAsBytes();
  final converted = await ImageConverter.convert(
    bytes: raw,
    format: ImageOutputFormat.jpeg,
    jpegQuality: kComposeImageJpegQuality,
    autoResize: true,
    maxWidth: kComposeImageMaxEdge,
    maxHeight: kComposeImageMaxEdge,
  );
  final bytes = converted.bytes;
  if (bytes.length > kMaxImageBytes) {
    final mib = (bytes.length / 1024 / 1024).toStringAsFixed(1);
    throw ComposerImageAttachError(
      'Image too large after compression ($mib MiB > 5 MiB)',
    );
  }
  final mime = _mimeForExtension(converted.extension);
  if (mime == null) {
    throw ComposerImageAttachError(
      'Unsupported image format: ${converted.extension}',
    );
  }
  return ComposerImageAttachment(
    mimeType: mime,
    data: base64Encode(bytes),
  );
}

class ComposerImageAttachError implements Exception {
  final String message;
  ComposerImageAttachError(this.message);
  @override
  String toString() => message;
}

/// Resolves the agent-side capability flag for inline image input.
/// Joins `agent.kind` + `agent.driving_mode` against the family
/// registry's `prompt_image[mode]` flag (ADR-021 D5 / W4.6). Exposed
/// so widget tests can pin the gate without spinning up a fake hub.
@visibleForTesting
bool resolveCanAttachImages({
  required String? kind,
  required String? drivingMode,
  required List<Map<String, dynamic>> families,
}) {
  if (kind == null || kind.isEmpty) return false;
  final mode = (drivingMode == null || drivingMode.isEmpty) ? 'M4' : drivingMode;
  for (final f in families) {
    if (f['family'] == kind) {
      final pi = f['prompt_image'];
      if (pi is Map && pi[mode] == true) return true;
      return false;
    }
  }
  return false;
}

String? _mimeForExtension(String ext) {
  switch (ext) {
    case 'jpg':
    case 'jpeg':
      return 'image/jpeg';
    case 'png':
      return 'image/png';
    case 'gif':
      return 'image/gif';
    default:
      return null;
  }
}

/// Horizontal strip of pending-image thumbnails with a remove button
/// per entry. Used by both AgentCompose and the steward overlay chat
/// so the affordance + visual treatment stays identical across
/// surfaces.
class ComposerImageThumbnailStrip extends StatelessWidget {
  final List<Map<String, String>> images;
  final ValueChanged<int> onRemove;
  const ComposerImageThumbnailStrip({
    super.key,
    required this.images,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 64,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.only(bottom: 6, left: 4, right: 4),
        itemCount: images.length,
        separatorBuilder: (_, __) => const SizedBox(width: 6),
        itemBuilder: (ctx, i) {
          final entry = images[i];
          Uint8List? bytes;
          try {
            bytes = base64Decode(entry['data'] ?? '');
          } catch (_) {
            bytes = null;
          }
          return Stack(
            clipBehavior: Clip.none,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(6),
                child: Container(
                  width: 56,
                  height: 56,
                  color: DesignColors.surfaceDark,
                  child: bytes != null
                      ? Image.memory(bytes,
                          fit: BoxFit.cover, gaplessPlayback: true)
                      : const Icon(Icons.broken_image_outlined),
                ),
              ),
              Positioned(
                top: -6,
                right: -6,
                child: GestureDetector(
                  onTap: () => onRemove(i),
                  child: Container(
                    padding: const EdgeInsets.all(2),
                    decoration: const BoxDecoration(
                      color: DesignColors.error,
                      shape: BoxShape.circle,
                    ),
                    child: const Icon(Icons.close,
                        size: 12, color: Colors.white),
                  ),
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
