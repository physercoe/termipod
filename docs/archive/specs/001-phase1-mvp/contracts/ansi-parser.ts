/**
 * ANSI Parser Contract
 *
 * ANSIエスケープシーケンスをパースし、スタイル付きテキストに変換する。
 */

/**
 * テキストスパン（同一スタイルのテキスト断片）
 */
export interface AnsiSpan {
  /** テキスト内容 */
  text: string;
  /** 前景色 (0-255, undefined=デフォルト) */
  fg?: number;
  /** 背景色 (0-255, undefined=デフォルト) */
  bg?: number;
  /** 太字 */
  bold?: boolean;
  /** 薄字 */
  dim?: boolean;
  /** イタリック */
  italic?: boolean;
  /** 下線 */
  underline?: boolean;
  /** 点滅 */
  blink?: boolean;
  /** 反転 */
  inverse?: boolean;
  /** 非表示 */
  hidden?: boolean;
  /** 取り消し線 */
  strikethrough?: boolean;
}

/**
 * パース済み行
 */
export interface AnsiLine {
  /** スパン配列 */
  spans: AnsiSpan[];
}

/**
 * ANSIパーサーインターフェース
 */
export interface IAnsiParser {
  /**
   * ANSIエスケープシーケンスを含む行をパースする
   * @param line 生テキスト
   * @returns パース済みスパン配列
   */
  parseLine(line: string): AnsiSpan[];

  /**
   * 複数行をパースする
   * @param lines 生テキスト行配列
   * @returns パース済み行配列
   */
  parseLines(lines: string[]): AnsiLine[];

  /**
   * ANSIエスケープシーケンスを削除する
   * @param text ANSIシーケンスを含むテキスト
   * @returns プレーンテキスト
   */
  stripAnsi(text: string): string;
}

/**
 * 16色パレット（標準）
 */
export const ANSI_16_COLORS = {
  // 標準色 (30-37, 40-47)
  0: '#000000', // Black
  1: '#CC0000', // Red
  2: '#00CC00', // Green
  3: '#CCCC00', // Yellow
  4: '#0000CC', // Blue
  5: '#CC00CC', // Magenta
  6: '#00CCCC', // Cyan
  7: '#CCCCCC', // White
  // 明るい色 (90-97, 100-107)
  8: '#666666',  // Bright Black
  9: '#FF0000',  // Bright Red
  10: '#00FF00', // Bright Green
  11: '#FFFF00', // Bright Yellow
  12: '#0000FF', // Bright Blue
  13: '#FF00FF', // Bright Magenta
  14: '#00FFFF', // Bright Cyan
  15: '#FFFFFF', // Bright White
} as const;

/**
 * 256色を16進数カラーコードに変換する
 * @param colorIndex 0-255
 * @returns #RRGGBB形式の色
 */
export function ansi256ToHex(colorIndex: number): string {
  if (colorIndex < 16) {
    return ANSI_16_COLORS[colorIndex as keyof typeof ANSI_16_COLORS];
  }

  if (colorIndex < 232) {
    // 6x6x6 色キューブ (16-231)
    const index = colorIndex - 16;
    const r = Math.floor(index / 36);
    const g = Math.floor((index % 36) / 6);
    const b = index % 6;
    const toHex = (v: number) => (v === 0 ? 0 : 55 + v * 40).toString(16).padStart(2, '0');
    return `#${toHex(r)}${toHex(g)}${toHex(b)}`;
  }

  // グレースケール (232-255)
  const gray = (colorIndex - 232) * 10 + 8;
  const hex = gray.toString(16).padStart(2, '0');
  return `#${hex}${hex}${hex}`;
}

/**
 * テーマ定義（ターミナルカラー）
 */
export interface TerminalTheme {
  /** 背景色 */
  background: string;
  /** 前景色（デフォルト） */
  foreground: string;
  /** カーソル色 */
  cursor: string;
  /** 選択色 */
  selection: string;
  /** 16色パレット */
  palette: readonly [
    string, string, string, string, string, string, string, string,
    string, string, string, string, string, string, string, string
  ];
}

/**
 * Draculaテーマ
 */
export const DRACULA_THEME: TerminalTheme = {
  background: '#282A36',
  foreground: '#F8F8F2',
  cursor: '#F8F8F2',
  selection: '#44475A',
  palette: [
    '#21222C', '#FF5555', '#50FA7B', '#F1FA8C',
    '#BD93F9', '#FF79C6', '#8BE9FD', '#F8F8F2',
    '#6272A4', '#FF6E6E', '#69FF94', '#FFFFA5',
    '#D6ACFF', '#FF92DF', '#A4FFFF', '#FFFFFF',
  ],
};
