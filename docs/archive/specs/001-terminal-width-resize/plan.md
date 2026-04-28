# Implementation Plan: Terminal Width Auto-Resize

**Branch**: `001-terminal-width-resize` | **Date**: 2026-01-11 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-terminal-width-resize/spec.md`

## Summary

ペイン選択時にtmuxのpane_widthに合わせてターミナル表示幅を自動調整する機能。最小フォントサイズ制限を設定可能とし、制限を超える場合は水平スクロールを有効化。ピンチジェスチャーによる拡大縮小も対応。

## Technical Context

**Language/Version**: Dart 3.x / Flutter 3.24+
**Primary Dependencies**: flutter_riverpod (状態管理), xterm (ターミナル表示), dartssh2 (SSH接続)
**Storage**: shared_preferences (設定保存)
**Testing**: flutter test (widget tests, unit tests)
**Target Platform**: Android (primary), iOS (secondary)
**Project Type**: mobile (Flutter cross-platform)
**Performance Goals**: 60fps for pinch zoom, 500ms for pane selection → display adjustment
**Constraints**: Smooth gesture response, maintain terminal readability at minimum font size
**Scale/Scope**: Single terminal view, single active pane at a time

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Type Safety | PASS | Dart's null safety + strict mode |
| II. KISS & YAGNI | PASS | Minimal new abstractions, extend existing patterns |
| III. Test-First (TDD) | PASS | Widget tests for gesture handling, unit tests for calculations |
| IV. Security-First | N/A | No security-sensitive data in this feature |
| V. SOLID | PASS | Single responsibility: TerminalDisplayController |
| VI. DRY | PASS | Reuse existing settings infrastructure |
| Prohibited Naming | PASS | No utils/helpers/common directories |
| Mobile UX | PASS | Gesture support, foldable device consideration |

**Gate Result**: PASS - No violations

## Project Structure

### Documentation (this feature)

```text
specs/001-terminal-width-resize/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
lib/
├── providers/
│   ├── settings_provider.dart       # 既存: minFontSize追加
│   └── terminal_display_provider.dart  # 新規: 表示状態管理
├── screens/
│   └── terminal/
│       ├── terminal_screen.dart     # 既存: TerminalView wrapper変更
│       └── widgets/
│           └── scalable_terminal.dart  # 新規: ピンチ対応TerminalView
├── services/
│   └── terminal/
│       └── font_calculator.dart     # 新規: フォントサイズ計算ロジック
└── widgets/
    └── dialogs/
        └── min_font_size_dialog.dart   # 新規: 最小フォントサイズ設定

test/
├── providers/
│   └── terminal_display_provider_test.dart
├── services/
│   └── terminal/
│       └── font_calculator_test.dart
└── screens/
    └── terminal/
        └── scalable_terminal_test.dart
```

**Structure Decision**: Flutter mobile project structure。既存の`providers/`, `screens/`, `services/`パターンに従い、新規ファイルを追加。

## Complexity Tracking

> No violations to justify - all gates passed.
