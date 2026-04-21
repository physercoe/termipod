<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>移动端 SSH 终端，专为 tmux 和 AI 编程助手打造。</b><br>
  <sub>用手机或平板管理远程服务器。运行 Claude Code、Codex 或任何 CLI 工具，触控优化的终端体验。<br>Android、iOS、iPadOS — 同一套 Flutter 代码。</sub>
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

## 截图

<table>
<tr>
<td align="center"><b>仪表盘</b></td>
<td align="center"><b>代理命令</b></td>
<td align="center"><b>按键面板</b></td>
</tr>
<tr>
<td><img src="docs/screens/dashboard_dark.png" width="240" alt="仪表盘"></td>
<td><img src="docs/screens/bolt_menu_dark.png" width="240" alt="Claude Code 斜杠命令"></td>
<td><img src="docs/screens/key_palette_dark.png" width="240" alt="配置文件面板"></td>
</tr>
<tr>
<td align="center"><b>终端</b></td>
<td align="center"><b>保险库（密钥和片段）</b></td>
<td align="center"><b>插入菜单</b></td>
</tr>
<tr>
<td><img src="docs/screens/terminal_dark.png" width="240" alt="操作栏终端"></td>
<td><img src="docs/screens/vault_dark.png" width="240" alt="SSH 密钥、代码片段、命令历史"></td>
<td><img src="docs/screens/insert_menu_dark.png" width="240" alt="插入菜单"></td>
</tr>
</table>

---

## TermiPod 是什么？

与通用 SSH 应用不同，TermiPod 围绕移动端终端的实际使用场景设计：

- **服务端零配置** — 任何运行 `sshd` 的机器都能直接连。无需在服务端安装代理、守护进程或任何额外组件
- **可视化 tmux 会话导航** — 点击切换会话、窗口和窗格，无需记忆快捷键
- **仪表盘一键重连** — 最近会话按访问时间排序，点击即可回到上次的窗口和窗格
- **运行 AI 编程助手**（Claude Code、Codex、Aider）— 预配置按钮布局和结构化斜杠命令
- **每个窗格独立的配置** — 每个 tmux 窗格记住自己的操作栏布局，自动切换
- **自定义键盘** — Flutter 原生 QWERTY，集成 Ctrl/Alt/Esc/方向键
- **文件传输** — 通过 SFTP 上传下载文件，浏览远程目录
- **跳板机和代理** — SSH ProxyJump 和 SOCKS5 代理
- **弱网下不掉线** — 指数退避自动重连；断线期间输入会进入队列，连接恢复后自动发送

### 适用人群

| | |
|---|---|
| **AI 代理用户** | 在 tmux 中运行 Claude Code / Codex，用手机监控和交互 |
| **开发者** | SSH 到开发机、CI 服务器或云端虚拟机 |
| **运维/SRE** | 在外出时检查服务、查看日志、重启进程 |
| **家庭实验室爱好者** | 用手机管理服务器、树莓派、NAS |

---

## 功能特性

### SSH 连接
- **Ed25519/RSA 密钥** — 设备本地生成（RSA 支持 2048 / 3072 / 4096 位）或导入。加密存储于 Android Keystore / iOS Keychain，可选密码保护，公钥一键复制
- **SSH 跳板机 (ProxyJump)** — 通过堡垒机连接内网机器
- **SOCKS5 代理** — 通过企业代理、VPN 或 Shadowsocks/Clash 路由 SSH
- **原始 PTY 模式** — 无需 tmux 的直接 Shell 访问，从 tmux 连接卡片也可一键打开
- **连接测试** — 保存前验证 SSH + tmux 可用性
- **指数退避自动重连** — 最多 5 次重试。断线期间输入的命令会进入队列，连接恢复后自动发送
- **延迟指示器** — 标题栏实时显示 ping 值（绿色 &lt; 100 ms，红色 &gt; 500 ms），一眼看出卡顿来自手指还是网络
- **自适应轮询** — 操作活跃时 50 ms，空闲时降至 500 ms，节省电量
- **后台连接服务** — Android 前台服务保持 SSH 在后台运行，长时会话可选保持屏幕常亮

### tmux 会话管理
- **仪表盘** — 最近会话按访问时间排序，显示相对时间（"刚刚"、"5 分钟前"）；点击即可重连并恢复上次的窗口与窗格
- **可视化导航** — 面包屑标题栏点击切换会话/窗口/窗格
- **窗格布局预览** — 准确的分屏比例可视化，点击任意窗格即可聚焦
- **双指滑动** — 在 tmux 分屏间导航
- **捏合缩放** — 终端字体大小 50%–500% 缩放
- **复制 / 滚动模式** — 切换后选择文本时画面不会跳动，退出时选区自动复制到系统剪贴板
- **创建/重命名/关闭** 会话和窗口
- **Bell / Activity / Silence 警报** — 跨连接监控 tmux 窗口标志，点击警报直达对应窗口与窗格（警报自动清除）
- **256 色 ANSI** 终端渲染 + 自动扩展滚动缓冲

### 输入 UX（移动优化）

| 组件 | 功能 |
|------|------|
| **操作栏** | 每个配置文件有专属按钮组 — ESC、Tab、Ctrl+C，一触即达 |
| **编辑栏** | 多行文本输入，带发送按钮。多行内容以**括号粘贴**整体送达，AI 助手与 Shell 收到的是完整文本块，而非逐行执行的多条命令。长按发送可省略 Enter |
| **直接输入模式** | 实时按键流式发送，带在线指示灯。每次点按直达 pty，适合 vim、less、htop 与各类 REPL |
| **自定义键盘** | Flutter 原生 QWERTY，含 Ctrl/Alt/Esc/方向键。原本闲置的编辑行现集成**实时按键条**（Home / End / PgUp / PgDn / Del + 脉冲指示灯）。启用导航盘 / 摇杆时方向键行自动隐藏。CJK / 语音输入时可整体关闭 |
| **导航盘** | D-pad、摇杆或手势面板 |
| **代码片段** | 枚举选项为下拉菜单，自由输入为文本框的斜杠命令。**长按 Bolt 键**可将当前编辑栏内容存为草稿片段 |
| **修饰键** | Ctrl/Alt — 单击激活，双击锁定 |

**4 个内置配置文件** — Claude Code、Codex、通用终端、tmux。可为任意 CLI 创建自定义配置文件。

### 文件传输
- **SFTP 上传/下载** — 带进度追踪和远程目录浏览
- **图片传输** — 发送照片，支持格式转换、尺寸调整和路径注入

### Termipod Hub（可选）

面向团队协调多个 AI 编码代理的可选层。在 **设置 → Hub** 粘贴 hub URL 与 bearer token 后，TermiPod 将启用一整套工作区界面：

- **Inbox**（首页）— 整合 attention 项、未读频道、最近任务的统一工作流收件箱，支持搜索，顶部有待处理项 SliverAppBar
- **Projects** — 项目清单与 Linear 风格详情页（概览 / 任务 / 频道 / Docs / Blobs）。支持任务创建、Markdown Docs 只读查看、Blob 附件上传与下载
- **Agents** — List / Tree 视图可切换；Tree 按 `agent_spawns` 渲染父→子组织图。FAB 打开 YAML **Spawn Agent** 表单，支持模板选择、主机选择与端侧 **保存预设**（handle + kind + YAML）
- **Hosts** — Host-agent 签到与最近在线时间
- **Templates** — 浏览团队共享的 agent / prompt / policy YAML
- **Team** 设置屏 — **Schedules**（cron 触发定时 spawn）、**Usage**（按项目 / 代理汇总的预算仪表盘）、Members、Policies、Channels

Hub 本体是 `hub/` 下的独立 Go 守护进程，可通过 `go install` 或直接运行源码部署。详见 [docs/hub-mobile-test.md](docs/hub-mobile-test.md)。

### 其他
- **数据导出/导入** — 将连接、密钥、代码片段、历史记录和设置导出为 JSON 备份文件，支持跨设备恢复和从旧版 MuxPod 迁移
- **内置文件浏览器** — 在设置中管理 SFTP 下载与应用存储，可直接分享或删除
- **更新检查** — 设置 → 检查更新，向 GitHub Releases 查询最新版并直达 APK 下载
- **帮助与新手引导** — 操作栏与 tmux 快捷键速查表，4 卡片教程
- **深度链接** — `termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>` 可从外部应用直接跳转到指定的服务器 / 会话 / 窗口 / 窗格。每个连接可设置稳定的 **Deep Link ID**（编辑界面中），改名后 URL 仍然可用；与 [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) 配合，点 Telegram 通知即可直达对应窗格。旧版 `muxpod://` URL 同样可解析
- **平板与折叠屏** 自适应布局
- **多语言** — 英文与中文，跟随系统语言

---

## 与同类应用对比

| 功能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **平台** | Android + iOS + iPad | Android | Android | 多平台 | Android |
| **tmux 集成** | 原生可视化 | 手动命令行 | 无 | 无 | 无 |
| **AI 代理配置** | Claude Code + Codex，每个窗格独立 | 无 | 无 | 无 | 无 |
| **SSH 跳板机** | 内置 | 命令行 | 命令行 | 内置 | 无 |
| **SOCKS5 代理** | 内置 | 命令行 | 无 | 无 | 无 |
| **文件传输** | SFTP（带 UI） | 本地文件系统 | 无 | SFTP | 无 |
| **开源** | 是 (Apache 2.0) | 是 | 否 | 否 | 是 |

---

## 快速开始

### 安装

**Android:** 从 [**Releases**](https://github.com/physercoe/termipod/releases) 下载最新 APK 并安装。

**iOS / iPadOS:** 请使用 Xcode 从源码构建。TestFlight 分发在计划中。

### 从源码构建

```bash
git clone https://github.com/physercoe/termipod.git
cd termipod
flutter pub get

# Android
flutter build apk --release

# iOS / iPadOS（需要 macOS + Xcode）
flutter build ios --release
```

### 连接

1. **添加服务器** — 在服务器标签页点击 +，输入主机/端口/用户名
2. **认证** — 选择密码或 SSH 密钥（可在保险库 > 密钥中生成）
3. **可选** — 在连接表单中配置跳板机或 SOCKS5 代理
4. **导航** — 展开服务器 > 会话 > 窗口 > 窗格
5. **操作** — 操作栏发送快捷键，编辑栏输入命令，[+] 访问代码片段和文件传输

---

## 系统要求

| 组件 | 要求 |
|------|------|
| **设备** | Android 8.0+(API 26)、iOS 13.0+、iPadOS 13.0+ |
| **服务器** | 任意 SSH 服务器（OpenSSH、Dropbear 等） |
| **tmux** | 任意版本（2.9+ 已测试）— 原始 PTY 模式下可选 |
| **网络** | 直连 SSH，或通过跳板机 / SOCKS5 代理 |

---

## 路线图

- 混合 xterm 模式 — 将 PTY 流渲染与 tmux 会话导航结合
- Mosh 支持 — UDP 传输与 IP 漫游，移动弱网场景的最佳选择
- AI 代理输出监控 — 重新设计 Notify 标签页，监听 Claude Code / Codex 窗格中的提示、失败与完成模式
- 本地回显 — 预测性字符显示，低延迟输入体验
- 光标对齐 — 基于字体字形宽度校准
- iOS TestFlight / App Store 分发

---

## 致谢

TermiPod 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox，[Apache License 2.0](LICENSE)）开发。TermiPod 是独立项目，与原作者无关联。详见 [NOTICE](NOTICE)。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>使用 Flutter 构建。为移动端设计。为终端而生的开发者工具。</sub>
</p>
