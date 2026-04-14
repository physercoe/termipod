<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod — Mobile SSH Terminal Client for tmux and AI Coding Agents" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>Mobile SSH terminal client for tmux and AI coding agents — Android, iOS, iPadOS.</b><br>
  <sub>The best way to manage remote servers, run Claude Code / Codex CLI, and interact with tmux sessions from your phone or tablet. Open-source, Flutter-based, touch-optimized.</sub>
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

> **TermiPod** is a fork of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura), adding a complete input UX redesign with CLI agent profiles, code snippets, SSH jump host/proxy support, navigation pad with gesture controls, raw PTY mode, help system, and other enhancements.

---

<!-- TODO: Replace with updated demo video showing new action bar + compose bar UI -->
<div align="center">
  <video src="https://github.com/user-attachments/assets/c7405e41-41ed-43ac-afb0-35091a357117" width="280" autoplay loop muted playsinline></video>
</div>

---

## What is TermiPod?

TermiPod is an **open-source cross-platform mobile SSH client and tmux session manager** for Android, iOS, and iPadOS. It is purpose-built for developers who run long-lived terminal sessions on remote servers — whether that's AI coding agents like **Claude Code** or **Codex CLI**, production services, CI runners, or homelab machines — and need to check in, interact, or operate from a phone or tablet.

Built on **Flutter** so the same touch-optimized UI runs on every platform from a single codebase. Also supports **raw PTY connections** for servers without tmux.

Unlike generic SSH apps that give you a raw terminal and a tiny keyboard, TermiPod is designed around how people actually use terminals on mobile:

- **Navigate tmux sessions visually** — tap through sessions, windows, and panes instead of memorizing keybindings
- **Run AI coding agents** (Claude Code, Codex, Aider, Cursor Agent) with pre-configured button layouts and structured slash-command snippets (`/model`, `/effort`, `/permissions` — pick options from dropdowns instead of typing them)
- **Per-pane action bar profiles** — each tmux pane remembers its own profile, so switching from a `claude` pane to a `codex` pane flips the button layout automatically
- **Send commands without fighting the keyboard** — compose bar for multi-line input, action bar for quick keys, snippets with fill-in variables for parameterized commands
- **Transfer files** to and from servers via SFTP — upload from phone, download and share, browse remote directories
- **Connect through jump hosts and proxies** — SSH ProxyJump and SOCKS5 proxy support for machines behind NAT or corporate firewalls

### Who is this for?

- **AI agent users** who run Claude Code, Codex CLI, Aider, or similar AI coding tools in remote tmux sessions and want a mobile-optimized way to monitor, interact, and send commands
- **Developers** who SSH into dev machines, CI runners, or cloud VMs (AWS, GCP, Azure, DigitalOcean)
- **DevOps / SRE / platform engineers** who need to check on services, tail logs, restart processes on the go
- **Homelab enthusiasts** managing servers, Raspberry Pis, NAS boxes from their phone
- **Anyone with a remote tmux session** who wants a better mobile terminal experience than Termux, JuiceSSH, or Termius

### Keywords

`ssh client` `mobile terminal` `tmux manager` `claude code mobile` `codex cli mobile` `ai coding agent` `remote terminal` `flutter ssh` `android ssh client` `ios ssh client` `ipad terminal` `sftp transfer` `ssh jump host` `socks5 proxy ssh` `terminal emulator` `remote development` `devops mobile`

---

## Features

### SSH Connection
- **Password and key authentication** — Ed25519 and RSA keys, generated on-device or imported
- **SSH ProxyJump** — Connect through a bastion/jump host to reach internal machines
- **SOCKS5 proxy** — Route SSH through corporate proxies, VPNs, or Shadowsocks/Clash
- **Connection testing** — Verify SSH + tmux availability before saving
- **Secure storage** — Keys and passwords encrypted in Android Keystore / iOS Keychain via flutter_secure_storage
- **Zero server setup** — Works with any server running `sshd` (+ `tmux` for session management). Nothing to install remotely.

### tmux Session Management
- **Visual session/window/pane navigation** — Breadcrumb header with tap-to-switch
- **Pane layout visualization** — Accurate proportional view of split panes
- **Two-finger swipe** between panes — Navigate tmux splits with touch gestures
- **Create/rename/close** sessions and windows from the app
- **ANSI color support** — Full 256-color terminal rendering
- **Auto-extend scrollback** — Scroll to the top and more history loads automatically; tap jump-to-bottom to reset

### Input UX (Mobile-Optimized)
- **Action bar** — Swipeable button groups with profile-specific layouts. ESC, Tab, Ctrl+C, arrow keys — all one tap away.
- **Compose bar** — Multi-line text input with send button. Type a command, review it, send it. Long-press send to omit Enter.
- **4 built-in profiles** — Claude Code, Codex, General Terminal, tmux. Each with optimized button groups. Users can create custom profiles for any other CLI.
- **Per-pane profile state** — Each tmux pane remembers its own active profile, so switching panes flips the action bar layout automatically. Auto-detection seeds a pane's profile from `pane_current_command` the first time you visit it.
- **Structured agent snippets** — Slash commands shipped with variable placeholders: enum options like `/model {default|opus|sonnet|haiku}`, `/effort {low|medium|high|max|auto}`, `/permissions {Auto|Read Only|Full Access}` render as dropdowns; free-form args like `/add-dir {{path}}`, `/mention {{file}}`, `/compact {{focus}}` render as text fields. Sourced from current Claude Code and Codex CLI slash-command docs.
- **Custom snippets** — Save your own commands as snippets with categories, optional `{{var}}` placeholders, and send-immediate vs insert-into-compose modes.
- **Command history** — Recent commands from [+] menu. Full archive in Vault with search and save-as-snippet.
- **Custom keyboard** — Flutter-native QWERTY for direct input mode with Ctrl/Alt/Esc/arrows integrated natively. Toggle off for CJK input.
- **Direct input mode** — Keystroke-by-keystroke mode for vim, nano, and interactive CLIs
- **Modifier keys** — Ctrl and Alt as toggle buttons (tap to arm, double-tap to lock)
- **Key overlay** — Visual feedback showing key names on press (configurable per key category)

### File & Image Transfer
- **SFTP upload** — Pick files from phone, upload to server with progress tracking
- **SFTP download** — Browse remote directories, download files, share via system share sheet
- **Image transfer** — Send photos with format conversion, resize presets, and path injection
- **Bracketed paste** — Auto-wrap paths in bracketed paste mode for safe insertion

### Navigation Pad
- **D-pad mode** — Classic cross layout for arrow keys with auto-repeat on hold
- **Joystick mode** — Circular drag zone for directional input
- **Action buttons** — 2x2 grid of customizable keys (default: ESC, TAB, C-C, ENT)
- **Compact mode** — Single-row layout with arrows + action buttons
- **Gesture surface** — Overlay mode: swipe for arrows, double-tap for Tab, two-finger for Enter, three-finger for Escape, long-press for paste
- **Adaptive layout** — Auto-detects screen width for foldable/tablet optimization

### Raw PTY Mode
- **No-tmux connections** — Connect to servers without tmux installed, direct shell access
- **xterm VT state machine** — Headless xterm.dart Terminal processes PTY byte stream with full ANSI rendering
- **Terminal mode selector** — Choose tmux or raw shell per connection in the connection form
- **Full input support** — Action bar, nav pad, compose bar, and gesture surface all work in raw mode

### Help & Onboarding
- **Built-in help** — Tabbed cheat sheet for action bar buttons, gesture controls, and tmux keybindings
- **First-run walkthrough** — 4-card onboarding overlay explaining compose bar, action bar, insert menu, and terminal menu

### Other
- **Notification alerts** — Monitor tmux window flags (bell, activity, silence) across all connections
- **Deep linking** — `termipod://` URL scheme for opening specific terminal sessions from external apps (legacy `muxpod://` also accepted)
- **Tablet & foldable support** — Adapts layout for iPad, Android tablets, and foldable inner screens
- **Auto-resize** — Adjusts terminal dimensions to fit screen
- **i18n** — English and Chinese (Simplified), follows system locale
- **Feedback** — Send feedback directly from Settings > About via email

---

## How TermiPod Compares

| Feature | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|---------|----------|--------|----------|---------|------------|
| **Platform** | Android + iOS + iPad | Android | Android | Multi | Android |
| **tmux integration** | Native (visual navigation) | Manual (CLI) | None | None | None |
| **AI agent profiles** | Built-in Claude Code + Codex, per-pane state | None | None | None | None |
| **Action bar** | Swipeable, per-profile | Extra keys row | Basic buttons | Custom toolbar | None |
| **Snippet system** | Categories + agent presets | None | Snippet support | Snippet support | None |
| **SSH jump host** | Built-in ProxyJump | Via CLI | Via CLI | Built-in | None |
| **SOCKS5 proxy** | Built-in | Via CLI | None | None | None |
| **File transfer** | SFTP with UI | Local filesystem | None | SFTP | None |
| **Navigation pad** | D-pad/joystick + gestures | None | None | None | None |
| **Raw PTY mode** | No-tmux shell support | N/A (local) | Yes | Yes | Yes |
| **Help / cheat sheet** | Action bar + gestures + tmux | None | None | None | None |
| **Open source** | Yes (Apache 2.0) | Yes | No | No | Yes |
| **Price** | Free | Free | Freemium | Freemium | Free |

**TermiPod is not a local terminal emulator** (like Termux). It's a remote terminal client that connects to servers via SSH. Think of it as a mobile-optimized replacement for opening a terminal on your laptop, running `ssh server`, and attaching to a tmux session.

---

## Use Cases

### Monitor and interact with AI coding agents from your phone

SSH into your dev machine, attach to the tmux session running Claude Code or Codex CLI, and interact using optimized profiles with pre-configured buttons for `/help`, `/compact`, escape, and interrupt. Review diffs, approve changes, adjust effort — all from your phone while away from your desk.

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
| **Two-finger swipe** | Switch tmux panes |
| **Pinch** | Zoom in/out (50%-500%) |
| **Tap pane indicator** | Quick pane switcher with visual layout |

#### Navigation Pad & Gesture Surface

| Input | Action |
|-------|--------|
| **D-pad / Joystick** | Arrow keys (hold to repeat) |
| **Action buttons** | 4 customizable keys (ESC, TAB, C-C, ENT) |
| **Swipe on gesture surface** | Arrow keys (L/R/U/D) |
| **Double-tap** | Tab key |
| **Two-finger tap** | Enter key |
| **Three-finger tap** | Escape key |
| **Long-press** | Paste from clipboard |

### Action Bar

A single-row swipeable toolbar with per-profile button layouts:

```
<- [ESC] [TAB] [C-C] [y] [n] [C-D] [bolt] ->    [menu]
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

4 built-in profiles with per-pane state — each tmux pane remembers its own active profile, so walking between a `claude` pane and a `codex` pane flips the action bar automatically:

| Profile | Optimized For | Key Groups |
|---------|---------------|------------|
| **Claude Code** | `claude` | Quick, Navigate, Ctrl, Edit |
| **Codex** | `codex` | Quick, Navigate, Ctrl, Edit |
| **General** | Any terminal | Keys, Navigate, Chars, Page |
| **tmux** | tmux operations | Windows, Panes, Session, Copy |

Auto-detection seeds a pane's profile from `pane_current_command` the first time you visit it. Explicit user choices are never overwritten by subsequent auto-detect. Create custom profiles for any other CLI from the profile sheet.

### Deep Linking

Open TermiPod directly from external apps using the `termipod://` URL scheme. The legacy `muxpod://` scheme is also accepted during the transition period.

```
termipod://connect?server=<id>&session=<name>&window=<name>&pane=<index>
```

Works with [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) for tappable notifications that open the right terminal.

---

## Quick Start

### Install

**Android:** Download the latest APK from [**Releases**](https://github.com/physercoe/termipod/releases) and sideload.

**iOS / iPadOS:** No App Store build yet — build from source with Xcode (see below). TestFlight distribution is on the roadmap.

### Or build from source

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
flutter pub get

# Android
flutter build apk --release

# iOS / iPadOS (macOS with Xcode required)
flutter build ios --release
# or open ios/Runner.xcworkspace in Xcode and archive for device/TestFlight
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
| **Device** | Android 8.0+ (API 26), iOS 13.0+, iPadOS 13.0+ — phone, tablet, or foldable |
| **Server** | Any SSH server (OpenSSH, Dropbear, etc.) |
| **tmux** | Any version (tested with 2.9+) — optional in raw PTY mode |
| **Network** | Direct SSH, or via jump host / SOCKS5 proxy |

---

## Tech Stack

| | |
|---|---|
| **Framework** | Flutter 3.24+ / Dart 3.x |
| **SSH** | [dartssh2](https://pub.dev/packages/dartssh2) |
| **Terminal** | [xterm](https://pub.dev/packages/xterm) (rendering + headless VT for raw PTY) |
| **State** | [flutter_riverpod](https://pub.dev/packages/flutter_riverpod) |
| **Security** | [flutter_secure_storage](https://pub.dev/packages/flutter_secure_storage) (Android Keystore / iOS Keychain) |

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

- Hybrid xterm mode — Combine PTY stream rendering with tmux session navigation
- Local echo — Predictive character display for low-latency feel on slow connections
- Cursor alignment — Font glyph width calibration for pixel-perfect cursor positioning
- iOS TestFlight / App Store distribution
- Drag-to-reorder action bar buttons

---

## Acknowledgments

TermiPod is built on top of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura). Thanks for the excellent foundation.

## Feedback

Found a bug or have a feature request? [Open an issue](https://github.com/physercoe/termipod/issues) or send feedback from **Settings > About** in the app.

## License

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>Built with Flutter. Designed for mobile — phone, tablet, Android, iOS. Made for developers who live in the terminal.<br>
  <b>TermiPod</b> — SSH terminal | tmux manager | AI agent interface | mobile remote development</sub>
</p>
