import 'dart:async';
import 'dart:convert';

import 'package:dartssh2/dartssh2.dart';
import 'package:flutter/foundation.dart';

/// 持続的シェルセッション
///
/// コマンドを書き込み、マーカーで出力終了を検知して結果を返す。
/// チャネル開閉のオーバーヘッドを排除し、1 RTT程度でコマンド実行可能。
class PersistentShell {
  final SSHClient _sshClient;
  SSHSession? _session;

  /// マーカーのコアテキスト
  static const String _markerId = '7f3d8a2b';

  /// コマンド開始検知用マーカー（\x01プレフィックス/サフィックス付き）
  ///
  /// \x01（SOH制御文字）を含めることで、シェルのエコーバックテキスト内の
  /// リテラル文字列（`\x01`=4文字）と区別する。
  /// printfの実出力のみがバイト0x01を含むため、エコーバック内では一致しない。
  static const String _startMarker = '\x01###START_$_markerId###\x01';

  /// コマンド終了検知用マーカー
  static const String _endMarker = '\x01###END_$_markerId###\x01';

  /// printf用のマーカー文字列（シェルコマンド内で使用）
  static const String _printfStartMarker =
      r'\001###START_'
      '$_markerId'
      r'###\001';
  static const String _printfEndMarker =
      r'\001###END_'
      '$_markerId'
      r'###\001';

  /// 出力バッファ（バイト列として蓄積し、UTF-8マルチバイト境界分割を防ぐ）
  final _rawBuffer = <int>[];

  /// コマンド実行中のCompleter
  Completer<String>? _pendingCommand;

  /// シェルが開始されているかどうか
  bool get isStarted => _session != null && !_isClosed;

  /// セッション切断検知用
  bool _isClosed = false;

  /// stdoutサブスクリプション
  StreamSubscription<Uint8List>? _stdoutSubscription;
  StreamSubscription<Uint8List>? _stderrSubscription;

  PersistentShell(this._sshClient);

  /// シェルセッションを開始
  Future<void> start({Duration? timeout}) async {
    if (_session != null) {
      return; // すでに開始済み
    }

    final opening = _sshClient.execute('/bin/sh');
    _session = await opening.timeout(
      timeout ?? const Duration(seconds: 3),
      onTimeout: () {
        unawaited(opening.then((late) => late.close(), onError: (Object _) {}));
        throw PersistentShellError('Shell start timed out');
      },
    );
    if (_isClosed) {
      _session!.close();
      _session = null;
      throw PersistentShellError('Shell disposed');
    }
    _stdoutSubscription = _session!.stdout.listen(
      _onData,
      onDone: _onDone,
      onError: _onError,
    );
    _stderrSubscription = _session!.stderr.listen((_) {}, onError: _onError);
    // No interactive/login startup files, sleeps, prompts or PTY echo.
  }

  /// コマンドを実行して結果を取得
  ///
  /// [command] 実行するコマンド
  /// [timeout] タイムアウト（デフォルト: 5秒）
  /// 戻り値: コマンドの標準出力
  Future<String> exec(String command, {Duration? timeout}) async {
    if (_session == null) {
      throw PersistentShellError('Shell not started');
    }

    if (_isClosed) {
      throw PersistentShellError('Shell session is closed');
    }

    if (_pendingCommand != null && !_pendingCommand!.isCompleted) {
      throw PersistentShellError('Another command is already running');
    }

    _pendingCommand = Completer<String>();
    _rawBuffer.clear();

    // printfでマーカーを出力（\x01バイトを含む）
    // echoではなくprintfを使用: シェルのエコーバック内ではリテラル'\x01'（4文字）が
    // 表示されるが、printfの実出力はバイト0x01を含む。
    // これによりエコーバック内のマーカーと実出力のマーカーを確実に区別できる。
    final commandWithMarkers =
        "printf '$_printfStartMarker\\n'; $command; printf '$_printfEndMarker\\n'\n";
    _session!.write(utf8.encode(commandWithMarkers));

    // タイムアウト付きで結果を待機
    final effectiveTimeout = timeout ?? const Duration(seconds: 5);
    try {
      return await _pendingCommand!.future.timeout(effectiveTimeout);
    } on TimeoutException {
      _pendingCommand = null;
      await dispose();
      throw PersistentShellError(
        'Command execution timed out',
        mayHaveExecuted: true,
      );
    }
  }

  /// stdout受信時の処理
  void _onData(Uint8List data) {
    // 待機中のコマンドがない、または完了済みの場合は無視
    final pending = _pendingCommand;
    if (pending == null || pending.isCompleted) {
      return;
    }

    // デバッグ: UTF-8境界分割の検出（debugビルドのみ）
    assert(() {
      final chunkDecoded = utf8.decode(data, allowMalformed: true);
      if (chunkDecoded.contains('\uFFFD')) {
        final lastBytes = data.length > 6
            ? data.sublist(data.length - 6)
            : data;
        debugPrint(
          '[PersistentShell] UTF-8 boundary split detected!'
          ' chunk_size=${data.length}'
          ' last_bytes=${lastBytes.map((b) => '0x${b.toRadixString(16).padLeft(2, '0')}').join(' ')}',
        );
      }
      return true;
    }());

    // バイト列として蓄積（チャンク単位デコードによるUTF-8境界分割を防止）
    _rawBuffer.addAll(data);

    // 蓄積したバイト列全体を一度にデコード
    final content = utf8.decode(_rawBuffer, allowMalformed: true);

    // 開始マーカーと終了マーカーの両方が揃っているかチェック
    final startIndex = content.indexOf(_startMarker);
    final endIndex = content.indexOf(_endMarker);

    if (startIndex != -1 && endIndex != -1 && endIndex > startIndex) {
      // 開始マーカーの次の行から終了マーカーの前までを抽出
      final startPos = startIndex + _startMarker.length;
      var result = content.substring(startPos, endIndex);

      // PTYの出力変換で\r\nや\rが使われる場合があるため正規化
      // 事実: macOS PTYではnewlines=0, CRs=19（\nが\rに変換されている）
      result = result.replaceAll('\r\n', '\n').replaceAll('\r', '\n');

      // 先頭と末尾の改行を削除
      if (result.startsWith('\n')) {
        result = result.substring(1);
      }
      if (result.endsWith('\n')) {
        result = result.substring(0, result.length - 1);
      }

      // Completerを先にnullにしてから完了（再入防止）
      _pendingCommand = null;
      _rawBuffer.clear();
      pending.complete(result);
    }
  }

  /// セッション終了時の処理
  void _onDone() {
    _isClosed = true;
    if (_pendingCommand != null && !_pendingCommand!.isCompleted) {
      _pendingCommand!.completeError(
        PersistentShellError('Shell session closed', mayHaveExecuted: true),
      );
    }
  }

  /// エラー発生時の処理
  void _onError(Object error) {
    _isClosed = true;
    if (_pendingCommand != null && !_pendingCommand!.isCompleted) {
      _pendingCommand!.completeError(
        PersistentShellError('Shell error: $error', mayHaveExecuted: true),
      );
    }
  }

  /// シェルセッションを再起動
  ///
  /// セッションが切断された場合に呼び出す
  Future<void> restart() async {
    await dispose();
    _isClosed = false;
    await start();
  }

  /// リソースを解放
  Future<void> dispose() async {
    _isClosed = true;

    if (_pendingCommand != null && !_pendingCommand!.isCompleted) {
      _pendingCommand!.completeError(
        PersistentShellError('Shell disposed', mayHaveExecuted: true),
      );
    }
    _pendingCommand = null;

    await _stdoutSubscription?.cancel();
    _stdoutSubscription = null;
    await _stderrSubscription?.cancel();
    _stderrSubscription = null;

    _session?.close();
    _session = null;

    _rawBuffer.clear();
  }
}

/// PersistentShellのエラー
class PersistentShellError implements Exception {
  final String message;

  final bool mayHaveExecuted;

  PersistentShellError(this.message, {this.mayHaveExecuted = false});

  @override
  String toString() => 'PersistentShellError: $message';
}
