# Quickstart: MuxPod Phase 1 MVP

**Feature**: 001-phase1-mvp
**Date**: 2026-01-10

## Prerequisites

- Node.js 18+
- pnpm 8+
- Android Studio (Android SDK)
- 実機またはエミュレータ（Android 10+推奨）

## Setup

### 1. リポジトリクローン

```bash
git clone <repo>
cd mux-pod
git checkout 001-phase1-mvp
```

### 2. 依存関係インストール

```bash
pnpm install
```

### 3. 開発サーバー起動

```bash
pnpm start
```

### 4. Androidで実行

```bash
# 別ターミナルで
pnpm android
```

## Project Structure Overview

```
mux-pod/
├── app/                    # Expo Router screens
│   ├── index.tsx           # 接続一覧（ホーム）
│   ├── connection/         # 接続追加・編集
│   └── (main)/terminal/    # ターミナル画面
├── src/
│   ├── components/         # React components
│   ├── hooks/              # Custom hooks
│   ├── stores/             # Zustand stores
│   ├── services/           # Business logic
│   └── types/              # TypeScript types
└── __tests__/              # Test files
```

## Key Files to Implement

### Phase 1 MVP - 優先順位順

#### 1. SSH接続基盤 (P1)

```
src/services/ssh/client.ts      # SSHクライアント
src/services/ssh/auth.ts        # 認証処理
src/types/connection.ts         # Connection型定義
```

#### 2. 接続管理 (P1)

```
src/stores/connectionStore.ts   # 接続設定の永続化
app/index.tsx                   # 接続一覧画面
app/connection/add.tsx          # 接続追加
app/connection/[id]/edit.tsx    # 接続編集
src/components/connection/      # UI components
```

#### 3. tmux操作 (P2)

```
src/services/tmux/commands.ts   # tmuxコマンド
src/services/tmux/parser.ts     # 出力パーサー
src/types/tmux.ts               # TmuxSession等の型
src/stores/sessionStore.ts      # セッション状態管理
```

#### 4. ターミナル表示 (P2)

```
src/services/ansi/parser.ts     # ANSIパーサー
src/components/terminal/TerminalView.tsx
src/stores/terminalStore.ts
```

#### 5. キー入力 (P2)

```
src/components/terminal/TerminalInput.tsx
src/components/terminal/SpecialKeys.tsx
```

## Development Commands

```bash
# 開発
pnpm start                 # Expo dev server
pnpm android               # Android実行
pnpm ios                   # iOS実行 (optional)

# 品質チェック
pnpm typecheck             # TypeScript型チェック
pnpm lint                  # ESLint
pnpm test                  # Jest tests

# ビルド
pnpm build:android         # APK/AAB生成
```

## Testing

### ユニットテスト

```bash
pnpm test                  # 全テスト
pnpm test -- --watch       # ウォッチモード
pnpm test -- src/services  # 特定ディレクトリ
```

### テストファイル命名規則

```
__tests__/
├── services/
│   ├── ssh/
│   │   └── client.test.ts
│   ├── tmux/
│   │   └── commands.test.ts
│   └── ansi/
│       └── parser.test.ts
└── components/
    └── terminal/
        └── TerminalView.test.tsx
```

## Key Patterns

### 1. SSHクライアント使用

```typescript
import { SSHClient } from '@/services/ssh/client';

const client = new SSHClient();
await client.connect(connection, { password });
const output = await client.exec('tmux list-sessions');
await client.disconnect();
```

### 2. tmuxコマンド実行

```typescript
import { TmuxCommands } from '@/services/tmux/commands';

const tmux = new TmuxCommands(sshClient);
const sessions = await tmux.listSessions();
await tmux.sendKeys('main', 0, 0, 'ls -la');
await tmux.sendKeys('main', 0, 0, 'Enter');
```

### 3. Zustand Store使用

```typescript
import { useConnectionStore } from '@/stores/connectionStore';

// コンポーネント内
const { connections, addConnection } = useConnectionStore();

// 非コンポーネント
const store = useConnectionStore.getState();
store.addConnection({ name: 'Server', host: '192.168.1.1', ... });
```

### 4. ANSIパース

```typescript
import { AnsiParser } from '@/services/ansi/parser';

const parser = new AnsiParser();
const spans = parser.parseLine('\x1b[32mgreen text\x1b[0m');
// [{ text: 'green text', fg: 2 }]
```

## Troubleshooting

### SSH接続エラー

1. ホスト/ポートが正しいか確認
2. ファイアウォール設定を確認
3. パスワード/鍵が正しいか確認

### tmuxが見つからない

```bash
# サーバー上で確認
which tmux
# インストールされていない場合
sudo apt install tmux  # Ubuntu/Debian
sudo yum install tmux  # CentOS/RHEL
```

### 日本語が文字化けする

1. フォント設定を確認（HackGen, PlemolJP推奨）
2. サーバー側のロケール設定を確認

## References

- [spec.md](./spec.md) - 機能仕様
- [plan.md](./plan.md) - 実装計画
- [research.md](./research.md) - 技術調査
- [data-model.md](./data-model.md) - データモデル
- [contracts/](./contracts/) - サービスインターフェース
