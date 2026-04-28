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

## 与众不同之处

其他「在手机上跑 Claude Code」类的工具都是单个本地会话的 1:1 桥接。TermiPod 是面向舰队的 1:N 控制面：

- **多代理** — 同时调度 Claude Code、Codex、Aider 或任何 CLI；每个都有独立的窗格、配置文件和预算。所有代理共享同一个收件箱。
- **多主机** — 跑在 5 美元/月 VPS 上的 Steward 代理，可以通过反向隧道中继上的 A2A，把任务下发给家里 NAT 后面的 GPU 主机。其他工具都没有这种跨主机能力。
- **导演而非操作员** — 写下自然语言目标，Steward 拆解成 plan，你审批。无需编排 DAG,也无需盯着终端。
- **内建治理** — 预算上限、策略覆盖、按代理用量、不可篡改审计日志、团队角色。其他移动代理工具都没有这些。
- **离线优先** — 当 hub 不可达时,每个屏幕都用 SQLite 快照缓存呈现上一份可用数据。地铁里也能看。
- **不绑定厂商,自托管** — Apache 2.0 协议的 Go hub,自己跑。无账号门槛、无云端中继、无厂商锁定。

**30 秒 Demo。** 在手机上输入「对 nanoGPT 做 ablation sweep,告诉我哪个 optimizer 扩展性更好」。VPS 上的 Steward 返回一份 6 步计划,你点 Approve。家里的 GPU 主机起 6 个训练 run。三小时后,一份带 loss 曲线的简报文档落到你的 **Me** Tab,你在回家的地铁上完成审阅。

完整的论点陈述、目标用户画像与竞争分析见 [docs/discussions/positioning.md](docs/discussions/positioning.md)。完整的文档索引在 [docs/README.md](docs/README.md)。

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

## 为什么选择 TermiPod？

AI 代理产出的内容量是任何人类审阅速度的十倍以上。一旦你同时跑**多个**代理,或跨**多台**机器,现有的移动端工具就会崩溃 — 会话桥接类应用(Claude Code Remote Control、Happy、Tactic Remote)只能把单个会话装进口袋;消息桥接类代理(OpenClaw、Hermes、Claude Code Channels)只能在你的聊天 App 里塞一个对话助手。两者都不是舰队驾驶舱。TermiPod 基于另一条公理:**人是导演,不是操作员**。

| 维度 | Remote Control / Happy / Tactic | TermiPod |
|---|---|---|
| 拓扑 | 1 手机 ↔ 1 会话 ↔ 1 主机 | 1 导演 ↔ N 代理 ↔ M 主机 |
| 代理数量 | 一次一个活跃 | 舰队;Steward 自行扩容 |
| 主机跨度 | 单台本地机器,需保持唤醒 | VPS + GPU + 笔记本,通过 A2A 协同 |
| 代理厂商 | Claude Code(+ Codex) | 厂商无关 — 任何接受 pty 的 CLI |
| 创作模式 | 你打字发消息 | 你写目标,Steward 拆解 |
| 治理 | 无 | 策略、预算、审计日志、团队角色 |
| 数据归属 | 云中继或仅笔记本本地 | Hub 持有名字与事件;主机持有字节 |
| 离线 | 需要在线中继 | SQLite 快照缓存 — 每个列表都有上次的数据 |
| 开源 | Happy: 是;其余: 否 | Apache 2.0,自托管 Go hub |

### 适用人群

| | |
|---|---|
| **独立 ML 研究者** | 在 VPS + 家用 GPU 主机上跑夜间 sweep — 从手机启动,早上审阅 briefing |
| **独立 AI 开发者** | 跨项目运行多个代理 CLI — 统一收件箱、注意力队列、按窗格的配置文件 |
| **专注自动化的 1–5 人创业团队** | 预算上限、策略覆盖、审计日志、团队角色 — 其他移动代理工具都没有这些 |
| **开源项目维护者** | 夜间跑 triage / 评审代理;在床上批准代理生成的 PR |
| **Homelab / 自托管玩家** | 把工作从笔记本卸到手机,无需把原生 SSH 暴露到公网 |

### 什么时候**不**适合用 TermiPod

实话实说:如果你只在一台机器上跑一个 Claude Code 会话,而且笔记本一直开着,请用 [Anthropic 官方 Remote Control](https://code.claude.com/docs/en/remote-control) 或 [Happy](https://happy.engineering/)。TermiPod 的治理、多主机、自托管能力都有部署成本。除非你需要这些买来的能力,否则不必为此付费。

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

**研究 Demo 工作流:** 在手机上写下项目 directive → Steward 代理将其分解为 plan → 工人代理通过跨主机 A2A 在 GPU 主机上并行执行 runs → Briefing 代理在夜间汇总为可评审的文档。每一步都会浮到手机上;用户是审批/评审者而非操作者。MVP demo 目标是已锁定的 **ablation-sweep** 模板(nanoGPT-Shakespeare optimizer × size;见 [docs/decisions/001-locked-candidate-a.md](docs/decisions/001-locked-candidate-a.md));论文复现、benchmark 对比等模板将在 demo 落地后补齐。

- **Me**（中心,默认）— 个人 triage:待审批项、紧急任务、最近活动摘要,以及 Vault 入口。在卡片上直接 Approve / Reject。
- **Projects** — 项目清单。点开任意项目进入详情页:Overview / Tasks / Plans / Runs / Reviews / **Outputs**(run 产出的 checkpoint / 曲线 / 报告) / Documents / Blobs / Channels / Schedules,以及按 `agent_spawns` 渲染父→子关系的 Agents 视图。FAB 打开 YAML Spawn 表单(模板 / 主机选择 + 保存预设),Steward 拥有独立的 spawn 流程。**trackio / wandb / TensorBoard** 指标摘要自动以 sparkline 出现在 Run 详情。项目名称 / 目标 / 模板 / docs root / 预算均可原地编辑。
- **Activity** — 团队级审计 feed(audit_events 提升至顶层 Tab):策略变更、模板编辑、代理生命周期、频道发帖、run 状态切换,统一的可筛选时间线。
- **Hosts** — Host-runner 签到与最近在线时间。NAT 背后的主机会把代理卡片发布到 hub 目录,并通过**反向隧道中继**接收 peer A2A 调用,因此 VPS 上的 Steward 代理可以端到端地调用 GPU 机器上的工人。
- **Team** 屏(Projects 头部图标进入)— Members / Policies / 团队级频道(包含 `#hub-meta` Steward 房间,可从 AppBar chip 直达),Team Settings 内含 cron **Schedules**、按代理汇总的 **Usage / 预算**、**审计日志**,以及 **Templates** 浏览器(团队共享的 agent / prompt / policy YAML)。决定行为的东西 — 项目模板、代理 skills、launcher 命令 — 都是磁盘上可编辑的数据;新增代理 kind 无需改代码。

Hub 本体是 `hub/` 下的独立 Go 守护进程,可通过 `go install` 或直接运行源码部署。Hub 安装见 [docs/how-to/install-hub-server.md](docs/how-to/install-hub-server.md);新增 worker 主机见 [docs/how-to/install-host-runner.md](docs/how-to/install-host-runner.md);免 GPU 的 dress-rehearsal 见 [docs/how-to/run-the-demo.md](docs/how-to/run-the-demo.md)。

### 其他
- **数据导出/导入** — 将连接、密钥、代码片段、历史记录和设置导出为 JSON 备份文件，支持跨设备恢复和从旧版 MuxPod 迁移
- **内置文件浏览器** — 在设置中管理 SFTP 下载与应用存储，可直接分享或删除
- **更新检查** — 设置 → 检查更新，向 GitHub Releases 查询最新版并直达 APK 下载
- **帮助与新手引导** — 操作栏与 tmux 快捷键速查表，4 卡片教程
- **深度链接** — `termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>` 可从外部应用直接跳转到指定的服务器 / 会话 / 窗口 / 窗格。每个连接可设置稳定的 **Deep Link ID**（编辑界面中），改名后 URL 仍然可用；与 [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify) 配合，点 Telegram 通知即可直达对应窗格。旧版 `muxpod://` URL 同样可解析
- **平板与折叠屏** 自适应布局
- **多语言** — 英文与中文，跟随系统语言

---

## TermiPod 横向对比

对比移动端**代理控制**类工具(会话桥接类):

| 功能 | TermiPod | Claude Code Remote Control | Happy Coder | Tactic Remote |
|---|---|---|---|---|
| **多代理舰队** | 是 | 单会话 | 每个 CLI 包装一个 | 单会话 |
| **多主机 (A2A)** | 是 — VPS + GPU + NAT 经中继 | 否 | 否 | 否 |
| **代理厂商无关** | 是 — 任何 CLI | 仅 Claude Code | Claude Code + Codex | Claude Code + Codex |
| **导演 / Steward 模式** | 是 — Steward 拆解目标 | 否 | 否 | 否 |
| **治理(预算、审计、策略)** | 是 | 否 | 否 | 否 |
| **自托管 hub** | 是(Go 守护进程) | 云中继(Anthropic API) | 中继服务器 | Cloudflare Tunnel |
| **离线快照缓存** | 是(SQLite) | 否 | 否 | 否 |
| **平台** | Android + iOS + iPad | iOS + Android + Web | iOS + Android + Web | 仅 iOS |
| **价格** | 免费,Apache 2.0 | Claude Max($100+) | 免费,开源 | 付费试用 |

对比**消息桥接型代理**(论点不同,但用户问题有重叠):

| 功能 | TermiPod | OpenClaw | Hermes Agent | Claude Code Channels |
|---|---|---|---|---|
| **UI 层** | 专建的移动端 App | 已有的消息 App(WhatsApp、Telegram、Slack、Signal、iMessage、Discord 等 15+) | Telegram / Discord / Slack / WhatsApp / Signal | Telegram + Discord |
| **主要用途** | 舰队驾驶舱 + 科研运维 | 跨消息 App 的个人助手 | 个人助手 + 自我进化技能 | 通过 chat 远程使用 Claude Code |
| **代理拓扑** | 导演 → Steward → 舰队 | 单代理、跨平台记忆 | 单一自我进化代理 | 单一本地 Claude Code 会话 |
| **多主机 / A2A** | 是 | 否 | 否 | 否 |
| **计划审批** | 结构化 plan + Approve | 对话式 | 对话式 | 对话式 |
| **治理 UI** | 预算、策略、审计 | 无(仅聊天) | 无(仅聊天) | 无 |
| **富 UI(图表、看板、runs)** | 原生 | 仅文本 | 仅文本 | 仅文本 |
| **离线快照** | 是 | 否 | 否 | 否 |
| **开源** | Apache 2.0 | MIT | 开源 | 插件仓库 |
| **厂商绑定** | 无 | 无 | 无 | 需要 Claude Pro/Max |

**如何选择:** 如果你想要一个在每天用 20 小时的聊天 App 里答复你的个人助手,选消息桥接类。如果你在指挥**一支舰队**、跨自己的基础设施工作、需要为 plans / runs / reviews / 治理量身定制的屏幕,选 TermiPod。两者可以在同一台手机上和平共存。

对比移动端 **SSH 客户端**(TermiPod 也覆盖这一层 — 兜底用,但不是产品定位):

| 功能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **平台** | Android + iOS + iPad | Android | Android | 多平台 | Android |
| **tmux 集成** | 原生可视化 | 手动命令行 | 无 | 无 | 无 |
| **AI 代理配置** | Claude Code + Codex,每个窗格独立 | 无 | 无 | 无 | 无 |
| **SSH 跳板机** | 内置 | 命令行 | 命令行 | 内置 | 无 |
| **SOCKS5 代理** | 内置 | 命令行 | 无 | 无 | 无 |
| **文件传输** | SFTP(带 UI) | 本地文件系统 | 无 | SFTP | 无 |
| **自定义键盘** | Flutter 原生 | 无 | 无 | 无 | 无 |
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

1. **添加主机** — 在 **Hosts** 标签页点击 +，输入主机/端口/用户名
2. **认证** — 选择密码或 SSH 密钥（可从主机详情页的 Keys 入口生成）
3. **可选** — 在连接表单中配置跳板机或 SOCKS5 代理
4. **导航** — 展开主机 > 会话 > 窗口 > 窗格
5. **操作** — 操作栏发送快捷键，编辑栏输入命令，闪电按钮访问代码片段，[+] 访问文件传输

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

MVP 目标是 [docs/spine/blueprint.md](docs/spine/blueprint.md) §9
Phase 4 的**研究 Demo**：用户写下 directive → Steward 分解 → 代理队伍
跨主机执行 runs → Briefing 代理在夜间汇总 → 用户在手机上评审。

**阶段状态(v1.0.318):**

| 阶段 | 状态 |
|---|---|
| P0 — Hub 基本原语(schema) | ✅ 已发布 |
| P1 — 结构化通信协议 | ✅ 已发布 |
| P2 — 应用 UI | ✅ 已发布 |
| P3 — 集成(trackio、A2A relay) | ✅ 已发布 |
| P4 — 研究 Demo | 🟡 后端功能完整;**硬件运行待完成** |

Demo 路径已通过免 GPU 的 dress-rehearsal 工具(`seed-demo` +
`mock-trainer`) 端到端跑通。Candidate A(nanoGPT-Shakespeare 优化器
× 模型大小 sweep)的硬件运行是 MVP 里程碑 — 触发条件是连续两次
device walkthrough 无 principal-blocking bug。

**尚未完成(post-demo):**

- **Briefing 代理的夜间调度** — 由 Steward 自动安排 briefing(目前
  仍由用户触发)。
- **iOS TestFlight / App Store 分发** — Android APK 已发布;iOS 目前仅
  支持本地构建。
- **A2A peer 认证** — 为反向隧道中继添加按代理的 token。
- **Domain packs / 市场** — 内容打包扩展性(post-MVP;见
  [docs/discussions/post-mvp-domain-packs.md](docs/discussions/post-mvp-domain-packs.md))。

实时跟踪:
- [docs/roadmap.md](docs/roadmap.md) — Now / Next / Later
- [docs/plans/research-demo-gaps.md](docs/plans/research-demo-gaps.md) — Demo 详细差距
- [docs/changelog.md](docs/changelog.md) — 每次发布的具体变更
- [docs/decisions/](docs/decisions/) — ADR(编号、追加式)

---

## 致谢

TermiPod 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox，[Apache License 2.0](LICENSE)）开发。TermiPod 是独立项目，与原作者无关联。详见 [NOTICE](NOTICE)。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>使用 Flutter 构建。为移动端设计。为终端而生的开发者工具。</sub>
</p>
