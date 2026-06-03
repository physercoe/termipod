import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/live_feed.dart';

// Tests for the envelope `from:` label resolver (v1.0.710 fix to
// ADR-032 D-10). Background: v1.0.708 made the engine-facing prose
// configurable via `hub/templates/envelope/active.yaml` but left a
// parallel hardcoded Dart map in `live_feed.dart:_envelopeRoleLabel`
// rendering the mobile transcript header. Editing
// `roles.principal: "the director"` reached the engine but the
// mobile feed stayed "from: the principal". The hub now stamps
// `payload.from_label` from the same template; mobile prefers it.
//
// These tests pin:
//   - the precedence (`from_label` over the static map)
//   - the static fallbacks for legacy / sparse payloads
//   - shape stability for the test that holds the agent_feed render
//     contract: an empty `from_label` doesn't accidentally win over
//     the static fallback.
void main() {
  group('envelopeSenderLabel — payload-driven precedence', () {
    test('from_label wins over the static map when present', () {
      // Operator edited roles.principal → "the director". Hub stamps
      // the override. Mobile renders it verbatim.
      final got = envelopeSenderLabel(
        role: 'principal',
        handle: '',
        fromLabel: 'the director',
      );
      expect(got, 'the director');
    });

    test('peer_steward override carries the operator-rendered handle', () {
      // The YAML's peer_steward template renders to e.g.
      // "@research (a peer steward)". The hub-stamped string
      // already contains the @handle; mobile uses it as-is.
      final got = envelopeSenderLabel(
        role: 'peer_steward',
        handle: 'research',
        fromLabel: '@research (a peer steward)',
      );
      expect(got, '@research (a peer steward)');
    });

    test('whitespace-only from_label is treated as absent', () {
      // Defensive: a buggy hub render (or a malformed legacy row)
      // that stamps "   " must not eclipse the static fallback.
      final got = envelopeSenderLabel(
        role: 'principal',
        handle: '',
        fromLabel: '   ',
      );
      expect(got, 'the principal');
    });
  });

  group('envelopeSenderLabel — static fallback for legacy events', () {
    test('principal with no handle and no from_label → "the principal"', () {
      final got = envelopeSenderLabel(role: 'principal', handle: '');
      expect(got, 'the principal');
    });

    test('peer_steward with handle composes "@h (peer steward)"', () {
      // Pre-v1.0.710 events have no from_label. Mobile composes the
      // string client-side, matching the wording the YAML's
      // peer_steward template ships with by default (modulo the
      // article "a" — the static map omits it; the YAML adds it).
      final got = envelopeSenderLabel(
        role: 'peer_steward',
        handle: 'research',
      );
      expect(got, '@research (peer steward)');
    });

    test('unknown role falls through to the bare role string', () {
      // The static map's default branch returns the role verbatim.
      // Forward-compat hook: a future role added in the YAML still
      // renders something legible until mobile catches up.
      final got = envelopeSenderLabel(role: 'observer', handle: '');
      expect(got, 'observer');
    });

    test('system role with no handle → "the system"', () {
      final got = envelopeSenderLabel(role: 'system', handle: '');
      expect(got, 'the system');
    });
  });

  group('envelopeRoleLabel — static map shape', () {
    // The static map is the safety net under envelopeSenderLabel
    // when from_label is absent. Pinning the four documented role
    // → label entries guards against an accidental rename that
    // would leave the legacy-events fallback misaligned with the
    // YAML's default content.
    test('principal', () => expect(envelopeRoleLabel('principal'), 'the principal'));
    test('system', () => expect(envelopeRoleLabel('system'), 'the system'));
    test('peer_steward',
        () => expect(envelopeRoleLabel('peer_steward'), 'peer steward'));
    test('peer_worker',
        () => expect(envelopeRoleLabel('peer_worker'), 'peer worker'));
    test('unknown passes through',
        () => expect(envelopeRoleLabel('observer'), 'observer'));
  });
}
