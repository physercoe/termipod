<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>モバイル SSH ターミナル — tmux と AI コーディングエージェント対応。</b><br>
  <sub>スマートフォンやタブレットからリモートサーバーを管理。Claude Code、Codex などの CLI ツールを、タッチ最適化されたターミナルで実行。<br>Android、iOS、iPadOS — 単一の Flutter コードベース。</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/termipod/releases"><img src="https://img.shields.io/github/v/release/physercoe/termipod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/termipod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Android">
  <img src="https://img.shields.io/badge/iOS-000000?style=flat-square&logo=apple&logoColor=white" alt="iOS">
  <img src="https://img.shields.io/badge/iPadOS-000000?style=flat-square&logo=apple&logoColor=white" alt="iPadOS">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.md">English</a> &nbsp;|&nbsp;
  <a href="README.zh.md">中文</a>
</p>

---

## スクリーンショット

<table>
<tr>
<td align="center"><b>ダッシュボード</b></td>
<td align="center"><b>エージェントコマンド</b></td>
<td align="center"><b>キーパレット</b></td>
</tr>
<tr>
<td><img src="docs/screens/dashboard_dark.png" width="240" alt="ダッシュボード"></td>
<td><img src="docs/screens/bolt_menu_dark.png" width="240" alt="Claude Code スラッシュコマンド"></td>
<td><img src="docs/screens/key_palette_dark.png" width="240" alt="プロファイルシート"></td>
</tr>
<tr>
<td align="center"><b>ターミナル</b></td>
<td align="center"><b>Vault（鍵・スニペット）</b></td>
<td align="center"><b>挿入メニュー</b></td>
</tr>
<tr>
<td><img src="docs/screens/terminal_dark.png" width="240" alt="アクションバー付きターミナル"></td>
<td><img src="docs/screens/vault_dark.png" width="240" alt="SSH 鍵、スニペット、コマンド履歴"></td>
<td><img src="docs/screens/insert_menu_dark.png" width="240" alt="挿入メニュー"></td>
</tr>
</table>

---

## TermiPod とは？

一般的な SSH アプリが生のターミナルと小さなキーボードを提供するだけなのに対し、TermiPod はモバイルでのターミナル利用の実態に合わせて設計されています：

- **サーバー側の追加セットアップ不要** — `sshd` が動いていればそのまま使えます。エージェントもデーモンも、サーバー側にインストールするものは一切なし
- **tmux セッションの視覚的ナビゲーション** — セッション、ウィンドウ、ペインをタップで切り替え
- **ダッシュボードからワンタップ再接続** — 最終アクセス順にソートされた最近のセッション。タップで前回の window / pane に直行
- **AI コーディングエージェントの実行**（Claude Code、Codex、Aider）— 事前設定されたボタンレイアウトと構造化スラッシュコマンド
- **ペイン毎のプロファイル** — 各 tmux ペインが独自のアクションバーレイアウトを記憶
- **カスタムキーボード** — Ctrl/Alt/Esc/矢印を組み込んだ Flutter ネイティブ QWERTY
- **ファイル転送** — SFTP でサーバーとファイルのアップロード・ダウンロード
- **踏み台サーバー/プロキシ対応** — SSH ProxyJump と SOCKS5 プロキシ
- **不安定回線でも切れない** — 指数バックオフによる自動再接続、切断中の入力もキューに溜めて復帰時に送信

### 対象ユーザー

| | |
|---|---|
| **AI エージェントユーザー** | tmux で Claude Code / Codex を実行、スマホから監視・操作 |
| **開発者** | 開発マシン、CI サーバー、クラウド VM に SSH 接続 |
| **DevOps/SRE** | 外出中にサービスの確認、ログ確認、プロセス再起動 |
| **ホームラボ愛好者** | スマホからサーバー、Raspberry Pi、NAS を管理 |

---

## 機能

### SSH・接続
- **Ed25519/RSA 鍵** — デバイス上で生成（RSA は 2048 / 3072 / 4096 ビット選択可）またはインポート。Android Keystore / iOS Keychain に暗号化保存、パスフレーズ任意、公開鍵はワンタップでクリップボードへ
- **SSH ProxyJump（踏み台サーバー）** — 踏み台経由で内部ネットワークのマシンに接続
- **SOCKS5 プロキシ** — 企業プロキシ、VPN、Shadowsocks/Clash 経由で SSH 接続
- **Raw PTY モード** — tmux なしのサーバーへ直接シェルアクセス。tmux 接続カードからもワンタップで起動可能
- **接続テスト** — 保存前に SSH + tmux の可用性を確認
- **指数バックオフ付き自動再接続** — 最大 5 回リトライ。切断中に入力したコマンドはキューに保持され、復帰時に自動送信
- **レイテンシインジケーター** — ヘッダーにリアルタイム ping を表示（緑 &lt; 100 ms、赤 &gt; 500 ms）。ラグの原因が指か回線か一目でわかります
- **アダプティブポーリング** — 操作中は 50 ms、アイドル時は 500 ms まで段階的に低速化してバッテリーを節約
- **バックグラウンド接続サービス** — Android のフォアグラウンドサービスで SSH を維持。長時間セッション向けに画面オン保持オプションも

### tmux セッション管理
- **ダッシュボード** — 最近のセッションを最終アクセス順に表示、相対時刻（"たった今"、"5分前"）。タップで前回の window + pane を復元してワンタップ再接続
- **視覚的ナビゲーション** — ブレッドクラムヘッダーでセッション/ウィンドウ/ペインを切り替え
- **ペインレイアウト表示** — 分割ペインの正確な比率表示。タップで該当ペインへフォーカス
- **2本指スワイプ** — tmux 分割ペイン間のナビゲーション
- **ピンチでズーム** — ターミナル表示を 50%〜500% で拡大縮小
- **コピー / スクロールモード** — 切り替えると画面が固定されてテキスト選択中もスクロールジャンプせず、終了時に選択範囲をシステムクリップボードへコピー
- **セッション・ウィンドウの作成/名前変更/閉じる**
- **Bell / Activity / Silence アラート** — 全接続の tmux ウィンドウフラグを監視。アラートをタップすると該当 window / pane へ直接ジャンプ（アラートは自動クリア）
- **256 色 ANSI** ターミナルレンダリング + 自動スクロールバック拡張

### 入力 UX（モバイル最適化）

| コンポーネント | 機能 |
|-------------|------|
| **アクションバー** | プロファイル毎のスワイプ可能なボタングループ — ESC、Tab、Ctrl+C がワンタップ |
| **コンポーズバー** | 送信ボタン付き複数行テキスト入力。複数行入力は **ブラケットペースト**として一括送信され、AI エージェントやシェルがブロックを丸ごと受け取れます（行ごとの個別実行になりません）。長押し送信で Enter 省略 |
| **ダイレクト入力モード** | ライブインジケーター付きのリアルタイムキーストリーミング。打鍵がそのまま pty に流れるので vim、less、htop、REPL に最適 |
| **カスタムキーボード** | Ctrl/Alt/Esc/矢印付き Flutter ネイティブ QWERTY。コンポーズ行の余白に **ライブキーストリップ**（Home / End / PgUp / PgDn / Del + パルスインジケーター）を内蔵。ナビパッド／ジョイスティック有効時は矢印行を自動非表示。CJK／音声入力時は全体オフ |
| **ナビゲーションパッド** | D-pad、ジョイスティック、ジェスチャーサーフェス |
| **スニペット** | 列挙型はドロップダウン、自由入力はテキストフィールドのスラッシュコマンド。**Bolt キー長押し**で現在のコンポーズ内容を下書きスニペットとして保存 |
| **修飾キー** | Ctrl/Alt — タップでアーム、ダブルタップでロック |

**4つの内蔵プロファイル** — Claude Code、Codex、汎用ターミナル、tmux。カスタムプロファイルも作成可能。

### ファイル転送
- **SFTP アップロード/ダウンロード** — 進捗表示、リモートディレクトリ閲覧
- **画像転送** — フォーマット変換、リサイズ、パス注入対応

### Termipod Hub（オプション）

複数の AI コーディングエージェントを複数マシンにまたがって運用するための調整レイヤーです。**設定 → Hub** で hub URL と bearer トークンを貼り付けると、下部ナビの **Inbox** タブと **Hub** タブが有効化されます。

**リサーチデモのワークフロー：** スマホでプロジェクトの方針を書く → スチュワード エージェントがそれをプランに分解 → ワーカーが GPU ホスト上でクロスホスト A2A 経由の並列 runs を実行 → ブリーフィング エージェントが夜間にレビュー可能なドキュメントへまとめる。各ステップはスマホに浮上するので、ユーザーは「操作する」のではなく「承認・レビューする」立場で関わります。ablation sweep・論文再現・ベンチマーク比較のテンプレートは hub に同梱されています。

**Inbox** — 承認待ち / エージェント状態（アイドル・エラー）/ メッセージ / SSH セッションを一つのフィードに統合し、チップで絞り込み可能。承認はその場で Approve / Reject、ルーペから全イベント横断の全文検索へ。

**Hub** — 4 つのサブタブ：
- **Projects** — Linear スタイルのプロジェクト詳細。Activity / Tasks / Plans / Runs / Reviews / Agents / Docs / Blobs / Info のピル切替。Activity は SSE でチャットをストリーム、Tasks はステータスでフィルタ可能なカンバン（本文プレビューとマークダウン表示付き）、Plans はステップ仕様を構造化表示してプロンプト/コマンドをコードブロックで描画、Runs は経過時間とマークダウン整形されたサマリーを表示、Reviews はプロジェクトで絞り込み可能、Docs はプロジェクトの `docs_root` をマークダウンビューアで閲覧、Blobs は端末ローカルにキャッシュしたアップロードを任意のチャットへ共有。プロジェクトの名前 / ゴール / テンプレート / docs root / 予算はその場で編集可能
- **Agents** — リスト表示／ツリー表示を切替。ツリーは `agent_spawns` を辿って親→子のオーガチャートを描画。FAB から YAML の **Spawn Agent** フォーム（テンプレート選択・ホスト選択・保存済みプリセット）。終了済みエージェントはアーカイブして、spawn spec とジャーナル付きの墓碑スクリーンに退避可能。**trackio / wandb / TensorBoard** のメトリクスダイジェストは run 詳細画面のインライン スパークラインで自動表示
- **Hosts** — host-runner のチェックイン状況と last-seen。NAT 背後のホストはエージェントカードを hub ディレクトリに公開し、**逆トンネルリレー**経由でピア A2A 呼び出しを受け取るので、VPS 上のスチュワード エージェントが GPU マシンのワーカーをエンドツーエンドで呼び出せます
- **Templates** — チーム全体のエージェント / プロンプト / ポリシー YAML。挙動を決めるもの — プロジェクトテンプレート・エージェントのスキル・ランチャーコマンド — はすべてディスク上で編集可能なデータです。新しいエージェント種を追加するのにコード変更は不要

**Team** 画面（Hub ヘッダーのアイコンから起動） — メンバー、ポリシー、チーム範囲のチャネル（`#hub-meta` のスチュワードルームへは AppBar のチップから）、そして **Settings** に cron ベースの **Schedules**、エージェント別の **Usage / 予算** サマリー、ポリシー / テンプレート / エージェントライフサイクルの **監査ログ**。

Hub 本体は `hub/` 配下の Go デーモンとして別途配布されます。`go install` または `go run` で起動してください。セットアップと各タブの検証手順は [docs/hub-mobile-test.md](docs/hub-mobile-test.md) を参照。

### その他
- **データエクスポート/インポート** — 接続・鍵・スニペット・履歴・設定を JSON 形式でバックアップ、別デバイスへの復元や旧 MuxPod アプリからの移行に対応
- **内蔵ファイルブラウザ** — 設定画面から SFTP ダウンロードとアプリストレージを管理、その場で共有・削除可能
- **アップデートチェッカー** — 設定 → アップデート確認で GitHub Releases の最新版を確認、APK へ直接リンク
- **ヘルプ・オンボーディング** — アクションバー＋ tmux キーバインドのチートシートと 4 カードのウォークスルー
- **ディープリンク** — `termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>` で外部アプリから特定のサーバー／セッション／ウィンドウ／ペインへ直接ジャンプ。各接続には安定した **Deep Link ID** を設定可能（編集画面）で、リネームしても URL が壊れません。[claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) と組み合わせれば、Telegram 通知をタップして該当ペインへ直接遷移できます。レガシーの `muxpod://` URL も解決可能
- **タブレット・折りたたみ** 適応レイアウト
- **i18n** — 英語と中国語、システムロケールに追従

---

## 同類アプリとの比較

| 機能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **プラットフォーム** | Android + iOS + iPad | Android | Android | マルチ | Android |
| **tmux 統合** | ネイティブ（視覚的） | 手動（CLI） | なし | なし | なし |
| **AI エージェント対応** | Claude Code + Codex、ペイン毎の状態 | なし | なし | なし | なし |
| **SSH 踏み台サーバー** | 内蔵 | CLI 経由 | CLI 経由 | 内蔵 | なし |
| **SOCKS5 プロキシ** | 内蔵 | CLI 経由 | なし | なし | なし |
| **ファイル転送** | SFTP（UI 付き） | ローカル FS | なし | SFTP | なし |
| **オープンソース** | はい (Apache 2.0) | はい | いいえ | いいえ | はい |

---

## クイックスタート

### インストール

**Android:** [**Releases**](https://github.com/physercoe/termipod/releases) から最新の APK をダウンロードしてインストール。

**iOS / iPadOS:** Xcode でソースからビルドしてください。TestFlight 配布はロードマップ上にあります。

### ソースからビルド

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
flutter pub get

# Android
flutter build apk --release

# iOS / iPadOS（macOS + Xcode が必要）
flutter build ios --release
```

### 接続

1. **サーバーを追加** — サーバータブで + をタップ、ホスト/ポート/ユーザー名を入力
2. **認証** — パスワードまたは SSH 鍵を選択（Vault > Keys で生成可能）
3. **オプション** — 接続フォームでジャンプホストまたは SOCKS5 プロキシを設定
4. **ナビゲーション** — サーバー展開 > セッション > ウィンドウ > ペイン
5. **操作** — アクションバーでクイックキー、コンポーズバーでコマンド、[+] でスニペットとファイル転送

---

## 必要要件

| コンポーネント | 要件 |
|----------------|------|
| **デバイス** | Android 8.0+(API 26)、iOS 13.0+、iPadOS 13.0+ |
| **サーバー** | 任意の SSH サーバー（OpenSSH、Dropbear 等） |
| **tmux** | 任意のバージョン（2.9+ で動作確認）— Raw PTY モードでは不要 |
| **ネットワーク** | 直接 SSH、または踏み台サーバー / SOCKS5 プロキシ経由 |

---

## ロードマップ

TermiPod の MVP は `docs/blueprint.md` §9 Phase 4 の**リサーチデモ**です：
ユーザーが方針（directive）を書く → スチュワードが分解 → 複数ホストで runs が実行 →
ブリーフィングエージェントが夜間にまとめ → 朝スマホでレビュー。

デモ経路はエンドツーエンドで shipped 済み：ビルトイン プロジェクトテンプレート
（ablation-sweep / reproduce-paper / benchmark-comparison / write-memo）を YAML
オーバーレイで配置、具体的なスチュワード分解レシピ、cron 付きブリーフィング
エージェント、NAT 背後の GPU ホスト向け逆トンネルリレーによるホスト間 A2A、
trackio / wandb / TensorBoard のメトリクスダイジェストを run 詳細画面の
インラインスパークラインで表示。モバイルのハブ CRUD 画面はすべてスチュワード
から MCP ツールで到達できるため、スマホは「操作する場所」ではなく
「承認・レビューする場所」として成立します。

未着手：

- **iOS TestFlight / App Store 配布** — Android APK はリリース済み。iOS は
  ローカルビルドのみ。TestFlight が次の配布ステップ。
- **Projects / Channels タブのアクティビティフィード** — v1.0.160 で各画面の
  「+ 新規」をオーバーフローに降格しました。承認/レビュー姿勢を正しく
  機能させる残タスクは、ランディング画面に統合されたアクティビティ
  ストリーム（runs / docs / attention / schedule fires）を出すこと。
- **A2A ピア認証** — 逆トンネルリレーにエージェントごとのトークンを足し、
  クロスチーム呼び出しをエンドツーエンドで認証可能にする。

ギャップの最新状況は [docs/research-demo-gaps.md](docs/research-demo-gaps.md)、
全体フェーズ計画は [docs/blueprint.md](docs/blueprint.md) §9 を参照。

---

## 謝辞

TermiPod は [@moezakura](https://github.com/moezakura) による [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox、[Apache License 2.0](LICENSE)）をベースに開発されています。TermiPod は独立プロジェクトであり、原作者とは提携・推奨関係にありません。詳細は [NOTICE](NOTICE) を参照してください。

## ライセンス

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Flutter で構築。モバイルのために設計。ターミナルに生きる開発者のために。</sub>
</p>
