# Shared design tokens (DTCG)

The neutral **source of truth** for TermiPod's visual scales, shared by both
clients. WS1 of [`../docs/plans/desktop-control-plane.md`](../docs/plans/desktop-control-plane.md),
implementing [ADR-051](../docs/decisions/051-desktop-client-stack.md) and
extending the [ADR-047](../docs/decisions/047-design-system-enforcement.md)
token system to the web/desktop client.

## Layout

| File | Role |
|---|---|
| `tokens.json` | **Source of truth.** DTCG-format spacing / radius / fontSize / iconSize / color values. |
| `build.mjs` | Zero-dependency Node emitter + verifier. |
| `build/tokens.css` | **Generated.** CSS custom properties (`--spacing-s16`, `--color-primary`, …) for the web/desktop client. Committed; CI checks it's current. |

## How the two clients stay in sync

The Flutter app's `lib/theme/tokens.dart` + `lib/theme/design_colors.dart`
remain **hand-authored** — they carry the ADR-047 guidance docs — but they are
**authoritative-by-enforcement**: `build.mjs --check` verifies every value in
those files matches `tokens.json` (both directions), so the clients cannot
drift. The web client consumes `build/tokens.css`. (Open Question 1 of the plan
is resolved this way — verify the Dart rather than regenerate it, which keeps
the Flutter build byte-unchanged and needs no Flutter SDK to gate.)

## Commands

```bash
node design-tokens/build.mjs          # regenerate build/tokens.css
node design-tokens/build.mjs --check   # CI: verify Dart parity + tokens.css current (exit 1 on drift)
```

CI runs `--check` in the "Verify shared design tokens (DTCG)" step. When you
change a token:

1. Edit `tokens.json`.
2. Mirror the value in `tokens.dart` / `design_colors.dart` (keep the docs).
3. `node design-tokens/build.mjs` to regenerate `build/tokens.css`.
4. Commit all three.

## Notes

- Colors are opaque; Dart stores `Color(0xFF<hex>)`, the JSON stores `#<hex>`.
- The emitter is intentionally dependency-free (a ~60-value set doesn't justify
  a Style Dictionary `node_modules`). The input is standard DTCG, so swapping in
  Style Dictionary later is a drop-in if the token set outgrows this.
