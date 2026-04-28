# Data Model: Terminal Width Auto-Resize

**Date**: 2026-01-11
**Feature**: Terminal Width Auto-Resize

## Entity Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                         AppSettings                         │
│  (既存 - 拡張)                                               │
├─────────────────────────────────────────────────────────────┤
│ + fontSize: double (既存)                                   │
│ + fontFamily: String (既存)                                 │
│ + minFontSize: double (新規) ─── Default: 8.0              │
│ + autoFitEnabled: bool (新規) ─── Default: true            │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ provides settings
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                   TerminalDisplayState                      │
│  (新規 - Riverpod State)                                    │
├─────────────────────────────────────────────────────────────┤
│ + paneWidth: int              ─── tmux pane_width (文字数)  │
│ + paneHeight: int             ─── tmux pane_height (行数)   │
│ + screenWidth: double         ─── 利用可能なスクリーン幅     │
│ + calculatedFontSize: double  ─── 計算されたフォントサイズ   │
│ + effectiveFontSize: double   ─── 適用されるフォントサイズ   │
│ + needsHorizontalScroll: bool ─── 水平スクロール必要か       │
│ + horizontalScrollOffset: double ─── 水平スクロール位置     │
│ + zoomScale: double           ─── ピンチズーム倍率 (1.0=等倍)│
│ + isZooming: bool             ─── ズーム操作中か            │
└─────────────────────────────────────────────────────────────┘
                              │
                              │ uses
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                      TmuxPane (既存)                        │
├─────────────────────────────────────────────────────────────┤
│ + index: int                                                │
│ + id: String                                                │
│ + active: bool                                              │
│ + width: int      ◄── pane_width (文字数)                   │
│ + height: int     ◄── pane_height (行数)                    │
│ + cursorX: int                                              │
│ + cursorY: int                                              │
└─────────────────────────────────────────────────────────────┘
```

## Entity Definitions

### AppSettings (既存 - 拡張)

アプリケーション全体の設定を管理する。

**新規フィールド**:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| minFontSize | double | 8.0 | 自動調整時の最小フォントサイズ (px) |
| autoFitEnabled | bool | true | ペイン幅に自動フィットするか |

**Validation Rules**:
- minFontSize: 4.0 <= value <= 24.0
- autoFitEnabled: boolean

**Persistence**: shared_preferences (既存パターン)

---

### TerminalDisplayState (新規)

ターミナル表示の動的状態を管理する。Riverpod の StateNotifier で管理。

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| paneWidth | int | 80 | tmux ペインの横幅（文字数） |
| paneHeight | int | 24 | tmux ペインの縦幅（行数） |
| screenWidth | double | 0.0 | 利用可能なスクリーン幅（ピクセル） |
| calculatedFontSize | double | 14.0 | 計算されたフォントサイズ |
| effectiveFontSize | double | 14.0 | 実際に適用されるフォントサイズ |
| needsHorizontalScroll | bool | false | 水平スクロールが必要か |
| horizontalScrollOffset | double | 0.0 | 水平スクロール位置 |
| zoomScale | double | 1.0 | ピンチズーム倍率 |
| isZooming | bool | false | ズーム操作中フラグ |

**Computed Properties**:

```dart
/// 実際に適用されるフォントサイズ
double get effectiveFontSize {
  if (isZooming) {
    return calculatedFontSize * zoomScale;
  }
  return max(calculatedFontSize, minFontSize);
}

/// 水平スクロールが必要か
bool get needsHorizontalScroll {
  return calculatedFontSize < minFontSize;
}

/// ターミナルの表示幅（ピクセル）
double get terminalWidth {
  return paneWidth * charWidth * effectiveFontSize;
}
```

**State Transitions**:

```
[Initial] ──────► [Pane Selected] ──────► [Font Calculated]
                        │                        │
                        │                        ▼
                        │               [Scroll Enabled if needed]
                        │
                        └────────► [Pinch Start] ──► [Zooming] ──► [Pinch End]
                                                                      │
                                                                      ▼
                                                              [Font Recalculated]
```

---

### TmuxPane (既存 - 変更なし)

tmux ペインの情報。既存の `width` と `height` フィールドを使用。

---

## Relationships

1. **AppSettings → TerminalDisplayState**: 設定値（minFontSize, autoFitEnabled）を提供
2. **TmuxPane → TerminalDisplayState**: pane_width, pane_height を提供
3. **TerminalDisplayState → TerminalView**: effectiveFontSize を提供

## State Flow

```
User selects pane
       │
       ▼
TmuxPane.width/height ──────────────────────┐
       │                                    │
       ▼                                    ▼
TerminalDisplayState.updatePane()    screenWidth (from LayoutBuilder)
       │                                    │
       ├────────────────────────────────────┘
       │
       ▼
FontCalculator.calculate(screenWidth, paneWidth, fontFamily)
       │
       ▼
calculatedFontSize = result
       │
       ▼
effectiveFontSize = max(calculatedFontSize, minFontSize)
       │
       ├─── if calculatedFontSize < minFontSize ──► needsHorizontalScroll = true
       │
       ▼
TerminalView rebuilds with new fontSize
```
