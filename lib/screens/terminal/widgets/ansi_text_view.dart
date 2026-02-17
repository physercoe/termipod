import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../providers/settings_provider.dart';
import '../../../services/terminal/ansi_parser.dart';
import '../../../services/terminal/font_calculator.dart';
import '../../../services/terminal/terminal_diff.dart';
import '../../../services/terminal/terminal_font_styles.dart';
import '../../../theme/design_colors.dart';

/// キー入力イベント
class KeyInputEvent {
  /// キーデータ（エスケープシーケンスまたは文字）
  final String data;

  /// 特殊キーかどうか
  final bool isSpecialKey;

  /// tmux形式のキー名（Enterの場合は'Enter'など）
  /// isSpecialKeyがtrueの場合に使用
  final String? tmuxKeyName;

  const KeyInputEvent({
    required this.data,
    this.isSpecialKey = false,
    this.tmuxKeyName,
  });
}

/// ターミナルの操作モード
enum TerminalMode {
  /// 通常モード（キー入力が有効）
  normal,

  /// コピペモード（テキスト選択が有効、キー入力は無効）
  copyPaste,
}

/// ANSIテキスト表示ウィジェット
///
/// capture-pane -e の出力をANSIカラー付きで表示する。
/// RichText/SelectableTextを使用し、xterm依存を排除。
class AnsiTextView extends ConsumerStatefulWidget {
  /// 表示するANSIテキスト
  final String text;

  /// ペインの文字幅
  final int paneWidth;

  /// ペインの文字高さ
  final int paneHeight;

  /// キー入力コールバック
  final void Function(KeyInputEvent)? onKeyInput;

  /// 背景色
  final Color backgroundColor;

  /// 前景色
  final Color foregroundColor;

  /// 操作モード
  final TerminalMode mode;

  /// ピンチズームが有効かどうか
  final bool zoomEnabled;

  /// ズームスケール変更時のコールバック
  final void Function(double scale)? onZoomChanged;

  /// 外部から渡される垂直スクロールコントローラー（オプション）
  final ScrollController? verticalScrollController;

  /// カーソルX位置（0-based）
  final int cursorX;

  /// カーソルY位置（0-based, ペイン上部基準）
  final int cursorY;

  /// ホールド+スワイプで矢印キー入力時のコールバック
  /// direction: 'Up', 'Down', 'Left', 'Right'
  final void Function(String direction)? onArrowSwipe;

  const AnsiTextView({
    super.key,
    required this.text,
    required this.paneWidth,
    required this.paneHeight,
    this.onKeyInput,
    this.backgroundColor = const Color(0xFF1E1E1E),
    this.foregroundColor = const Color(0xFFD4D4D4),
    this.mode = TerminalMode.normal,
    this.zoomEnabled = true,
    this.onZoomChanged,
    this.verticalScrollController,
    this.cursorX = 0,
    this.cursorY = 0,
    this.onArrowSwipe,
  });

  @override
  ConsumerState<AnsiTextView> createState() => AnsiTextViewState();
}

class AnsiTextViewState extends ConsumerState<AnsiTextView>
    with SingleTickerProviderStateMixin {
  final FocusNode _focusNode = FocusNode();
  final ScrollController _horizontalScrollController = ScrollController();
  ScrollController? _internalVerticalScrollController;

  /// キャレット点滅用コントローラー
  late final AnimationController _caretBlinkController;

  /// 使用する垂直スクロールコントローラー
  ScrollController get _verticalScrollController =>
      widget.verticalScrollController ?? _internalVerticalScrollController!;

  late AnsiParser _parser;

  /// 差分計算サービス
  final TerminalDiff _terminalDiff = TerminalDiff();

  /// 修飾キー状態
  bool _ctrlPressed = false;
  bool _altPressed = false;
  bool _shiftPressed = false;

  /// ホールド+スワイプ用の状態
  bool _isLongPressing = false;
  Offset? _longPressStartPosition;
  String? _lastSwipeDirection;
  static const double _swipeThreshold = 30.0;

  /// 現在のズームスケール
  double _currentScale = 1.0;

  /// ピンチズーム開始時のスケール
  double _baseScale = 1.0;

  /// パース済み行データキャッシュ（仮想スクロール用）
  List<ParsedLine>? _cachedParsedLines;
  String? _cachedText;
  double? _cachedFontSize;
  String? _cachedFontFamily;

  /// 行の高さ（仮想スクロールで固定高さを使用）
  double _lineHeight = 20.0;

  /// 最後の差分結果（適応型ポーリング用）
  DiffResult? _lastDiffResult;

  @override
  void initState() {
    super.initState();
    // 外部からScrollControllerが渡されていない場合は内部で作成
    if (widget.verticalScrollController == null) {
      _internalVerticalScrollController = ScrollController();
    }
    _parser = AnsiParser(
      defaultForeground: widget.foregroundColor,
      defaultBackground: widget.backgroundColor,
    );

    // 500ms周期で点滅（1秒で1サイクル）
    _caretBlinkController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 500),
    )..repeat(reverse: true);
  }

  @override
  void didUpdateWidget(AnsiTextView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foregroundColor != widget.foregroundColor ||
        oldWidget.backgroundColor != widget.backgroundColor) {
      _parser = AnsiParser(
        defaultForeground: widget.foregroundColor,
        defaultBackground: widget.backgroundColor,
      );
      // パーサーが変わったのでキャッシュを無効化
      _invalidateCache();
    }
  }

  /// キャッシュを無効化
  void _invalidateCache() {
    _cachedParsedLines = null;
    _cachedText = null;
    _cachedFontSize = null;
    _cachedFontFamily = null;
  }

  /// 行データを取得（キャッシュ使用・仮想スクロール用）
  List<ParsedLine> _getParsedLines({
    required double fontSize,
    required String fontFamily,
  }) {
    // 差分計算を実行
    _lastDiffResult = _terminalDiff.calculateDiff(widget.text);

    // キャッシュが有効かチェック
    if (_cachedParsedLines != null &&
        _cachedText == widget.text &&
        _cachedFontSize == fontSize &&
        _cachedFontFamily == fontFamily) {
      return _cachedParsedLines!;
    }

    // 新しくパースしてキャッシュ
    _cachedParsedLines = _parser.parseLines(widget.text);
    _cachedText = widget.text;
    _cachedFontSize = fontSize;
    _cachedFontFamily = fontFamily;

    // 行の高さを計算（fontSize * lineHeight係数）
    _lineHeight = fontSize * 1.4;

    return _cachedParsedLines!;
  }

  /// 最後の差分結果を取得（親ウィジェットから参照用）
  DiffResult? get lastDiffResult => _lastDiffResult;

  /// 推奨ポーリング間隔を取得（適応型ポーリング用）
  int get recommendedPollingInterval {
    if (_lastDiffResult == null) {
      return AdaptivePollingInterval.defaultInterval;
    }
    return AdaptivePollingInterval.calculateInterval(
      _lastDiffResult!.unchangedFrames,
      _lastDiffResult!.changeRatio,
    );
  }

  @override
  void dispose() {
    _caretBlinkController.dispose();
    _focusNode.dispose();
    _horizontalScrollController.dispose();
    // 内部で作成した場合のみ破棄
    _internalVerticalScrollController?.dispose();
    super.dispose();
  }

  /// ズームをリセット
  void resetZoom() {
    setState(() {
      _currentScale = 1.0;
      _baseScale = 1.0;
    });
    widget.onZoomChanged?.call(1.0);
  }

  // === ピンチズーム処理 ===

  void _onScaleStart(ScaleStartDetails details) {
    _baseScale = _currentScale;
  }

  void _onScaleUpdate(ScaleUpdateDetails details) {
    // 1本指のドラッグ（scale == 1.0）はスクロールに任せる
    if (details.scale == 1.0) return;

    final newScale = (_baseScale * details.scale).clamp(0.5, 5.0);
    if (newScale != _currentScale) {
      setState(() {
        _currentScale = newScale;
      });
      widget.onZoomChanged?.call(newScale);
    }
  }

  void _onScaleEnd(ScaleEndDetails details) {
    // ズーム終了時の処理（必要に応じて）
  }

  // === ホールド+スワイプ処理 ===

  void _onLongPressStart(LongPressStartDetails details) {
    setState(() {
      _isLongPressing = true;
      _longPressStartPosition = details.localPosition;
      _lastSwipeDirection = null;
    });
    HapticFeedback.lightImpact();
  }

  void _onLongPressMoveUpdate(LongPressMoveUpdateDetails details) {
    if (!_isLongPressing || _longPressStartPosition == null) return;

    final delta = details.localPosition - _longPressStartPosition!;
    String? direction;

    // 閾値を超えた方向を検出
    if (delta.dx.abs() > delta.dy.abs()) {
      // 水平方向
      if (delta.dx > _swipeThreshold) {
        direction = 'Right';
      } else if (delta.dx < -_swipeThreshold) {
        direction = 'Left';
      }
    } else {
      // 垂直方向
      if (delta.dy > _swipeThreshold) {
        direction = 'Down';
      } else if (delta.dy < -_swipeThreshold) {
        direction = 'Up';
      }
    }

    if (direction != null) {
      setState(() {
        _lastSwipeDirection = direction;
      });
      widget.onArrowSwipe?.call(direction);
      HapticFeedback.selectionClick();
      // 起点をリセットして連続スワイプ対応
      _longPressStartPosition = details.localPosition;
      // ハイライトを短時間後にリセット
      Future.delayed(const Duration(milliseconds: 150), () {
        if (mounted && _isLongPressing) {
          setState(() {
            _lastSwipeDirection = null;
          });
        }
      });
    }
  }

  void _onLongPressEnd(LongPressEndDetails details) {
    setState(() {
      _isLongPressing = false;
      _longPressStartPosition = null;
      _lastSwipeDirection = null;
    });
  }

  /// スワイプオーバーレイウィジェット
  Widget _buildSwipeOverlay() {
    return Center(
      child: AnimatedOpacity(
        opacity: _isLongPressing ? 1.0 : 0.0,
        duration: const Duration(milliseconds: 150),
        child: Container(
          width: 120,
          height: 120,
          decoration: BoxDecoration(
            color: Colors.black.withValues(alpha: 0.7),
            borderRadius: BorderRadius.circular(16),
          ),
          child: Stack(
            children: [
              // 上矢印
              Positioned(
                top: 8,
                left: 0,
                right: 0,
                child: Icon(
                  Icons.arrow_drop_up,
                  size: 40,
                  color: _lastSwipeDirection == 'Up'
                      ? Colors.amber
                      : Colors.white.withValues(alpha: 0.6),
                ),
              ),
              // 下矢印
              Positioned(
                bottom: 8,
                left: 0,
                right: 0,
                child: Icon(
                  Icons.arrow_drop_down,
                  size: 40,
                  color: _lastSwipeDirection == 'Down'
                      ? Colors.amber
                      : Colors.white.withValues(alpha: 0.6),
                ),
              ),
              // 左矢印
              Positioned(
                left: 8,
                top: 0,
                bottom: 0,
                child: Icon(
                  Icons.arrow_left,
                  size: 40,
                  color: _lastSwipeDirection == 'Left'
                      ? Colors.amber
                      : Colors.white.withValues(alpha: 0.6),
                ),
              ),
              // 右矢印
              Positioned(
                right: 8,
                top: 0,
                bottom: 0,
                child: Icon(
                  Icons.arrow_right,
                  size: 40,
                  color: _lastSwipeDirection == 'Right'
                      ? Colors.amber
                      : Colors.white.withValues(alpha: 0.6),
                ),
              ),
              // 中央の点
              Center(
                child: Container(
                  width: 12,
                  height: 12,
                  decoration: BoxDecoration(
                    color: Colors.white.withValues(alpha: 0.4),
                    shape: BoxShape.circle,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// 現在のズームスケールを取得
  double get currentScale => _currentScale;

  @override
  Widget build(BuildContext context) {
    final settings = ref.watch(settingsProvider);
    final isCopyPasteMode = widget.mode == TerminalMode.copyPaste;

    return LayoutBuilder(
      builder: (context, constraints) {
        // フォントサイズを決定
        late final double fontSize;
        late final bool needsHorizontalScroll;

        if (settings.autoFitEnabled) {
          // 自動フィット: 画面幅に合わせて計算
          final calcResult = FontCalculator.calculate(
            screenWidth: constraints.maxWidth,
            paneCharWidth: widget.paneWidth,
            fontFamily: settings.fontFamily,
            minFontSize: settings.minFontSize,
          );
          fontSize = calcResult.fontSize;
          needsHorizontalScroll = calcResult.needsScroll;
        } else {
          // 手動設定: settings.fontSizeを使用
          fontSize = settings.fontSize;
          // 水平スクロールの必要性を判定
          final terminalWidth = FontCalculator.calculateTerminalWidth(
            paneCharWidth: widget.paneWidth,
            fontSize: fontSize,
            fontFamily: settings.fontFamily,
          );
          needsHorizontalScroll = terminalWidth > constraints.maxWidth;
        }

        // ターミナル幅を計算
        final terminalWidth = FontCalculator.calculateTerminalWidth(
          paneCharWidth: widget.paneWidth,
          fontSize: fontSize,
          fontFamily: settings.fontFamily,
        );

        // 行データを取得（キャッシュ使用・仮想スクロール用）
        final parsedLines = _getParsedLines(
          fontSize: fontSize,
          fontFamily: settings.fontFamily,
        );

        // 仮想スクロール対応のListView.builder
        Widget listWidget = ListView.builder(
          controller: _verticalScrollController,
          padding: EdgeInsets.zero, // パディングを明示的にゼロにする
          physics: const ClampingScrollPhysics(),
          itemCount: parsedLines.length,
          // 固定の行高さを使用してスクロール計算を高速化
          itemExtent: _lineHeight,
          // RepaintBoundaryを自動追加
          addRepaintBoundaries: true,
          itemBuilder: (context, index) {
            final line = parsedLines[index];
            final textSpan = _parser.lineToTextSpan(
              line,
              fontSize: fontSize,
              fontFamily: settings.fontFamily,
            );

            // 各行のテキストウィジェット
            Widget lineWidget = Text.rich(
              textSpan,
              style: TerminalFontStyles.getTextStyle(
                settings.fontFamily,
                fontSize: fontSize,
                height: 1.4,
                color: widget.foregroundColor,
              ),
              textScaler: TextScaler.noScaling,
              maxLines: 1,
              softWrap: false,
              overflow: TextOverflow.visible,
            );

            // カーソルの描画処理
            // カーソル位置の行インデックスを計算
            // parsedLinesには履歴+可視領域が含まれる。
            // 末尾のpaneHeight分が可視領域となる。
            final int cursorLineIndex;
            if (parsedLines.length >= widget.paneHeight) {
              cursorLineIndex = parsedLines.length - widget.paneHeight + widget.cursorY;
            } else {
              // 行数がpaneHeight未満の場合は、単純にcursorYを使用（初期状態など）
              cursorLineIndex = widget.cursorY;
            }

            // 現在の行がカーソル位置と一致する場合、Stackでカーソルを重ねる
            if (index == cursorLineIndex &&
                widget.mode == TerminalMode.normal &&
                settings.showTerminalCursor) {
              // TextPainter.getOffsetForCaretを使用して、レンダリングエンジンが計算した正確なカーソル位置を取得
              double cursorLeft;
              double charWidth;

              // 行全体のテキストとスタイルを使用してTextPainterを作成
              final textSpanFull = _parser.lineToTextSpan(
                line,
                fontSize: fontSize,
                fontFamily: settings.fontFamily,
              );

              final painter = TextPainter(
                text: textSpanFull,
                textDirection: TextDirection.ltr,
                textScaler: TextScaler.noScaling,
              )..layout();

              // 行のプレーンテキストを取得
              final lineText = line.segments.map((s) => s.text).join();
              final lineTextLength = lineText.length;

              // 全角文字を考慮してカラム位置を文字オフセットに変換
              // tmuxのcursor_xはカラム位置（全角=2）だが、
              // TextPositionは文字オフセット（全角=1）を期待する
              final lineDisplayWidth = FontCalculator.getTextDisplayWidth(lineText);
              final charOffset = FontCalculator.columnToCharOffset(lineText, widget.cursorX);

              if (widget.cursorX <= lineDisplayWidth) {
                 // カーソルが行内にある場合、getOffsetForCaretで位置を取得
                 final offset = painter.getOffsetForCaret(
                   TextPosition(offset: charOffset),
                   Rect.zero,
                 );
                 cursorLeft = offset.dx;

                 // カーソル幅も現在の文字の位置から取得（次の文字までの幅）
                 // 行末の場合は標準幅を使用
                 if (charOffset < lineTextLength) {
                    final nextOffset = painter.getOffsetForCaret(
                      TextPosition(offset: charOffset + 1),
                      Rect.zero,
                    );
                    charWidth = nextOffset.dx - offset.dx;
                 } else {
                    charWidth = FontCalculator.measureCharWidth(settings.fontFamily, fontSize);
                 }
              } else {
                 // カーソルが行末より先にある場合（空行や行末以降のスペース）
                 // 行末の位置を取得し、超過分を加算
                 cursorLeft = painter.width;
                 charWidth = FontCalculator.measureCharWidth(settings.fontFamily, fontSize);
                 cursorLeft += (widget.cursorX - lineDisplayWidth) * charWidth;
              }

              lineWidget = Stack(
                clipBehavior: Clip.none,
                children: [
                  lineWidget,
                  AnimatedBuilder(
                    animation: _caretBlinkController,
                    builder: (context, child) {
                      // キャレットの高さを文字サイズに合わせる（行間を含めない）
                      final caretHeight = fontSize;
                      // 行内で垂直方向に中央寄せ
                      final caretTop = (_lineHeight - caretHeight) / 2;

                      return Positioned(
                        left: cursorLeft,
                        top: caretTop,
                        width: 2,
                        height: caretHeight,
                        child: Opacity(
                          opacity: _caretBlinkController.value, // フェードイン・アウト
                          child: Container(
                            color: DesignColors.primary,
                          ),
                        ),
                      );
                    },
                  ),
                ],
              );
            }

            // 固定幅コンテナ（水平スクロール用）
            if (needsHorizontalScroll) {
              lineWidget = SizedBox(
                width: terminalWidth,
                child: lineWidget,
              );
            }

            return lineWidget;
          },
        );

        // 水平スクロールが必要な場合
        if (needsHorizontalScroll) {
          listWidget = SingleChildScrollView(
            controller: _horizontalScrollController,
            scrollDirection: Axis.horizontal,
            physics: const ClampingScrollPhysics(),
            child: SizedBox(
              width: terminalWidth,
              height: constraints.maxHeight,
              child: listWidget,
            ),
          );
        }

        // ピンチズームが有効な場合、GestureDetector + Transform.scaleでズーム
        if (widget.zoomEnabled) {
          listWidget = GestureDetector(
            onScaleStart: _onScaleStart,
            onScaleUpdate: _onScaleUpdate,
            onScaleEnd: _onScaleEnd,
            child: Transform.scale(
              scale: _currentScale,
              alignment: Alignment.topLeft,
              child: listWidget,
            ),
          );
        }

        // コピペモードの場合はテキスト選択を有効化
        if (isCopyPasteMode) {
          return Container(
            color: widget.backgroundColor,
            child: SelectionArea(
              child: listWidget,
            ),
          );
        }

        // 通常モード：キーボード入力をハンドリング
        // ホールド+スワイプで矢印キー入力対応
        return Focus(
          focusNode: _focusNode,
          autofocus: true,
          onKeyEvent: _handleKeyEvent,
          child: GestureDetector(
            onTap: () => _focusNode.requestFocus(),
            onLongPressStart: _onLongPressStart,
            onLongPressMoveUpdate: _onLongPressMoveUpdate,
            onLongPressEnd: _onLongPressEnd,
            child: Stack(
              children: [
                Container(
                  color: widget.backgroundColor,
                  child: listWidget,
                ),
                // スワイプオーバーレイ
                if (_isLongPressing) _buildSwipeOverlay(),
              ],
            ),
          ),
        );
      },
    );
  }

  /// キーイベントをハンドリング
  KeyEventResult _handleKeyEvent(FocusNode node, KeyEvent event) {
    if (widget.onKeyInput == null) return KeyEventResult.ignored;

    if (event is KeyDownEvent || event is KeyRepeatEvent) {
      final key = event.logicalKey;

      // 修飾キーの状態を更新
      if (key == LogicalKeyboardKey.controlLeft ||
          key == LogicalKeyboardKey.controlRight) {
        _ctrlPressed = true;
        return KeyEventResult.handled;
      }
      if (key == LogicalKeyboardKey.altLeft ||
          key == LogicalKeyboardKey.altRight) {
        _altPressed = true;
        return KeyEventResult.handled;
      }
      if (key == LogicalKeyboardKey.shiftLeft ||
          key == LogicalKeyboardKey.shiftRight) {
        _shiftPressed = true;
        return KeyEventResult.handled;
      }

      // 特殊キーの処理
      String? data;
      bool isSpecialKey = false;
      String? tmuxKeyName;

      if (key == LogicalKeyboardKey.escape) {
        data = '\x1b';
        isSpecialKey = true;
        tmuxKeyName = 'Escape';
      } else if (key == LogicalKeyboardKey.enter) {
        // Shift+Enterの場合は別のキー名で送信
        if (_shiftPressed) {
          data = '\x1b[27;2;13~'; // xterm拡張: Shift+Enter
          isSpecialKey = true;
          tmuxKeyName = 'S-Enter';
          _shiftPressed = false;
        } else {
          data = '\r';
          isSpecialKey = true;
          tmuxKeyName = 'Enter';
        }
      } else if (key == LogicalKeyboardKey.backspace) {
        data = '\x7f';
        isSpecialKey = true;
        tmuxKeyName = 'BSpace';
      } else if (key == LogicalKeyboardKey.delete) {
        data = '\x1b[3~';
        isSpecialKey = true;
        tmuxKeyName = 'DC';
      } else if (key == LogicalKeyboardKey.tab) {
        data = '\t';
        isSpecialKey = true;
        tmuxKeyName = 'Tab';
      } else if (key == LogicalKeyboardKey.arrowUp) {
        data = _getArrowSequence('A');
        isSpecialKey = true;
        tmuxKeyName = _getArrowTmuxKey('Up');
      } else if (key == LogicalKeyboardKey.arrowDown) {
        data = _getArrowSequence('B');
        isSpecialKey = true;
        tmuxKeyName = _getArrowTmuxKey('Down');
      } else if (key == LogicalKeyboardKey.arrowRight) {
        data = _getArrowSequence('C');
        isSpecialKey = true;
        tmuxKeyName = _getArrowTmuxKey('Right');
      } else if (key == LogicalKeyboardKey.arrowLeft) {
        data = _getArrowSequence('D');
        isSpecialKey = true;
        tmuxKeyName = _getArrowTmuxKey('Left');
      } else if (key == LogicalKeyboardKey.home) {
        data = '\x1b[H';
        isSpecialKey = true;
        tmuxKeyName = 'Home';
      } else if (key == LogicalKeyboardKey.end) {
        data = '\x1b[F';
        isSpecialKey = true;
        tmuxKeyName = 'End';
      } else if (key == LogicalKeyboardKey.pageUp) {
        data = '\x1b[5~';
        isSpecialKey = true;
        tmuxKeyName = 'PPage';
      } else if (key == LogicalKeyboardKey.pageDown) {
        data = '\x1b[6~';
        isSpecialKey = true;
        tmuxKeyName = 'NPage';
      } else if (key == LogicalKeyboardKey.f1) {
        data = '\x1bOP';
        isSpecialKey = true;
        tmuxKeyName = 'F1';
      } else if (key == LogicalKeyboardKey.f2) {
        data = '\x1bOQ';
        isSpecialKey = true;
        tmuxKeyName = 'F2';
      } else if (key == LogicalKeyboardKey.f3) {
        data = '\x1bOR';
        isSpecialKey = true;
        tmuxKeyName = 'F3';
      } else if (key == LogicalKeyboardKey.f4) {
        data = '\x1bOS';
        isSpecialKey = true;
        tmuxKeyName = 'F4';
      } else if (key == LogicalKeyboardKey.f5) {
        data = '\x1b[15~';
        isSpecialKey = true;
        tmuxKeyName = 'F5';
      } else if (key == LogicalKeyboardKey.f6) {
        data = '\x1b[17~';
        isSpecialKey = true;
        tmuxKeyName = 'F6';
      } else if (key == LogicalKeyboardKey.f7) {
        data = '\x1b[18~';
        isSpecialKey = true;
        tmuxKeyName = 'F7';
      } else if (key == LogicalKeyboardKey.f8) {
        data = '\x1b[19~';
        isSpecialKey = true;
        tmuxKeyName = 'F8';
      } else if (key == LogicalKeyboardKey.f9) {
        data = '\x1b[20~';
        isSpecialKey = true;
        tmuxKeyName = 'F9';
      } else if (key == LogicalKeyboardKey.f10) {
        data = '\x1b[21~';
        isSpecialKey = true;
        tmuxKeyName = 'F10';
      } else if (key == LogicalKeyboardKey.f11) {
        data = '\x1b[23~';
        isSpecialKey = true;
        tmuxKeyName = 'F11';
      } else if (key == LogicalKeyboardKey.f12) {
        data = '\x1b[24~';
        isSpecialKey = true;
        tmuxKeyName = 'F12';
      } else if (event.character != null && event.character!.isNotEmpty) {
        // 通常文字
        data = event.character!;

        // Ctrl+文字の処理
        if (_ctrlPressed && data.length == 1) {
          final code = data.codeUnitAt(0);
          if ((code >= 0x61 && code <= 0x7a) ||
              (code >= 0x41 && code <= 0x5a)) {
            data = String.fromCharCode(code & 0x1f);
          }
        }

        // Alt+文字の処理
        if (_altPressed) {
          data = '\x1b$data';
        }
      }

      if (data != null) {
        widget.onKeyInput!(KeyInputEvent(
          data: data,
          isSpecialKey: isSpecialKey,
          tmuxKeyName: tmuxKeyName,
        ));
        return KeyEventResult.handled;
      }
    } else if (event is KeyUpEvent) {
      final key = event.logicalKey;

      // 修飾キーの解除
      if (key == LogicalKeyboardKey.controlLeft ||
          key == LogicalKeyboardKey.controlRight) {
        _ctrlPressed = false;
      } else if (key == LogicalKeyboardKey.altLeft ||
          key == LogicalKeyboardKey.altRight) {
        _altPressed = false;
      } else if (key == LogicalKeyboardKey.shiftLeft ||
          key == LogicalKeyboardKey.shiftRight) {
        _shiftPressed = false;
      }
    }

    return KeyEventResult.ignored;
  }

  /// 矢印キーのシーケンスを取得
  String _getArrowSequence(String code) {
    if (_shiftPressed) {
      return '\x1b[1;2$code';
    } else if (_ctrlPressed) {
      return '\x1b[1;5$code';
    } else if (_altPressed) {
      return '\x1b[1;3$code';
    }
    return '\x1b[$code';
  }

  /// 矢印キーのtmux形式キー名を取得
  String _getArrowTmuxKey(String direction) {
    if (_shiftPressed) {
      return 'S-$direction';
    } else if (_ctrlPressed) {
      return 'C-$direction';
    } else if (_altPressed) {
      return 'M-$direction';
    }
    return direction;
  }

  // === 修飾キートグル（外部からの制御用） ===

  void toggleCtrl() {
    setState(() {
      _ctrlPressed = !_ctrlPressed;
    });
    HapticFeedback.selectionClick();
  }

  void toggleAlt() {
    setState(() {
      _altPressed = !_altPressed;
    });
    HapticFeedback.selectionClick();
  }

  void toggleShift() {
    setState(() {
      _shiftPressed = !_shiftPressed;
    });
    HapticFeedback.selectionClick();
  }

  bool get ctrlPressed => _ctrlPressed;
  bool get altPressed => _altPressed;
  bool get shiftPressed => _shiftPressed;

  void resetModifiers() {
    setState(() {
      _ctrlPressed = false;
      _altPressed = false;
      _shiftPressed = false;
    });
  }

  // === スクロール制御 ===

  /// 一番下までスクロール
  void scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_verticalScrollController.hasClients) {
        _verticalScrollController.animateTo(
          _verticalScrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
      }
    });
  }

  /// 一番上までスクロール
  void scrollToTop() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_verticalScrollController.hasClients) {
        _verticalScrollController.animateTo(
          0,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
      }
    });
  }

  /// カーソル位置までスクロール
  void scrollToCaret() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      if (!_verticalScrollController.hasClients) return;

      final parsedLines = _cachedParsedLines;
      if (parsedLines == null || parsedLines.isEmpty) return;

      // カーソル行インデックスを計算（build内と同じロジック）
      final int cursorLineIndex;
      if (parsedLines.length >= widget.paneHeight) {
        cursorLineIndex =
            parsedLines.length - widget.paneHeight + widget.cursorY;
      } else {
        cursorLineIndex = widget.cursorY;
      }

      // カーソル行のスクロールオフセット
      final targetOffset = cursorLineIndex * _lineHeight;

      // ビューポート高さを考慮し、カーソル行が中央付近に来るよう調整
      final viewportHeight =
          _verticalScrollController.position.viewportDimension;
      final centeredOffset =
          targetOffset - (viewportHeight / 2) + (_lineHeight / 2);

      // 有効範囲にクランプ
      final maxExtent = _verticalScrollController.position.maxScrollExtent;
      final clampedOffset = centeredOffset.clamp(0.0, maxExtent);

      _verticalScrollController.animateTo(
        clampedOffset,
        duration: const Duration(milliseconds: 300),
        curve: Curves.easeOut,
      );
    });
  }
}
