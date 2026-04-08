<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>SSH terminal for Android, built for tmux and AI coding agents.</b><br>
  <sub>Manage remote servers from your phone — run Claude Code, Codex, Aider, or any CLI tool through a touch-optimized terminal.</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/mux-pod/releases"><img src="https://img.shields.io/github/v/release/physercoe/mux-pod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/mux-pod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.ja.md">日本語</a> &nbsp;|&nbsp;
  <a href="README.zh.md">中文</a>
</p>

---

> **TermiPod** is a fork of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura), adding a complete input UX redesign with CLI agent profiles, code snippets, SSH jump host/proxy support, help system, and other enhancements.

---

<!-- TODO: Replace with updated demo video showing new action bar + compose bar UI -->
<div align="center">
  <video src="https://github.com/user-attachments/assets/c7405e41-41ed-43ac-afb0-35091a357117" width="280" autoplay loop muted playsinline></video>
</div>

---

## What is TermiPod?

TermiPod is a **mobile SSH client and tmux manager for Android** — purpose-built for developers who run long-lived terminal sessions on remote servers and need to check in, interact, or operate from their phone.

Unlike generic SSH apps that give you a raw terminal and a tiny keyboard, TermiPod is designed around how people actually use terminals on mobile:

- **Navigate tmux sessions** visually — tap through sessions, windows, and panes instead of memorizing keybindings
- **Run AI coding agents** (Claude Code, Codex, Kimi Code, Aider) with pre-configured button layouts and command presets
- **Send commands without fighting the keyboard** — compose bar for multi-line input, action bar for quick keys, snippets for saved commands
- **Transfer files** to and from servers via SFTP — upload from phone, download and share, browse remote directories
- **Connect through jump hosts and proxies** — SSH ProxyJump and SOCKS5 proxy support for machines behind NAT or corporate firewalls

### Who is this for?

- **Developers** who SSH into dev machines, CI runners, or cloud VMs
- **DevOps/SRE** who need to check on services, tail logs, restart processes on the go
- **AI agent users** who run Claude Code, Codex, Aider, or similar CLI tools in tmux sessions
- **Homelab enthusiasts** managing servers, Raspberry Pis, NAS boxes from their phone
- **Anyone with a remote tmux session** who wants a better mobile experience than Termux or JuiceSSH

---

## Features

### SSH Connection
- **Password and key authentication** — Ed25519 and RSA keys, generated on-device or imported
- **SSH ProxyJump** — Connect through a bastion/jump host to reach internal machines
- **SOCKS5 proxy** — Route SSH through corporate proxies, VPNs, or Shadowsocks/Clash
- **Connection testing** — Verify SSH + tmux availability before saving
- **Secure storage** — Keys and passwords in Android Keystore via flutter_secure_storage
- **Zero server setup** — Works with any server running `sshd` + `tmux`. Nothing to install remotely.

### tmux Session Management
- **Visual session/window/pane navigation** — Breadcrumb header with tap-to-switch
- **Pane layout visualization** — Accurate proportional view of split panes
- **Two-finger swipe** between panes — Navigate tmux splits with touch gestures
- **Create/rename/close** sessions and windows from the app
- **ANSI color support** — Full 256-color terminal rendering

### Input UX (Mobile-Optimized)
- **Action bar** — Swipeable button groups with profile-specific layouts. ESC, Tab, Ctrl+C, arrow keys — all one tap away.
- **Compose bar** — Multi-line text input with send button. Type a command, review it, send it. Long-press send to omit Enter.
- **6 built-in profiles** — Claude Code, Codex, Kimi Code, OpenCode, Aider, General Terminal. Each with optimized button groups.
- **Profile auto-detection** — Detects which CLI tool is running and suggests the matching profile
- **Snippet system** — Save commands as snippets with categories. Preset agent-specific commands ship per profile.
- **Command history** — Recent commands from [+] menu. Full archive in Vault with search and save-as-snippet.
- **Direct input mode** — Keystroke-by-keystroke mode for vim, nano, and interactive CLIs
- **Modifier keys** — Ctrl and Alt as toggle buttons (tap to arm, double-tap to lock)
- **Key overlay** — Visual feedback showing key names on press (configurable per key category)

### File & Image Transfer
- **SFTP upload** — Pick files from phone, upload to server with progress tracking
- **SFTP download** — Browse remote directories, download files, share via Android share sheet
- **Image transfer** — Send photos with format conversion, resize presets, and path injection
- **Bracketed paste** — Auto-wrap paths in bracketed paste mode for safe insertion

### Help & Onboarding
- **Built-in help** — Tabbed cheat sheet for action bar buttons and tmux keybindings, accessible from terminal menu
- **First-run walkthrough** — 4-card onboarding overlay explaining compose bar, action bar, insert menu, and terminal menu

### Other
- **Notification alerts** — Monitor tmux window flags (bell, activity, silence) across all connections
- **Deep linking** — `muxpod://` URL scheme for opening specific terminal sessions from external apps
- **Foldable device support** — Adapts layout for large inner screens
- **Auto-resize** — Adjusts terminal dimensions to fit screen
- **i18n** — English and Chinese (Simplified), follows system locale

---

## How TermiPod Compares

| Feature | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|---------|----------|--------|----------|---------|------------|
| **tmux integration** | Native (visual navigation) | Manual (CLI) | None | None | None |
| **AI agent profiles** | 6 built-in (Claude, Codex, Aider...) | None | None | None | None |
| **Action bar** | Swipeable, per-profile | Extra keys row | Basic buttons | Custom toolbar | None |
| **Snippet system** | Categories + agent presets | None | Snippet support | Snippet support | None |
| **SSH jump host** | Built-in ProxyJump | Via CLI | Via CLI | Built-in | None |
| **SOCKS5 proxy** | Built-in | Via CLI | None | None | None |
| **File transfer** | SFTP with UI | Local filesystem | None | SFTP | None |
| **Help / cheat sheet** | Built-in tmux reference | None | None | None | None |
| **Open source** | Yes (Apache 2.0) | Yes | No | No | Yes |
| **Price** | Free | Free | Freemium | Freemium | Free |

**TermiPod is not a local terminal emulator** (like Termux). It's a remote terminal client that connects to servers via SSH. Think of it as a mobile-optimized replacement for opening a terminal on your laptop, running `ssh server`, and attaching to a tmux session.

---

## Common Use Cases

### Monitor Claude Code / AI agents
SSH into your dev machine, attach to the tmux session running Claude Code, and interact using the optimized Claude Code profile with pre-configured buttons for `/help`, `/compact`, escape, and interrupt.

### Quick server check from phone
Something alerting at 2am? Open TermiPod, tap your server from the dashboard, check logs, restart a service, detach. Total time: 30 seconds.

### Manage homelab / Raspberry Pi
Connect to your home server behind NAT using SSH ProxyJump through a VPS, or SOCKS5 through Tailscale/Cloudflare Tunnel. Transfer config files via SFTP.

### Pair programming on the go
Share a tmux session with a colleague. You watch and interact from your phone while they code on their laptop.

---

## App Structure

TermiPod uses a 5-tab navigation with Dashboard at the center for quick session access.

| Dashboard | Servers | Alerts | Vault | Settings |
|:---------:|:-------:|:------:|:-----:|:--------:|
| <!-- TODO: update screenshot --> <img src="docs/screens/dashboard.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/servers.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/alerts.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/keys.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/settings.png" width="160"> |

### Dashboard

Your home screen. Recent sessions sorted by last access time. **One tap to reconnect** — instantly returns to your last window and pane.

### Servers

Manage SSH connections. **Tap to expand** a server card and see active tmux sessions. Create new sessions or jump into existing ones. Configure jump hosts and SOCKS5 proxies per connection.

### Alerts

Monitor tmux window flags across all connections in real-time.

| Flag | Color | Meaning |
|------|-------|---------|
| Bell | Red | Window triggered a bell |
| Activity | Orange | Content changed in window |
| Silence | Gray | No activity for a while |

### Vault

Three sections in a scrollable list:
- **Keys** — Generate or import SSH keys (Ed25519/RSA). One-tap copy public key.
- **Snippets** — Manage saved commands with name, content, and category.
- **History** — Full command history archive with search, delete, and save-as-snippet.

### Settings

| Section | Options |
|---------|---------|
| **Terminal** | Cursor, adjust mode, font size/family, scrollback lines |
| **Key Overlay** | Toggle per key category, position |
| **Toolbar** | Active profile, customize groups and buttons |
| **Behavior** | Haptic feedback, keep screen on, invert pane navigation |
| **Appearance** | Theme (dark/light), language (English/Chinese/System) |
| **Image Transfer** | Remote path, format, quality, resize, path format |
| **File Transfer** | Remote path, path format, auto-enter, bracketed paste |

---

## Terminal Screen

The terminal screen is where TermiPod shines — purpose-built for mobile tmux interaction.

### Breadcrumb Navigation

Tap **Session > Window > Pane** in the header to switch contexts instantly. The pane selector shows a **visual layout** of your split panes with accurate proportions.

| Terminal | Pane Selector |
|:--------:|:-------------:|
| <!-- TODO: update screenshot --> <img src="docs/screens/terminal.png" width="200"> | <!-- TODO: update screenshot --> <img src="docs/screens/terminal_panes.png" width="200"> |

### Touch Gestures

| Gesture | Action |
|---------|--------|
| **Hold + Swipe** | Send arrow keys — perfect for vim/nano |
| **Two-finger swipe** | Switch tmux panes |
| **Pinch** | Zoom in/out (50%–500%) |
| **Tap pane indicator** | Quick pane switcher with visual layout |

### Action Bar

A single-row swipeable toolbar with per-profile button layouts:

```
← [ESC] [TAB] [C-C] [y] [n] [C-D] [bolt] →    [menu]
                    . . . . .
```

- **Swipe** between button groups (Quick, Navigate, Ctrl, Edit, etc.)
- **Page dots** show current position
- **[menu]** opens profile sheet — switch profiles, manage snippets
- **[bolt]** opens snippet picker — agent commands + user snippets with search
- **Modifier keys** (CTRL, ALT) toggle on tap, lock on double-tap
- **Confirm buttons** (y/n) send with Enter on tap, without Enter on long-press

### Compose Bar

Always-visible primary input below the action bar:

```
[+] [ Type command or prompt...        ][clear] [send]
```

- **[+]** insert menu — Recent, File Upload/Download, Image Transfer, Direct Input toggle
- **Multi-line** text field with auto-expand
- **Send** — tap sends with Enter, long-press sends without Enter
- **Clear** — tap clears text, long-press clears + sends C-u (kill line)

### Profiles

6 built-in profiles optimized for different CLI tools:

| Profile | Optimized For | Key Groups |
|---------|---------------|------------|
| **Claude Code** | `claude` | Quick, Navigate, Ctrl, Edit |
| **Codex** | `codex` | Quick, Navigate, Ctrl, Edit |
| **Kimi Code** | `kimi` | Quick, Navigate, Ctrl, Edit |
| **OpenCode** | `opencode` | Quick, Navigate, Leader, Edit |
| **Aider** | `aider` | Quick, Navigate, Emacs, Edit |
| **General** | Any terminal | Keys, Navigate, Chars, Page |

Auto-detection suggests the right profile when a CLI agent is running.

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
2. **Authenticate** — Choose password or SSH key (generate in Vault > Keys)
3. **Optional: Configure jump host or proxy** — Expand the Jump Host or SOCKS5 Proxy section in the connection form
4. **Navigate** — Expand server > select session > tap window > choose pane
5. **Interact** — Use the action bar for quick keys, compose bar for commands, [+] for snippets and file transfers

---

## Requirements

| Component | Requirement |
|-----------|-------------|
| **Device** | Android 8.0+ (API 26) |
| **Server** | Any SSH server (OpenSSH, Dropbear, etc.) |
| **tmux** | Any version (tested with 2.9+) |
| **Network** | Direct SSH, or via jump host / SOCKS5 proxy |

---

## Tech Stack

| | |
|---|---|
| **Framework** | Flutter 3.24+ / Dart 3.x |
| **SSH** | [dartssh2](https://pub.dev/packages/dartssh2) |
| **Terminal** | [xterm](https://pub.dev/packages/xterm) (rendering) |
| **State** | [flutter_riverpod](https://pub.dev/packages/flutter_riverpod) |
| **Security** | [flutter_secure_storage](https://pub.dev/packages/flutter_secure_storage) (Android Keystore) |

---

## Development

```bash
flutter run             # Debug mode
flutter analyze         # Static analysis
flutter test            # Run tests
```

See [docs/](docs/) for architecture details and coding conventions.

---

## Roadmap

- Navigation pad — Game-style D-pad and action buttons for thumb-optimized arrow/tab/enter input
- Custom terminal keyboard — Compact Flutter-native keyboard for direct input mode (half the height of system keyboard)
- Real xterm mode — Native VT terminal via PTY stream as alternative to tmux polling
- Local echo — Predictive character display for low-latency feel on slow connections

---

## Acknowledgments

TermiPod is built on top of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura). Thanks for the excellent foundation.

## License

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Built with Flutter. Designed for mobile. Made for developers who live in the terminal.</sub>
</p>
