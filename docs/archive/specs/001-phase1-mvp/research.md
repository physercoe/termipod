# Research: MuxPod Phase 1 MVP

**Feature**: 001-phase1-mvp
**Date**: 2026-01-10

## Research Topics

### 1. SSH接続ライブラリ選定

**Decision**: `react-native-ssh-sftp` を採用

**Rationale**:
- React Native向けに設計された唯一の成熟したSSHライブラリ
- パスワード認証・公開鍵認証の両方をサポート
- シェル接続（PTY）とコマンド実行の両方に対応
- iOS/Android両方で動作実績あり

**Alternatives Considered**:

| ライブラリ | 評価 | 却下理由 |
|-----------|------|----------|
| ssh2 (Node.js) | × | React Nativeでは動作しない |
| WebSocket経由 | × | サーバー側に追加コンポーネントが必要 |
| react-native-tcp | × | SSH実装を自前で行う必要がある |

**Implementation Notes**:
```typescript
// 基本的な接続パターン
import SSHClient from 'react-native-ssh-sftp';

const client = new SSHClient(host, port, username, {
  password: 'xxx', // or
  privateKey: 'xxx',
});

await client.connect();
const shell = await client.startShell('xterm-256color', { rows: 24, cols: 80 });
shell.on('data', (data: string) => { /* handle output */ });
shell.write('command\n');
```

---

### 2. ANSIエスケープシーケンス処理

**Decision**: カスタムパーサーを実装（軽量版）

**Rationale**:
- npmの`ansi-parser`等はNode.js依存が多い
- React Native環境で動作する軽量実装が必要
- 必要な機能は16色/256色表示のみ（Phase 1）

**Alternatives Considered**:

| アプローチ | 評価 | 却下理由 |
|-----------|------|----------|
| ansi-parser | × | Node.js依存 |
| xterm.js | × | DOM依存、React Native非対応 |
| strip-ansi | △ | 色情報が失われる |

**Implementation Pattern**:
```typescript
// ANSIパーサーの基本構造
interface AnsiSpan {
  text: string;
  fg?: number; // 0-255
  bg?: number; // 0-255
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
}

function parseAnsi(input: string): AnsiSpan[] {
  const ESC = '\x1b';
  const CSI = ESC + '[';
  // SGRシーケンス解析: \x1b[<params>m
  // 対応: 30-37 (fg), 40-47 (bg), 38;5;n (256色fg), 48;5;n (256色bg)
}
```

---

### 3. tmuxコマンド出力パース

**Decision**: タブ区切りフォーマット指定でパース

**Rationale**:
- tmuxの`-F`オプションでカスタムフォーマット指定可能
- タブ区切りにより確実なパースが可能
- 追加ライブラリ不要

**Command Patterns**:

```bash
# セッション一覧
tmux list-sessions -F "#{session_name}\t#{session_created}\t#{session_attached}\t#{session_windows}"

# ウィンドウ一覧
tmux list-windows -t SESSION -F "#{window_index}\t#{window_name}\t#{window_active}\t#{window_panes}"

# ペイン一覧
tmux list-panes -t SESSION:WINDOW -F "#{pane_index}\t#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_width}\t#{pane_height}"

# ペイン内容取得
tmux capture-pane -t SESSION:WINDOW.PANE -p -e  # -e でANSI保持
```

**Parser Pattern**:
```typescript
function parseTmuxOutput<T>(output: string, keys: (keyof T)[]): T[] {
  return output
    .trim()
    .split('\n')
    .filter(line => line.length > 0)
    .map(line => {
      const values = line.split('\t');
      return keys.reduce((obj, key, i) => {
        obj[key] = values[i];
        return obj;
      }, {} as T);
    });
}
```

---

### 4. 状態管理パターン（Zustand + 永続化）

**Decision**: Zustand + persist middleware + AsyncStorage

**Rationale**:
- Zustand 5.0は軽量でReact Nativeに最適
- persist middlewareで自動永続化
- 接続状態（ランタイム）と接続設定（永続化）を分離

**Implementation Pattern**:
```typescript
import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';
import AsyncStorage from '@react-native-async-storage/async-storage';

interface ConnectionStore {
  connections: Connection[];
  // Runtime state (not persisted)
  connectionStates: Map<string, ConnectionState>;

  addConnection: (conn: Omit<Connection, 'id'>) => void;
}

export const useConnectionStore = create<ConnectionStore>()(
  persist(
    (set, get) => ({
      connections: [],
      connectionStates: new Map(),
      addConnection: (conn) => set((state) => ({
        connections: [...state.connections, { ...conn, id: crypto.randomUUID() }],
      })),
    }),
    {
      name: 'muxpod-connections',
      storage: createJSONStorage(() => AsyncStorage),
      partialize: (state) => ({ connections: state.connections }), // Exclude runtime state
    }
  )
);
```

---

### 5. セキュアストレージ（パスワード保存）

**Decision**: expo-secure-store を使用

**Rationale**:
- ExpoのオフィシャルAPI
- Android Keystore / iOS Keychain を内部使用
- 同期API（使いやすい）

**Implementation Pattern**:
```typescript
import * as SecureStore from 'expo-secure-store';

// パスワード保存
await SecureStore.setItemAsync(`password-${connectionId}`, password);

// パスワード取得
const password = await SecureStore.getItemAsync(`password-${connectionId}`);

// パスワード削除
await SecureStore.deleteItemAsync(`password-${connectionId}`);
```

**Security Notes**:
- パスワードはConnectionオブジェクトに含めない
- 接続時にSecureStoreから取得
- アプリアンインストール時に自動削除される

---

### 6. ターミナル表示パフォーマンス

**Decision**: FlatList + メモ化 + 仮想化

**Rationale**:
- 1000行のスクロールバック履歴を効率的に表示
- FlatListの仮想化で画面外要素をアンマウント
- React.memoで再レンダリング最小化

**Implementation Pattern**:
```typescript
const TerminalLine = React.memo(({ line, spans }: { line: number; spans: AnsiSpan[] }) => {
  return (
    <Text style={styles.line}>
      {spans.map((span, i) => (
        <Text key={i} style={getSpanStyle(span)}>{span.text}</Text>
      ))}
    </Text>
  );
});

const TerminalView = ({ lines }: { lines: AnsiSpan[][] }) => {
  return (
    <FlatList
      data={lines}
      renderItem={({ item, index }) => <TerminalLine line={index} spans={item} />}
      keyExtractor={(_, index) => index.toString()}
      initialNumToRender={30}
      maxToRenderPerBatch={20}
      windowSize={10}
      inverted // 最新行を下に
    />
  );
};
```

---

### 7. 日本語・全角文字幅計算

**Decision**: East Asian Width対応のカスタム実装

**Rationale**:
- ターミナルでは全角文字は2カラム分
- Unicode East Asian Width プロパティに準拠

**Implementation Pattern**:
```typescript
// 簡易版: CJK範囲チェック
function getCharWidth(char: string): 1 | 2 {
  const code = char.charCodeAt(0);
  // CJK Unified Ideographs, Hiragana, Katakana, Fullwidth forms
  if (
    (code >= 0x4E00 && code <= 0x9FFF) || // CJK
    (code >= 0x3040 && code <= 0x30FF) || // Hiragana, Katakana
    (code >= 0xFF00 && code <= 0xFFEF)    // Fullwidth
  ) {
    return 2;
  }
  return 1;
}

function getStringWidth(str: string): number {
  return [...str].reduce((sum, char) => sum + getCharWidth(char), 0);
}
```

---

### 8. 特殊キー送信

**Decision**: tmux send-keysコマンドを使用

**Rationale**:
- tmuxのsend-keysは特殊キー名をサポート
- エスケープシーケンスを直接送る必要がない

**Key Mapping**:
```typescript
const SPECIAL_KEYS: Record<string, string> = {
  'Enter': 'Enter',
  'Escape': 'Escape',
  'Tab': 'Tab',
  'Backspace': 'BSpace',
  'Delete': 'DC',
  'Up': 'Up',
  'Down': 'Down',
  'Left': 'Left',
  'Right': 'Right',
  'Home': 'Home',
  'End': 'End',
  'PageUp': 'PPage',
  'PageDown': 'NPage',
};

// Ctrl+キー
function ctrlKey(key: string): string {
  return `C-${key.toLowerCase()}`;
}

// 送信例
await tmux.sendKeys(session, window, pane, 'C-c'); // Ctrl+C
await tmux.sendKeys(session, window, pane, 'Escape'); // ESC
```

---

## Summary of Decisions

| Topic | Decision | Key Benefit |
|-------|----------|-------------|
| SSH接続 | react-native-ssh-sftp | React Native対応の成熟ライブラリ |
| ANSIパース | カスタム軽量実装 | Node.js依存なし |
| tmux出力パース | タブ区切りフォーマット | 確実なパース |
| 状態管理 | Zustand + persist | 軽量 + 自動永続化 |
| パスワード保存 | expo-secure-store | OS標準のセキュアストレージ |
| ターミナル表示 | FlatList + 仮想化 | 1000行でも60fps |
| 文字幅 | East Asian Width対応 | 日本語正しく表示 |
| 特殊キー | tmux send-keys | キーマッピングが簡潔 |
