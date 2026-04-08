<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>tmuxセッションを、ポケットに。</b><br>
  <sub>Android向けモバイルファーストtmuxクライアント — SSH接続、セッション操作、外出先でも生産的に。</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/mux-pod/releases"><img src="https://img.shields.io/github/v/release/physercoe/mux-pod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/mux-pod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.md">🇺🇸 English</a> &nbsp;|&nbsp;
  <a href="README.zh.md">🇨🇳 中文</a>
</p>

---

> **TermiPod** は [@moezakura](https://github.com/moezakura) による [MuxPod](https://github.com/moezakura/mux-pod) のフォークです。i18n対応（英語/中国語）、入力UXの再設計、コードスニペット、CLIエージェント連携などの機能を追加しています。

---

<div align="center">
  <video src="https://github.com/user-attachments/assets/c7405e41-41ed-43ac-afb0-35091a357117" width="280" autoplay loop muted playsinline></video>
</div>

---

## なぜ TermiPod？

長時間実行中のプロセスを確認したい、サービスを再起動したい、ログを覗きたい — でもPCから離れている。そんな経験ありませんか？

**TermiPodは、あなたのAndroidをtmuxリモコンに変えます。**

- **サーバー設定ゼロ** — `sshd`が動いていればOK。エージェント不要、デーモン不要、インストール不要。
- **モバイルのために設計** — 無理やりスマホに押し込んだターミナルではない。タッチ操作を考え抜いたUI。
- **デフォルトで安全** — SSH鍵はAndroid Keystoreに保存。認証情報はデバイスの外に出ない。
- **ファイル転送内蔵** — ターミナルを離れずにSFTPでファイルをアップロード・ダウンロード。リモートディレクトリの閲覧、ファイル選択、進捗追跡。
- **多言語対応** — 英語・中国語（簡体字）をサポート、システム言語に自動追従。

---

## TermiPodの新機能

上流の [MuxPod](https://github.com/moezakura/mux-pod) との比較：

- **i18n** — 英語・中国語（簡体字）の完全ローカライズ、システム言語自動検出
- **入力UXの再設計** — 特殊キーバーの改善、スニペット統合、コマンド入力
- **コードスニペット** — よく使うコマンドを保存してすぐに貼り付け
- **CLIエージェント対応** — Claude Code / Kimi Codeワークフローに最適化（S-RET、DirectInput）
- **ファイル転送** — スマホからサーバーへのファイルアップロード、SFTPによるリモートファイルのダウンロード（進捗追跡、リモートファイルブラウザ、Android共有連携）
- **画像転送** — カメラやギャラリーからサーバーへ写真を送信（フォーマット変換、リサイズプリセット、パス注入）
- **バグ修正** — リサイズ時のスクロール、戻るボタン、バージョン表示など

---

## アプリ構成

TermiPodは中央にDashboardを配置した5タブナビゲーションで、セッションへ素早くアクセスできます。

| Dashboard | Servers | Alerts | Keys | Settings |
|:---------:|:-------:|:------:|:----:|:--------:|
| <img src="docs/screens/dashboard.png" width="160"> | <img src="docs/screens/servers.png" width="160"> | <img src="docs/screens/alerts.png" width="160"> | <img src="docs/screens/keys.png" width="160"> | <img src="docs/screens/settings.png" width="160"> |

### Dashboard

ホーム画面。最終アクセス日時順でセッション履歴を表示。**ワンタップで再接続** — 前回のウィンドウとペインに即座に復帰。

### Servers

SSH接続を管理。**タップして展開**するとアクティブなtmuxセッション一覧を表示。新規セッション作成も既存セッションへの接続もここから。

### Alerts

すべての接続にわたってtmuxウィンドウフラグをリアルタイム監視。

### Keys

**Ed25519**または**RSA**鍵をデバイス上で生成。既存の鍵をインポートも可能。**ワンタップで公開鍵をコピー**。

### Settings

ターミナルの外観、動作、接続設定をカスタマイズ。

---

## ターミナル体験

ターミナル画面はTermiPodの真骨頂 — モバイルでのtmux操作のために設計されています。

### タッチジェスチャー

| ジェスチャー | 動作 |
|-------------|------|
| **ホールド + スワイプ** | 矢印キー送信 — vim/nanoに最適 |
| **ピンチ** | ズームイン/アウト（50%〜500%） |
| **ペインインジケーターをタップ** | クイックペイン切り替え |

### 特殊キーバー

```
[ESC] [TAB] [CTRL] [ALT] [SHIFT] [ENTER] [S-RET] [/] [-]
[←] [↑] [↓] [→]  [File↕] [Image] [DirectInput]  [Input...]
```

### ディープリンク

`muxpod://` URLスキームで外部アプリからTermiPodを直接開けます。

```
muxpod://connect?server=<id>&session=<name>&window=<name>&pane=<index>
```

---

## クイックスタート

### インストール

[**Releases**](https://github.com/physercoe/mux-pod/releases) から最新のAPKをダウンロード。

### ソースからビルド

```bash
git clone https://github.com/physercoe/mux-pod.git
cd mux-pod
flutter pub get
flutter build apk --release
```

---

## 必要要件

| コンポーネント | 要件 |
|----------------|------|
| **デバイス** | Android 8.0以上（API 26） |
| **サーバー** | 任意のSSHサーバー |
| **tmux** | 任意のバージョン（2.9以上で動作確認） |

---

## 謝辞

TermiPodは [@moezakura](https://github.com/moezakura) による [MuxPod](https://github.com/moezakura/mux-pod) をベースに構築されています。素晴らしい基盤に感謝します。

## ライセンス

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Flutter で作られました</sub>
</p>
