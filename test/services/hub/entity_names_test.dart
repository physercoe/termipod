import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/hub/entity_names.dart';

void main() {
  group('projectNameFor', () {
    final projects = [
      {'id': 'p1', 'name': 'lab-ops'},
      {'id': 'p2', 'name': 'ablation-sweep-demo'},
      {'id': 'p3', 'name': ''},
    ];

    test('returns name when id matches', () {
      expect(projectNameFor('p1', projects), 'lab-ops');
    });

    test('falls back to id when not found', () {
      expect(projectNameFor('ghost', projects), 'ghost');
    });

    test('uses custom fallback when provided and missing', () {
      expect(projectNameFor('ghost', projects, fallback: '(unknown)'),
          '(unknown)');
    });

    test('falls back to id when matched row has empty name', () {
      expect(projectNameFor('p3', projects), 'p3');
    });

    test('empty id returns fallback', () {
      expect(projectNameFor('', projects, fallback: 'none'), 'none');
    });
  });

  group('agentHandleFor', () {
    final agents = [
      {'id': '01k-steward', 'handle': 'steward'},
      {'id': '01k-trainer', 'handle': 'trainer-0'},
    ];

    test('returns handle for matching id', () {
      expect(agentHandleFor('01k-steward', agents), 'steward');
    });

    test('falls back to id when no match', () {
      expect(agentHandleFor('01k-missing', agents), '01k-missing');
    });
  });

  group('runLabelFor', () {
    test('uses row[name] when present', () {
      expect(runLabelFor({'name': 'my-run', 'id': 'r1'}), 'my-run');
    });

    test('composes from ablation config (n_embd + optimizer)', () {
      expect(
        runLabelFor({
          'id': 'r1',
          'config_json': '{"n_embd":128,"optimizer":"adamw"}',
        }),
        'n_embd=128 · adamw',
      );
    });

    test('accepts pre-decoded map', () {
      expect(
        runLabelFor({
          'id': 'r2',
          'config_json': {'n_embd': 256, 'optimizer': 'lion'},
        }),
        'n_embd=256 · lion',
      );
    });

    test('falls back to generic kv when ablation keys missing', () {
      final out = runLabelFor({
        'id': 'r3',
        'config_json': '{"lr":0.001,"batch_size":32}',
      });
      expect(out, contains('lr=0.001'));
    });

    test('falls back to trailing id when config empty', () {
      expect(runLabelFor({'id': '01k12345abcdef'}), endsWith('abcdef'));
    });

    test('handles empty row', () {
      expect(runLabelFor({}), '(run)');
    });
  });
}
