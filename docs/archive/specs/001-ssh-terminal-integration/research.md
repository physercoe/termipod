# Research: SSH/Terminal統合機能

**Date**: 2026-01-11
**Branch**: `001-ssh-terminal-integration`

## 概要

既存のコードベースを分析し、SSH/Terminal統合に必要な技術的決定を文書化する。

## 既存コンポーネント分析

### 1. SshClient (`lib/services/ssh/ssh_client.dart`)

**状態**: 完全実装済み

| メソッド | 用途 | 統合での使用 |
|---------|------|-------------|
| `connect()` | SSH接続確立 | 接続パイプラインの開始 |
| `startShell()` | PTYシェル開始 | tmuxアタッチ前に必要 |
| `setEventHandlers()` | イベントコールバック設定 | データ受信→Terminal表示 |
| `write()` | データ送信 | キー入力送信 |
| `exec()` | コマンド実行 | tmuxコマンド実行 |
| `resize()` | PTYリサイズ | 画面サイズ同期 |

### 2. TmuxCommands (`lib/services/tmux/tmux_commands.dart`)

**状態**: 完全実装済み

| メソッド | 用途 | 統合での使用 |
|---------|------|-------------|
| `listSessions()` | セッション一覧取得 | 接続後の初期化 |
| `attachSession()` | セッションアタッチ | シェル内で実行 |
| `newSession()` | 新規セッション作成 | セッションがない場合 |
| `sendKeys()` | キー送信 | 直接使用しない（シェル経由） |

### 3. MuxTerminalController (`lib/services/terminal/terminal_controller.dart`)

**状態**: 完全実装済み

| プロパティ/メソッド | 用途 | 統合での使用 |
|-------------------|------|-------------|
| `terminal` | xtermインスタンス | UIウィジェットに提供 |
| `onInput` | 入力イベントストリーム | SSH経由で送信 |
| `onResize` | リサイズイベント | PTYリサイズ同期 |
| `write()` | データ書き込み | SSH出力を表示 |

### 4. Providers

| Provider | 状態 | 統合での役割 |
|----------|------|-------------|
| `sshProvider` | 実装済み | SSH接続状態管理 |
| `terminalProvider` | 実装済み | Terminal状態管理 |
| `tmuxProvider` | 実装済み | tmuxセッション状態管理 |
| `connectionsProvider` | 実装済み | 接続設定取得 |
| `keysProvider` | 実装済み | SSH鍵メタデータ管理 |

## 技術的決定

### Decision 1: tmuxアタッチ方法

**決定**: シェル内で `tmux attach-session` コマンドを実行

**理由**:
- SSHシェルは既にPTYを持っている
- `exec()`で直接実行すると、シェルが終了してしまう
- シェル内でアタッチすることで、PTYとtmuxが正しく連携

**代替案（却下）**:
- `exec("tmux attach")`: セッション終了でSSH切断
- 別セッションでアタッチ: 複雑性増加

**実装**:
```dart
// シェル開始後
await sshClient.startShell();

// tmuxアタッチコマンドをシェルに送信
final attachCmd = TmuxCommands.attachSession(sessionName);
sshClient.write('$attachCmd\n');
```

### Decision 2: イベントハンドラの接続

**決定**: SshProviderでイベントハンドラを設定し、TerminalProviderに委譲

**理由**:
- 関心の分離: SSH→データ受信、Terminal→表示
- テスト容易性: 各レイヤーを独立してテスト可能
- 既存構造との整合性

**実装フロー**:
```
SshClient.onData → SshProvider → TerminalProvider.write → Terminal表示
MuxTerminalController.onInput → TerminalScreen → SshProvider.write → SSH送信
```

### Decision 3: 認証情報の取得

**決定**: `flutter_secure_storage`から直接取得（KeychainServiceを使用）

**理由**:
- 認証情報（パスワード/秘密鍵）はセキュアストレージに保存済み
- KeysProviderはメタデータのみ、実際の鍵は別途取得

**実装**:
```dart
// パスワード認証の場合
final password = await secureStorage.read(key: 'connection_${connection.id}_password');

// 鍵認証の場合
final privateKey = await secureStorage.read(key: 'ssh_key_${connection.keyId}_private');
```

**注**: KeychainServiceが存在しない場合は、直接flutter_secure_storageを使用

### Decision 4: エラーハンドリング戦略

**決定**: 3層エラーハンドリング

| レイヤー | エラー種類 | 対応 |
|---------|-----------|------|
| SSH接続 | 接続エラー、認証エラー | SnackBar + 再接続ボタン |
| tmux操作 | セッションなし、サーバー未起動 | 自動作成 or メッセージ |
| ランタイム | ネットワーク切断 | 切断通知 + 再接続オプション |

### Decision 5: ターミナル-SSH間のデータフロー

**決定**: 双方向ストリーム接続

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Terminal UI    │     │   SshProvider   │     │   SSH Server    │
│  (xterm widget) │     │                 │     │   (tmux)        │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         │  onInput (keys)       │                       │
         ├──────────────────────►│      write()          │
         │                       ├──────────────────────►│
         │                       │                       │
         │                       │      onData()         │
         │  write() (display)    │◄──────────────────────┤
         │◄──────────────────────┤                       │
         │                       │                       │
```

## 未解決事項

なし - 全ての技術的決定が完了

## 次のステップ

1. `data-model.md` - データフロー図の詳細化
2. `contracts/` - インターフェース契約の定義
3. `quickstart.md` - 実装ガイド
