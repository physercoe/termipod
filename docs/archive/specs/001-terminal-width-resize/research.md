# Research: Terminal Width Auto-Resize

**Date**: 2026-01-11
**Feature**: Terminal Width Auto-Resize
**Status**: Complete

## Research Questions

### RQ-1: xterm パッケージでのフォントサイズ動的変更

**Question**: xterm パッケージで実行時にフォントサイズを変更できるか？

**Findings**:
- `TerminalView` は `textStyle` パラメータで `TerminalStyle` を受け取る
- `TerminalStyle` は `fontSize` を含む（デフォルト14）
- フォントサイズを変更するには、新しい `TerminalStyle` を渡して Widget をリビルドする
- パフォーマンス: TerminalView のリビルドは軽量（内部でキャッシュされている）

**Decision**: `TerminalStyle` の `fontSize` を動的に変更してリビルドすることでフォントサイズを調整

**Rationale**: xterm パッケージの標準的な使用方法であり、追加の依存関係不要

**Alternatives Considered**:
- Transform.scale でスケーリング → テキストがぼやける、品質低下
- カスタムレンダラー → 複雑すぎる、xterm の内部実装に依存

---

### RQ-2: 等幅フォントの文字幅計算

**Question**: 画面幅とペイン文字数からフォントサイズを計算する方法

**Findings**:
- 等幅フォントでは、1文字の幅 = fontSize × 文字幅比率
- JetBrains Mono の文字幅比率 ≈ 0.6（実測値）
- 計算式: `fontSize = screenWidth / (paneWidth × charWidthRatio)`
- Flutter の `TextPainter` で正確な文字幅を測定可能

**Decision**: TextPainter で実際の文字幅を測定し、正確なフォントサイズを計算

**Rationale**: フォントファミリーが変わっても正確な計算が可能

**Code Example**:
```dart
double calculateFontSize(double screenWidth, int paneCharWidth, String fontFamily) {
  // TextPainter で 1 文字の幅を測定
  final painter = TextPainter(
    text: TextSpan(text: 'M', style: TextStyle(fontFamily: fontFamily, fontSize: 100)),
    textDirection: TextDirection.ltr,
  )..layout();

  final charWidthRatio = painter.width / 100;
  return screenWidth / (paneCharWidth * charWidthRatio);
}
```

---

### RQ-3: Flutter でのピンチジェスチャー実装

**Question**: ピンチジェスチャーでスムーズにズームする方法

**Findings**:
- `GestureDetector` の `onScaleStart/Update/End` でピンチを検出
- `InteractiveViewer` は使用しない（TerminalView との統合が複雑）
- ズーム中は `Transform.scale` で即座に表示し、終了後に正確なフォントサイズに切り替え

**Decision**: GestureDetector + state でピンチ倍率を管理し、ズーム終了時にフォントサイズを更新

**Rationale**: 60fps のスムーズなアニメーションが可能で、xterm との統合も容易

**Implementation Pattern**:
```dart
GestureDetector(
  onScaleStart: (details) => _startScale = _currentScale,
  onScaleUpdate: (details) {
    setState(() => _currentScale = _startScale * details.scale);
  },
  onScaleEnd: (details) {
    final newFontSize = (baseFontSize * _currentScale).clamp(minFontSize, maxFontSize);
    // フォントサイズを確定し、スケールをリセット
  },
  child: Transform.scale(
    scale: _currentScale,
    child: TerminalView(...),
  ),
)
```

---

### RQ-4: 水平スクロールの実装

**Question**: TerminalView で水平スクロールを有効にする方法

**Findings**:
- `TerminalView` 自体は水平スクロールをサポートしていない
- `SingleChildScrollView` で wrap することで水平スクロール可能
- スクロール位置は状態として保持し、ペイン切り替え時にリセット

**Decision**: `SingleChildScrollView` (horizontal) で TerminalView を wrap

**Rationale**: シンプルで Flutter 標準のスクロール動作と一貫性がある

**Code Structure**:
```dart
SingleChildScrollView(
  scrollDirection: Axis.horizontal,
  physics: needsHorizontalScroll ? null : NeverScrollableScrollPhysics(),
  child: SizedBox(
    width: terminalWidth,  // paneWidth * charWidth
    child: TerminalView(...),
  ),
)
```

---

### RQ-5: 既存の設定インフラストラクチャ

**Question**: 最小フォントサイズ設定を既存システムに統合する方法

**Findings**:
- `AppSettings` に `minFontSize` フィールドを追加
- `SettingsNotifier` に getter/setter を追加
- `shared_preferences` で永続化（既存パターンに従う）
- 設定画面に MinFontSizeDialog を追加

**Decision**: 既存の `settings_provider.dart` を拡張

**Rationale**: 既存パターンに従うことで一貫性を維持、コード重複を回避

---

## Technology Decisions Summary

| Topic | Decision | Confidence |
|-------|----------|------------|
| フォントサイズ変更 | TerminalStyle.fontSize を動的変更 | High |
| 文字幅計算 | TextPainter で実測 | High |
| ピンチズーム | GestureDetector + Transform.scale | High |
| 水平スクロール | SingleChildScrollView wrap | High |
| 設定永続化 | 既存 settings_provider 拡張 | High |

## Dependencies

- xterm ^4.0.0 (既存)
- flutter_riverpod ^3.1.0 (既存)
- shared_preferences ^2.5.4 (既存)
- 追加依存なし
