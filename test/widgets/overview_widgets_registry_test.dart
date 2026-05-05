import 'package:flutter_test/flutter_test.dart';

import 'package:termipod/screens/projects/overview_widgets/registry.dart';

void main() {
  group('overview_widgets registry', () {
    test('research-template slugs are recognised (W7)', () {
      const research = [
        'portfolio_header',
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

    test('legacy slugs still recognised', () {
      const legacy = [
        'task_milestone_list',
        'sweep_compare',
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
