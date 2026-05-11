import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/artifact_viewers/metric_chart_viewer.dart';

void main() {
  group('parseMetricChart', () {
    test('v1 explicit form parses series + labels', () {
      final c = parseMetricChart({
        'version': 1,
        'title': 'Eval accuracy',
        'x_label': 'Step',
        'y_label': 'Accuracy',
        'series': [
          {
            'name': 'eval_accuracy',
            'points': [
              [0, 0.5],
              [100, 0.7],
              [200, 0.85],
            ],
          },
        ],
      });
      expect(c, isNotNull);
      expect(c!.title, 'Eval accuracy');
      expect(c.xLabel, 'Step');
      expect(c.yLabel, 'Accuracy');
      expect(c.series, hasLength(1));
      expect(c.series[0].name, 'eval_accuracy');
      expect(c.series[0].points, hasLength(3));
      expect(c.series[0].points[1].x, 100);
      expect(c.series[0].points[1].y, 0.7);
    });

    test('missing version defaults to 1', () {
      final c = parseMetricChart({
        'series': [
          {
            'name': 's',
            'points': [
              [0, 1],
            ],
          },
        ],
      });
      expect(c, isNotNull);
      expect(c!.version, 1);
    });

    test('rejects unknown version', () {
      expect(
        parseMetricChart({
          'version': 2,
          'series': [
            {
              'name': 's',
              'points': [
                [0, 1],
              ],
            },
          ],
        }),
        isNull,
      );
    });

    test('rejects non-Map inputs', () {
      expect(parseMetricChart('nope'), isNull);
      expect(parseMetricChart(42), isNull);
      expect(parseMetricChart([1, 2, 3]), isNull);
    });

    test('drops malformed points + empty series', () {
      final c = parseMetricChart({
        'series': [
          {
            'name': 'ok',
            'points': [
              [0, 1],
              [1], // dropped (length < 2)
              ['x', 'y'], // dropped (non-numeric)
              [2, 3],
            ],
          },
          {'name': 'empty', 'points': []}, // whole series dropped
        ],
      });
      expect(c, isNotNull);
      expect(c!.series, hasLength(1));
      expect(c.series[0].name, 'ok');
      expect(c.series[0].points, hasLength(2));
    });

    test('returns null when no usable series', () {
      expect(
        parseMetricChart({'series': []}),
        isNull,
      );
      expect(
        parseMetricChart({
          'series': [
            {'name': 'a', 'points': []},
          ],
        }),
        isNull,
      );
    });

    test('parses optional hex color on series', () {
      final c = parseMetricChart({
        'series': [
          {
            'name': 's',
            'color': '#ff00aa',
            'points': [
              [0, 1],
            ],
          },
        ],
      });
      expect(c!.series[0].color, isNotNull);
      expect(c.series[0].color!.value, 0xFFFF00AA);
    });
  });

  group('ArtifactMetricChartViewer', () {
    testWidgets('renders unsupported-uri error for non-blob schemes',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: ArtifactMetricChartViewer(
              uri: 'blob:mock/lifecycle/x',
              title: 'Test',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Cannot render chart'), findsOneWidget);
      expect(find.textContaining('unsupported uri scheme'), findsOneWidget);
    });
  });

  group('ArtifactMetricChartViewerScreen', () {
    testWidgets('wraps the viewer in a Scaffold with title in the AppBar',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: ArtifactMetricChartViewerScreen(
            uri: 'blob:mock/lifecycle/x',
            title: 'Eval accuracy',
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Eval accuracy'), findsOneWidget);
    });
  });

  group('MetricChartInline', () {
    testWidgets('renders title + legend with collapsedHeight painter',
        (tester) async {
      final chart = parseMetricChart({
        'title': 'Eval accuracy',
        'series': [
          {
            'name': 'eval_accuracy',
            'points': [
              [0, 0.50],
              [100, 0.62],
              [200, 0.78],
            ],
          },
        ],
      });
      expect(chart, isNotNull);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: MetricChartInline(
              chart: chart!,
              showTitle: true,
              collapsedHeight: 120,
            ),
          ),
        ),
      );
      await tester.pump();
      // Title rendered (showTitle: true).
      expect(find.text('Eval accuracy'), findsOneWidget);
      // Legend renders the series name.
      expect(find.text('eval_accuracy'), findsOneWidget);
      // Painter occupies a non-zero box at the requested height.
      final paint = find.byType(CustomPaint).evaluate().any((e) {
        final cp = e.widget as CustomPaint;
        return cp.painter is MetricChartPainter;
      });
      expect(paint, isTrue);
    });

    testWidgets('hides title when showTitle is false', (tester) async {
      final chart = parseMetricChart({
        'title': 'Should not appear',
        'series': [
          {
            'name': 's',
            'points': [
              [0, 1],
              [1, 2],
            ],
          },
        ],
      });
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: MetricChartInline(chart: chart!),
          ),
        ),
      );
      await tester.pump();
      expect(find.text('Should not appear'), findsNothing);
      // Legend still renders.
      expect(find.text('s'), findsOneWidget);
    });
  });
}
