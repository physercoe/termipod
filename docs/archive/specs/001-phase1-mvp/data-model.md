# Data Model: MuxPod Phase 1 MVP

**Feature**: 001-phase1-mvp
**Date**: 2026-01-10

## Entity Relationship Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                         Persistence Layer                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌─────────────┐                                                 │
│  │ Connection  │ ──────────────────────────────────────────────┐ │
│  │ (AsyncStorage)                                              │ │
│  └──────┬──────┘                                               │ │
│         │                                                       │ │
│         │ has password (optional)                               │ │
│         ▼                                                       │ │
│  ┌─────────────┐                                               │ │
│  │  Password   │                                               │ │
│  │ (SecureStore)                                               │ │
│  └─────────────┘                                               │ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                         Runtime Layer                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│  ┌─────────────┐       ┌─────────────┐                          │
│  │ Connection  │ 1───* │ TmuxSession │                          │
│  │   State     │       └──────┬──────┘                          │
│  └─────────────┘              │                                  │
│                                │ 1                               │
│                                ▼ *                               │
│                         ┌─────────────┐                          │
│                         │ TmuxWindow  │                          │
│                         └──────┬──────┘                          │
│                                │ 1                               │
│                                ▼ *                               │
│                         ┌─────────────┐       ┌─────────────┐   │
│                         │  TmuxPane   │ 1───1 │ PaneContent │   │
│                         └─────────────┘       └─────────────┘   │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

## Entities

### Connection (永続化)

SSH接続設定を表す。AsyncStorageに保存される。

```typescript
interface Connection {
  id: string;                      // UUID v4
  name: string;                    // 表示名 (e.g., "Production Server")
  host: string;                    // ホスト名 or IPアドレス
  port: number;                    // SSHポート (default: 22)
  username: string;                // SSHユーザー名
  authMethod: 'password' | 'key';  // 認証方式
  keyId?: string;                  // SSH鍵ID (key認証時)
  timeout: number;                 // 接続タイムアウト秒 (default: 30)
  keepAliveInterval: number;       // Keepalive間隔秒 (default: 60)

  // メタ情報
  icon?: string;                   // カスタムアイコン名
  color?: string;                  // カード色 (#RRGGBB)
  tags?: string[];                 // タグ
  lastConnected?: number;          // 最終接続日時 (Unix timestamp ms)
  createdAt: number;               // 作成日時 (Unix timestamp ms)
  updatedAt: number;               // 更新日時 (Unix timestamp ms)
}
```

**Validation Rules**:
- `id`: 必須、UUID v4形式
- `name`: 必須、1-50文字
- `host`: 必須、有効なホスト名またはIPアドレス
- `port`: 必須、1-65535の整数
- `username`: 必須、1-32文字
- `authMethod`: 必須、'password' | 'key'
- `timeout`: 1-300の整数
- `keepAliveInterval`: 0-300の整数 (0 = 無効)

**Storage Key**: `muxpod-connections`

---

### ConnectionState (ランタイム)

接続のランタイム状態を表す。永続化されない。

```typescript
interface ConnectionState {
  connectionId: string;
  status: 'disconnected' | 'connecting' | 'connected' | 'error';
  error?: string;                  // エラーメッセージ
  latency?: number;                // RTT (ms)
  connectedAt?: number;            // 接続開始日時
}
```

**State Transitions**:
```
disconnected ──connect()──> connecting ──success──> connected
                              │                        │
                              └──failure──> error      │
                                              │        │
connected ──disconnect()──> disconnected <────┘        │
                  ▲                                    │
                  └────────network error───────────────┘
```

---

### TmuxSession (ランタイム)

tmuxセッションを表す。SSH経由で取得。

```typescript
interface TmuxSession {
  name: string;                    // セッション名 (unique per server)
  created: number;                 // 作成日時 (Unix timestamp ms)
  attached: boolean;               // 他クライアントがアタッチ中か
  windowCount: number;             // ウィンドウ数
  windows: TmuxWindow[];           // 所属ウィンドウ (lazy load)
}
```

**Source**: `tmux list-sessions -F "#{session_name}\t#{session_created}\t#{session_attached}\t#{session_windows}"`

---

### TmuxWindow (ランタイム)

tmuxウィンドウを表す。

```typescript
interface TmuxWindow {
  index: number;                   // ウィンドウインデックス (0-based)
  name: string;                    // ウィンドウ名
  active: boolean;                 // アクティブウィンドウか
  paneCount: number;               // ペイン数
  panes: TmuxPane[];               // 所属ペイン (lazy load)
}
```

**Source**: `tmux list-windows -t {session} -F "#{window_index}\t#{window_name}\t#{window_active}\t#{window_panes}"`

---

### TmuxPane (ランタイム)

tmuxペインを表す。

```typescript
interface TmuxPane {
  index: number;                   // ペインインデックス (0-based)
  id: string;                      // ペインID (%0, %1, etc.)
  active: boolean;                 // アクティブペインか
  currentCommand: string;          // 現在実行中のコマンド
  title: string;                   // ペインタイトル
  width: number;                   // 幅（カラム数）
  height: number;                  // 高さ（行数）
  cursorX: number;                 // カーソルX位置
  cursorY: number;                 // カーソルY位置
}
```

**Source**: `tmux list-panes -t {session}:{window} -F "#{pane_index}\t#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_title}\t#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}"`

---

### PaneContent (ランタイム)

ペインの表示内容を表す。ポーリングで更新。

```typescript
interface PaneContent {
  paneId: string;                  // 対応するペインID
  lines: AnsiLine[];               // 行ごとの内容（パース済み）
  scrollbackSize: number;          // スクロールバック行数
  cursorX: number;                 // カーソルX位置
  cursorY: number;                 // カーソルY位置
  lastUpdated: number;             // 最終更新日時
}

interface AnsiLine {
  spans: AnsiSpan[];               // 同一スタイルのテキスト断片
}

interface AnsiSpan {
  text: string;                    // テキスト内容
  fg?: number;                     // 前景色 (0-255, undefined=default)
  bg?: number;                     // 背景色 (0-255, undefined=default)
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  strikethrough?: boolean;
}
```

**Source**: `tmux capture-pane -t {session}:{window}.{pane} -p -e -S -1000`

---

## Store Structure

### connectionStore

```typescript
interface ConnectionStore {
  // Persisted
  connections: Connection[];

  // Runtime (not persisted)
  connectionStates: Map<string, ConnectionState>;
  activeConnectionId: string | null;

  // Actions
  addConnection: (conn: Omit<Connection, 'id' | 'createdAt' | 'updatedAt'>) => string;
  updateConnection: (id: string, updates: Partial<Connection>) => void;
  removeConnection: (id: string) => void;
  setConnectionState: (id: string, state: Partial<ConnectionState>) => void;
  setActiveConnection: (id: string | null) => void;
  getConnection: (id: string) => Connection | undefined;
}
```

### sessionStore

```typescript
interface SessionStore {
  // Runtime only
  sessions: Map<string, TmuxSession[]>;  // connectionId -> sessions
  selectedSession: string | null;        // session name
  selectedWindow: number | null;         // window index
  selectedPane: number | null;           // pane index

  // Actions
  setSessions: (connectionId: string, sessions: TmuxSession[]) => void;
  selectSession: (name: string) => void;
  selectWindow: (index: number) => void;
  selectPane: (index: number) => void;
  clearSelection: () => void;
}
```

### terminalStore

```typescript
interface TerminalStore {
  // Runtime only
  paneContents: Map<string, PaneContent>;  // paneId -> content

  // Actions
  setContent: (paneId: string, content: PaneContent) => void;
  appendLine: (paneId: string, line: AnsiLine) => void;
  clearContent: (paneId: string) => void;
}
```

---

## Data Flow

### 接続フロー

```
1. User taps connection card
   ↓
2. connectionStore.setConnectionState(id, { status: 'connecting' })
   ↓
3. Load password from SecureStore (if authMethod === 'password')
   ↓
4. SSHClient.connect(connection, password)
   ↓
5. On success:
   - connectionStore.setConnectionState(id, { status: 'connected' })
   - connectionStore.setActiveConnection(id)
   - Navigate to terminal screen
   ↓
6. TmuxCommands.listSessions()
   ↓
7. sessionStore.setSessions(connectionId, sessions)
```

### ターミナル更新フロー

```
1. useTerminal hook starts polling (100ms interval)
   ↓
2. TmuxCommands.capturePane(session, window, pane, { escape: true })
   ↓
3. AnsiParser.parse(rawOutput)
   ↓
4. Compare with previous content
   ↓
5. If changed:
   - terminalStore.setContent(paneId, newContent)
   - Component re-renders via Zustand subscription
```

---

## Indexes and Lookups

| Entity | Lookup | Key |
|--------|--------|-----|
| Connection | By ID | `id` (Map key) |
| Connection | By host | Linear search (small dataset) |
| ConnectionState | By connection ID | `connectionId` (Map key) |
| TmuxSession | By name | `name` (within connection's sessions) |
| TmuxWindow | By index | `index` (within session's windows) |
| TmuxPane | By index | `index` (within window's panes) |
| PaneContent | By pane ID | `paneId` (Map key) |
