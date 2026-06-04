import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/screens/admin/admin_teams_controller.dart';

// Pins the Admin → Teams ordering seam: active team first, then
// case-insensitive by name with an id fallback, without mutating input.

Map<String, dynamic> team(String id, {String name = ''}) =>
    {'id': id, 'name': name, 'created_at': ''};

void main() {
  group('sortTeamsForDisplay', () {
    test('active team floats to the top', () {
      final out = sortTeamsForDisplay(
        [team('acme', name: 'Acme'), team('default', name: 'Default')],
        'default',
      );
      expect(out.first['id'], 'default');
    });

    test('non-active teams sort case-insensitively by name', () {
      final out = sortTeamsForDisplay(
        [team('z', name: 'zeta'), team('a', name: 'Alpha'), team('m', name: 'Mu')],
        'none',
      );
      expect(out.map((t) => t['id']), ['a', 'm', 'z']);
    });

    test('falls back to id when a team has no name', () {
      final out = sortTeamsForDisplay(
        [team('beta'), team('alpha')],
        'none',
      );
      expect(out.map((t) => t['id']), ['alpha', 'beta']);
    });

    test('does not mutate the caller list', () {
      final input = [team('b', name: 'B'), team('a', name: 'A')];
      sortTeamsForDisplay(input, 'none');
      expect(input.map((t) => t['id']), ['b', 'a']); // unchanged
    });
  });

  group('isActiveTeam', () {
    test('matches on id', () {
      expect(isActiveTeam(team('acme'), 'acme'), isTrue);
      expect(isActiveTeam(team('acme'), 'other'), isFalse);
    });
  });
}
