<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>移动端 SSH 终端，专为 tmux 和 AI 编程助手打造。</b><br>
  <sub>用手机或平板管理远程服务器 — 运行 Claude Code、Codex 或任何 CLI 工具，触控优化的终端体验。Android、iOS、iPadOS 共用同一套 Flutter 代码。</sub>
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
  <a href="README.ja.md">日本語</a>
</p>

---

> **TermiPod** 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox，[Apache License 2.0](LICENSE)）开发。原版 MuxPod 是 Android 端的 SSH 连接与 tmux 基础查看工具。TermiPod 现已作为独立项目大幅扩展：新增跨平台支持（iOS/iPadOS）、操作栏与配置文件、代码片段、SSH 跳板机/SOCKS5 代理、SFTP 文件传输、导航盘、原始 PTY 模式、自定义键盘、帮助系统等。TermiPod 是独立项目，与原作者无关联。详见 [NOTICE](NOTICE)。

---

## TermiPod 是什么？

TermiPod 是一款 **跨平台移动 SSH 客户端和 tmux 管理器**,支持 Android、iOS 和 iPadOS — 专为在远程服务器上运行长期终端会话、需要随时用手机或平板查看和操作的开发者设计。基于 Flutter 构建,同一套触控优化的界面在各平台原生运行。

与通用 SSH 应用不同，TermiPod 围绕移动端终端的实际使用场景设计：

- **可视化 tmux 会话导航** — 点击切换会话、窗口和窗格,无需记忆快捷键
- **运行 AI 编程助手**(Claude Code、Codex)— 预配置按钮布局,以及可在下拉菜单中选择参数的结构化斜杠命令片段(`/model`、`/effort`、`/permissions`)
- **每个窗格独立的操作栏配置** — 每个 tmux 窗格记住自己的配置文件,从 `claude` 窗格切换到 `codex` 窗格时,按钮布局会自动切换
- **无需与键盘搏斗** — 编辑栏输入多行命令,操作栏快捷键,带变量占位符的代码片段
- **内置文件传输** — 通过 SFTP 上传下载文件,浏览远程目录
- **支持跳板机和代理** — SSH ProxyJump 和 SOCKS5 代理,连接 NAT 后面的机器

### 适用人群

- **开发者** — SSH 到开发机、CI 服务器或云端虚拟机
- **运维/SRE** — 在外出时检查服务、查看日志、重启进程
- **AI 代理用户** — 在 tmux 中运行 Claude Code、Codex 等 CLI 工具
- **家庭实验室爱好者** — 用手机管理服务器、树莓派、NAS
- **需要更好的移动 tmux 体验的人** — 比 Termux 或 JuiceSSH 更专业的 tmux 管理

---

## 功能特性

### SSH 连接
- **密码和密钥认证** — 支持 Ed25519 和 RSA 密钥，可在设备上生成或导入
- **SSH 跳板机 (ProxyJump)** — 通过堡垒机连接内网机器
- **SOCKS5 代理** — 通过企业代理、VPN 或 Shadowsocks/Clash 路由 SSH
- **连接测试** — 保存前验证 SSH + tmux 可用性
- **安全存储** — 密钥和密码通过 flutter_secure_storage 加密存储在 Android Keystore / iOS Keychain 中
- **零服务器配置** — 只要有 `sshd` + `tmux` 即可，无需额外安装

### tmux 会话管理
- **可视化导航** — 面包屑标题栏点击切换会话/窗口/窗格
- **窗格布局预览** — 准确的分屏比例可视化
- **双指滑动** — 在 tmux 分屏间导航
- **创建/重命名/关闭** 会话和窗口
- **ANSI 颜色支持** — 完整的 256 色终端渲染

### 输入 UX(移动优化)
- **操作栏** — 可滑动按钮组,每个配置文件有专属布局。ESC、Tab、Ctrl+C、方向键,一触即达。
- **编辑栏** — 多行文本输入,带发送按钮。输入命令、检查、发送。长按发送可省略 Enter。
- **3 个内置配置文件** — Claude Code、Codex、通用终端。可为任意其他 CLI 创建自定义配置文件。
- **每个窗格独立的配置状态** — 每个 tmux 窗格记住自己的活动配置文件,切换窗格时操作栏布局自动切换。首次访问窗格时,从 `pane_current_command` 自动检测并设定。
- **结构化代理命令片段** — 内置 Claude Code 和 Codex 的斜杠命令,带变量占位符:枚举选项如 `/model {default|opus|sonnet|haiku}`、`/effort {low|medium|high|max|auto}`、`/permissions {Auto|Read Only|Full Access}` 渲染为下拉菜单,自由输入参数如 `/add-dir {{path}}`、`/mention {{file}}` 渲染为文本框。
- **自定义代码片段** — 自己保存命令,带分类、`{{var}}` 占位符,以及立即发送/插入编辑栏两种模式。
- **命令历史** — 从 [+] 菜单访问最近命令。在保险库中搜索完整历史。
- **自定义键盘** — Flutter 原生 QWERTY 键盘,集成 Ctrl/Alt/Esc/方向键,用于直接输入模式。需要 CJK 输入时可关闭。
- **直接输入模式** — 逐键输入模式,适合 vim、nano 等交互式 CLI
- **修饰键** — Ctrl 和 Alt 作为切换按钮(单击激活,双击锁定)
- **按键叠加层** — 按键时显示键名的视觉反馈

### 文件传输
- **SFTP 上传** — 从手机选择文件上传到服务器，带进度追踪
- **SFTP 下载** — 浏览远程目录，下载文件，通过 Android 分享功能分享
- **图片传输** — 发送照片，支持格式转换、尺寸调整和路径注入

### 帮助与引导
- **内置帮助** — 操作栏按钮和 tmux 快捷键速查表，从终端菜单访问
- **首次使用引导** — 4 张引导卡片介绍编辑栏、操作栏、插入菜单和终端菜单

---

## 与同类应用对比

| 功能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **tmux 集成** | 原生（可视化导航） | 手动（命令行） | 无 | 无 | 无 |
| **AI 代理配置** | 内置 Claude Code + Codex,每个窗格独立状态 | 无 | 无 | 无 | 无 |
| **SSH 跳板机** | 内置 | 命令行 | 命令行 | 内置 | 无 |
| **SOCKS5 代理** | 内置 | 命令行 | 无 | 无 | 无 |
| **文件传输** | SFTP（带 UI） | 本地文件系统 | 无 | SFTP | 无 |
| **开源** | 是 (Apache 2.0) | 是 | 否 | 否 | 是 |

---

## 快速开始

### 安装

**Android:** 从 [**Releases**](https://github.com/physercoe/termipod/releases) 下载最新 APK 并侧载安装。

**iOS / iPadOS:** 暂无 App Store 版本 — 请使用 Xcode 从源码构建(见下文)。TestFlight 分发在计划中。

### 从源码构建

```bash
git clone https://github.com/physercoe/termipod.git
cd mux-pod
flutter pub get

# Android
flutter build apk --release

# iOS / iPadOS(需要 macOS + Xcode)
flutter build ios --release
# 或在 Xcode 中打开 ios/Runner.xcworkspace 进行真机 / TestFlight 归档
```

### 连接

1. **添加服务器** — 在服务器标签页点击 +，输入主机/端口/用户名
2. **认证** — 选择密码或 SSH 密钥（可在保险库 > 密钥中生成）
3. **可选：配置跳板机或代理** — 在连接表单中展开跳板机或 SOCKS5 代理部分
4. **导航** — 展开服务器 > 选择会话 > 点击窗口 > 选择窗格
5. **操作** — 使用操作栏发送快捷键，编辑栏输入命令，[+] 访问代码片段和文件传输

---

## 系统要求

| 组件 | 要求 |
|------|------|
| **设备** | Android 8.0+(API 26)、iOS 13.0+、iPadOS 13.0+ — 手机、平板或折叠屏 |
| **服务器** | 任意 SSH 服务器（OpenSSH、Dropbear 等） |
| **tmux** | 任意版本（2.9+ 已测试） |
| **网络** | 直连 SSH，或通过跳板机 / SOCKS5 代理 |

---

## 路线图

- 混合 xterm 模式 — 将 PTY 流渲染与 tmux 会话导航结合
- 本地回显 — 预测性字符显示,在慢速连接上获得低延迟输入体验
- 光标对齐 — 基于字体字形宽度校准,实现像素级精确光标定位

---

## 致谢

TermiPod 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod) 构建。感谢出色的基础工作。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>使用 Flutter 构建。为移动端设计。为终端而生的开发者工具。</sub>
</p>
