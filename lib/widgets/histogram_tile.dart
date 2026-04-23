import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:google_fonts/google_fonts.dart';

import '../theme/design_colors.dart';

/// Renders one histogram series as a step-scrubbable bar chart. Each
/// [rows] entry carries `{name, step, buckets: {edges, counts}}`; the
/// slider selects which step's distribution to show. Mirrors the
/// "Distributions" panel archetype from wandb/tensorboard.
///
/// Data-ownership stays consistent with the rest of the run-detail
/// surface: bytes come from the hub's run_histograms digest store
/// (blueprint §4), never from the original tensor.
class HistogramSeriesTile extends StatefulWidget {
  final String groupName;
  final List<Map<String, dynamic>> rows;
  const HistogramSeriesTile({
    super.key,
    required this.groupName,
    required this.rows,
  });

  @override
  State<HistogramSeriesTile> createState() => _HistogramSeriesTileState();
}

class _HistogramSeriesTileState extends State<HistogramSeriesTile> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    if (widget.rows.isEmpty) return const SizedBox.shrink();
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final safeIndex = _index.clamp(0, widget.rows.length - 1);
    final current = widget.rows[safeIndex];
    final step = (current['step'] as num?)?.toInt() ?? 0;
    final buckets = _parseBuckets(current['buckets']);

    final border =
        isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final surface =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;

    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Material(
        color: surface,
        shape: RoundedRectangleBorder(
          side: BorderSide(color: border),
          borderRadius: BorderRadius.circular(8),
        ),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 10, 12, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.bar_chart_outlined,
                      size: 14, color: DesignColors.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      widget.groupName,
                      style: GoogleFonts.jetBrainsMono(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  Text(
                    'step $step',
                    style: GoogleFonts.jetBrainsMono(
                      fontSize: 10,
                      color: DesignColors.textMuted,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 6),
              SizedBox(
                height: 90,
                child: CustomPaint(
                  painter: _HistogramPainter(
                    buckets: buckets,
                    barColor: DesignColors.primary,
                    axisColor: isDark
                        ? DesignColors.textMuted
                        : DesignColors.textMutedLight,
                  ),
                  child: const SizedBox.expand(),
                ),
              ),
              if (widget.rows.length > 1)
                Row(
                  children: [
                    SizedBox(
                      width: 30,
                      child: Text(
                        '${safeIndex + 1}/${widget.rows.length}',
                        style: GoogleFonts.jetBrainsMono(
                          fontSize: 9,
                          color: DesignColors.textMuted,
                        ),
                      ),
                    ),
                    Expanded(
                      child: Slider(
                        min: 0,
                        max: (widget.rows.length - 1).toDouble(),
                        divisions: widget.rows.length - 1,
                        value: safeIndex.toDouble(),
                        onChanged: (v) => setState(() => _index = v.round()),
                      ),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }

  _Buckets _parseBuckets(Object? raw) {
    // Hub sends `{"edges":[...],"counts":[...]}` as a JSON object; the
    // _listJson helper returns it parsed, not as a string. Defensive
    // anyway — if parsing fails we render empty bars rather than
    // blowing up the whole run-detail surface.
    if (raw is Map) {
      final edges = (raw['edges'] as List?)
              ?.map((v) => (v as num).toDouble())
              .toList() ??
          const <double>[];
      final counts =
          (raw['counts'] as List?)?.map((v) => (v as num).toInt()).toList() ??
              const <int>[];
      return _Buckets(edges: edges, counts: counts);
    }
    return const _Buckets(edges: [], counts: []);
  }
}

class _Buckets {
  final List<double> edges;
  final List<int> counts;
  const _Buckets({required this.edges, required this.counts});
}

class _HistogramPainter extends CustomPainter {
  final _Buckets buckets;
  final Color barColor;
  final Color axisColor;
  _HistogramPainter({
    required this.buckets,
    required this.barColor,
    required this.axisColor,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (buckets.counts.isEmpty ||
        buckets.edges.length != buckets.counts.length + 1) {
      return;
    }
    const padLeft = 24.0;
    const padRight = 6.0;
    const padTop = 4.0;
    const padBottom = 18.0;
    final plotW = size.width - padLeft - padRight;
    final plotH = size.height - padTop - padBottom;

    final maxCount = buckets.counts.fold<int>(0, math.max);
    if (maxCount == 0) return;

    final xMin = buckets.edges.first;
    final xMax = buckets.edges.last;
    final dx = (xMax - xMin).abs() < 1e-12 ? 1.0 : (xMax - xMin);

    // Baseline axis.
    final axisPaint = Paint()
      ..color = axisColor.withValues(alpha: 0.4)
      ..strokeWidth = 1.0;
    canvas.drawLine(
      Offset(padLeft, padTop + plotH),
      Offset(padLeft + plotW, padTop + plotH),
      axisPaint,
    );

    final barPaint = Paint()..color = barColor.withValues(alpha: 0.85);

    for (var i = 0; i < buckets.counts.length; i++) {
      final left = padLeft + (buckets.edges[i] - xMin) / dx * plotW;
      final right = padLeft + (buckets.edges[i + 1] - xMin) / dx * plotW;
      final h = plotH * (buckets.counts[i] / maxCount);
      final rect = Rect.fromLTRB(
        left + 0.5,
        padTop + plotH - h,
        right - 0.5,
        padTop + plotH,
      );
      canvas.drawRect(rect, barPaint);
    }

    // Axis labels — min/max bucket edge + peak count.
    _drawText(canvas, _fmt(xMin),
        Offset(padLeft, padTop + plotH + 3), axisColor);
    _drawText(canvas, _fmt(xMax),
        Offset(padLeft + plotW, padTop + plotH + 3), axisColor,
        alignRight: true);
    _drawText(canvas, 'n=$maxCount',
        Offset(padLeft - 2, padTop), axisColor,
        alignRight: true);
  }

  void _drawText(Canvas canvas, String text, Offset at, Color color,
      {bool alignRight = false}) {
    final tp = TextPainter(
      text: TextSpan(
        text: text,
        style: TextStyle(
          fontSize: 9,
          color: color.withValues(alpha: 0.8),
          fontFamily: 'monospace',
        ),
      ),
      textDirection: TextDirection.ltr,
    )..layout();
    final ox = alignRight ? at.dx - tp.width : at.dx;
    tp.paint(canvas, Offset(ox, at.dy));
  }

  String _fmt(double v) {
    final abs = v.abs();
    if (abs >= 100) return v.toStringAsFixed(0);
    if (abs >= 10) return v.toStringAsFixed(1);
    if (abs >= 1) return v.toStringAsFixed(2);
    if (abs >= 0.01) return v.toStringAsFixed(3);
    return v.toStringAsExponential(1);
  }

  @override
  bool shouldRepaint(covariant _HistogramPainter old) =>
      old.buckets != buckets || old.barColor != barColor;
}
