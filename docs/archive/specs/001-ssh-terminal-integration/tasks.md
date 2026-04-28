# Tasks: SSH/Terminal統合機能

**Input**: Design documents from `/specs/001-ssh-terminal-integration/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: テストは将来の統合テストフェーズで追加予定（spec.mdで明示的に要求されていないため現時点ではスキップ）

**Organization**: タスクはユーザーストーリーごとにグループ化され、独立して実装・テスト可能

## Format: `[ID] [P?] [Story] Description`

- **[P]**: 並列実行可能（異なるファイル、依存関係なし）
- **[Story]**: タスクが属するユーザーストーリー（例: US1, US2）
- 説明に正確なファイルパスを含む

---

## Phase 1: Setup (共有インフラ)

**Purpose**: 既存プロジェクトへの追加設定

- [x] T001 [P] flutter_secure_storageのインポート追加確認 in `lib/screens/terminal/terminal_screen.dart`
- [x] T002 [P] 必要なProviderのインポート追加 in `lib/screens/terminal/terminal_screen.dart`
- [x] T003 [P] 必要なServiceのインポート追加 in `lib/screens/terminal/terminal_screen.dart`

---

## Phase 2: Foundational (基盤)

**Purpose**: 全ユーザーストーリーの前提となるコア機能

**⚠️ CRITICAL**: このフェーズ完了まで他のユーザーストーリーは開始不可

- [x] T004 _TerminalScreenStateに状態変数追加（_isConnecting, _connectionError）in `lib/screens/terminal/terminal_screen.dart`
- [x] T005 _TerminalScreenStateにFlutterSecureStorageインスタンス追加 in `lib/screens/terminal/terminal_screen.dart`
- [x] T006 _getAuthOptions()メソッド実装（認証情報取得）in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: 基盤準備完了 - ユーザーストーリー実装開始可能

---

## Phase 3: User Story 1 - SSH接続確立 (Priority: P1) 🎯 MVP

**Goal**: ユーザーが接続設定をタップしてSSH接続を確立し、tmuxセッションにアタッチできる

**Independent Test**: 接続タップ後、tmuxセッションが表示されることを確認

### Implementation for User Story 1

- [x] T007 [US1] _connectAndAttach()の基本フレームワーク実装（try-catch、setState）in `lib/screens/terminal/terminal_screen.dart:39`
- [x] T008 [US1] Connection取得処理実装（connectionsProvider.notifier.getById）in `lib/screens/terminal/terminal_screen.dart`
- [x] T009 [US1] SSH接続処理実装（sshProvider.notifier.connect）in `lib/screens/terminal/terminal_screen.dart`
- [x] T010 [US1] SSHイベントハンドラ設定（onData, onClose, onError）in `lib/screens/terminal/terminal_screen.dart`
- [x] T011 [US1] tmuxセッション一覧取得処理実装（TmuxCommands.listSessions + TmuxParser.parseSessions）in `lib/screens/terminal/terminal_screen.dart`
- [x] T012 [US1] tmuxセッションアタッチ/新規作成処理実装 in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: SSH接続→tmuxアタッチが動作することを確認

---

## Phase 4: User Story 2 - キー入力送信 (Priority: P1) 🎯 MVP

**Goal**: ユーザーがキーボードまたは特殊キーバーから入力したキーがリモートサーバーに送信される

**Independent Test**: ESC/CTRL+C等の特殊キーを押下し、リモートに反映されることを確認

### Implementation for User Story 2

- [x] T013 [US2] _sendKey()メソッド実装（SshProvider.write呼び出し）in `lib/screens/terminal/terminal_screen.dart:287`
- [x] T014 [US2] 接続状態チェック追加（sshState.isConnected確認）in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: キー入力がリモートサーバーに送信されることを確認

---

## Phase 5: User Story 3 - リアルタイム出力表示 (Priority: P1) 🎯 MVP

**Goal**: リモートサーバーからの出力がリアルタイムでターミナルに表示される

**Independent Test**: リモートでコマンド実行し、出力が即座に表示されることを確認

### Implementation for User Story 3

- [x] T015 [US3] SSHイベントハンドラのonDataでTerminal.write呼び出し実装 in `lib/screens/terminal/terminal_screen.dart`
- [x] T016 [US3] ANSIエスケープシーケンスの正しい処理確認（xterm側で自動処理）in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: リモート出力がリアルタイムで表示されることを確認

---

## Phase 6: User Story 4 - 接続エラーハンドリング (Priority: P2)

**Goal**: 接続エラー時にユーザーに適切なフィードバックを提供し、再接続を可能にする

**Independent Test**: 無効なホストに接続試行し、エラーメッセージと再接続ボタンが表示されることを確認

### Implementation for User Story 4

- [x] T017 [US4] _handleDisconnect()メソッド実装 in `lib/screens/terminal/terminal_screen.dart`
- [x] T018 [US4] _handleError()メソッド実装 in `lib/screens/terminal/terminal_screen.dart`
- [x] T019 [US4] _showErrorSnackBar()メソッド実装（再接続ボタン付き）in `lib/screens/terminal/terminal_screen.dart`
- [x] T020 [US4] build()にローディングオーバーレイ追加 in `lib/screens/terminal/terminal_screen.dart`
- [x] T021 [US4] build()にエラーオーバーレイ追加（_buildErrorOverlay）in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: エラー時に適切なUI表示と再接続機能が動作することを確認

---

## Phase 7: User Story 5 - ターミナルリサイズ (Priority: P3)

**Goal**: 画面サイズ変更時にPTYサイズが同期される

**Independent Test**: 画面回転時にターミナル表示が適切にリサイズされることを確認

### Implementation for User Story 5

- [x] T022 [US5] onTerminalResize()メソッド実装（SshProvider.resize呼び出し）in `lib/screens/terminal/terminal_screen.dart`
- [x] T023 [US5] MuxTerminalController.onResizeへのコールバック接続 in `lib/screens/terminal/terminal_screen.dart`

**Checkpoint**: 画面リサイズ時にPTYサイズが同期されることを確認

---

## Phase 8: Polish & Cross-Cutting Concerns

**Purpose**: 複数ユーザーストーリーに影響する改善

- [x] T024 [P] dispose()でSSH接続クリーンアップ実装 in `lib/screens/terminal/terminal_screen.dart`
- [x] T025 [P] build()でref.watch(sshProvider)を使用した状態監視追加 in `lib/screens/terminal/terminal_screen.dart`
- [x] T026 flutter analyze実行と警告解消
- [ ] T027 quickstart.md手順での動作確認

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: 依存なし - 即座に開始可能
- **Foundational (Phase 2)**: Setup完了に依存 - 全ユーザーストーリーをブロック
- **User Stories (Phase 3-7)**: Foundational完了に依存
  - US1, US2, US3 (P1): 順次実行推奨（US1 → US2 → US3）
  - US4 (P2): US1-3完了後に実装
  - US5 (P3): 最後に実装
- **Polish (Phase 8)**: 全ユーザーストーリー完了に依存

### User Story Dependencies

- **US1 (SSH接続)**: Foundational完了後に開始
- **US2 (キー入力)**: US1完了後（接続が必要）
- **US3 (出力表示)**: US1完了後（接続が必要）
- **US4 (エラー処理)**: US1-3完了後（エラーケースの確認のため）
- **US5 (リサイズ)**: US1完了後（接続が必要）

### Parallel Opportunities

- Phase 1: T001, T002, T003 は並列実行可能
- Phase 8: T024, T025 は並列実行可能

---

## Implementation Strategy

### MVP First (User Story 1-3)

1. Phase 1: Setup完了
2. Phase 2: Foundational完了（CRITICAL）
3. Phase 3: US1 - SSH接続確立
4. Phase 4: US2 - キー入力送信
5. Phase 5: US3 - 出力表示
6. **STOP and VALIDATE**: MVP動作確認

### Full Implementation

1. MVP完了後
2. Phase 6: US4 - エラーハンドリング
3. Phase 7: US5 - リサイズ
4. Phase 8: Polish

---

## Notes

- 主要修正対象: `lib/screens/terminal/terminal_screen.dart`
- 既存サービス活用: `ssh_client.dart`, `tmux_commands.dart`, `tmux_parser.dart`
- 各タスク完了後にコミット推奨
- チェックポイントで動作確認を行う
