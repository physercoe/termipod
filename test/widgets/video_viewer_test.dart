import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/artifact_viewers/video_viewer.dart';

void main() {
  group('ArtifactVideoViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactVideoViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot play video'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactVideoViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with title in the AppBar',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactVideoViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'Walkthrough',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Walkthrough'), findsOneWidget);
    });
  });
}
