<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>Android SSH ターミナル — tmux と AI コーディングエージェント対応。</b><br>
  <sub>スマートフォンからリモートサーバーを管理 — Claude Code、Codex、Aider などの CLI ツールを、タッチ最適化されたターミナルで実行。</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/mux-pod/releases"><img src="https://img.shields.io/github/v/release/physercoe/mux-pod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/mux-pod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.md">English</a> &nbsp;|&nbsp;
  <a href="README.zh.md">中文</a>
</p>

---

> **TermiPod** は [@moezakura](https://github.com/moezakura) による [MuxPod](https://github.com/moezakura/mux-pod) のフォークです。入力UXの完全な再設計、CLIエージェントプロファイル、コードスニペット、SSHジャンプホスト/プロキシ対応、ヘルプシステムなどの機能を追加しています。

---

## TermiPod とは？

TermiPod は **Android 向けモバイル SSH クライアント兼 tmux マネージャー** です。リモートサーバーで長時間実行されるターミナルセッションを持ち、外出先からスマートフォンで確認・操作する必要がある開発者のために設計されています。

一般的な SSH アプリが生のターミナルと小さなキーボードを提供するだけなのに対し、TermiPod はモバイルでのターミナル利用の実態に合わせて設計されています：

- **tmux セッションの視覚的ナビゲーション** — セッション、ウィンドウ、ペインをタップで切り替え
- **AI コーディングエージェントの実行**（Claude Code、Codex、Kimi Code、Aider）— 事前設定されたボタンレイアウトとコマンドプリセット
- **キーボードと格闘しない入力** — 複数行入力のコンポーズバー、クイックキーのアクションバー、保存コマンドのスニペット
- **ファイル転送** — SFTP でサーバーとファイルのアップロード・ダウンロード
- **踏み台サーバー/プロキシ対応** — SSH ProxyJump と SOCKS5 プロキシで NAT 内のマシンに接続

### 対象ユーザー

- **開発者** — 開発マシン、CI サーバー、クラウド VM に SSH 接続
- **DevOps/SRE** — 外出中にサービスの確認、ログ確認、プロセス再起動
- **AI エージェントユーザー** — tmux で Claude Code、Codex、Aider などを実行
- **ホームラボ愛好者** — スマホからサーバー、Raspberry Pi、NAS を管理
- **Termux や JuiceSSH より良い tmux 体験が欲しい人**

---

## 機能

### SSH 接続
- **パスワード・鍵認証** — Ed25519/RSA 鍵、デバイス上で生成またはインポート
- **SSH ProxyJump（踏み台サーバー）** — 踏み台経由で内部ネットワークのマシンに接続
- **SOCKS5 プロキシ** — 企業プロキシ、VPN、Shadowsocks/Clash 経由で SSH 接続
- **接続テスト** — 保存前に SSH + tmux の可用性を確認
- **セキュア ストレージ** — 鍵とパスワードを Android Keystore に保存
- **サーバー設定不要** — `sshd` + `tmux` があれば OK

### tmux セッション管理
- **視覚的ナビゲーション** — ブレッドクラムヘッダーでセッション/ウィンドウ/ペインを切り替え
- **ペインレイアウト表示** — 分割ペインの正確な比率表示
- **2本指スワイプ** — tmux 分割ペイン間のナビゲーション
- **セッション・ウィンドウの作成/名前変更/閉じる**
- **ANSI カラー対応** — 完全な 256 色ターミナルレンダリング

### 入力 UX（モバイル最適化）
- **アクションバー** — スワイプ可能なボタングループ、プロファイル毎のレイアウト。ESC、Tab、Ctrl+C、矢印キーがワンタップ。
- **コンポーズバー** — 送信ボタン付き複数行テキスト入力。長押し送信で Enter 省略。
- **6つの内蔵プロファイル** — Claude Code、Codex、Kimi Code、OpenCode、Aider、汎用ターミナル
- **プロファイル自動検出** — 実行中の CLI ツールを検出し、マッチするプロファイルを提案
- **スニペットシステム** — カテゴリ付きでコマンドを保存。エージェント固有のコマンドプリセット。
- **コマンド履歴** — [+] メニューから最近のコマンド。Vault で全履歴を検索。
- **ダイレクト入力モード** — vim、nano 等の対話型 CLI 向けキーストローク毎の入力
- **修飾キー** — Ctrl/Alt をトグルボタンとして（タップでアーム、ダブルタップでロック）
- **キーオーバーレイ** — キー押下時に名前を表示する視覚的フィードバック

### ファイル・画像転送
- **SFTP アップロード** — スマホからファイルを選択してサーバーにアップロード（進捗表示）
- **SFTP ダウンロード** — リモートディレクトリを閲覧、ファイルをダウンロード、Android 共有
- **画像転送** — フォーマット変換、リサイズ、パス注入対応

### ヘルプ・オンボーディング
- **内蔵ヘルプ** — アクションバーボタンと tmux キーバインドのチートシート
- **初回ウォークスルー** — コンポーズバー、アクションバー、挿入メニュー、ターミナルメニューを紹介する4枚のカード

---

## 同類アプリとの比較

| 機能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **tmux 統合** | ネイティブ（視覚的） | 手動（CLI） | なし | なし | なし |
| **AI エージェント対応** | 6プロファイル内蔵 | なし | なし | なし | なし |
| **SSH 踏み台サーバー** | 内蔵 | CLI 経由 | CLI 経由 | 内蔵 | なし |
| **SOCKS5 プロキシ** | 内蔵 | CLI 経由 | なし | なし | なし |
| **ファイル転送** | SFTP（UI 付き） | ローカル FS | なし | SFTP | なし |
| **オープンソース** | はい (Apache 2.0) | はい | いいえ | いいえ | はい |

---

## クイックスタート

### インストール

[**Releases**](https://github.com/physercoe/mux-pod/releases) から最新の APK をダウンロード。

### ソースからビルド

```bash
git clone https://github.com/physercoe/mux-pod.git
cd mux-pod
flutter pub get
flutter build apk --release
```

### 接続

1. **サーバーを追加** — サーバータブで + をタップ、ホスト/ポート/ユーザー名を入力
2. **認証** — パスワードまたは SSH 鍵を選択（Vault > Keys で生成可能）
3. **オプション：踏み台/プロキシ設定** — 接続フォームでジャンプホストまたは SOCKS5 プロキシセクションを展開
4. **ナビゲーション** — サーバー展開 > セッション選択 > ウィンドウタップ > ペイン選択
5. **操作** — アクションバーでクイックキー、コンポーズバーでコマンド、[+] でスニペットとファイル転送

---

## 必要要件

| コンポーネント | 要件 |
|----------------|------|
| **デバイス** | Android 8.0+（API 26） |
| **サーバー** | 任意の SSH サーバー（OpenSSH、Dropbear 等） |
| **tmux** | 任意のバージョン（2.9+ で動作確認） |
| **ネットワーク** | 直接 SSH、または踏み台サーバー / SOCKS5 プロキシ経由 |

---

## ロードマップ

- ナビゲーションパッド — ゲーム風 D-pad とアクションボタン、親指に最適化された入力
- カスタムターミナルキーボード — システムキーボードの半分の高さの Flutter ネイティブキーボード
- リアル xterm モード — PTY ストリームによるネイティブ VT ターミナル
- ローカルエコー — 低遅延接続のための予測文字表示

---

## 謝辞

TermiPod は [@moezakura](https://github.com/moezakura) による [MuxPod](https://github.com/moezakura/mux-pod) をベースに構築されています。素晴らしい基盤に感謝します。

## ライセンス

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Flutter で構築。モバイルのために設計。ターミナルに生きる開発者のために。</sub>
</p>
