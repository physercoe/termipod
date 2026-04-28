# Implementation Plan: MuxPod Phase 1 MVP

**Branch**: `001-phase1-mvp` | **Date**: 2026-01-10 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-phase1-mvp/spec.md`

## Summary

MuxPod Phase 1 MVPは、AndroidスマートフォンからSSH経由でリモートサーバーのtmuxセッションを閲覧・操作するExpo (React Native) アプリケーション。SSH接続基盤、接続管理UI、tmuxセッション/ウィンドウ/ペインのナビゲーション、ANSIカラー対応ターミナル表示、特殊キー入力機能を実装する。

## Technical Context

**Language/Version**: TypeScript 5.6+
**Framework**: Expo ~52.0.0 / React Native 0.76.0
**Primary Dependencies**:
- expo-router ~4.0.0 (ファイルベースルーティング)
- zustand ^5.0.0 (状態管理)
- react-native-ssh-sftp ^1.4.0 (SSH接続)
- expo-secure-store ~13.0.0 (セキュア保存)
- @react-native-async-storage/async-storage 2.1.0 (永続化)

**Storage**: AsyncStorage (接続設定), expo-secure-store (パスワード暗号化)
**Testing**: Jest + React Native Testing Library
**Target Platform**: Android (primary), iOS (secondary)
**Project Type**: Mobile application
**Package Manager**: pnpm

**Performance Goals**:
- SSH接続確立からtmuxセッション一覧表示まで5秒以内
- ペイン選択からターミナル内容表示まで1秒以内
- ターミナル更新遅延200ms以下
- キー入力から画面反映まで300ms以下
- 60fps維持でのスクロール

**Constraints**:
- オフライン時のグレースフルデグラデーション
- 1000行スクロールバック履歴
- ポーリング間隔100ms

**Scale/Scope**:
- 5+接続設定保存
- 10+セッション、各10+ウィンドウのナビゲーション

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Type Safety | ✅ PASS | strict: true維持、外部入力（SSH応答）に型ガード適用 |
| II. KISS & YAGNI | ✅ PASS | Phase 1はMVP機能のみ、Phase 2機能は除外 |
| III. Test-First (TDD) | ✅ PASS | SSHコマンド/tmux操作はモック可能な設計 |
| IV. Security-First | ✅ PASS | パスワードはexpo-secure-store、コマンドエスケープ必須 |
| V. SOLID | ✅ PASS | SSH/tmux/UI責務分離、DIP適用 |
| VI. DRY | ✅ PASS | 共通型はsrc/types/に集約 |
| Prohibited Naming | ✅ PASS | utils/helpers禁止、ドメイン名使用 |
| Quality Gates | ✅ PASS | pnpm typecheck/lint必須 |

## Project Structure

### Documentation (this feature)

```text
specs/001-phase1-mvp/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
└── tasks.md             # Phase 2 output (NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
app/                           # Expo Router (画面定義)
├── _layout.tsx                # Root layout
├── index.tsx                  # 接続一覧画面
├── (main)/
│   ├── _layout.tsx            # メインレイアウト
│   └── terminal/
│       └── [connectionId].tsx # ターミナル画面
└── connection/
    ├── add.tsx                # 接続追加
    └── [id]/
        └── edit.tsx           # 接続編集

src/
├── components/
│   ├── terminal/
│   │   ├── TerminalView.tsx   # ターミナル表示
│   │   ├── TerminalInput.tsx  # 入力欄
│   │   └── SpecialKeys.tsx    # ESC/CTRL/ALT等
│   ├── connection/
│   │   ├── ConnectionList.tsx
│   │   ├── ConnectionCard.tsx
│   │   └── SessionTree.tsx    # セッション/ウィンドウ/ペイン
│   └── navigation/
│       ├── SessionTabs.tsx
│       ├── WindowTabs.tsx
│       └── PaneSelector.tsx
├── hooks/
│   ├── useSSH.ts              # SSH接続管理
│   ├── useTmux.ts             # tmuxコマンド
│   └── useTerminal.ts         # ターミナル状態
├── stores/
│   ├── connectionStore.ts     # 接続設定
│   ├── sessionStore.ts        # tmuxセッション状態
│   └── terminalStore.ts       # ターミナル内容
├── services/
│   ├── ssh/
│   │   ├── client.ts          # SSHクライアント
│   │   └── auth.ts            # 認証処理
│   ├── tmux/
│   │   ├── commands.ts        # tmuxコマンド実行
│   │   └── parser.ts          # 出力パーサー
│   ├── ansi/
│   │   └── parser.ts          # ANSIエスケープ処理
│   └── terminal/
│       ├── charWidth.ts       # 文字幅計算
│       └── formatter.ts       # 出力整形
└── types/
    ├── connection.ts
    ├── tmux.ts
    └── terminal.ts

__tests__/
├── services/
│   ├── ssh/
│   ├── tmux/
│   └── ansi/
├── hooks/
└── components/
```

**Structure Decision**: Mobile application構造を採用。Expo Routerによるファイルベースルーティング（app/）とビジネスロジック（src/）を分離。設計書のディレクトリ構成に準拠。

## Complexity Tracking

> No Constitution violations requiring justification.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | - | - |
