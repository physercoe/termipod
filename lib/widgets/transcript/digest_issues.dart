/// Reader for the digest's structural-issues aggregation (transcript P5 §2 —
/// `hub/internal/server/digest_issues.go`). The hub folds the findings once and
/// serves them to both clients; this turns the wire map into the shape the
/// Issues sheet renders, and nothing more.
///
/// Pure and l10n-free on purpose: class keys stay keys, the widget resolves the
/// display strings. That makes it directly unit-testable — which matters more
/// than usual here, because this file's whole job is to hold the two rules that
/// must NOT be re-derived per call site:
///
///  * the severity ordering, which must match the hub's `issueSeverityRank` and
///    the desktop reader's (`desktop/src/state/digestIssues.ts`);
///  * the seek anchor — `sample_ordinals[i]` when > 0, else `sample_seqs[i]`.
///    `seq` COLLIDES across a resumed session's agents (ADR-042), so picking the
///    wrong one lands on the wrong row in exactly the case the sheet is most
///    useful. Same rule `_firstErrorSeq` and the turn rows already follow.
///
/// This family has shipped four mobile↔desktop misses (`callToolIdOf` ×3, the
/// stats strip #375); a shared, tested shape on each side is the answer.
library;

/// Severity ranks, worst first. Pinned to the hub's table.
const Map<String, int> _severityRank = {
  'error': 3,
  'warning': 2,
  'info': 1,
};

/// One sampled finding: where to seek, when it happened, what to call it.
class IssueSample {
  /// Transcript coordinate — the dense `session_ordinal` when the agent has
  /// one, else the per-agent `seq`. 0 when neither is usable.
  final int coord;
  final String ts;

  /// Short headline — the failing tool's name, the stop reason, the
  /// `kind.key` that changed shape. Empty when the class label carries it all.
  final String label;

  const IssueSample({required this.coord, this.ts = '', this.label = ''});
}

/// One issue class with its samples.
class IssueClassGroup {
  final String cls;
  final String severity;
  final int count;
  final List<IssueSample> samples;

  const IssueClassGroup({
    required this.cls,
    required this.severity,
    required this.count,
    required this.samples,
  });

  /// True when the sample list is a prefix of `count` findings (the hub caps
  /// them at `maxDigestErrorSeqs`). Surfaced in the sheet — a truncated list
  /// must never read as a complete one.
  bool get capped => count > samples.length;

  int get rank => _severityRank[severity] ?? 0;
}

/// The whole-run rollup the stat chip and the sheet read.
class IssueSummary {
  final int total;

  /// Worst severity present, or '' when there are none.
  final String worst;

  /// Classes ordered severity-first, then loudest, then alphabetical.
  final List<IssueClassGroup> classes;

  const IssueSummary({
    required this.total,
    required this.worst,
    required this.classes,
  });

  bool get isEmpty => classes.isEmpty;
}

int _asInt(dynamic v) {
  if (v is int) return v;
  if (v is double) return v.toInt();
  if (v is String) return int.tryParse(v) ?? 0;
  return 0;
}

String _asString(dynamic v) => v == null ? '' : v.toString();

String _severityOf(dynamic v) {
  final s = _asString(v);
  // An unknown severity degrades to the middle rather than dropping the row —
  // a rule added hub-side must never vanish from the client that has not
  // learned its name yet.
  return _severityRank.containsKey(s) ? s : 'warning';
}

/// Reads `issues` / `issue_worst_severity` off a digest body.
///
/// A hub that predates the aggregation carries no `issues` key at all and
/// yields an empty summary, so the sheet simply never offers itself — rather
/// than reporting a clean bill of health for a hub that never ran the checks.
IssueSummary readDigestIssues(Map<String, dynamic>? digest) {
  final raw = digest?['issues'];
  if (raw is! Map) return const IssueSummary(total: 0, worst: '', classes: []);

  final classes = <IssueClassGroup>[];
  for (final entry in raw.entries) {
    final v = entry.value;
    if (v is! Map) continue;
    final count = _asInt(v['count']);
    if (count <= 0) continue;
    final seqs = v['sample_seqs'];
    final ords = v['sample_ordinals'];
    final tss = v['sample_ts'];
    final labels = v['sample_labels'];
    final samples = <IssueSample>[];
    if (seqs is List) {
      for (var i = 0; i < seqs.length; i++) {
        final ord = (ords is List && i < ords.length) ? _asInt(ords[i]) : 0;
        final seq = _asInt(seqs[i]);
        samples.add(IssueSample(
          coord: ord > 0 ? ord : seq,
          ts: (tss is List && i < tss.length) ? _asString(tss[i]) : '',
          label: (labels is List && i < labels.length) ? _asString(labels[i]) : '',
        ));
      }
    }
    classes.add(IssueClassGroup(
      cls: entry.key.toString(),
      severity: _severityOf(v['severity']),
      count: count,
      samples: samples,
    ));
  }

  classes.sort((a, b) {
    final bySev = b.rank.compareTo(a.rank);
    if (bySev != 0) return bySev;
    final byCount = b.count.compareTo(a.count);
    if (byCount != 0) return byCount;
    return a.cls.compareTo(b.cls);
  });

  var total = 0;
  for (final c in classes) {
    total += c.count;
  }
  // Prefer the hub's own rollup: it ranks with the server's table, so a
  // severity this client cannot rank yet still tints correctly. Fall back to
  // the computed worst for a hub that predates the field.
  final wire = _asString(digest?['issue_worst_severity']);
  final worst = wire.isNotEmpty
      ? _severityOf(wire)
      : (classes.isEmpty ? '' : classes.first.severity);
  return IssueSummary(total: total, worst: worst, classes: classes);
}
