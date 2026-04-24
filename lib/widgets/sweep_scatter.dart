import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import '../providers/hub_provider.dart';
import '../theme/design_colors.dart';

/// Cross-run scatter panel — wandb "parallel coords" / sweep-compare
/// archetype (plot type 9). Each point is one run in the project,
/// positioned by (X, Y) where each axis can be a config parameter
/// (parsed from config_json) or a final metric value. A third
/// categorical config dimension drives point color so the reviewer
/// sees e.g. "adamw vs lion" at a glance.
///
/// Fetches via `getProjectSweepSummary(projectId)` — no N+1 fan-out.
/// Safe on empty projects: renders a neutral empty-state instead of an
/// axis-less chart.
class SweepScatter extends ConsumerStatefulWidget {
  final String projectId;
  const SweepScatter({super.key, required this.projectId});

  @override
  ConsumerState<SweepScatter> createState() => _SweepScatterState();
}

class _SweepScatterState extends ConsumerState<SweepScatter> {
  Future<List<Map<String, dynamic>>>? _future;
  String? _xAxis;
  String? _yAxis;
  String? _colorBy;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  void _reload() {
    final client = ref.read(hubProvider.notifier).client;
    if (client == null) {
      _future = Future.value(const []);
      return;
    }
    _future = client
        .getProjectSweepSummaryCached(widget.projectId)
        .then((r) => r.body);
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<Map<String, dynamic>>>(
      future: _future,
      builder: (context, snap) {
        if (snap.connectionState != ConnectionState.done) {
          return const SizedBox(
            height: 220,
            child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
          );
        }
        if (snap.hasError) {
          return _errorCard('Sweep summary failed: ${snap.error}');
        }
        final rows = snap.data ?? const <Map<String, dynamic>>[];
        if (rows.length < 2) {
          // A scatter needs at least two points to convey anything —
          // single-run "sweeps" fall back to a neutral hint.
          return _emptyCard(rows.length);
        }
        return _buildChart(context, rows);
      },
    );
  }

  Widget _buildChart(BuildContext context, List<Map<String, dynamic>> rows) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    // Parse each row into a typed shape once; the rest of the widget
    // works off the parsed list.
    final parsed = rows.map(_SweepPoint.parse).toList(growable: false);

    // Collect axis candidates.
    // Numeric = plottable on an axis (config number OR a final metric).
    // Categorical = usable as color key (string-valued config entry).
    final numericKeys = <String>{};
    final categoricalKeys = <String>{};
    for (final p in parsed) {
      p.numericFields.forEach((k, _) => numericKeys.add(k));
      p.categoricalFields.forEach((k, _) => categoricalKeys.add(k));
    }
    if (numericKeys.length < 2) {
      // Not enough usable axes — one-parameter sweep or degenerate data.
      return _emptyCard(rows.length, reason: 'Need ≥2 numeric fields');
    }

    // Pick reasonable defaults the first time we render. Prefer a final
    // metric for Y ("what did it achieve"), a config dim for X ("what
    // was varied"). If either heuristic fails, fall back to sorted keys.
    final numericSorted = numericKeys.toList()..sort();
    final metricKeys =
        numericSorted.where((k) => k.contains('/') || k.contains('_')).toList();
    final configKeys =
        numericSorted.where((k) => !metricKeys.contains(k)).toList();
    _xAxis ??= configKeys.isNotEmpty ? configKeys.first : numericSorted.first;
    _yAxis ??= _preferMetric(metricKeys, const [
      'loss/val',
      'eval/accuracy',
      'eval/perplexity',
    ]) ??
        numericSorted.firstWhere((k) => k != _xAxis,
            orElse: () => numericSorted.last);
    if (categoricalKeys.isNotEmpty) {
      _colorBy ??= categoricalKeys.contains('optimizer')
          ? 'optimizer'
          : categoricalKeys.first;
    }

    // Build the plottable series: list of (x, y, category, label).
    final pts = <_ChartPoint>[];
    for (final p in parsed) {
      final x = p.numericFields[_xAxis!];
      final y = p.numericFields[_yAxis!];
      if (x == null || y == null) continue;
      final cat = _colorBy == null ? '' : (p.categoricalFields[_colorBy!] ?? '');
      pts.add(_ChartPoint(
        x: x,
        y: y,
        category: cat,
        label: _shortId(p.runId),
      ));
    }
    if (pts.length < 2) {
      return _emptyCard(rows.length, reason: 'Axes missing on runs');
    }

    // Assign one color per distinct category, in order of first
    // appearance so legend ordering is deterministic.
    final catOrder = <String>[];
    for (final p in pts) {
      if (!catOrder.contains(p.category)) catOrder.add(p.category);
    }
    final catColors = <String, Color>{};
    for (var i = 0; i < catOrder.length; i++) {
      catColors[catOrder[i]] = _categoryColor(i);
    }

    final border = isDark ? DesignColors.borderDark : DesignColors.borderLight;
    final surface =
        isDark ? DesignColors.surfaceDark : DesignColors.surfaceLight;

    return Material(
      color: surface,
      shape: RoundedRectangleBorder(
        side: BorderSide(color: border),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 12, 12, 10),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.scatter_plot_outlined,
                    size: 16, color: DesignColors.primary),
                const SizedBox(width: 6),
                Text(
                  'Sweep compare',
                  style: GoogleFonts.spaceGrotesk(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 0.4,
                  ),
                ),
                const Spacer(),
                Text(
                  '${pts.length} runs',
                  style: GoogleFonts.jetBrainsMono(
                    fontSize: 10,
                    color: DesignColors.textMuted,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            SizedBox(
              height: 180,
              child: CustomPaint(
                painter: _ScatterPainter(
                  points: pts,
                  colorFor: (c) =>
                      catColors[c] ?? DesignColors.textSecondary,
                  axisColor: isDark
                      ? DesignColors.textMuted
                      : DesignColors.textMutedLight,
                  labelColor: isDark
                      ? DesignColors.textSecondary
                      : DesignColors.textSecondaryLight,
                ),
                child: const SizedBox.expand(),
              ),
            ),
            const SizedBox(height: 8),
            // Axis pickers. Kept as a Wrap so short-labelled data fits
            // one line, long labels gracefully wrap.
            Wrap(
              spacing: 8,
              runSpacing: 4,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                _axisPill(
                  label: 'X',
                  value: _xAxis!,
                  options: numericSorted,
                  onChanged: (v) => setState(() => _xAxis = v),
                ),
                _axisPill(
                  label: 'Y',
                  value: _yAxis!,
                  options: numericSorted,
                  onChanged: (v) => setState(() => _yAxis = v),
                ),
                if (categoricalKeys.isNotEmpty)
                  _axisPill(
                    label: 'color',
                    value: _colorBy ?? categoricalKeys.first,
                    options: categoricalKeys.toList()..sort(),
                    onChanged: (v) => setState(() => _colorBy = v),
                  ),
              ],
            ),
            if (catOrder.length > 1 &&
                !(catOrder.length == 1 && catOrder.first.isEmpty)) ...[
              const SizedBox(height: 8),
              Wrap(
                spacing: 10,
                runSpacing: 4,
                children: [
                  for (final cat in catOrder)
                    if (cat.isNotEmpty)
                      Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Container(
                            width: 8,
                            height: 8,
                            decoration: BoxDecoration(
                              color: catColors[cat],
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 4),
                          Text(
                            cat,
                            style: GoogleFonts.jetBrainsMono(
                              fontSize: 10,
                              color: DesignColors.textMuted,
                            ),
                          ),
                        ],
                      ),
                ],
              ),
            ],
          ],
        ),
      ),
    );
  }

  Widget _axisPill({
    required String label,
    required String value,
    required List<String> options,
    required ValueChanged<String> onChanged,
  }) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '$label:',
          style: GoogleFonts.jetBrainsMono(
            fontSize: 10,
            color: DesignColors.textMuted,
          ),
        ),
        const SizedBox(width: 4),
        DropdownButton<String>(
          value: options.contains(value) ? value : options.first,
          isDense: true,
          underline: const SizedBox.shrink(),
          style: GoogleFonts.jetBrainsMono(
            fontSize: 11,
            color: DesignColors.primary,
          ),
          items: [
            for (final o in options)
              DropdownMenuItem(value: o, child: Text(o)),
          ],
          onChanged: (v) {
            if (v != null) onChanged(v);
          },
        ),
      ],
    );
  }

  Widget _emptyCard(int runCount, {String? reason}) {
    final msg = runCount == 0
        ? 'No runs in this project yet'
        : runCount == 1
            ? 'Only 1 run — sweep compare needs ≥2'
            : (reason ?? 'Not enough axis candidates');
    return _hintCard(Icons.scatter_plot_outlined, msg);
  }

  Widget _errorCard(String msg) =>
      _hintCard(Icons.error_outline, msg, danger: true);

  Widget _hintCard(IconData icon, String msg, {bool danger = false}) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Icon(icon,
              size: 16,
              color: danger ? DesignColors.error : DesignColors.textMuted),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              msg,
              style: GoogleFonts.jetBrainsMono(
                fontSize: 11,
                color: DesignColors.textMuted,
              ),
            ),
          ),
        ],
      ),
    );
  }

  String? _preferMetric(List<String> keys, List<String> preferred) {
    for (final p in preferred) {
      if (keys.contains(p)) return p;
    }
    return null;
  }

  static String _shortId(String id) =>
      id.length <= 8 ? id : id.substring(0, 8);

  static Color _categoryColor(int i) {
    // Small deterministic palette; cycles once exhausted. Hand-picked
    // so adjacent categories remain distinguishable in both light and
    // dark themes.
    const palette = [
      Color(0xFF4F8EF7), // blue
      Color(0xFFF07B4F), // orange
      Color(0xFF5FBF6E), // green
      Color(0xFFB66FD4), // violet
      Color(0xFFE3C04A), // gold
      Color(0xFF54B9C2), // teal
    ];
    return palette[i % palette.length];
  }
}

/// One parsed sweep-summary row: run id + flattened numeric/categorical
/// fields from config_json + final_metrics.
class _SweepPoint {
  final String runId;
  final Map<String, double> numericFields;
  final Map<String, String> categoricalFields;

  _SweepPoint({
    required this.runId,
    required this.numericFields,
    required this.categoricalFields,
  });

  static _SweepPoint parse(Map<String, dynamic> row) {
    final runId = (row['run_id'] ?? '').toString();
    final numeric = <String, double>{};
    final categorical = <String, String>{};

    // Metrics — already numeric at the boundary.
    final metrics = row['final_metrics'];
    if (metrics is Map) {
      metrics.forEach((k, v) {
        if (v is num) numeric[k.toString()] = v.toDouble();
      });
    }

    // Config — parse config_json string if present, then split each
    // key by type. num → numeric axis; string → categorical color.
    // Bools/nested objects skipped (not useful on a 2D scatter).
    final configRaw = row['config_json'];
    if (configRaw is String && configRaw.isNotEmpty) {
      try {
        final parsed = jsonDecode(configRaw);
        if (parsed is Map) {
          parsed.forEach((k, v) {
            final key = k.toString();
            if (v is num) {
              numeric[key] = v.toDouble();
            } else if (v is String) {
              categorical[key] = v;
            }
          });
        }
      } catch (_) {
        // Non-JSON config_json — leave both maps alone; the row
        // simply won't contribute axis candidates.
      }
    }

    return _SweepPoint(
      runId: runId,
      numericFields: numeric,
      categoricalFields: categorical,
    );
  }
}

class _ChartPoint {
  final double x;
  final double y;
  final String category;
  final String label;
  _ChartPoint({
    required this.x,
    required this.y,
    required this.category,
    required this.label,
  });
}

class _ScatterPainter extends CustomPainter {
  final List<_ChartPoint> points;
  final Color Function(String category) colorFor;
  final Color axisColor;
  final Color labelColor;

  _ScatterPainter({
    required this.points,
    required this.colorFor,
    required this.axisColor,
    required this.labelColor,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (points.isEmpty) return;

    const padLeft = 28.0;
    const padRight = 8.0;
    const padTop = 8.0;
    const padBottom = 22.0;
    final plotW = size.width - padLeft - padRight;
    final plotH = size.height - padTop - padBottom;

    double minX = points.first.x, maxX = points.first.x;
    double minY = points.first.y, maxY = points.first.y;
    for (final p in points) {
      minX = math.min(minX, p.x);
      maxX = math.max(maxX, p.x);
      minY = math.min(minY, p.y);
      maxY = math.max(maxY, p.y);
    }
    // Add ~6% padding on each axis so markers don't hug the frame.
    final xPad = (maxX - minX).abs() < 1e-12 ? 1.0 : (maxX - minX) * 0.08;
    final yPad = (maxY - minY).abs() < 1e-12 ? 1.0 : (maxY - minY) * 0.08;
    minX -= xPad;
    maxX += xPad;
    minY -= yPad;
    maxY += yPad;
    final dx = maxX - minX;
    final dy = maxY - minY;

    // Axes.
    final axisPaint = Paint()
      ..color = axisColor.withValues(alpha: 0.4)
      ..strokeWidth = 1.0;
    canvas.drawLine(
      Offset(padLeft, padTop + plotH),
      Offset(padLeft + plotW, padTop + plotH),
      axisPaint,
    );
    canvas.drawLine(
      Offset(padLeft, padTop),
      Offset(padLeft, padTop + plotH),
      axisPaint,
    );

    // Axis tick labels (min + max on each side). Keep lightweight;
    // real charting is out of scope for a sparkline-adjacent widget.
    _drawText(canvas, _fmt(minX),
        Offset(padLeft, padTop + plotH + 4), labelColor);
    _drawText(canvas, _fmt(maxX),
        Offset(padLeft + plotW, padTop + plotH + 4), labelColor,
        alignRight: true);
    _drawText(canvas, _fmt(maxY), Offset(padLeft - 4, padTop), labelColor,
        alignRight: true);
    _drawText(canvas, _fmt(minY),
        Offset(padLeft - 4, padTop + plotH - 10), labelColor,
        alignRight: true);

    // Points.
    for (final p in points) {
      final px = padLeft + (p.x - minX) / dx * plotW;
      final py = padTop + plotH - (p.y - minY) / dy * plotH;
      final color = colorFor(p.category);
      canvas.drawCircle(
        Offset(px, py),
        4.0,
        Paint()..color = color.withValues(alpha: 0.25),
      );
      canvas.drawCircle(Offset(px, py), 2.6, Paint()..color = color);
    }
  }

  void _drawText(Canvas canvas, String text, Offset at, Color color,
      {bool alignRight = false}) {
    final tp = TextPainter(
      text: TextSpan(
        text: text,
        style: TextStyle(
          fontSize: 9,
          color: color.withValues(alpha: 0.85),
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
  bool shouldRepaint(covariant _ScatterPainter old) =>
      old.points != points || old.axisColor != axisColor;
}
