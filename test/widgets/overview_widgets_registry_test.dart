import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/screens/projects/overview_widgets/registry.dart';

void main() {
  group('overview_widgets registry', () {
    test('research-template slugs are recognised (W7)', () {
      // `portfolio_header` was retired in v1.0.501 — chassis-A header
      // already covers what it pointed at, so it falls through to the
      // chassis default rather than rendering an explanatory paragraph.
      const research = [
        'idea_conversation',
        'deliverable_focus',
        'experiment_dash',
        'paper_acceptance',
      ];
      for (final slug in research) {
        expect(kKnownOverviewWidgets.contains(slug), isTrue,
            reason: 'expected $slug in kKnownOverviewWidgets');
        expect(normalizeOverviewWidget(slug), slug,
            reason: 'normalize should pass $slug through unchanged');
      }
    });

    test('retired portfolio_header slug normalises to default', () {
      // Regression guard: keep portfolio_header from sneaking back in.
      expect(kKnownOverviewWidgets.contains('portfolio_header'), isFalse);
      expect(normalizeOverviewWidget('portfolio_header'),
          kDefaultOverviewWidget);
    });

    test('retired sweep_compare slug normalises to default', () {
      // Regression guard: kept out after v1.0.506 retirement (the
      // multi-series metric-chart embedded by experiment_dash now
      // subsumes the cross-run scatter use case).
      expect(kKnownOverviewWidgets.contains('sweep_compare'), isFalse);
      expect(normalizeOverviewWidget('sweep_compare'),
          kDefaultOverviewWidget);
    });

    test('legacy slugs still recognised', () {
      const legacy = [
        'task_milestone_list',
        'recent_artifacts',
        'children_status',
        'recent_firings_list',
      ];
      for (final slug in legacy) {
        expect(kKnownOverviewWidgets.contains(slug), isTrue);
        expect(normalizeOverviewWidget(slug), slug);
      }
    });

    test('unknown slug normalises to default', () {
      expect(
        normalizeOverviewWidget('mystery_slug'),
        kDefaultOverviewWidget,
      );
      expect(normalizeOverviewWidget(''), kDefaultOverviewWidget);
      expect(normalizeOverviewWidget(null), kDefaultOverviewWidget);
    });
  });
}
