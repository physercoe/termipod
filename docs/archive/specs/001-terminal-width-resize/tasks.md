# Tasks: Terminal Width Auto-Resize

**Input**: Design documents from `/specs/001-terminal-width-resize/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, quickstart.md

**Tests**: TDD approach per Constitution (III. Test-First)

**Organization**: Tasks are grouped by user story to enable independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3, US4)
- Include exact file paths in descriptions

## Path Conventions

- **Flutter project**: `lib/` for source, `test/` for tests
- Per plan.md project structure

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Core calculation logic and state management foundation

- [x] T001 [P] Create FontCalculator service in lib/services/terminal/font_calculator.dart
- [x] T002 [P] Create TerminalDisplayState model in lib/providers/terminal_display_provider.dart
- [x] T003 Add minFontSize and autoFitEnabled fields to AppSettings in lib/providers/settings_provider.dart

---

## Phase 2: Foundational (Tests & Blocking Prerequisites)

**Purpose**: Core unit tests that MUST pass before UI implementation

**⚠️ CRITICAL**: Write tests FIRST, ensure they FAIL before implementation

- [x] T004 [P] Create unit tests for FontCalculator in test/services/terminal/font_calculator_test.dart
- [x] T005 [P] Create unit tests for TerminalDisplayNotifier in test/providers/terminal_display_provider_test.dart
- [x] T006 Implement FontCalculator.calculate() to pass tests in lib/services/terminal/font_calculator.dart
- [x] T007 Implement TerminalDisplayNotifier methods to pass tests in lib/providers/terminal_display_provider.dart

**Checkpoint**: Core calculation and state logic verified - UI implementation can now begin

---

## Phase 3: User Story 1 - Auto-fit Terminal to Pane Width (Priority: P1) 🎯 MVP

**Goal**: ペイン選択時にtmuxのpane_widthに合わせてターミナル表示幅を自動調整

**Independent Test**: ペインを選択し、ターミナル表示がペインの横幅（80文字、120文字、200文字）に正確にフィットすることを確認

### Implementation for User Story 1

- [x] T008 [US1] Create ScalableTerminal widget base structure in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T009 [US1] Integrate TerminalDisplayProvider with ScalableTerminal in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T010 [US1] Add LayoutBuilder to track screen width in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T011 [US1] Implement dynamic TerminalStyle.fontSize based on calculated size in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T012 [US1] Update TerminalScreen to use ScalableTerminal instead of TerminalView in lib/screens/terminal/terminal_screen.dart
- [x] T013 [US1] Wire pane selection to TerminalDisplayProvider.updatePane() in lib/screens/terminal/terminal_screen.dart
- [x] T014 [US1] Handle screen rotation recalculation in lib/screens/terminal/widgets/scalable_terminal.dart

**Checkpoint**: ペイン横幅に合わせた自動フォントサイズ調整が動作。US1のMVPが完成。

---

## Phase 4: User Story 2 - Minimum Font Size Setting (Priority: P2)

**Goal**: 設定画面で最小フォントサイズを指定可能にする

**Independent Test**: 設定画面で最小フォントサイズを変更し、その値がターミナル表示の自動調整に反映されることを確認

### Implementation for User Story 2

- [x] T015 [P] [US2] Create MinFontSizeDialog widget in lib/widgets/dialogs/min_font_size_dialog.dart
- [x] T016 [US2] Add setMinFontSize() method to SettingsNotifier in lib/providers/settings_provider.dart
- [x] T017 [US2] Add persistence for minFontSize in SettingsNotifier in lib/providers/settings_provider.dart
- [x] T018 [US2] Add Minimum Font Size setting row to SettingsScreen in lib/screens/settings/settings_screen.dart
- [x] T019 [US2] Wire minFontSize from settings to FontCalculator in lib/providers/terminal_display_provider.dart

**Checkpoint**: 最小フォントサイズ設定が保存され、ターミナル表示に反映される。

---

## Phase 5: User Story 3 - Horizontal Scroll for Wide Panes (Priority: P2)

**Goal**: 最小フォントサイズでも画面幅を超える場合、水平スクロールを有効化

**Independent Test**: 300文字幅ペインを選択し、水平スクロールでコンテンツ全体を確認

### Implementation for User Story 3

- [x] T020 [US3] Add needsHorizontalScroll computed property to TerminalDisplayState in lib/providers/terminal_display_provider.dart
- [x] T021 [US3] Wrap TerminalView with conditional SingleChildScrollView in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T022 [US3] Calculate terminal width for horizontal scroll container in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T023 [US3] Reset horizontal scroll position on pane switch in lib/screens/terminal/widgets/scalable_terminal.dart

**Checkpoint**: 広いペインで水平スクロールが機能し、全コンテンツにアクセス可能。

---

## Phase 6: User Story 4 - Pinch to Zoom (Priority: P3)

**Goal**: ピンチジェスチャーでフォントサイズを拡大・縮小

**Independent Test**: ターミナル表示中にピンチイン/ピンチアウトし、フォントサイズが動的に変化することを確認

### Tests for User Story 4

- [x] T024 [P] [US4] Create widget tests for pinch zoom in test/screens/terminal/scalable_terminal_test.dart (unit tests in terminal_display_provider_test.dart)

### Implementation for User Story 4

- [x] T025 [US4] Add zoom state (zoomScale, isZooming) to TerminalDisplayState in lib/providers/terminal_display_provider.dart
- [x] T026 [US4] Add startZoom(), updateZoom(), endZoom() methods to TerminalDisplayNotifier in lib/providers/terminal_display_provider.dart
- [x] T027 [US4] Wrap ScalableTerminal content with GestureDetector for scale events in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T028 [US4] Apply Transform.scale during zoom operation in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T029 [US4] Finalize font size on zoom end and reset scale in lib/screens/terminal/widgets/scalable_terminal.dart
- [x] T030 [US4] Reset zoom on pane switch to restore auto-fit mode in lib/screens/terminal/widgets/scalable_terminal.dart

**Checkpoint**: ピンチズームが60fpsで滑らかに動作し、ズーム後もターミナルが正常に機能。

---

## Phase 7: Polish & Cross-Cutting Concerns

**Purpose**: エッジケース対応と最終調整

- [x] T031 [P] Handle edge case: pane width = 0 fallback to 80 in lib/services/terminal/font_calculator.dart
- [x] T032 [P] Handle edge case: extremely narrow panes (< 10 chars) in lib/services/terminal/font_calculator.dart
- [x] T033 Handle foldable device screen width changes in lib/screens/terminal/widgets/scalable_terminal.dart (LayoutBuilder handles automatically)
- [x] T034 Add logging for font size calculations in lib/services/terminal/font_calculator.dart
- [x] T035 Run flutter analyze and fix warnings (only pre-existing deprecation warnings remain)
- [ ] T036 Manual testing: verify all acceptance scenarios from spec.md

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies - can start immediately
- **Foundational (Phase 2)**: Depends on Setup completion
- **User Story 1 (Phase 3)**: Depends on Foundational - CORE MVP
- **User Story 2 (Phase 4)**: Depends on Foundational, can parallel with US1
- **User Story 3 (Phase 5)**: Depends on US1 (uses needsHorizontalScroll)
- **User Story 4 (Phase 6)**: Depends on US1 (extends ScalableTerminal)
- **Polish (Phase 7)**: Depends on all user stories

### User Story Dependencies

```
           ┌─────────────┐
           │   Setup     │
           │  (Phase 1)  │
           └──────┬──────┘
                  │
           ┌──────▼──────┐
           │ Foundational│
           │  (Phase 2)  │
           └──────┬──────┘
                  │
     ┌────────────┼────────────┐
     │            │            │
     ▼            ▼            │
┌─────────┐ ┌─────────┐        │
│  US1    │ │  US2    │        │
│ (P1) MVP│ │  (P2)   │        │
└────┬────┘ └─────────┘        │
     │                         │
     ├─────────────────────────┤
     │                         │
     ▼                         ▼
┌─────────┐              ┌─────────┐
│  US3    │              │  US4    │
│  (P2)   │              │  (P3)   │
└─────────┘              └─────────┘
```

- **US1 (P1)**: MVP - 独立してテスト可能
- **US2 (P2)**: Foundational完了後すぐに開始可能、US1と並列実行可
- **US3 (P2)**: US1のScalableTerminalを拡張
- **US4 (P3)**: US1のScalableTerminalを拡張

### Parallel Opportunities

**Phase 1 (Setup)**:
```bash
# T001 と T002 は並列実行可能（異なるファイル）
Task: "T001 Create FontCalculator service"
Task: "T002 Create TerminalDisplayState model"
```

**Phase 2 (Foundational)**:
```bash
# T004 と T005 は並列実行可能（異なるテストファイル）
Task: "T004 Unit tests for FontCalculator"
Task: "T005 Unit tests for TerminalDisplayNotifier"
```

**Phase 4 (US2)**:
```bash
# T015 はUS1と並列実行可能（独立したダイアログWidget）
Task: "T015 Create MinFontSizeDialog widget"
```

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Complete Phase 1: Setup (T001-T003)
2. Complete Phase 2: Foundational (T004-T007)
3. Complete Phase 3: User Story 1 (T008-T014)
4. **STOP and VALIDATE**: ペイン横幅に合わせた自動調整をテスト
5. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Core logic ready
2. Add US1 → Test: 自動フィット動作確認 → **MVP Complete**
3. Add US2 → Test: 設定画面で最小フォントサイズ変更 → Deploy
4. Add US3 → Test: 広いペインで水平スクロール → Deploy
5. Add US4 → Test: ピンチズーム → Deploy

---

## Summary

| Phase | Tasks | Parallel | Story |
|-------|-------|----------|-------|
| Setup | 3 | 2 | - |
| Foundational | 4 | 2 | - |
| US1 (P1) MVP | 7 | 0 | Auto-fit |
| US2 (P2) | 5 | 1 | Min Font Setting |
| US3 (P2) | 4 | 0 | Horizontal Scroll |
| US4 (P3) | 7 | 1 | Pinch to Zoom |
| Polish | 6 | 2 | - |
| **Total** | **36** | **8** | - |

---

## Notes

- [P] tasks = different files, no dependencies
- [Story] label maps task to specific user story
- Each user story is independently testable
- Constitution III (TDD): Tests first in Foundational phase
- Commit after each task or logical group
- Stop at any checkpoint to validate story independently
