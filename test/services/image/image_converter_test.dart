import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';

import 'package:flutter_muxpod/services/image/image_converter.dart';

void main() {
  group('ImageOutputFormat', () {
    test('fromString parses known values', () {
      expect(ImageOutputFormat.fromString('original'), ImageOutputFormat.original);
      expect(ImageOutputFormat.fromString('png'), ImageOutputFormat.png);
      expect(ImageOutputFormat.fromString('jpeg'), ImageOutputFormat.jpeg);
    });

    test('fromString returns original for unknown values', () {
      expect(ImageOutputFormat.fromString('bmp'), ImageOutputFormat.original);
      expect(ImageOutputFormat.fromString(''), ImageOutputFormat.original);
    });
  });

  group('ImageConverter.detectExtension', () {
    test('detects JPEG from magic bytes', () {
      final bytes = Uint8List.fromList([0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10]);
      expect(ImageConverter.detectExtension(bytes), 'jpg');
    });

    test('detects PNG from magic bytes', () {
      final bytes = Uint8List.fromList([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A]);
      expect(ImageConverter.detectExtension(bytes), 'png');
    });

    test('detects GIF from magic bytes', () {
      final bytes = Uint8List.fromList([0x47, 0x49, 0x46, 0x38, 0x39, 0x61]);
      expect(ImageConverter.detectExtension(bytes), 'gif');
    });

    test('returns png for unknown format', () {
      final bytes = Uint8List.fromList([0x00, 0x00, 0x00, 0x00]);
      expect(ImageConverter.detectExtension(bytes), 'png');
    });

    test('returns png for empty bytes', () {
      final bytes = Uint8List.fromList([]);
      expect(ImageConverter.detectExtension(bytes), 'png');
    });
  });

  group('ImageConverter.convert', () {
    test('returns original bytes when format is original and no resize', () async {
      final jpegHeader = Uint8List.fromList([0xFF, 0xD8, 0xFF, 0xE0, ...List.filled(100, 0)]);
      final result = await ImageConverter.convert(
        bytes: jpegHeader,
        format: ImageOutputFormat.original,
        autoResize: false,
      );
      expect(result.bytes, jpegHeader);
      expect(result.extension, 'jpg');
    });
  });
}
