<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod logo — chevron prompt with twinkling 4-point sparkle" width="140" height="140">
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

- **Zero server setup** — works with any host running `sshd`. No agents, no daemons, nothing to install on the server side
- **Navigate tmux visually** — tap through sessions, windows, and panes instead of memorizing `Ctrl-b` keybindings
- **Dashboard with one-tap reconnect** — recent sessions sorted by last access, with relative timestamps; tap to jump straight back to the last window and pane you were on
- **Run AI coding agents** (Claude Code, Codex, Aider) with pre-configured button layouts and structured slash-command snippets — pick `/model`, `/effort`, `/permissions` from dropdowns instead of typing them
- **Per-pane profiles** — each tmux pane remembers its own action bar layout, auto-switching when you move between panes
- **Custom keyboard** — Flutter-native QWERTY with Ctrl/Alt/Esc/arrows built in, no more hunting through the system IME
- **Transfer files** via SFTP — upload from phone, download, browse remote directories
- **Jump hosts & proxies** — SSH ProxyJump and SOCKS5 for machines behind NAT or firewalls
- **Survives flaky networks** — auto-reconnect with exponential backoff, input queued while offline so nothing is lost

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
- **Ed25519 / RSA keys** — generate on-device (RSA 2048 / 3072 / 4096) or import, stored in Android Keystore / iOS Keychain with optional passphrase, one-tap public-key copy
- **SSH ProxyJump** — connect through bastion/jump hosts to internal machines
- **SOCKS5 proxy** — route through corporate proxies, VPNs, or Shadowsocks/Clash
- **Raw PTY mode** — direct shell access for servers without tmux, with a one-tap shortcut from any tmux connection card
- **Connection testing** — verify SSH + tmux before saving
- **Auto-reconnect with exponential backoff** — up to 5 retries; commands you type while disconnected are queued and flushed automatically once the link is back
- **Latency indicator** — live ping in the header (color-coded: green &lt; 100 ms, red &gt; 500 ms) so you know whether the lag is your fingers or the network
- **Adaptive polling** — refresh rate ramps from 50 ms (active) down to 500 ms (idle) to save battery
- **Background connection service** — Android foreground service keeps SSH alive while the app is backgrounded; optional keep-screen-on for long sessions

### tmux Session Management
- **Dashboard** — recent sessions sorted by last access with relative timestamps ("Just now", "5 min ago"); one tap reconnects and restores the last window + pane
- **Visual navigation** — breadcrumb header: tap Session > Window > Pane to switch
- **Pane layout view** — accurate proportional split visualization, tap any pane to focus
- **Two-finger swipe** between panes
- **Pinch to zoom** the terminal (50%–500%) for quick readability bumps
- **Copy / scroll mode** — toggle to select text without the screen jumping; updates buffer until you exit and copy lands in the system clipboard
- **Create / rename / close** sessions and windows
- **Bell / Activity / Silence alerts** — tmux window flags monitored across all connections; tap any alert to jump straight to that window and pane (alert auto-clears)
- **256-color ANSI** terminal rendering with auto-extend scrollback

### Input UX (Mobile-Optimized)

| Component | What it does |
|-----------|-------------|
| **Action bar** | Swipeable button groups per profile — ESC, Tab, Ctrl+C, arrows, one tap away |
| **Compose bar** | Multi-line text field with send button. Multi-line input ships as **one bracketed paste** so AI agents and shells see the block intact, not N separate commands. Long-press send to omit Enter |
| **Direct Input mode** | Real-time keystroke streaming with a live indicator — every tap goes straight to the pty, ideal for vim, less, htop, REPLs |
| **Custom keyboard** | Flutter-native QWERTY with Ctrl/Alt/Esc/arrows. Built-in **live key strip** (Home / End / PgUp / PgDn / Del + pulse indicator) replaces the wasted compose-row gap. Arrow row auto-hides when nav pad / joystick is on. Toggle off entirely for CJK / voice input |
| **Navigation pad** | D-pad, joystick, or gesture surface for arrow keys + action buttons |
| **Snippets** | Slash commands with dropdowns for enums, text fields for free-form args. **Long-press the bolt key** to stash the current compose text as a draft snippet |
| **Modifier keys** | Ctrl / Alt as toggle buttons — tap to arm, double-tap to lock |

**4 built-in profiles** — Claude Code, Codex, General Terminal, tmux — each with optimized button groups. Create custom profiles for any CLI. Each pane remembers its profile and auto-detects from `pane_current_command`.

### File Transfer
- **SFTP upload/download** with progress tracking and remote directory browser
- **Image transfer** with format conversion, resize presets, and path injection

### Termipod Hub (optional)

Opt-in coordination layer for teams running multiple AI coding agents. Paste a hub URL + bearer token in **Settings → Hub** and the bottom **Inbox** tab plus the **Hub** tab come alive.

**Inbox** — unified triage: approvals, agent states (idle / errored), messages, and SSH sessions in one filterable feed. Tap a pending approval to Approve / Reject inline; tap the magnifier to full-text search across all hub events.

**Hub** — four sub-tabs:
- **Projects** — Linear-style project detail with Activity / Tasks / Agents / Docs / Blobs / Info pill sections. Activity chat streams via SSE; Tasks are a Kanban with full-screen detail; Docs are a read-only browser over the project's `docs_root` with a markdown viewer; Blobs are cached device-local uploads shareable by any chat
- **Agents** — list or tree view walking `agent_spawns` for a parent→child org chart; FAB spawns via YAML with template picker, host picker, and saved presets
- **Hosts** — host-runner check-ins with last-seen timestamps
- **Templates** — browse team-wide agent / prompt / policy YAML

**Team** screen (header icon on Hub) — Members, Policies, team-scope channels (including the `#hub-meta` steward room, reachable from the AppBar chip), and **Settings** with cron **Schedules** and per-agent **Usage / budget** rollups.

The hub itself ships as a separate Go daemon under `hub/` — install with `go install` or run from source. See [docs/hub-mobile-test.md](docs/hub-mobile-test.md) for setup and tab-by-tab verification.

### Other
- **Data export / import** — full JSON backup of connections, keys, snippets, history, and settings; restore on a new device or migrate from the legacy MuxPod app
- **Built-in file browser** — manage SFTP downloads and app storage from Settings, share or delete files in place
- **Update checker** — Settings → Check for updates queries GitHub releases and links to the latest APK
- **Help & onboarding** — cheat sheet for action bar + tmux keybindings, 4-card walkthrough
- **Deep linking** — `termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>` opens a specific server / session / window / pane from external apps. Each connection has a stable **Deep Link ID** (set in Edit) so URLs survive renames; pairs with [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) to tap a Telegram alert straight into the right pane. Legacy `muxpod://` URLs still resolve
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
- Mosh support — UDP transport with IP roaming, best-in-class for flaky mobile networks
- Agent output monitoring — redesigned Notify tab that watches panes for patterns (prompts, failures, completion) from Claude Code / Codex
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
