/**
 * tmux Service Contract
 *
 * tmuxコマンド実行サービスのインターフェース定義。
 * SSH経由でtmuxコマンドを実行し、結果をパースする。
 */

import type { TmuxSession, TmuxWindow, TmuxPane } from '../../../src/types/tmux';

/**
 * ペインキャプチャオプション
 */
export interface CapturePaneOptions {
  /** 開始行（負の値でスクロールバック） */
  start?: number;
  /** 終了行 */
  end?: number;
  /** ANSIエスケープシーケンスを保持するか */
  escape?: boolean;
}

/**
 * tmuxサービスインターフェース
 */
export interface ITmuxService {
  /**
   * セッション一覧を取得する
   * @returns セッション配列（空配列 = tmux未実行）
   */
  listSessions(): Promise<TmuxSession[]>;

  /**
   * 指定セッションのウィンドウ一覧を取得する
   * @param sessionName セッション名
   * @returns ウィンドウ配列
   * @throws セッションが存在しない場合
   */
  listWindows(sessionName: string): Promise<TmuxWindow[]>;

  /**
   * 指定ウィンドウのペイン一覧を取得する
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   * @returns ペイン配列
   * @throws セッション/ウィンドウが存在しない場合
   */
  listPanes(sessionName: string, windowIndex: number): Promise<TmuxPane[]>;

  /**
   * ペインの内容をキャプチャする
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   * @param paneIndex ペインインデックス
   * @param options キャプチャオプション
   * @returns 行配列（生テキスト、ANSIエスケープ含む）
   */
  capturePane(
    sessionName: string,
    windowIndex: number,
    paneIndex: number,
    options?: CapturePaneOptions
  ): Promise<string[]>;

  /**
   * ペインにキー入力を送信する
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   * @param paneIndex ペインインデックス
   * @param keys キー文字列（tmux形式: Enter, Escape, C-c 等）
   * @param literal リテラル送信するか（エスケープ解釈しない）
   */
  sendKeys(
    sessionName: string,
    windowIndex: number,
    paneIndex: number,
    keys: string,
    literal?: boolean
  ): Promise<void>;

  /**
   * ペインを選択する（アクティブにする）
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   * @param paneIndex ペインインデックス
   */
  selectPane(
    sessionName: string,
    windowIndex: number,
    paneIndex: number
  ): Promise<void>;

  /**
   * ウィンドウを選択する
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   */
  selectWindow(sessionName: string, windowIndex: number): Promise<void>;

  /**
   * ペインをリサイズする
   * @param sessionName セッション名
   * @param windowIndex ウィンドウインデックス
   * @param paneIndex ペインインデックス
   * @param width 幅（カラム数）
   * @param height 高さ（行数）
   */
  resizePane(
    sessionName: string,
    windowIndex: number,
    paneIndex: number,
    width: number,
    height: number
  ): Promise<void>;
}

/**
 * 特殊キーマッピング
 */
export const SPECIAL_KEYS = {
  Enter: 'Enter',
  Escape: 'Escape',
  Tab: 'Tab',
  Backspace: 'BSpace',
  Delete: 'DC',
  Up: 'Up',
  Down: 'Down',
  Left: 'Left',
  Right: 'Right',
  Home: 'Home',
  End: 'End',
  PageUp: 'PPage',
  PageDown: 'NPage',
  Insert: 'IC',
  F1: 'F1',
  F2: 'F2',
  F3: 'F3',
  F4: 'F4',
  F5: 'F5',
  F6: 'F6',
  F7: 'F7',
  F8: 'F8',
  F9: 'F9',
  F10: 'F10',
  F11: 'F11',
  F12: 'F12',
} as const;

/**
 * Ctrl+キーを生成する
 * @param key 英字キー (a-z)
 * @returns tmux形式のCtrlキー (C-a 等)
 */
export function ctrlKey(key: string): string {
  return `C-${key.toLowerCase()}`;
}

/**
 * Alt+キーを生成する
 * @param key キー
 * @returns tmux形式のAltキー (M-a 等)
 */
export function altKey(key: string): string {
  return `M-${key}`;
}
