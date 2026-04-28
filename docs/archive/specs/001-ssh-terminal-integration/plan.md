# Implementation Plan: SSH/Terminal統合機能

**Branch**: `001-ssh-terminal-integration` | **Date**: 2026-01-11 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-ssh-terminal-integration/spec.md`

## Summary

SSH接続→tmuxアタッチ→キー送信のパイプラインを完成させ、`terminal_screen.dart`の2つのTODOコメント（39行目と287行目）を解決する。既存の`SshClient`、`TmuxCommands`、および各Providerを活用して、ユーザーが接続を選択してからtmuxセッションを操作できるまでのフローを実装する。

## Technical Context

**Language/Version**: Dart 3.10+ / Flutter 3.24+
**Primary Dependencies**: dartssh2 (SSH), xterm (ターミナル表示), flutter_riverpod (状態管理)
**Storage**: flutter_secure_storage (SSH鍵/パスワード), shared_preferences (接続設定)
**Testing**: flutter_test
**Target Platform**: Android (将来的にiOS)
**Project Type**: Mobile application
**Performance Goals**: キー入力から画面反映まで200ms以内、接続確立3秒以内
**Constraints**: モバイル環境でのバッテリー・メモリ効率
**Scale/Scope**: 単一デバイスから複数サーバーへの接続

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| 原則 | 状態 | 備考 |
|------|------|------|
| I. Type Safety | ✅ Pass | Dart言語は静的型付け、null safety有効 |
| II. KISS & YAGNI | ✅ Pass | 既存サービスを活用、新規抽象化最小限 |
| III. Test-First (TDD) | ⚠️ 要対応 | 統合テストの設計が必要 |
| IV. Security-First | ✅ Pass | flutter_secure_storage使用、SSH鍵は暗号化保存 |
| V. SOLID | ✅ Pass | 既存Provider/Service構造を維持 |
| VI. DRY | ✅ Pass | 既存TmuxCommands/SshClientを再利用 |
| Prohibited Naming | ✅ Pass | utils/helpers/common使用なし |
| Mobile UX | ✅ Pass | 特殊キーバー対応、レスポンス目標設定済み |

**Gate Status**: ✅ PASS - 全原則に準拠

## Project Structure

### Documentation (this feature)

```text
specs/001-ssh-terminal-integration/
├── spec.md              # 機能仕様書
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   └── terminal_integration.dart
└── tasks.md             # Phase 2 output (/speckit.tasks)
```

### Source Code (repository root)

```text
lib/
├── main.dart                    # エントリーポイント
├── providers/
│   ├── connection_provider.dart # 接続設定管理
│   ├── ssh_provider.dart        # SSH接続状態管理 [修正対象]
│   ├── terminal_provider.dart   # ターミナル状態管理
│   └── tmux_provider.dart       # tmuxセッション管理 [修正対象]
├── screens/
│   └── terminal/
│       └── terminal_screen.dart # ターミナル画面 [主要修正対象]
├── services/
│   ├── ssh/
│   │   └── ssh_client.dart      # SSH接続サービス [活用]
│   ├── tmux/
│   │   ├── tmux_commands.dart   # tmuxコマンド生成 [活用]
│   │   └── tmux_parser.dart     # tmux出力パーサー [活用]
│   └── terminal/
│       └── terminal_controller.dart # ターミナル制御
├── theme/                       # デザイン定義
└── widgets/
    └── special_keys_bar.dart    # 特殊キーバー

test/
├── unit/
│   └── services/
│       ├── ssh_client_test.dart
│       └── tmux_commands_test.dart
└── integration/
    └── terminal_integration_test.dart # 新規作成
```

**Structure Decision**: 既存のFlutter標準構造（lib/providers, lib/services, lib/screens）を維持。新規ファイル追加は最小限に抑え、主に既存ファイルの修正で実装。

## Complexity Tracking

> 本機能は既存アーキテクチャ内で実装可能。Constitution違反なし。

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | - | - |
