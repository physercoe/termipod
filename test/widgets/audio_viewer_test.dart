import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/artifact_viewers/audio_viewer.dart';

void main() {
  group('formatAudioDuration', () {
    test('renders sub-hour durations as m:ss', () {
      expect(formatAudioDuration(const Duration(seconds: 0)), '0:00');
      expect(formatAudioDuration(const Duration(seconds: 5)), '0:05');
      expect(formatAudioDuration(const Duration(minutes: 3, seconds: 42)),
          '3:42');
      expect(formatAudioDuration(const Duration(minutes: 59, seconds: 59)),
          '59:59');
    });

    test('renders ≥1h durations as h:mm:ss', () {
      expect(formatAudioDuration(const Duration(hours: 1)), '1:00:00');
      expect(
        formatAudioDuration(
            const Duration(hours: 2, minutes: 5, seconds: 3)),
        '2:05:03',
      );
    });
  });

  group('ArtifactAudioViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactAudioViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot play audio'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactAudioViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with title in the AppBar',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactAudioViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'Voice memo',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Voice memo'), findsOneWidget);
    });
  });
}
