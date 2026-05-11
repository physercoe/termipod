import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/widgets/image_attach/composer_image_attach.dart';

// Tests for ADR-021 W4.6 — the image-attach affordance gate.
// resolveCanAttachImages joins an agent's `kind` + `driving_mode`
// against the family registry's `prompt_image[mode]` flag. It's the
// load-bearing primitive that decides whether the composer renders
// the attach button; getting the gate wrong either hides a working
// path (gemini M1, claude M2, codex M2) or surfaces a misleading
// affordance on engines that strip images downstream (gemini M2).

void main() {
  // Mirrors the merged registry shape (handlers_agent_families.go's
  // /agent-families response) for the three families we ship today.
  // Per agent_families.yaml: claude/codex have prompt_image on M1+M2,
  // gemini-cli on M1 only.
  final families = [
    {
      'family': 'claude-code',
      'prompt_image': {'M1': true, 'M2': true, 'M4': false},
    },
    {
      'family': 'codex',
      'prompt_image': {'M1': true, 'M2': true, 'M4': false},
    },
    {
      'family': 'gemini-cli',
      'prompt_image': {'M1': true, 'M2': false, 'M4': false},
    },
  ];

  group('resolveCanAttachImages', () {
    test('claude-code on M2 → true (stream-json content array)', () {
      expect(
        resolveCanAttachImages(
          kind: 'claude-code',
          drivingMode: 'M2',
          families: families,
        ),
        isTrue,
      );
    });

    test('codex on M2 → true (app-server input array)', () {
      expect(
        resolveCanAttachImages(
          kind: 'codex',
          drivingMode: 'M2',
          families: families,
        ),
        isTrue,
      );
    });

    test('gemini-cli on M1 → true (ACP)', () {
      expect(
        resolveCanAttachImages(
          kind: 'gemini-cli',
          drivingMode: 'M1',
          families: families,
        ),
        isTrue,
      );
    });

    test('gemini-cli on M2 → false (exec-per-turn argv strips)', () {
      // The driver-side W4.5 strip-and-warn is a fallback. The
      // composer must not invite the user to send images that the
      // engine path will silently drop — affordance off.
      expect(
        resolveCanAttachImages(
          kind: 'gemini-cli',
          drivingMode: 'M2',
          families: families,
        ),
        isFalse,
      );
    });

    test('M4 (tmux pane) → false for every family', () {
      for (final fam in ['claude-code', 'codex', 'gemini-cli']) {
        expect(
          resolveCanAttachImages(
            kind: fam,
            drivingMode: 'M4',
            families: families,
          ),
          isFalse,
          reason: '$fam M4 should not surface attach',
        );
      }
    });

    test('unknown family → false (safe default)', () {
      expect(
        resolveCanAttachImages(
          kind: 'no-such-engine',
          drivingMode: 'M1',
          families: families,
        ),
        isFalse,
      );
    });

    test('empty kind → false', () {
      expect(
        resolveCanAttachImages(
          kind: '',
          drivingMode: 'M2',
          families: families,
        ),
        isFalse,
      );
    });

    test('null kind → false', () {
      expect(
        resolveCanAttachImages(
          kind: null,
          drivingMode: 'M2',
          families: families,
        ),
        isFalse,
      );
    });

    test('null driving_mode → defaults to M4 (tmux fallback) → false', () {
      // When the agent row has no driving_mode (very-early-spawn or
      // hand-crafted insert), the resolver treats it as M4. That's
      // the safest default — no engine path supports attach there.
      expect(
        resolveCanAttachImages(
          kind: 'claude-code',
          drivingMode: null,
          families: families,
        ),
        isFalse,
      );
    });

    test('family with no prompt_image map → false', () {
      final partial = [
        {'family': 'pre-W4.6', 'supports': ['M1', 'M2']},
      ];
      expect(
        resolveCanAttachImages(
          kind: 'pre-W4.6',
          drivingMode: 'M1',
          families: partial,
        ),
        isFalse,
      );
    });
  });

  // W7.2 — per-modality gates (prompt_pdf / prompt_audio / prompt_video)
  // share the same family/mode join. PDF is cross-engine; audio/video
  // are Gemini-only.
  group('resolveCanAttach{Pdfs,Audio,Video}', () {
    final w72Families = [
      {
        'family': 'claude-code',
        'prompt_pdf': {'M1': true, 'M2': true, 'M4': false},
      },
      {
        'family': 'codex',
        'prompt_pdf': {'M1': true, 'M2': true, 'M4': false},
      },
      {
        'family': 'gemini-cli',
        'prompt_pdf': {'M1': true, 'M2': false, 'M4': false},
        'prompt_audio': {'M1': true, 'M2': false, 'M4': false},
        'prompt_video': {'M1': true, 'M2': false, 'M4': false},
      },
    ];

    test('PDF is cross-engine on supported modes', () {
      for (final kind in ['claude-code', 'codex', 'gemini-cli']) {
        expect(
          resolveCanAttachPdfs(
              kind: kind, drivingMode: 'M1', families: w72Families),
          isTrue,
          reason: '$kind/M1 should accept PDF',
        );
      }
    });

    test('PDF gemini M2 → false (exec-per-turn has no inline path)', () {
      expect(
        resolveCanAttachPdfs(
            kind: 'gemini-cli', drivingMode: 'M2', families: w72Families),
        isFalse,
      );
    });

    test('audio is gemini-only', () {
      expect(
        resolveCanAttachAudio(
            kind: 'gemini-cli', drivingMode: 'M1', families: w72Families),
        isTrue,
      );
      expect(
        resolveCanAttachAudio(
            kind: 'claude-code', drivingMode: 'M2', families: w72Families),
        isFalse,
      );
      expect(
        resolveCanAttachAudio(
            kind: 'codex', drivingMode: 'M2', families: w72Families),
        isFalse,
      );
    });

    test('video is gemini-only', () {
      expect(
        resolveCanAttachVideo(
            kind: 'gemini-cli', drivingMode: 'M1', families: w72Families),
        isTrue,
      );
      expect(
        resolveCanAttachVideo(
            kind: 'claude-code', drivingMode: 'M2', families: w72Families),
        isFalse,
      );
    });
  });
}
