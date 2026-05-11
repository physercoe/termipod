import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/multimodal_attach/composer_multimodal_attach.dart';

void main() {
  group('mimeForExtension', () {
    test('pdf maps to application/pdf', () {
      expect(mimeForExtension('pdf', MultimodalKind.pdf), 'application/pdf');
      expect(mimeForExtension('PDF', MultimodalKind.pdf), 'application/pdf');
    });

    test('audio extensions map to expected mime types', () {
      expect(mimeForExtension('mp3', MultimodalKind.audio), 'audio/mpeg');
      expect(mimeForExtension('m4a', MultimodalKind.audio), 'audio/mp4');
      expect(mimeForExtension('wav', MultimodalKind.audio), 'audio/wav');
      expect(mimeForExtension('flac', MultimodalKind.audio), 'audio/flac');
    });

    test('video extensions map to expected mime types', () {
      expect(mimeForExtension('mp4', MultimodalKind.video), 'video/mp4');
      expect(mimeForExtension('mov', MultimodalKind.video), 'video/quicktime');
    });

    test('webm is disambiguated by kind', () {
      expect(mimeForExtension('webm', MultimodalKind.audio), 'audio/webm');
      expect(mimeForExtension('webm', MultimodalKind.video), 'video/webm');
    });

    test('unknown extensions return null', () {
      expect(mimeForExtension('xyz', MultimodalKind.pdf), null);
      expect(mimeForExtension('avi', MultimodalKind.video), null);
    });
  });

  group('MultimodalKindX extension', () {
    test('label/maxBytes/mimes/extensions wired correctly', () {
      expect(MultimodalKind.pdf.label, 'PDF');
      expect(MultimodalKind.audio.label, 'audio');
      expect(MultimodalKind.video.label, 'video');
      expect(MultimodalKind.pdf.maxBytes, kMaxPdfBytes);
      expect(MultimodalKind.audio.maxBytes, kMaxAudioBytes);
      expect(MultimodalKind.video.maxBytes, kMaxVideoBytes);
      expect(MultimodalKind.pdf.mimes, kPdfMimes);
      expect(MultimodalKind.audio.mimes, kAudioMimes);
      expect(MultimodalKind.video.mimes, kVideoMimes);
      expect(MultimodalKind.pdf.extensions, ['pdf']);
      expect(MultimodalKind.video.extensions, ['mp4', 'webm', 'mov']);
    });
  });
}
