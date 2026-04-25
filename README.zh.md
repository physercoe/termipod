<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>AI Agent 舰队的移动指挥中心。</b><br>
  <sub>在手机上下达目标，Steward Agent 拆解为计划，分布于你自有基础设施上的 Agent 舰队执行，你审批并审阅结果。<br>Android、iOS、iPadOS — 开源自托管，Apache 2.0。</sub>
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
  <a href="README.md">English</a>
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

面向团队在多台机器上协调多个 AI 编码代理的可选层。在 **设置 → Hub** 粘贴 hub URL 与 bearer token 后,五个底部 Tab — **Projects · Activity · Me · Hosts · Settings** — 全部启用。

**研究 Demo 工作流:** 在手机上写下项目 directive → Steward 代理将其分解为 plan → 工人代理通过跨主机 A2A 在 GPU 主机上并行执行 runs → Briefing 代理在夜间汇总为可评审的文档。每一步都会浮到手机上;用户是审批/评审者而非操作者。Hub 内置 ablation sweep、论文复现、benchmark 对比等模板。

- **Me**（中心,默认）— 个人 triage:待审批项、紧急任务、最近活动摘要,以及 Vault 入口。在卡片上直接 Approve / Reject。
- **Projects** — 项目清单。点开任意项目进入详情页:Overview / Tasks / Plans / Runs / Reviews / **Outputs**(run 产出的 checkpoint / 曲线 / 报告) / Documents / Blobs / Channels / Schedules,以及按 `agent_spawns` 渲染父→子关系的 Agents 视图。FAB 打开 YAML Spawn 表单(模板 / 主机选择 + 保存预设),Steward 拥有独立的 spawn 流程。**trackio / wandb / TensorBoard** 指标摘要自动以 sparkline 出现在 Run 详情。项目名称 / 目标 / 模板 / docs root / 预算均可原地编辑。
- **Activity** — 团队级审计 feed(audit_events 提升至顶层 Tab):策略变更、模板编辑、代理生命周期、频道发帖、run 状态切换,统一的可筛选时间线。
- **Hosts** — Host-runner 签到与最近在线时间。NAT 背后的主机会把代理卡片发布到 hub 目录,并通过**反向隧道中继**接收 peer A2A 调用,因此 VPS 上的 Steward 代理可以端到端地调用 GPU 机器上的工人。
- **Team** 屏(Projects 头部图标进入)— Members / Policies / 团队级频道(包含 `#hub-meta` Steward 房间,可从 AppBar chip 直达),Team Settings 内含 cron **Schedules**、按代理汇总的 **Usage / 预算**、**审计日志**,以及 **Templates** 浏览器(团队共享的 agent / prompt / policy YAML)。决定行为的东西 — 项目模板、代理 skills、launcher 命令 — 都是磁盘上可编辑的数据;新增代理 kind 无需改代码。

Hub 本体是 `hub/` 下的独立 Go 守护进程,可通过 `go install` 或直接运行源码部署。详见 [docs/hub-mobile-test.md](docs/hub-mobile-test.md)。

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

TermiPod 的 MVP 是 `docs/blueprint.md` §9 Phase 4 的**研究 Demo**：
用户写下 directive → Steward 进行分解 → 代理队伍跨主机执行 runs →
Briefing 代理在夜间汇总 → 用户在手机上评审。路线图围绕该 Demo 跟踪；
已发布的 P0–P2 已并入上方功能列表。

Demo 路径已端到端就绪:以 YAML overlay 方式内置的项目模板
(ablation-sweep / reproduce-paper / benchmark-comparison / write-memo)、
具体的 Steward 分解 recipe、带 cron 的 Briefing 代理、为 NAT 背后
GPU 主机准备的反向隧道中继用于跨主机 A2A、以及在 run 详情页以内嵌
sparkline 呈现的 trackio / wandb / TensorBoard 指标摘要。手机上每一个
hub CRUD 界面都有对应的 Steward MCP 工具,因此手机是"审批/评审"的场所,
而非"操作"的场所。

尚未完成:

- **iOS TestFlight / App Store 分发** — Android APK 已发布;iOS 目前仅支持
  本地构建。TestFlight 是下一步分发工作。
- **Projects / Channels 标签页的活动流** — v1.0.160 已将各屏幕的 "+ 新建"
  降级到溢出菜单。要让审批/评审姿态真正到位,剩下的是在登录页展示统一的
  近期活动流(runs / docs / attention / schedule 触发)。
- **A2A peer 认证** — 为反向隧道中继添加按代理的 token,使跨团队调用能端
  到端地被鉴权。

差距状态见 [docs/research-demo-gaps.md](docs/research-demo-gaps.md)，
完整阶段计划见 [docs/blueprint.md](docs/blueprint.md) §9。

---

## 致谢

TermiPod 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox，[Apache License 2.0](LICENSE)）开发。TermiPod 是独立项目，与原作者无关联。详见 [NOTICE](NOTICE)。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>使用 Flutter 构建。为移动端设计。为终端而生的开发者工具。</sub>
</p>
