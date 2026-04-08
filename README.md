<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>Your tmux sessions, in your pocket.</b><br>
  <sub>A mobile-first tmux client for Android — SSH in, navigate sessions, and stay productive on the go.</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/mux-pod/releases"><img src="https://img.shields.io/github/v/release/physercoe/mux-pod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/mux-pod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.ja.md">🇯🇵 日本語</a> &nbsp;|&nbsp;
  <a href="README.zh.md">🇨🇳 中文</a>
</p>

---

> **TermiPod** is a fork of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura), adding i18n support (English/Chinese), input UX redesign, code snippets, CLI agent integration, and other enhancements.

---

<div align="center">
  <video src="https://github.com/user-attachments/assets/c7405e41-41ed-43ac-afb0-35091a357117" width="280" autoplay loop muted playsinline></video>
</div>

---

## Why TermiPod?

Ever needed to check on a long-running process, restart a service, or peek at logs while away from your desk?

**TermiPod turns your Android phone into a tmux remote control.**

- **Zero server setup** — Works with any server running `sshd`. No agents, no daemons, nothing to install.
- **Built for mobile** — Not a terminal crammed into a phone. A thoughtful UI designed for touch.
- **Secure by default** — SSH keys stored in Android Keystore. Your credentials never leave the device.
- **Multi-language** — English and Chinese (Simplified) out of the box, follows system locale.

---

## What's New in TermiPod

Compared to the upstream [MuxPod](https://github.com/moezakura/mux-pod):

- **i18n** — Full English and Chinese Simplified localization, auto-detects system language
- **Input UX redesign** — Improved special keys bar, snippet integration, command input
- **Code snippets** — Save and quickly paste frequently used commands
- **CLI agent support** — Optimized for Claude Code / Kimi Code workflows (S-RET, DirectInput)
- **Bug fixes** — Scroll-on-resize, back button handling, version display, and more

---

## App Structure

TermiPod uses a 5-tab navigation with Dashboard at the center for quick session access.

| Dashboard | Servers | Alerts | Keys | Settings |
|:---------:|:-------:|:------:|:----:|:--------:|
| <img src="docs/screens/dashboard.png" width="160"> | <img src="docs/screens/servers.png" width="160"> | <img src="docs/screens/alerts.png" width="160"> | <img src="docs/screens/keys.png" width="160"> | <img src="docs/screens/settings.png" width="160"> |

### Dashboard

Your home screen. Recent sessions sorted by last access time. **One tap to reconnect** — instantly returns to your last window and pane.

### Servers

Manage SSH connections. **Tap to expand** a server card and see active tmux sessions. Create new sessions or jump into existing ones.

### Alerts

Monitor tmux window flags across all connections in real-time.

| Flag | Color | Meaning |
|------|-------|---------|
| Bell | Red | Window triggered a bell |
| Activity | Orange | Content changed in window |
| Silence | Gray | No activity for a while |

### Keys

Generate **Ed25519** or **RSA** keys on-device. Import existing keys. All stored securely with optional passphrase protection. **One-tap copy** public key to clipboard.

### Settings

Customize terminal appearance (fonts, colors), behavior (haptic feedback, keep screen on), and connection settings.

---

## Terminal Experience

The terminal screen is where TermiPod shines — purpose-built for mobile tmux interaction.

### Breadcrumb Navigation

Tap **Session > Window > Pane** in the header to switch contexts instantly. The pane selector shows a **visual layout** of your split panes with accurate proportions.

| Terminal | Pane Selector |
|:--------:|:-------------:|
| <img src="docs/screens/terminal.png" width="200"> | <img src="docs/screens/terminal_panes.png" width="200"> |

### Touch Gestures

| Gesture | Action |
|---------|--------|
| **Hold + Swipe** | Send arrow keys — perfect for vim/nano |
| **Pinch** | Zoom in/out (50%–500%) |
| **Tap pane indicator** | Quick pane switcher with visual layout |

### Special Keys Bar

Dedicated buttons for terminal essentials:

```
[ESC] [TAB] [CTRL] [ALT] [SHIFT] [ENTER] [S-RET] [/] [-]
[←] [↑] [↓] [→]  [DirectInput]  [Input...]
```

- **Modifier keys toggle** — Tap CTRL, then type 'c' for Ctrl-C
- **S-RET** — Shift+Enter for Claude Code confirmation
- **DirectInput mode** — Real-time keystroke streaming

### Deep Linking

Open TermiPod directly from external apps using the `muxpod://` URL scheme.

```
muxpod://connect?server=<id>&session=<name>&window=<name>&pane=<index>
```

Works with [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) for tappable notifications that open the right terminal.

---

## Quick Start

### Install

Download the latest APK from [**Releases**](https://github.com/physercoe/mux-pod/releases).

### Or build from source

```bash
git clone https://github.com/physercoe/mux-pod.git
cd mux-pod
flutter pub get
flutter build apk --release
```

### Connect

1. **Add a server** — Tap + on Servers tab, enter host/port/username
2. **Authenticate** — Choose password or SSH key (generate in Keys tab)
3. **Navigate** — Expand server > select session > tap window > choose pane
4. **Interact** — Use touch gestures, special keys bar, or DirectInput mode

---

## Requirements

| Component | Requirement |
|-----------|-------------|
| **Device** | Android 8.0+ (API 26) |
| **Server** | Any SSH server (OpenSSH, Dropbear, etc.) |
| **tmux** | Any version (tested with 2.9+) |

---

## Tech Stack

| | |
|---|---|
| **Framework** | Flutter 3.24+ / Dart 3.x |
| **SSH** | [dartssh2](https://pub.dev/packages/dartssh2) |
| **Terminal** | [xterm](https://pub.dev/packages/xterm) |
| **State** | [flutter_riverpod](https://pub.dev/packages/flutter_riverpod) |
| **Security** | [flutter_secure_storage](https://pub.dev/packages/flutter_secure_storage) |

---

## Development

```bash
flutter run             # Debug mode
flutter analyze         # Static analysis
flutter test            # Run tests
```

See [docs/](docs/) for architecture details and coding conventions.

---

## Acknowledgments

TermiPod is built on top of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura). Thanks for the excellent foundation.

## License

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Built with Flutter</sub>
</p>
