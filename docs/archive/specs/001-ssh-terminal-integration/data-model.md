# Data Model: SSH/Terminal統合機能

**Date**: 2026-01-11
**Branch**: `001-ssh-terminal-integration`

## エンティティ関係図

```
┌─────────────────────────────────────────────────────────────────────┐
│                         MuxPod Data Flow                            │
└─────────────────────────────────────────────────────────────────────┘

┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Connection  │────►│  SshClient   │────►│  SSH Server  │
│  (設定)      │     │  (接続)      │     │  (リモート)  │
└──────────────┘     └──────┬───────┘     └──────┬───────┘
                           │                     │
                           │ startShell()        │
                           ▼                     │
                    ┌──────────────┐             │
                    │  SSHSession  │◄────────────┘
                    │  (PTY)       │
                    └──────┬───────┘
                           │
                           │ write("tmux attach")
                           ▼
                    ┌──────────────┐
                    │ TmuxSession  │
                    │  (セッション) │
                    └──────┬───────┘
                           │
           ┌───────────────┼───────────────┐
           ▼               ▼               ▼
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │ TmuxPane │    │ TmuxPane │    │ TmuxPane │
    │ (ペイン) │    │ (ペイン) │    │ (ペイン) │
    └──────────┘    └──────────┘    └──────────┘
```

## 既存エンティティ

### Connection (接続設定)

**ファイル**: `lib/providers/connection_provider.dart`

```dart
class Connection {
  final String id;           // UUID
  final String name;         // 表示名
  final String host;         // ホスト名/IP
  final int port;            // ポート (default: 22)
  final String username;     // ユーザー名
  final String authMethod;   // 'password' | 'key'
  final String? keyId;       // SSH鍵ID (authMethod='key'の場合)
  final DateTime createdAt;
  final DateTime? lastConnectedAt;
}
```

**バリデーション**:
- `host`: 空でないこと
- `port`: 1-65535
- `username`: 空でないこと
- `authMethod`: 'password' または 'key'

### SshKeyMeta (SSH鍵メタデータ)

**ファイル**: `lib/providers/key_provider.dart`

```dart
class SshKeyMeta {
  final String id;           // UUID
  final String name;         // 表示名
  final String type;         // 'rsa' | 'ed25519' | 'ecdsa'
  final String? publicKey;   // 公開鍵 (表示用)
  final bool hasPassphrase;  // パスフレーズ有無
  final DateTime createdAt;
  final String? comment;     // コメント
}
```

**注**: 秘密鍵は `flutter_secure_storage` に別途保存

### TmuxSession (tmuxセッション)

**ファイル**: `lib/services/tmux/tmux_parser.dart`

```dart
class TmuxSession {
  final String name;         // セッション名
  final String? id;          // セッションID ($0, $1, ...)
  final DateTime? created;   // 作成日時
  final bool attached;       // アタッチ状態
  final int windowCount;     // ウィンドウ数
  final List<TmuxWindow> windows;
}
```

### TmuxWindow (tmuxウィンドウ)

**ファイル**: `lib/services/tmux/tmux_parser.dart`

```dart
class TmuxWindow {
  final int index;           // ウィンドウインデックス
  final String? id;          // ウィンドウID (@0, @1, ...)
  final String name;         // ウィンドウ名
  final bool active;         // アクティブ状態
  final int paneCount;       // ペイン数
  final Set<TmuxWindowFlag> flags;
  final List<TmuxPane> panes;
}
```

### TmuxPane (tmuxペイン)

**ファイル**: `lib/services/tmux/tmux_parser.dart`

```dart
class TmuxPane {
  final int index;           // ペインインデックス
  final String id;           // ペインID (%0, %1, ...)
  final bool active;         // アクティブ状態
  final String? currentCommand;
  final String? title;
  final int width;           // 幅 (cols)
  final int height;          // 高さ (rows)
  final int cursorX;
  final int cursorY;
}
```

## 状態モデル

### SshState

**ファイル**: `lib/providers/ssh_provider.dart`

```dart
class SshState {
  final SshConnectionState connectionState;  // disconnected|connecting|connected|error
  final String? error;
  final String? sessionTitle;
}
```

**状態遷移**:
```
disconnected ──connect()──► connecting
connecting ───success───► connected
connecting ───failure───► error
connected ──disconnect()─► disconnected
connected ───error──────► error
error ────retry()──────► connecting
```

### TerminalState

**ファイル**: `lib/providers/terminal_provider.dart`

```dart
class TerminalState {
  final MuxTerminalController? controller;
  final bool isInitialized;
  final int cols;
  final int rows;
  final String? title;
}
```

### TmuxState

**ファイル**: `lib/providers/tmux_provider.dart`

```dart
class TmuxState {
  final List<TmuxSession> sessions;
  final String? activeSessionName;
  final int? activeWindowIndex;
  final String? activePaneId;
  final bool isLoading;
  final String? error;
}
```

## データフロー

### 接続フロー

```
1. ユーザーが接続をタップ
   ↓
2. ConnectionからconnectionId取得
   ↓
3. SshProviderで接続開始
   - SshClient.connect(host, port, username, options)
   - SshClient.startShell()
   ↓
4. イベントハンドラ設定
   - onData → Terminal.write()
   - onClose → 切断処理
   - onError → エラー表示
   ↓
5. tmuxセッション一覧取得
   - SshClient.exec(TmuxCommands.listSessions())
   ↓
6. セッションにアタッチ
   - SshClient.write("tmux attach -t session\n")
   ↓
7. 接続完了
```

### キー入力フロー

```
1. ユーザーがキー入力
   ↓
2. MuxTerminalController.onInput
   ↓
3. TerminalScreen._sendKey()
   ↓
4. SshProvider.write(data)
   ↓
5. SshClient.write(data)
   ↓
6. SSH → tmux → シェル
```

### 出力表示フロー

```
1. SSH Server → データ送信
   ↓
2. SshClient.onData
   ↓
3. SshEvents.onData callback
   ↓
4. TerminalProvider.write()
   ↓
5. Terminal.write() (xterm)
   ↓
6. 画面更新
```

## セキュアストレージキー

| キー | 用途 | 保存タイミング |
|-----|------|--------------|
| `connection_{id}_password` | 接続パスワード | 接続設定保存時 |
| `ssh_key_{id}_private` | SSH秘密鍵 | 鍵インポート時 |
| `ssh_key_{id}_passphrase` | 鍵パスフレーズ | 鍵インポート時 |
