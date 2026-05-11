import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/artifact_viewers/tabular_viewer.dart';

void main() {
  group('ArtifactTabularViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactTabularViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot render table'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactTabularViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with the title in the AppBar',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactTabularViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'References',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('References'), findsOneWidget);
    });
  });
}
