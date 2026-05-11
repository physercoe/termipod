import 'dart:convert';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../../providers/hub_provider.dart';
import '../../theme/design_colors.dart';

/// Wire shape for `metric-chart`-kind artifacts. v1 is one or more
/// series of `[x, y]` numeric points with optional axis labels. The
/// hub seeds this shape for the demo eval-results artifact; agents
/// targeting this viewer should produce the same.
///
///   {
///     "version": 1,
///     "title": "Eval accuracy",
///     "x_label": "Step",
///     "y_label": "Accuracy",
///     "series": [
///       {"name": "eval_accuracy", "points": [[0, 0.50], [100, 0.62], …]}
///     ]
///   }
///
/// `series[].color` is optional; the viewer picks brand colors from a
/// fixed palette so distinct series stay legible without coordination.
class ArtifactMetricChartViewer extends ConsumerStatefulWidget {
  final String uri;
  final String? title;

  const ArtifactMetricChartViewer({
    super.key,
    required this.uri,
    this.title,
  });

  @override
  ConsumerState<ArtifactMetricChartViewer> createState() =>
      _ArtifactMetricChartViewerState();
}

class _ArtifactMetricChartViewerState
    extends ConsumerState<ArtifactMetricChartViewer> {
  MetricChartBody? _chart;
  String? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final uri = widget.uri;
    if (!uri.startsWith('blob:sha256/')) {
      setState(() {
        _loading = false;
        _error = 'unsupported uri scheme — only hub-served blobs '
            '(blob:sha256/…) render today';
      });
      return;
    }
    final sha = uri.substring('blob:sha256/'.length);
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      setState(() {
        _loading = false;
        _error = 'hub not connected';
      });
      return;
    }
    try {
      final bytes = await client.downloadBlobCached(sha);
      if (!mounted) return;
      final decoded = jsonDecode(utf8.decode(Uint8List.fromList(bytes)));
      final chart = parseMetricChart(decoded);
      if (chart == null) {
        setState(() {
          _loading = false;
          _error =
              'metric-chart parse error — body is not the expected shape';
        });
        return;
      }
      setState(() {
        _chart = chart;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return _MetricChartLoadError(message: _error!, uri: widget.uri);
    }
    final chart = _chart;
    if (chart == null || chart.series.isEmpty) {
      return _MetricChartLoadError(
        message: 'chart has no series',
        uri: widget.uri,
      );
    }
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final muted =
        isDark ? DesignColors.textMuted : DesignColors.textMutedLight;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (chart.title.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(
                chart.title,
                style: GoogleFonts.spaceGrotesk(
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
          Expanded(
            child: CustomPaint(
              painter: _MetricChartPainter(
                chart: chart,
                axisColor: muted,
                gridColor: muted.withValues(alpha: 0.18),
                labelColor: muted,
              ),
              child: const SizedBox.expand(),
            ),
          ),
          const SizedBox(height: 8),
          _LegendRow(chart: chart, muted: muted),
        ],
      ),
    );
  }
}

class _LegendRow extends StatelessWidget {
  final MetricChartBody chart;
  final Color muted;
  const _LegendRow({required this.chart, required this.muted});

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 12,
      runSpacing: 4,
      children: [
        for (var i = 0; i < chart.series.length; i++)
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(
                  color: _seriesColor(chart.series[i], i),
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(width: 6),
              Text(
                chart.series[i].name,
                style: GoogleFonts.jetBrainsMono(fontSize: 11, color: muted),
              ),
            ],
          ),
      ],
    );
  }
}

/// One named series of `[x, y]` numeric points. `color` is optional;
/// when null the viewer falls back to the brand palette by index.
@immutable
class MetricChartSeries {
  final String name;
  final Color? color;
  final List<({double x, double y})> points;
  const MetricChartSeries({
    required this.name,
    required this.points,
    this.color,
  });
}

@immutable
class MetricChartBody {
  final int version;
  final String title;
  final String xLabel;
  final String yLabel;
  final List<MetricChartSeries> series;
  const MetricChartBody({
    required this.version,
    required this.title,
    required this.xLabel,
    required this.yLabel,
    required this.series,
  });
}

/// Parse a decoded JSON value into a [MetricChartBody]. Tolerant of
/// trivial shape drift (missing labels, extra fields). Returns null
/// when the input is not a Map or the version is unknown. Public for
/// unit testing.
@visibleForTesting
MetricChartBody? parseMetricChart(dynamic decoded) {
  if (decoded is! Map) return null;
  final version = decoded['version'];
  if (version != null && version is int && version != 1) return null;
  final rawSeries = decoded['series'];
  if (rawSeries is! List) return null;
  final series = <MetricChartSeries>[];
  for (final s in rawSeries) {
    if (s is! Map) continue;
    final name = (s['name'] ?? '').toString();
    final rawPoints = s['points'];
    if (rawPoints is! List) continue;
    final points = <({double x, double y})>[];
    for (final p in rawPoints) {
      if (p is! List || p.length < 2) continue;
      final x = (p[0] as num?)?.toDouble();
      final y = (p[1] as num?)?.toDouble();
      if (x == null || y == null) continue;
      points.add((x: x, y: y));
    }
    if (points.isEmpty) continue;
    Color? color;
    final rawColor = s['color'];
    if (rawColor is String && rawColor.startsWith('#')) {
      color = _parseHexColor(rawColor);
    }
    series.add(MetricChartSeries(
      name: name.isEmpty ? 'series ${series.length + 1}' : name,
      points: points,
      color: color,
    ));
  }
  if (series.isEmpty) return null;
  return MetricChartBody(
    version: 1,
    title: (decoded['title'] ?? '').toString(),
    xLabel: (decoded['x_label'] ?? '').toString(),
    yLabel: (decoded['y_label'] ?? '').toString(),
    series: series,
  );
}

Color? _parseHexColor(String hex) {
  final h = hex.replaceFirst('#', '');
  if (h.length != 6) return null;
  final v = int.tryParse(h, radix: 16);
  if (v == null) return null;
  return Color(0xFF000000 | v);
}

/// Brand palette for unnamed series colors. Cycles by index so two
/// series get distinct hues without the producer having to pick them.
const _kSeriesPalette = <Color>[
  DesignColors.primary,
  DesignColors.terminalBlue,
  DesignColors.warning,
];

Color _seriesColor(MetricChartSeries s, int index) {
  return s.color ?? _kSeriesPalette[index % _kSeriesPalette.length];
}

class _MetricChartPainter extends CustomPainter {
  final MetricChartBody chart;
  final Color axisColor;
  final Color gridColor;
  final Color labelColor;
  _MetricChartPainter({
    required this.chart,
    required this.axisColor,
    required this.gridColor,
    required this.labelColor,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (chart.series.isEmpty) return;
    const padLeft = 44.0;
    const padRight = 16.0;
    const padTop = 12.0;
    const padBottom = 30.0;
    final plotW = size.width - padLeft - padRight;
    final plotH = size.height - padTop - padBottom;
    if (plotW <= 0 || plotH <= 0) return;

    var xMin = double.infinity;
    var xMax = double.negativeInfinity;
    var yMin = double.infinity;
    var yMax = double.negativeInfinity;
    for (final s in chart.series) {
      for (final p in s.points) {
        xMin = math.min(xMin, p.x);
        xMax = math.max(xMax, p.x);
        yMin = math.min(yMin, p.y);
        yMax = math.max(yMax, p.y);
      }
    }
    if (!xMin.isFinite || !yMin.isFinite) return;
    if (xMax == xMin) xMax = xMin + 1;
    if (yMax == yMin) yMax = yMin + 1;
    // Pad y range by 5% so the polyline doesn't kiss the top/bottom edges.
    final yPad = (yMax - yMin) * 0.05;
    yMin -= yPad;
    yMax += yPad;

    double toX(double v) =>
        padLeft + plotW * (v - xMin) / (xMax - xMin);
    double toY(double v) =>
        padTop + plotH * (1 - (v - yMin) / (yMax - yMin));

    final axisPaint = Paint()
      ..color = axisColor
      ..strokeWidth = 1
      ..style = PaintingStyle.stroke;
    final gridPaint = Paint()
      ..color = gridColor
      ..strokeWidth = 0.5
      ..style = PaintingStyle.stroke;

    // Y gridlines + labels (4 ticks).
    const ticks = 4;
    for (var i = 0; i <= ticks; i++) {
      final t = i / ticks;
      final y = padTop + plotH * t;
      canvas.drawLine(
        Offset(padLeft, y),
        Offset(padLeft + plotW, y),
        i == ticks ? axisPaint : gridPaint,
      );
      final value = yMax - (yMax - yMin) * t;
      _drawLabel(
        canvas,
        _formatTick(value),
        Offset(padLeft - 6, y),
        labelColor,
        align: _LabelAlign.rightCenter,
      );
    }
    // X axis baseline + 3 ticks.
    for (var i = 0; i <= 3; i++) {
      final t = i / 3;
      final x = padLeft + plotW * t;
      canvas.drawLine(
        Offset(x, padTop + plotH),
        Offset(x, padTop + plotH + 4),
        axisPaint,
      );
      final value = xMin + (xMax - xMin) * t;
      _drawLabel(
        canvas,
        _formatTick(value),
        Offset(x, padTop + plotH + 6),
        labelColor,
        align: _LabelAlign.topCenter,
      );
    }

    // Axis labels.
    if (chart.yLabel.isNotEmpty) {
      _drawLabel(
        canvas,
        chart.yLabel,
        Offset(8, padTop + plotH / 2),
        labelColor,
        align: _LabelAlign.leftCenterRotated,
      );
    }
    if (chart.xLabel.isNotEmpty) {
      _drawLabel(
        canvas,
        chart.xLabel,
        Offset(padLeft + plotW / 2, size.height - 6),
        labelColor,
        align: _LabelAlign.bottomCenter,
      );
    }

    // Polylines.
    for (var i = 0; i < chart.series.length; i++) {
      final s = chart.series[i];
      final color = _seriesColor(s, i);
      final paint = Paint()
        ..color = color
        ..strokeWidth = 2
        ..style = PaintingStyle.stroke
        ..strokeJoin = StrokeJoin.round
        ..strokeCap = StrokeCap.round;
      final path = Path();
      for (var j = 0; j < s.points.length; j++) {
        final p = s.points[j];
        final pos = Offset(toX(p.x), toY(p.y));
        if (j == 0) {
          path.moveTo(pos.dx, pos.dy);
        } else {
          path.lineTo(pos.dx, pos.dy);
        }
      }
      canvas.drawPath(path, paint);
      // Endpoints as filled dots so single-point series still render.
      final dotPaint = Paint()..color = color;
      for (final p in s.points) {
        canvas.drawCircle(Offset(toX(p.x), toY(p.y)), 2.5, dotPaint);
      }
    }
  }

  static String _formatTick(double v) {
    if (v.abs() >= 1000) return v.toStringAsFixed(0);
    if (v.abs() >= 10) return v.toStringAsFixed(1);
    return v.toStringAsFixed(2);
  }

  @override
  bool shouldRepaint(covariant _MetricChartPainter old) =>
      old.chart != chart ||
      old.axisColor != axisColor ||
      old.gridColor != gridColor ||
      old.labelColor != labelColor;
}

enum _LabelAlign {
  rightCenter,
  topCenter,
  bottomCenter,
  leftCenterRotated,
}

void _drawLabel(
  Canvas canvas,
  String text,
  Offset anchor,
  Color color, {
  required _LabelAlign align,
}) {
  final tp = TextPainter(
    text: TextSpan(
      text: text,
      style: TextStyle(fontSize: 10, color: color),
    ),
    textDirection: TextDirection.ltr,
    maxLines: 1,
    ellipsis: '…',
  );
  tp.layout();
  Offset offset;
  switch (align) {
    case _LabelAlign.rightCenter:
      offset = Offset(anchor.dx - tp.width, anchor.dy - tp.height / 2);
    case _LabelAlign.topCenter:
      offset = Offset(anchor.dx - tp.width / 2, anchor.dy);
    case _LabelAlign.bottomCenter:
      offset = Offset(anchor.dx - tp.width / 2, anchor.dy - tp.height);
    case _LabelAlign.leftCenterRotated:
      canvas.save();
      canvas.translate(anchor.dx + tp.height / 2, anchor.dy + tp.width / 2);
      canvas.rotate(-math.pi / 2);
      tp.paint(canvas, Offset.zero);
      canvas.restore();
      return;
  }
  tp.paint(canvas, offset);
}

class _MetricChartLoadError extends StatelessWidget {
  final String message;
  final String uri;
  const _MetricChartLoadError({required this.message, required this.uri});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.show_chart, size: 36, color: DesignColors.textMuted),
          const SizedBox(height: 12),
          Text(
            'Cannot render chart',
            style: GoogleFonts.spaceGrotesk(
                fontSize: 14, fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 6),
          Text(
            message,
            textAlign: TextAlign.center,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 11, color: DesignColors.textMuted),
          ),
          const SizedBox(height: 8),
          SelectableText(
            uri,
            style: GoogleFonts.jetBrainsMono(
                fontSize: 10, color: DesignColors.textMuted),
          ),
        ],
      ),
    );
  }
}

/// Fullscreen route for the metric-chart viewer. Mirrors the other
/// wave-2 viewer screens so `_ArtifactViewerLauncher` routes all kinds
/// the same way.
class ArtifactMetricChartViewerScreen extends StatelessWidget {
  final String uri;
  final String title;
  const ArtifactMetricChartViewerScreen({
    super.key,
    required this.uri,
    required this.title,
  });

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          title,
          style: GoogleFonts.spaceGrotesk(
              fontSize: 14, fontWeight: FontWeight.w700),
          overflow: TextOverflow.ellipsis,
        ),
      ),
      body: ArtifactMetricChartViewer(uri: uri, title: title),
    );
  }
}
