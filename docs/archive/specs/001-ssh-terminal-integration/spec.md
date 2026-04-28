# Feature Specification: SSH/Terminal統合機能

**Feature Branch**: `001-ssh-terminal-integration`
**Created**: 2026-01-11
**Status**: Draft
**Input**: User description: "SSH/Terminal統合機能を実装してください。terminal_screen.dartのTODOコメント(39行目と287行目)を解決し、既存のlib/services/ssh/ssh_client.dartとlib/services/tmux/tmux_commands.dartを使ってSSH接続→tmuxアタッチ→キー送信のパイプラインを完成させてください。"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - ターミナル画面でSSH接続確立 (Priority: P1)

ユーザーが接続一覧からサーバーを選択してターミナル画面に遷移すると、自動的にSSH接続が確立され、tmuxセッションの内容が表示される。

**Why this priority**: アプリの核となる機能。SSH接続なしではターミナル操作ができないため最優先。

**Independent Test**: ターミナル画面を開いた時点でSSH接続が確立され、リモートサーバーのtmuxセッション内容が画面に表示されることを確認できる。

**Acceptance Scenarios**:

1. **Given** 有効な接続設定が保存されている, **When** ユーザーがその接続をタップしてターミナル画面を開く, **Then** SSH接続が確立されtmuxセッション一覧が取得される
2. **Given** SSH接続が確立している, **When** tmuxセッションが存在する, **Then** 最初のセッションに自動アタッチしてターミナル出力が表示される
3. **Given** SSH接続が確立している, **When** tmuxセッションが存在しない, **Then** 新しいセッションが作成されてアタッチされる

---

### User Story 2 - キー入力送信 (Priority: P1)

ユーザーがターミナル画面でキーを入力すると、SSH経由でリモートサーバーのtmuxセッションにキーが送信され、結果が画面に反映される。

**Why this priority**: ターミナル操作の基本機能。接続だけでは操作できないため同等に重要。

**Independent Test**: 特殊キーバーのボタンやテキスト入力でキーを送信し、リモートのtmuxセッションに反映されることを確認できる。

**Acceptance Scenarios**:

1. **Given** tmuxセッションにアタッチしている, **When** ユーザーが特殊キーバーのESCボタンを押す, **Then** ESCキーがSSH経由で送信される
2. **Given** tmuxセッションにアタッチしている, **When** ユーザーがテキスト入力ダイアログからコマンドを入力する, **Then** コマンド文字列がSSH経由で送信され、結果が表示される
3. **Given** tmuxセッションにアタッチしている, **When** ユーザーがCtrl+Cを押す, **Then** 割り込みシグナルが送信される

---

### User Story 3 - ターミナル出力のリアルタイム表示 (Priority: P1)

リモートサーバーからのターミナル出力がリアルタイムでxterm画面に表示される。ANSIカラーコードも正しく解釈される。

**Why this priority**: 出力が見えなければターミナルとして機能しないため最優先。

**Independent Test**: リモートでコマンドを実行し、その出力がローカルのターミナル画面にリアルタイムで表示されることを確認できる。

**Acceptance Scenarios**:

1. **Given** tmuxセッションにアタッチしている, **When** リモートからデータが到着する, **Then** xtermウィジェットにリアルタイムで表示される
2. **Given** tmuxセッションにアタッチしている, **When** ANSIカラーコードを含む出力が到着する, **Then** 適切な色で表示される

---

### User Story 4 - 接続エラーハンドリング (Priority: P2)

接続失敗時やネットワーク切断時に、ユーザーに分かりやすいエラーメッセージを表示し、再接続オプションを提供する。

**Why this priority**: ユーザー体験に重要だが、基本機能が動作してから実装可能。

**Independent Test**: ネットワークを切断した状態で接続を試み、エラーメッセージが表示されることを確認できる。

**Acceptance Scenarios**:

1. **Given** 無効なホスト名で接続しようとする, **When** 接続タイムアウトが発生する, **Then** タイムアウトエラーメッセージが表示される
2. **Given** SSH接続中, **When** ネットワークが切断される, **Then** 切断通知が表示され再接続ボタンが表示される
3. **Given** 認証情報が間違っている, **When** 接続を試みる, **Then** 認証エラーメッセージが表示される

---

### User Story 5 - ターミナルリサイズ (Priority: P3)

デバイスの画面サイズ変更や回転に応じて、ターミナルサイズが自動調整される。

**Why this priority**: 機能的には重要だが、固定サイズでも基本操作は可能。

**Independent Test**: デバイスを回転させてターミナルサイズが変更されることを確認できる。

**Acceptance Scenarios**:

1. **Given** ターミナル画面を表示中, **When** デバイスを縦横回転する, **Then** ターミナルサイズが新しい画面サイズに合わせて更新される

---

### Edge Cases

- SSH接続中にアプリがバックグラウンドになった場合の動作
- 認証情報（パスワードまたはSSH鍵）がストレージから取得できない場合
- tmuxがリモートサーバーにインストールされていない場合
- セッションが別のクライアントから削除された場合

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: TerminalScreenはconnectionIdを受け取り、対応する接続情報を使用してSSH接続を確立できること
- **FR-002**: SSH接続確立後、tmuxセッション一覧を取得し、存在するセッションにアタッチできること
- **FR-003**: tmuxセッションが存在しない場合、新規セッションを作成してアタッチできること
- **FR-004**: SSHシェルからのデータ（stdout/stderr）をxtermウィジェットにリアルタイム表示できること
- **FR-005**: 特殊キーバーからのキー入力をSSH経由でリモートに送信できること
- **FR-006**: テキスト入力ダイアログからの入力をSSH経由でリモートに送信できること
- **FR-007**: ターミナル画面終了時にSSH接続を適切にクリーンアップできること
- **FR-008**: 接続エラー・認証エラー時にユーザーフレンドリーなエラーメッセージを表示できること
- **FR-009**: 画面サイズ変更時にtmuxのウィンドウサイズを同期できること

### Key Entities

- **Connection**: SSH接続情報（ホスト、ポート、ユーザー名、認証方式）
- **SshClient**: SSH接続を管理し、データ送受信を行う
- **TmuxSession**: tmuxセッション情報（名前、ウィンドウ一覧）
- **Terminal**: xtermウィジェットのバックエンド、ANSIシーケンス処理

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: ユーザーが接続タップから3秒以内にターミナル画面でリモート出力を確認できる（ネットワーク遅延除く）
- **SC-002**: キー入力から画面反映まで200ms以内の応答性を維持できる
- **SC-003**: 接続エラー発生時、5秒以内にユーザーにエラー内容が通知される
- **SC-004**: ANSIカラー256色が正しく表示される
- **SC-005**: ターミナル画面から戻った際、SSH接続リソースが確実に解放される

## Assumptions

- リモートサーバーにはtmuxがインストールされている（tmuxがない場合のフォールバックは将来対応）
- 認証情報（パスワードまたはSSH鍵）は既にflutter_secure_storageに保存されている
- ネットワーク接続は基本的に安定している（不安定なネットワークでの自動再接続は将来対応）
- ターミナルサイズの初期値は80列x24行を使用
