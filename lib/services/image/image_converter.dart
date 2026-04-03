import 'dart:isolate';
import 'dart:typed_data';

import 'package:image/image.dart' as img;

/// 画像出力フォーマット
enum ImageOutputFormat {
  original,
  png,
  jpeg;

  static ImageOutputFormat fromString(String value) {
    switch (value) {
      case 'png':
        return ImageOutputFormat.png;
      case 'jpeg':
        return ImageOutputFormat.jpeg;
      default:
        return ImageOutputFormat.original;
    }
  }
}

/// 画像変換結果
class ImageConvertResult {
  final Uint8List bytes;
  final String extension;

  const ImageConvertResult({required this.bytes, required this.extension});
}

/// 画像フォーマット変換 + リサイズサービス
///
/// `image` パッケージを使用し、Isolate でバックグラウンド実行する。
class ImageConverter {
  /// 画像のフォーマット変換とリサイズを行う
  ///
  /// [bytes] 元の画像バイトデータ
  /// [format] 出力フォーマット（original/png/jpeg）
  /// [jpegQuality] JPEG品質（1-100）
  /// [autoResize] リサイズを行うか
  /// [maxWidth] 最大幅（0 = 無制限）
  /// [maxHeight] 最大高さ（0 = 無制限）
  static Future<ImageConvertResult> convert({
    required Uint8List bytes,
    required ImageOutputFormat format,
    int jpegQuality = 85,
    bool autoResize = false,
    int maxWidth = 1920,
    int maxHeight = 1080,
  }) async {
    // 変換不要の場合はそのまま返す
    if (format == ImageOutputFormat.original && !autoResize) {
      return ImageConvertResult(
        bytes: bytes,
        extension: detectExtension(bytes),
      );
    }

    // Isolate でバックグラウンド実行（UIスレッドブロック防止）
    return await Isolate.run(() => _processImage(
          bytes: bytes,
          format: format,
          jpegQuality: jpegQuality,
          autoResize: autoResize,
          maxWidth: maxWidth,
          maxHeight: maxHeight,
        ));
  }

  /// Isolate 内で実行される画像処理
  static ImageConvertResult _processImage({
    required Uint8List bytes,
    required ImageOutputFormat format,
    required int jpegQuality,
    required bool autoResize,
    required int maxWidth,
    required int maxHeight,
  }) {
    final image = img.decodeImage(bytes);
    if (image == null) {
      throw FormatException('Failed to decode image');
    }

    var processed = image;

    // リサイズ
    if (autoResize) {
      final needsResize = (maxWidth > 0 && processed.width > maxWidth) ||
          (maxHeight > 0 && processed.height > maxHeight);
      if (needsResize) {
        // アスペクト比を維持してリサイズ
        final widthRatio = maxWidth > 0 ? processed.width / maxWidth : 0.0;
        final heightRatio = maxHeight > 0 ? processed.height / maxHeight : 0.0;
        final ratio = widthRatio > heightRatio ? widthRatio : heightRatio;
        if (ratio > 1.0) {
          processed = img.copyResize(
            processed,
            width: (processed.width / ratio).round(),
            height: (processed.height / ratio).round(),
          );
        }
      }
    }

    // フォーマット変換
    switch (format) {
      case ImageOutputFormat.png:
        return ImageConvertResult(
          bytes: Uint8List.fromList(img.encodePng(processed)),
          extension: 'png',
        );
      case ImageOutputFormat.jpeg:
        return ImageConvertResult(
          bytes: Uint8List.fromList(img.encodeJpg(processed, quality: jpegQuality)),
          extension: 'jpg',
        );
      case ImageOutputFormat.original:
        // リサイズのみ（元フォーマットで再エンコード）
        final ext = detectExtension(bytes);
        if (ext == 'jpg' || ext == 'jpeg') {
          return ImageConvertResult(
            bytes: Uint8List.fromList(img.encodeJpg(processed, quality: jpegQuality)),
            extension: ext,
          );
        }
        return ImageConvertResult(
          bytes: Uint8List.fromList(img.encodePng(processed)),
          extension: 'png',
        );
    }
  }

  /// バイトデータのマジックバイトからファイルフォーマットを検出
  static String detectExtension(Uint8List bytes) {
    if (bytes.length >= 3 && bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF) {
      return 'jpg';
    }
    if (bytes.length >= 4 && bytes[0] == 0x89 && bytes[1] == 0x50 && bytes[2] == 0x4E && bytes[3] == 0x47) {
      return 'png';
    }
    if (bytes.length >= 3 && bytes[0] == 0x47 && bytes[1] == 0x49 && bytes[2] == 0x46) {
      return 'gif';
    }
    return 'png'; // デフォルト
  }
}
