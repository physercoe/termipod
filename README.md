<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod — Mobile SSH Terminal for tmux & AI Coding Agents" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>Mobile SSH terminal — built for tmux and AI coding agents.</b><br>
  <sub>Manage remote servers from your phone. Run Claude Code, Codex, or any CLI tool in a touch-optimized terminal.<br>Android, iOS, iPadOS — one Flutter codebase.</sub>
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
  <a href="README.ja.md">日本語</a> &nbsp;|&nbsp;
  <a href="README.zh.md">中文</a>
</p>

---

## Screenshots

<table>
<tr>
<td align="center"><b>Dashboard</b></td>
<td align="center"><b>Agent Commands</b></td>
<td align="center"><b>Key Palette</b></td>
</tr>
<tr>
<td><img src="docs/screens/dashboard_dark.png" width="240" alt="Dashboard — recent sessions, one-tap reconnect"></td>
<td><img src="docs/screens/bolt_menu_dark.png" width="240" alt="Claude Code slash commands with dropdowns"></td>
<td><img src="docs/screens/key_palette_dark.png" width="240" alt="Profile sheet with key group grid"></td>
</tr>
<tr>
<td align="center"><b>Terminal</b></td>
<td align="center"><b>Vault (Keys & Snippets)</b></td>
<td align="center"><b>Insert Menu</b></td>
</tr>
<tr>
<td><img src="docs/screens/terminal_dark.png" width="240" alt="Terminal with action bar and compose bar"></td>
<td><img src="docs/screens/vault_dark.png" width="240" alt="SSH keys, snippets, command history"></td>
<td><img src="docs/screens/insert_menu_dark.png" width="240" alt="Insert menu — file transfer, image, direct input"></td>
</tr>
</table>

---

## Why TermiPod?

Unlike generic SSH apps that give you a raw terminal and a tiny keyboard, TermiPod is designed around how developers actually use terminals on mobile:

- **Navigate tmux visually** — tap through sessions, windows, and panes instead of memorizing `Ctrl-b` keybindings
- **Run AI coding agents** (Claude Code, Codex, Aider) with pre-configured button layouts and structured slash-command snippets — pick `/model`, `/effort`, `/permissions` from dropdowns instead of typing them
- **Per-pane profiles** — each tmux pane remembers its own action bar layout, auto-switching when you move between panes
- **Custom keyboard** — Flutter-native QWERTY with Ctrl/Alt/Esc/arrows built in, no more hunting through the system IME
- **Transfer files** via SFTP — upload from phone, download, browse remote directories
- **Jump hosts & proxies** — SSH ProxyJump and SOCKS5 for machines behind NAT or firewalls

### Who is this for?

| | |
|---|---|
| **AI agent users** | Run Claude Code / Codex / Aider in tmux, monitor and interact from your phone |
| **Developers** | SSH into dev machines, CI runners, cloud VMs |
| **DevOps / SRE** | Check services, tail logs, restart processes on the go |
| **Homelab enthusiasts** | Manage servers, Raspberry Pi, NAS from your phone |

---

## Features

### SSH & Connectivity
- **Ed25519 / RSA keys** — generate on-device or import, stored in Android Keystore / iOS Keychain
- **SSH ProxyJump** — connect through bastion/jump hosts to internal machines
- **SOCKS5 proxy** — route through corporate proxies, VPNs, or Shadowsocks/Clash
- **Raw PTY mode** — direct shell access for servers without tmux
- **Connection testing** — verify SSH + tmux before saving

### tmux Session Management
- **Visual navigation** — breadcrumb header: tap Session > Window > Pane to switch
- **Pane layout view** — accurate proportional split visualization
- **Two-finger swipe** between panes
- **Create / rename / close** sessions and windows
- **256-color ANSI** terminal rendering with auto-extend scrollback

### Input UX (Mobile-Optimized)

| Component | What it does |
|-----------|-------------|
| **Action bar** | Swipeable button groups per profile — ESC, Tab, Ctrl+C, arrows, one tap away |
| **Compose bar** | Multi-line text field with send button. Long-press send to omit Enter |
| **Custom keyboard** | Flutter-native QWERTY with Ctrl/Alt/Esc/arrows. Toggle off for CJK input |
| **Navigation pad** | D-pad, joystick, or gesture surface for arrow keys + action buttons |
| **Snippets** | Slash commands with dropdowns for enums, text fields for free-form args |
| **Modifier keys** | Ctrl / Alt as toggle buttons — tap to arm, double-tap to lock |

**4 built-in profiles** — Claude Code, Codex, General Terminal, tmux — each with optimized button groups. Create custom profiles for any CLI. Each pane remembers its profile and auto-detects from `pane_current_command`.

### File Transfer
- **SFTP upload/download** with progress tracking and remote directory browser
- **Image transfer** with format conversion, resize presets, and path injection

### Other
- **Notification alerts** — monitor bell/activity/silence flags across all connections
- **Data export / import** — full JSON backup of connections, keys, snippets, history, and settings; restore on a new device or migrate from the legacy MuxPod app
- **Help & onboarding** — cheat sheet for action bar + tmux keybindings, 4-card walkthrough
- **Deep linking** — `termipod://` URL scheme for direct session access from external apps
- **Tablet & foldable** adaptive layout
- **i18n** — English and Chinese, follows system locale

---

## How TermiPod Compares

| Feature | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|---------|----------|--------|----------|---------|------------|
| **Platform** | Android + iOS + iPad | Android | Android | Multi | Android |
| **tmux integration** | Native visual | Manual CLI | None | None | None |
| **AI agent profiles** | Claude Code + Codex, per-pane | None | None | None | None |
| **SSH jump host** | Built-in | CLI | CLI | Built-in | None |
| **SOCKS5 proxy** | Built-in | CLI | None | None | None |
| **File transfer** | SFTP with UI | Local FS | None | SFTP | None |
| **Custom keyboard** | Flutter-native | None | None | None | None |
| **Open source** | Yes (Apache 2.0) | Yes | No | No | Yes |

---

## Quick Start

### Install

**Android:** Download the latest APK from [**Releases**](https://github.com/physercoe/termipod/releases) and install.

**iOS / iPadOS:** Build from source with Xcode (see below). TestFlight is on the roadmap.

### Build from source

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
flutter pub get

# Android
flutter build apk --release

# iOS / iPadOS (requires macOS + Xcode)
flutter build ios --release
```

### Connect

1. **Add a server** — Tap + on Servers tab, enter host / port / username
2. **Authenticate** — Password or SSH key (generate in Vault > Keys)
3. **Optional** — Configure jump host or SOCKS5 proxy in the connection form
4. **Navigate** — Expand server > session > window > pane
5. **Interact** — Action bar for quick keys, compose bar for commands, [+] for snippets and file transfers

---

## Requirements

| Component | Requirement |
|-----------|-------------|
| **Device** | Android 8.0+ (API 26), iOS 13.0+, iPadOS 13.0+ |
| **Server** | Any SSH server (OpenSSH, Dropbear, etc.) |
| **tmux** | Any version (tested 2.9+) — optional with raw PTY mode |
| **Network** | Direct SSH, or via jump host / SOCKS5 proxy |

---

## Roadmap

- Hybrid xterm mode — PTY stream rendering + tmux session navigation
- Local echo — predictive character display for low-latency feel
- Cursor alignment — font glyph width calibration
- iOS TestFlight / App Store distribution

---

## Attribution

TermiPod is a derivative work of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura) (Copyright 2025 mox), licensed under the [Apache License 2.0](LICENSE). The original MuxPod provided SSH connectivity and basic tmux session viewing for Android. TermiPod has since diverged substantially with cross-platform support, input UX redesign, agent profiles, SFTP, ProxyJump, SOCKS5, custom keyboard, and more. TermiPod is an independent project, not affiliated with the original author. See [NOTICE](NOTICE) for full attribution.

## Feedback

Found a bug or have a feature request? [Open an issue](https://github.com/physercoe/termipod/issues) or send feedback from **Settings > About** in the app.

## License

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Built with Flutter. Designed for mobile. Made for developers who live in the terminal.</sub>
</p>
