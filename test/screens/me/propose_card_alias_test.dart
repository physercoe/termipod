import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/me/widgets/propose_card_agent_spawn.dart';
import 'package:termipod/screens/me/widgets/propose_card_template_install.dart';

import '../../helpers/test_helpers.dart';

// ADR-030 W18 — per-kind propose cards for the two alias kinds:
// agent.spawn (re-routed legacy approval_request+spawnIn) and
// template.install (re-routed legacy template_proposal).
//
// Both cards intentionally surface a compact summary and punt the
// full payload (spawn_spec_yaml / template body blob) to the Details
// affordance — keeps the Me-page card lightweight while preserving
// access to the legacy preview screens for full inspection.

Map<String, dynamic> _agentSpawnRow({
  required String assignedTier,
  String escalationState = 'none',
}) {
  return {
    'id': 'att-w18-spawn',
    'kind': 'propose',
    'change_kind': 'agent.spawn',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      'child_handle': '@worker.coder-x',
      'kind': 'claude-code',
      'host_id': 'host-vps-1',
      'project_id': 'proj-research-2',
      'spawn_spec_yaml': '...',
    },
    'summary': 'Propose agent.spawn — coder for paper draft',
  };
}

Map<String, dynamic> _templateInstallRow({
  required String assignedTier,
  String escalationState = 'none',
  String? rationale = 'add a new lit-reviewer variant',
}) {
  return {
    'id': 'att-w18-template',
    'kind': 'propose',
    'change_kind': 'template.install',
    'assigned_tier': assignedTier,
    'escalation_state': escalationState,
    'change_spec': {
      'category': 'agents',
      'name': 'lit-reviewer.v2.yaml',
      'blob_sha256':
          'abcdef0123456789fedcba9876543210abcdef0123456789fedcba9876543210',
      if (rationale != null) 'rationale': rationale,
      'proposed_by': '@steward.research',
    },
    'summary': 'Propose template.install — lit-reviewer v2',
  };
}

Widget _wrap(Widget child) {
  return ProviderScope(
    child: MaterialApp(
      localizationsDelegates: testLocalizationsDelegates,
      supportedLocales: testSupportedLocales,
      home: Scaffold(body: child),
    ),
  );
}

void main() {
  group('ProposeCardAgentSpawn', () {
    testWidgets('primary variant — header shows handle + engine chip',
        (tester) async {
      final row = _agentSpawnRow(assignedTier: 'principal');
      await tester.pumpWidget(
          _wrap(ProposeCardAgentSpawn(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('@worker.coder-x'), findsOneWidget);
      expect(find.text('claude-code'), findsOneWidget);
      expect(find.text('host: host-vps-1'), findsOneWidget);
      expect(find.text('project: proj-research-2'), findsOneWidget);
      expect(find.text('Approve'), findsOneWidget);
    });

    testWidgets('stalled variant — shows Override + View spawn detail',
        (tester) async {
      final row = _agentSpawnRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(
          _wrap(ProposeCardAgentSpawn(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View spawn detail'), findsOneWidget);
      expect(find.textContaining('Stuck'), findsOneWidget);
    });

    testWidgets('header tolerates missing child_handle', (tester) async {
      final row = _agentSpawnRow(assignedTier: 'principal');
      (row['change_spec'] as Map).remove('child_handle');
      await tester.pumpWidget(
          _wrap(ProposeCardAgentSpawn(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('(no handle)'), findsOneWidget);
    });
  });

  group('ProposeCardTemplateInstall', () {
    testWidgets('primary variant — header shows category/name path',
        (tester) async {
      final row = _templateInstallRow(assignedTier: 'principal');
      await tester.pumpWidget(_wrap(
          ProposeCardTemplateInstall(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('agents/lit-reviewer.v2.yaml'), findsOneWidget);
      expect(find.text('add a new lit-reviewer variant'), findsOneWidget);
      expect(find.text('proposed by: @steward.research'), findsOneWidget);
      // sha rendered as 12-char prefix only.
      expect(find.text('sha256: abcdef012345'), findsOneWidget);
      expect(find.text('Approve'), findsOneWidget);
    });

    testWidgets('stalled variant — shows Override + View template body',
        (tester) async {
      final row = _templateInstallRow(
        assignedTier: 'project-steward',
        escalationState: 'escalated_principal',
      );
      await tester.pumpWidget(_wrap(
          ProposeCardTemplateInstall(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('Override'), findsOneWidget);
      expect(find.text('View template body'), findsOneWidget);
    });

    testWidgets('missing category or name renders "(unknown)"',
        (tester) async {
      final row = _templateInstallRow(assignedTier: 'principal');
      (row['change_spec'] as Map).remove('name');
      await tester.pumpWidget(_wrap(
          ProposeCardTemplateInstall(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('(unknown)'), findsOneWidget);
    });

    testWidgets('rationale omitted when not present', (tester) async {
      final row = _templateInstallRow(
        assignedTier: 'principal',
        rationale: null,
      );
      await tester.pumpWidget(_wrap(
          ProposeCardTemplateInstall(attention: row, myTier: 'principal')));
      await tester.pumpAndSettle();

      expect(find.text('add a new lit-reviewer variant'), findsNothing);
      // sha + proposed-by + path still rendered.
      expect(find.text('agents/lit-reviewer.v2.yaml'), findsOneWidget);
    });
  });
}
