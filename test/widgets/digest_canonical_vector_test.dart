// The Dart half of the ADR-038 shared canonical-error contract. Reads the
// SAME fixture as the Go test (hub/internal/server/digest_fold_test.go) and
// asserts the mobile classifier (agentEventCanonicalErrorClass /
// agentRunCanonicalErrorCount) agrees with the hub on what counts as an error
// in a run. If you change the canonical union on one side, this test (or its
// Go twin) fails until both sides + the vector match again.
//
// The vector lives under hub/ so there is a single source of truth; the
// flutter test runner's cwd is the repo root, so the relative path resolves.

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/live_feed.dart';

void main() {
  test('canonical error classification matches the shared hub vector', () {
    final file =
        File('hub/internal/server/testdata/digest_canonical_vector.json');
    expect(file.existsSync(), isTrue,
        reason: 'shared vector not found at ${file.path} (cwd ${Directory.current.path})');

    final vector = jsonDecode(file.readAsStringSync()) as Map<String, dynamic>;
    final rawEvents = (vector['events'] as List).cast<Map<String, dynamic>>();
    final expected = vector['expected'] as Map<String, dynamic>;

    // Total canonical errors == the digest's error_count.
    final total = agentRunCanonicalErrorCount(rawEvents);
    expect(total, expected['error_count'],
        reason: 'total canonical errors must equal digest error_count');

    // Per-class taxonomy == expected.errors.
    final byClass = <String, int>{};
    for (final e in rawEvents) {
      final cls = agentEventCanonicalErrorClass(e);
      if (cls != null) byClass[cls] = (byClass[cls] ?? 0) + 1;
    }
    final expectedErrors =
        (expected['errors'] as Map).cast<String, dynamic>();
    expectedErrors.forEach((cls, count) {
      expect(byClass[cls], count, reason: 'errors[$cls] mismatch');
    });
    expect(byClass.length, expectedErrors.length,
        reason: 'error classes must match exactly (no extras/missing)');
  });
}
