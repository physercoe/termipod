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
  <a href="README.ja.md">日本語</a> &nbsp;|&nbsp;
  <a href="README.zh.md">中文</a>
</p>

---

> **TermiPod** is a fork of [MuxPod](https://github.com/moezakura/mux-pod) by [@moezakura](https://github.com/moezakura), adding i18n support, a complete input UX redesign with CLI agent profiles, code snippets, command history, and other enhancements.

---

<!-- TODO: Replace with updated demo video showing new action bar + compose bar UI -->
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
- **CLI agent optimized** — Built-in profiles for Claude Code, Codex, Kimi Code, OpenCode, and Aider with per-agent button layouts and command presets.
- **File transfer built-in** — Upload and download files via SFTP without leaving the terminal. Browse remote directories, pick files, track progress.
- **Multi-language** — English and Chinese (Simplified) out of the box, follows system locale.

---

## What's New in TermiPod

Compared to the upstream [MuxPod](https://github.com/moezakura/mux-pod):

### Input UX Redesign
- **Action bar** — Single-row swipeable button groups replacing the old fixed special keys bar. Each profile defines its own groups (Quick, Navigate, Ctrl, Edit, etc.) with 4-6 buttons per page.
- **Compose bar** — Always-visible primary input with `[+]` insert menu, multi-line text field, and Send button (tap = send with Enter, long-press = send without Enter). Clear button appears when text is entered.
- **6 built-in profiles** — Claude Code, Codex, Kimi Code, OpenCode, Aider, and General Terminal — each with optimized button layouts.
- **Profile auto-detection** — Suggests switching profile based on `pane_current_command` (e.g., detects `claude` running and offers Claude Code profile).
- **Full customization** — Reorder groups, add/remove buttons, create custom profiles, all from Settings > Toolbar.

### Snippets & Command History
- **Snippet system** — Save frequently-used commands as snippets with categories. Preset agent-specific commands (e.g., `/help`, `/compact`, `/model`) ship per profile.
- **Bolt button** — Quick access to snippet picker from the action bar. Shows preset agent commands for the active profile plus user snippets, with search and categories.
- **Command history** — Recent commands (last 10) accessible from `[+]` menu. Full history searchable in Vault. Save any history item as a snippet.

### Vault
- **Keys** — Generate Ed25519/RSA keys on-device, import existing keys. Stored securely with optional passphrase.
- **Snippets** — Manage user-created snippets with name, content, and category.
- **History** — Full command history archive with search, swipe-to-delete, and save-as-snippet.

### Key Overlay
- **Visual key feedback** — Configurable overlay showing key names on press (modifiers, special keys, arrows, shortcuts). Adjustable position (above keyboard, center, below header).

### Other Features
- **i18n** — Full English and Chinese Simplified localization across all screens, auto-detects system language.
- **File transfer** — Upload files from phone to server and download remote files via SFTP, with progress tracking, remote file browser, and Android share integration.
- **Image transfer** — Send photos from camera or gallery to the server with format conversion, resize presets, and path injection into terminal.
- **Notification alerts** — Monitor tmux window flags (bell, activity, silence) across all connections in real-time.
- **Deep linking** — `muxpod://` URL scheme for external app integration (e.g., Telegram notifications that open the right terminal).
- **Foldable device support** — Adapts UI for large inner screens on foldable phones.
- **Auto-resize** — Adjusts terminal font size or tmux pane dimensions to fit screen.
- **Bug fixes** — Scroll-on-resize, session list separation, back button handling, CSI private mode sequences, and more.

---

## App Structure

TermiPod uses a 5-tab navigation with Dashboard at the center for quick session access.

| Dashboard | Servers | Alerts | Vault | Settings |
|:---------:|:-------:|:------:|:-----:|:--------:|
| <!-- TODO: update screenshot --> <img src="docs/screens/dashboard.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/servers.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/alerts.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/keys.png" width="160"> | <!-- TODO: update screenshot --> <img src="docs/screens/settings.png" width="160"> |

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

### Vault

Three sections in a scrollable list:
- **Keys** — Generate or import SSH keys. One-tap copy public key.
- **Snippets** — Manage saved commands with name, content, and category.
- **History** — Full command history with search, delete, and save-as-snippet.

### Settings

| Section | Options |
|---------|---------|
| **Terminal** | Cursor, adjust mode, font size/family, scrollback lines |
| **Key Overlay** | Toggle per key category, position |
| **Toolbar** | Active profile, customize groups |
| **Behavior** | Haptic feedback, keep screen on, invert pane navigation |
| **Appearance** | Theme (dark/light), language (English/Chinese/System) |
| **Image Transfer** | Remote path, format, quality, resize, path format |
| **File Transfer** | Remote path, path format, auto-enter, bracketed paste |

---

## Terminal Experience

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
| **Pinch** | Zoom in/out (50%–500%) |
| **Tap pane indicator** | Quick pane switcher with visual layout |

### Action Bar

A single-row swipeable toolbar replacing the old fixed special keys bar:

```
← [ESC] [TAB] [C-C] [y] [n] [C-D] [⚡] →    [⋮]
                    · · ● · ·
```

- **Swipe** between button groups (Quick, Navigate, Ctrl, Edit, etc.)
- **Page dots** show current position
- **[⋮]** opens profile sheet — switch profiles, manage snippets, reset to default
- **[⚡] bolt** opens snippet picker — agent commands + user snippets
- **Modifier keys** (CTRL, ALT) toggle on tap, lock on double-tap
- **Confirm buttons** (y/n) send with Enter on tap, without Enter on long-press

### Compose Bar

Always-visible primary input below the action bar:

```
[+] [ Type command or prompt...        ][×] [▶]
```

- **[+]** insert menu — Recent, File Upload/Download, Image Transfer, Direct Input toggle
- **Multi-line** text field with auto-expand
- **[▶] Send** — tap sends with Enter, long-press sends without Enter
- **[×] Clear** — tap clears text, long-press clears + sends C-u

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
2. **Authenticate** — Choose password or SSH key (generate in Keys tab)
3. **Navigate** — Expand server > select session > tap window > choose pane
4. **Interact** — Use the action bar, compose bar, or Direct Input mode

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
