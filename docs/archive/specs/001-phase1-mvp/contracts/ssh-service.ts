/**
 * SSH Service Contract
 *
 * SSHクライアントサービスのインターフェース定義。
 * 実装は react-native-ssh-sftp をラップする。
 */

import type { Connection } from '../../../src/types/connection';

/**
 * SSH接続オプション
 */
export interface SSHConnectOptions {
  /** パスワード認証時のパスワード */
  password?: string;
  /** 鍵認証時の秘密鍵（PEM形式） */
  privateKey?: string;
  /** 秘密鍵のパスフレーズ */
  passphrase?: string;
}

/**
 * シェルオプション
 */
export interface ShellOptions {
  /** ターミナルタイプ */
  term?: string;
  /** カラム数 */
  cols?: number;
  /** 行数 */
  rows?: number;
}

/**
 * SSH接続状態イベント
 */
export interface SSHEvents {
  /** データ受信時 */
  onData: (data: string) => void;
  /** 接続クローズ時 */
  onClose: () => void;
  /** エラー発生時 */
  onError: (error: Error) => void;
}

/**
 * SSHクライアントインターフェース
 */
export interface ISSHClient {
  /**
   * SSH接続を確立する
   * @param connection 接続設定
   * @param options 認証オプション
   * @throws 接続失敗時
   */
  connect(connection: Connection, options: SSHConnectOptions): Promise<void>;

  /**
   * 接続を切断する
   */
  disconnect(): Promise<void>;

  /**
   * 接続中かどうか
   */
  isConnected(): boolean;

  /**
   * インタラクティブシェルを開始する
   * @param options シェルオプション
   */
  startShell(options?: ShellOptions): Promise<void>;

  /**
   * シェルにデータを書き込む
   * @param data 送信データ
   */
  write(data: string): Promise<void>;

  /**
   * ターミナルサイズを変更する
   * @param cols カラム数
   * @param rows 行数
   */
  resize(cols: number, rows: number): Promise<void>;

  /**
   * コマンドを実行して結果を取得する
   * @param command 実行コマンド
   * @returns コマンド出力
   */
  exec(command: string): Promise<string>;

  /**
   * イベントハンドラを設定する
   * @param events イベントハンドラ
   */
  setEventHandlers(events: Partial<SSHEvents>): void;
}

/**
 * SSHクライアントファクトリ
 */
export interface ISSHClientFactory {
  /**
   * 新しいSSHクライアントインスタンスを作成する
   */
  create(): ISSHClient;
}
