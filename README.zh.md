<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod 标志——光标线上的 4 点星标与 chevron 提示符" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>面向 AI Agent 舰队的控制面——手机与桌面双端。</b><br>
  <sub>在口袋中或书桌前指挥你的 Agent：Steward 把目标拆解为计划，舰队在你自有的硬件上执行，你只需审定与复核。<br>Android、iOS、iPadOS、macOS、Windows、Linux——开源、自托管，遵循 Apache 2.0。</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/termipod/releases"><img src="https://img.shields.io/github/v/release/physercoe/termipod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/termipod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Android">
  <img src="https://img.shields.io/badge/iOS-000000?style=flat-square&logo=apple&logoColor=white" alt="iOS">
  <img src="https://img.shields.io/badge/iPadOS-000000?style=flat-square&logo=apple&logoColor=white" alt="iPadOS">
  <img src="https://img.shields.io/badge/macOS-000000?style=flat-square&logo=apple&logoColor=white" alt="macOS">
  <img src="https://img.shields.io/badge/Windows-0078D6?style=flat-square&logo=windows&logoColor=white" alt="Windows">
  <img src="https://img.shields.io/badge/Linux-FCC624?style=flat-square&logo=linux&logoColor=black" alt="Linux">
</p>

<p align="center">
  <a href="README.md">English</a>
</p>

---

## 与众不同之处

各类 Agent 前端正在追平会话数量——如今不少都能同时管理多个会话。但 TermiPod 不是又一个会话多路复用器，而是一套面向「导演」的控制面：你下达目标，Steward 将其拆解为计划与任务，一支受治理的舰队在你自有的机器上执行，最后再闭环回到你手中——无论你在手机上还是电脑前。

- **多 Agent 舰队**——统一调度 Claude Code、Codex、Gemini CLI、Kimi Code，乃至任何接受 pty 的 CLI；每个 Agent 都有独立的窗格、配置文件与预算，并汇入同一个收件箱。
- **跑在你自有基建上的多主机**——一台 5 美元/月的 VPS 上的 Steward，可经由自托管反向隧道中继上的 A2A，把工作下发给家中 NAT 之后的 GPU 主机。没有任何云端中间方持有你的会话。
- **导演，而非操作员**——写下一句自然语言目标，Steward 便把它拆解成一份计划和一组「一等公民」式的**任务**，派发给 Worker，交由你审定。你无需编排 DAG，也无需盯着终端。
- **受治理的动作**——有后果的操作只会被「提议」，而不会被悄悄执行：Steward 会把它呈交给你审定。其背后是预算上限、策略覆盖、分 Agent 的用量统计、不可篡改的审计日志与团队角色。
- **闭环编排**——下发出去的工作在完成或受阻时会自动回流给你，让你被动收到通知，而非主动轮询。就连舰队本身——host-runner、Agent、令牌——也能直接从客户端启动、停止与更新。
- **手机与桌面双端**——同一套控制面，两个客户端：Flutter 移动应用（Android / iOS / iPadOS），以及基于共享 React + TypeScript 前端、以 Electron 为壳的桌面工作台（macOS / Windows / Linux）。两端指向同一个 Hub。
- **离线优先**——当 Hub 不可达时，每个界面都会用 SQLite 快照缓存呈现最近一次的可用数据。地铁里也照看不误。
- **小内存 VPS 也扛得住**——Go Hub 无需任何额外基础设施（MVP 阶段不需要 Redis，也不需要托管数据库）即可在廉价硬件上驱动一支大舰队：按团队分片的 SQLite、读写连接池分离、以及延迟摘要折叠，使其在 **2 GB 内存 / 2 vCPU 的机器上承载约 1,000 个并发 Agent 而不触发写入错误悬崖**（已压测验证）。当单机不够用时，**可切换的 PostgreSQL 后端** 是为高可用与高写入并发设计的横向扩展路径。
- **不绑定厂商、可自托管**——一个由你自己运行的 Apache 2.0 Go Hub。没有账号门槛，没有云端中继，没有厂商锁定。

**30 秒 Demo。** 在手机上输入「给 nanoGPT 跑一组 ablation sweep，告诉我哪个 optimizer 的扩展性更好」。你 VPS 上的 Steward 会把它拆解成一份计划和几个**任务**，随后「提议」在家中的 GPU 主机上启动六个训练 Run——你点一下 **Approve**。这些 Run 经由 A2A 跨主机执行，期间你大可关掉 App。等工作完成（或受阻），闭环就会回到你这里：一份带 loss 曲线的简报落入你的 **Me** 标签页，你在回家的地铁上完成审定。

完整的论点陈述、目标用户画像与竞争分析见 [docs/discussions/positioning.md](docs/discussions/positioning.md)；完整的文档索引见 [docs/README.md](docs/README.md)。

---

## 演示

<p align="center">
  <a href="docs/screens/termipod-demo.mp4">
    <img src="docs/screens/demo.jpg" width="300" alt="TermiPod Me 标签页（折叠屏）——General Steward、活跃会话、待办队列。点击播放演示视频">
  </a>
</p>

> **⚠️ 初步预览，仅为片段。** 该演示录制于真实折叠屏设备之上，时间在
> Candidate-A 硬件 demo 的最终 UX 打磨之前；它只覆盖了应用的一部分，
> 并非完整的功能巡览——先行放出，供希望在正式 demo 前一睹为快的读者。
> **点击上方截图即可播放演示视频**
> （[docs/screens/termipod-demo.mp4](docs/screens/termipod-demo.mp4)）。
> 一份完整、由 CI 自动录制的演练正在规划中，设计见
> [docs/discussions/screenshot-automation.md](docs/discussions/screenshot-automation.md)。

---

## 为什么选择 TermiPod？

AI Agent 的产出速度，是任何人都审阅不过来的——足足十倍有余。一旦你同时跑起**多个** Agent，或横跨**多台**机器，现有的工具便开始捉襟见肘。会话桥接类应用（Claude Code Remote Control、Happy、Tactic Remote、Codex App）能把一个——如今往往是好几个——会话装进口袋，但每一个都仍只是你*亲手*启动、亲自照看的 Agent 的一扇窗；消息桥接类 Agent（OpenClaw、Hermes、Claude Code Channels）则只能在你的聊天 App 里塞进一个对话助手。它们都不会替你拆解目标、在你自有的机器上调度一支舰队，更不会治理这些 Agent 能做什么。TermiPod 立足于另一条公理：人是**导演**，而非操作员。

| 维度 | Remote Control / Happy / Tactic / Codex | TermiPod |
|---|---|---|
| 拓扑 | 你 ↔ 你亲手启动的会话 | 导演 → Steward → 横跨 M 台主机的舰队 |
| Agent 数量 | 可以有多个会话，但每个都由你启动、由你驱动 | 一支由 Steward 派生并协调的舰队 |
| 主机跨度 | 单台、需常亮的机器（部分通过厂商云同步） | VPS + GPU + 笔记本，经 A2A 协同 |
| Agent 引擎 | Claude Code / Codex | claude-code、codex、gemini-cli、kimi-code——以及任何 pty CLI |
| 协作模式 | 你逐条打字发消息 | 你写下目标，Steward 拆解为计划 + 任务；你审定 |
| 治理 | 几近于无 | 受治理动作的提议/审定，加上策略、预算、审计与团队角色 |
| 数据归属 | 厂商云，或仅存于笔记本 | Hub 持有名称与事件，主机持有字节——两者皆由你运行 |
| 离线 | 需要实时连接 | SQLite 快照缓存——每个列表都留有最近的可用数据 |
| 客户端 | 手机或 Web，只能二选一 | 移动 App **与**桌面工作台，共用同一个 Hub |
| 开源 | 参差（Happy：是；其余：否） | Apache 2.0，自托管 Go Hub |

### 适用人群

| | |
|---|---|
| **独立 ML 研究者** | 在 VPS 与家用 GPU 主机上跑夜间 sweep——从手机发起，清晨审阅简报 |
| **独立 AI 开发者** | 跨项目运行多个 Agent CLI——统一收件箱、注意力队列、分窗格的配置文件 |
| **专注自动化的小团队（1–5 人）** | 预算上限、策略覆盖、审计日志、团队角色——这些是其他移动端 Agent 工具所没有的 |
| **开源项目维护者** | 让 triage / 评审 Agent 通宵值守；躺在床上就能批准 Agent 提交的 PR |
| **Homelab / 自托管玩家** | 把活儿从笔记本挪到手机，而无需把裸 SSH 暴露到公网 |

### 什么时候**不**适合用 TermiPod

说句实在话：如果你只是在一台机器上跑一个 Claude Code 会话，而且笔记本始终开着，那就用 [Anthropic 官方 Remote Control](https://code.claude.com/docs/en/remote-control) 或 [Happy](https://happy.engineering/) 吧。TermiPod 的治理、多主机与自托管能力都有相应的部署成本——除非你确实需要它们带来的好处，否则不必为此买单。

---

## 功能特性

### 桌面工作台（macOS · Windows · Linux）

同一套控制面的全尺寸形态。桌面客户端是一套 **React + TypeScript** 前端，由 **Electron** 外壳承载（`desktop/`，ADR-055）——它是最初 Tauri 外壳的继任者，两者包裹的是同一份前端。既可连接你的 Hub，也可独立使用内置 SSH 终端。

- **Mission-control 外壳**——三区布局：舰队 Navigator（主机 ▸ Agent 树，带实时状态点）、常驻状态栏、⌘K 命令面板
- **Transcript 工作台**——基于 SSE 的实时 Agent transcript（tail 回填 + seq 游标），带输入框、摘要页与 Run 洞察
- **审批停靠栏**——受治理动作以常驻的注意力卡片呈现（权限请求、propose+override、求助请求），就地批准或驳回
- **Projects**——含阶段轨道与交付物的 Overview、可就地修改状态/优先级的 Tasks 看板、Runs 与 Plans
- **管理与治理**——团队成员、可编辑的策略 YAML、主机/Agent/团队管理、数据库维护，破坏性操作均需二次确认
- **应急 SSH 终端**——直连 SSH + SFTP（密码或密钥、应用内生成 ed25519、TOFU 主机密钥固定）。密钥只留在本机——绝不发送给 Hub
- **本地 PTY 与 Agent CLI**——在桌面本机拉起本地终端、运行各类 Agent CLI
- **Vault 与同步**——WASM 版 vault 加密；WebDAV、S3 与 Zotero 布局的目录同步后端
- **主题与语言随你**——明 / 暗 / 跟随系统主题，全界面 English / 中文双语
- **自更新安装包**——macOS `.dmg`、Windows `.msi`/`.exe`、Linux `.AppImage`/`.deb`，内置更新器；首次启动可从 Tauri 安装迁移状态与密钥

### 移动应用（Android · iOS · iPadOS）

#### SSH 连接

- **Ed25519 / RSA 密钥**——设备本地生成（RSA 支持 2048 / 3072 / 4096 位）或导入，加密存储于 Android Keystore / iOS Keychain，可选密码保护，公钥一键复制
- **SSH 跳板机（ProxyJump）**——通过堡垒机连接内网机器
- **SOCKS5 代理**——通过企业代理、VPN 或 Shadowsocks / Clash 转发 SSH
- **原始 PTY 模式**——为没有 tmux 的服务器提供直接的 Shell 访问，并可从任意 tmux 连接卡片一键打开
- **连接测试**——保存前先验证 SSH 与 tmux 是否可用
- **指数退避自动重连**——最多重试 5 次；断线期间输入的命令会进入队列，连接恢复后自动补发
- **延迟指示器**——标题栏实时显示 ping 值（绿色 &lt; 100 ms，红色 &gt; 500 ms），一眼看出卡顿来自手指还是网络
- **自适应轮询**——刷新频率随状态在 50 ms（活跃）到 500 ms（空闲）之间浮动，以节省电量
- **后台连接服务**——Android 前台服务可在应用退至后台时保持 SSH 存活，长会话还可选保持屏幕常亮

#### tmux 会话管理

- **仪表盘**——最近会话按访问时间排序，并以相对时间标注（「刚刚」「5 分钟前」）；轻点一下即可重连，并恢复上次的窗口与窗格
- **可视化导航**——面包屑标题栏：依次轻点「会话 > 窗口 > 窗格」即可切换
- **窗格布局预览**——精确呈现分屏比例，轻点任意窗格即可聚焦
- **双指滑动**——在相邻窗格之间切换
- **捏合缩放**——终端字号可在 50%–500% 间缩放，便于随手放大看清
- **复制 / 滚动模式**——开启后选取文本时画面不再跳动；缓冲会持续更新，退出时选区自动落入系统剪贴板
- **创建 / 重命名 / 关闭**会话与窗口
- **Bell / Activity / Silence 警报**——跨所有连接监控 tmux 窗口标志，轻点任一警报即可直达对应窗口与窗格（警报随即自动清除）
- **256 色 ANSI** 终端渲染，并自动扩展回滚缓冲

#### 输入体验（移动端优化）

| 组件 | 功能 |
|------|------|
| **操作栏** | 按配置文件提供可滑动的按钮组——ESC、Tab、Ctrl+C、方向键，一触即达 |
| **编辑栏** | 带发送按钮的多行文本框。多行内容会作为**一次括号粘贴**整体送达，让 AI Agent 与 Shell 看到的是完整文本块，而非被拆成 N 条命令。长按发送可省略 Enter |
| **直接输入模式** | 实时按键流式发送，并带在线指示灯——每次点按都直达 pty，最适合 vim、less、htop 与各类 REPL |
| **自定义键盘** | Flutter 原生 QWERTY，含 Ctrl / Alt / Esc / 方向键。内置**实时按键条**（Home / End / PgUp / PgDn / Del + 脉冲指示灯），填补了原本闲置的编辑行空隙。启用导航盘 / 摇杆时，方向键行会自动隐藏。也可整体关闭以便 CJK / 语音输入 |
| **导航盘** | D-pad、摇杆或手势面板，用于方向键与操作按钮 |
| **代码片段** | 斜杠命令，枚举项以下拉菜单呈现、自由参数以文本框填写。**长按 Bolt 键**可把当前编辑栏内容存为草稿片段 |
| **修饰键** | Ctrl / Alt 作为切换按钮——轻点激活，双击锁定 |

**4 个内置配置文件**——Claude Code、Codex、通用终端、tmux——各自带有优化过的按钮组。你可为任意 CLI 创建自定义配置文件。每个窗格都会记住自己的配置文件，并依据 `pane_current_command` 自动识别。

#### 文件传输

- **SFTP 上传 / 下载**——带进度追踪与远程目录浏览器
- **图片传输**——支持格式转换、尺寸预设与路径注入

### Termipod Hub（可选）

面向团队的可选协同层，适用于在多台机器上运行多个 AI Agent 的场景。在手机（**设置 → Hub**）或桌面连接面板粘贴 Hub URL 与 bearer token 后，共享的信息架构——**Projects · Activity · Me · Hosts · Settings**——便会全部激活。

**研究 Demo 工作流：** 在手机上写下项目指令 → Steward Agent 将其分解为计划 → Worker 经由跨主机 A2A 在 GPU 主机上并行执行 Run → Briefing Agent 在夜间汇总成一份可评审的文档。每一步都会浮现到客户端；你是审定 / 评审者，而非操作者。MVP demo 的目标是已锁定的 **ablation-sweep** 模板（nanoGPT-Shakespeare，optimizer × size；见 [docs/decisions/001-locked-candidate-a.md](docs/decisions/001-locked-candidate-a.md)）；论文复现与 benchmark 对比等模板将在 demo 落地后再补齐。

- **Me**（居中，默认）——你的个人 triage：待审批项、紧急任务、近期活动摘要，以及 Vault 快捷入口。轻点待审批项即可就地 Approve / Reject。
- **Projects**——项目清单；轻点任意一行进入项目详情页，内含 Overview、Tasks、Plans、Runs、Reviews、**Outputs**（Run 产出的 checkpoint / 曲线 / 报告）、Documents、Blobs、Channels、Schedules，以及一个按 `agent_spawns` 展开父→子组织结构的 Agents 区块。FAB 可通过 YAML 派生 Worker（含模板选择器、主机选择器与已保存的预设），Steward 则有专属的派生流程。来自 **trackio / wandb / TensorBoard** 的指标摘要会以内联 sparkline 自动出现在 Run 详情中。项目的名称 / 目标 / 模板 / docs 根目录 / 预算均可就地编辑。
- **Activity**——团队级审计 feed（由 audit_events 提升而来）：策略变更、模板编辑、Agent 生命周期、频道发帖、Run 状态流转——统统汇于一条可筛选的时间线。
- **Hosts**——host-runner 的签到与最近在线时间；NAT 之后的主机会把 agent-card 发布到 Hub 目录，并经由**反向隧道中继**接受对端 A2A 调用，从而让 VPS 上的 Steward Agent 能端到端地调用 GPU 机器上的 Worker。
- **Team** 页（由 Projects 顶栏图标进入）——Members、Policies、团队范围的频道（含 `#hub-meta` Steward 房间，可从 AppBar chip 直达），以及 Team Settings：cron **Schedules**、分 Agent 的 **Usage / 预算**汇总、一份**审计日志**，还有面向团队共享的 agent / prompt / policy YAML 的 **Templates** 浏览器。一切驱动行为的东西——项目模板、Agent 技能、launcher 命令——都是磁盘上可编辑的数据；新增一种 Agent kind 无需改动代码。

**扩展性与容量。** Hub 的设计目标就是在**一台廉价 VPS** 上跑起一支真正的舰队，无需任何外部服务。存储与写入并发都**按团队分片**——`events.db` 与 `digest.db` 是各团队独立的 SQLite 文件（各自拥有自己的写入者），而全局的 `hub.db` 只持有名称与事件——每个 Agent 的摘要折叠则脱离写入热路径，在「有界陈旧」触发器下按团队跨核并行运行。读写连接池分离再加上调优过的 SQLite pragma（WAL、`synchronous=NORMAL`、mmap、有界写入缓存）消除了锁竞争悬崖：在 **2 GB 内存 / 2 vCPU** 机器上的饱和压测可稳定支撑 **200 个 Agent 下约 1,000 事件/秒（深度负载下约 760–850 事件/秒）**，并在 **多达约 1,000 个并发 Agent 的全程跑通中零 `SQLITE_BUSY` 错误**。诚实的天花板是**事件/秒，而非 Agent 数量**——而且真实 Agent 是突发式的，实际余量远高于这个饱和数字。当单机不够用——高可用、内存吃紧的离机主机、或持续的高写入并发——存储后端**可按 store 切换**（`sqlite | postgres`）：SQLite 是零依赖默认项，外部托管 PostgreSQL 则是可选的退路（已在 [ADR-045](docs/decisions/045-hub-storage-scaling.md) 中决策；SQLite 分片已落地，PostgreSQL 后端在路线图上）。完整分析见 [docs/discussions/hub-scaling-storage-and-concurrency.md](docs/discussions/hub-scaling-storage-and-concurrency.md)。

Hub 本体作为一个独立的 Go 守护进程随 `hub/` 目录提供，可用 `go install` 安装或直接从源码运行。Hub 的安装见 [docs/how-to/install-hub-server.md](docs/how-to/install-hub-server.md)，新增 Worker 主机见 [docs/how-to/install-host-runner.md](docs/how-to/install-host-runner.md)，免 GPU 的彩排见 [docs/how-to/run-the-demo.md](docs/how-to/run-the-demo.md)。

### 其他

- **数据导出 / 导入**——把连接、密钥、代码片段、历史记录与设置完整备份为 JSON；可在新设备上恢复，或从旧版 MuxPod 应用迁移
- **内置文件浏览器**——在设置中管理 SFTP 下载与应用存储，并可就地分享或删除文件
- **更新检查**——移动端在「设置 → 检查更新」中查询 GitHub Releases 上的最新版本；桌面端安装包内置自更新
- **帮助与新手引导**——操作栏与 tmux 快捷键速查表，外加 4 卡片式引导
- **深度链接**——`termipod://connect?server=<id>&session=<n>&window=<n>&pane=<i>` 可从外部应用直接打开指定的服务器 / 会话 / 窗口 / 窗格。每个连接都有一个稳定的 **Deep Link ID**（在编辑页设置），URL 不会因改名而失效；搭配 [claude-telegram-notify](https://github.com/launch52-ai/claude-telegram-notify)，轻点一条 Telegram 通知便能直达对应窗格。旧版 `muxpod://` URL 仍可解析
- **平板与折叠屏**——自适应布局
- **多语言**——英文与中文，跟随系统语言

---

## TermiPod 横向对比

对比移动端**Agent 控制**类工具（会话桥接类）：

| 功能 | TermiPod | Claude Code Remote Control | Happy Coder | Tactic Remote |
|---|---|---|---|---|
| **多 Agent 舰队** | 是——Steward 派生并协调 | 只有会话，不是舰队 | 只有会话，不是舰队 | 只有会话，不是舰队 |
| **多主机（A2A）** | 是——VPS + GPU + NAT 经中继 | 否 | 否 | 否 |
| **Agent 引擎无关** | 是——任何 CLI | 仅 Claude Code | Claude Code + Codex | Claude Code + Codex |
| **导演 / Steward 模式** | 是——Steward 拆解目标 | 否 | 否 | 否 |
| **治理（预算、审计、策略）** | 是 | 否 | 否 | 否 |
| **自托管 Hub** | 是（Go 守护进程） | 云中继（Anthropic API） | 中继服务器 | Cloudflare Tunnel |
| **离线快照缓存** | 是（SQLite） | 否 | 否 | 否 |
| **平台** | Android + iOS + iPad + macOS + Windows + Linux | iOS + Android + Web | iOS + Android + Web | 仅 iOS |
| **价格** | 免费，Apache 2.0 | Claude Max（$100+） | 免费，开源 | 付费试用 |

对比**消息桥接型 Agent**（理念不同，但用户的疑问有所重叠）：

| 功能 | TermiPod | OpenClaw | Hermes Agent | Claude Code Channels |
|---|---|---|---|---|
| **UI 层** | 专建的移动端 + 桌面 App | 已有的消息 App（WhatsApp、Telegram、Slack、Signal、iMessage、Discord 等 15+） | Telegram / Discord / Slack / WhatsApp / Signal | Telegram + Discord |
| **主要用途** | 舰队驾驶舱 + 科研运维 | 跨消息 App 的个人助手 | 个人助手 + 自我进化技能 | 经由聊天远程使用 Claude Code |
| **Agent 拓扑** | 导演 → Steward → 舰队 | 单个 Agent，跨平台记忆 | 单个自我进化 Agent | 单个本地 Claude Code 会话 |
| **多主机 / A2A** | 是 | 否 | 否 | 否 |
| **计划审批** | 结构化计划 + Approve | 对话式 | 对话式 | 对话式 |
| **治理 UI** | 预算、策略、审计 | 无（仅聊天） | 无（仅聊天） | 无 |
| **富 UI（图表、看板、Run）** | 原生 | 仅文本 | 仅文本 | 仅文本 |
| **离线快照** | 是 | 否 | 否 | 否 |
| **开源** | Apache 2.0 | MIT | 开源 | 插件仓库 |
| **厂商绑定** | 无 | 无 | 无 | 需要 Claude Pro/Max |

**如何选择：** 如果你想要一个在你每天泡上 20 小时的聊天 App 里随时答复你的个人助手，就选消息桥接类；如果你要指挥**一支舰队**、横跨自有基础设施工作，并需要为计划、Run、评审与治理量身打造的界面，就选 TermiPod。两者完全可以在同一台手机上和睦共处。

对比移动端 **SSH 客户端**（TermiPod 也覆盖这一层——作为应急通道，而非产品定位）：

| 功能 | TermiPod | Termux | JuiceSSH | Termius | ConnectBot |
|------|----------|--------|----------|---------|------------|
| **平台** | Android + iOS + iPad + 桌面 | Android | Android | 多平台 | Android |
| **tmux 集成** | 原生可视化 | 手动命令行 | 无 | 无 | 无 |
| **AI Agent 配置** | Claude Code + Codex，分窗格独立 | 无 | 无 | 无 | 无 |
| **SSH 跳板机** | 内置 | 命令行 | 命令行 | 内置 | 无 |
| **SOCKS5 代理** | 内置 | 命令行 | 无 | 无 | 无 |
| **文件传输** | SFTP（带 UI） | 本地文件系统 | 无 | SFTP | 无 |
| **自定义键盘** | Flutter 原生 | 无 | 无 | 无 | 无 |
| **开源** | 是（Apache 2.0） | 是 | 否 | 否 | 是 |

---

## 快速开始

### 安装

**Android：** 从 [**Releases**](https://github.com/physercoe/termipod/releases) 下载最新 APK 并安装。

**iOS / iPadOS：** 请用 Xcode 从源码构建（见下文）。TestFlight 分发尚在规划中。

**桌面（macOS / Windows / Linux）：** 从 [**Releases**](https://github.com/physercoe/termipod/releases) 下载最新的桌面安装包（找 `desktop-v*` / `electron-v*` 资源：`.dmg`、`.msi`/`.exe`、`.AppImage`/`.deb`）。未签名构建：macOS 首次打开请右键 → 打开。

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

```bash
# 桌面工作台（需要 Node 22+）
cd desktop
npm ci && npm run build        # 共享的 React + TS 前端

cd electron && npm install
npm start                      # 以开发模式启动 Electron 外壳
# 安装包：npx electron-builder --mac | --win | --linux
#（打包、签名与更新源见 desktop/electron/README.md）
```

### 连接

1. **添加主机**——在 **Hosts** 标签页点击 +（移动端）或打开连接面板（桌面端），输入主机 / 端口 / 用户名
2. **认证**——选择密码或 SSH 密钥（可从主机详情页的 Keys 入口生成）
3. **可选**——在连接表单中配置跳板机或 SOCKS5 代理
4. **导航**——依次展开「主机 > 会话 > 窗口 > 窗格」
5. **操作**——操作栏发送快捷键，编辑栏输入命令，Bolt 按钮调出代码片段，[+] 进行文件传输

---

## 系统要求

| 组件 | 要求 |
|------|------|
| **移动设备** | Android 8.0+（API 26）、iOS 13.0+、iPadOS 13.0+ |
| **桌面** | macOS（Apple Silicon + Intel）、Windows 10+、或主流 Linux 发行版；仅从源码构建时需要 Node 22+ |
| **服务器** | 任意 SSH 服务器（OpenSSH、Dropbear 等） |
| **tmux** | 任意版本（已在 2.9+ 上测试）——在原始 PTY 模式下可选 |
| **网络** | 直连 SSH，或经跳板机 / SOCKS5 代理 |
| **Hub（可选）** | `hub/` 目录下的 Go 守护进程，可跑在任意小内存 VPS 上；客户端凭 URL + bearer token 接入 |

---

## 路线图

MVP 目标是 [docs/spine/blueprint.md](docs/spine/blueprint.md) §9
Phase 4 所述的**研究 Demo**：用户写下指令 → Steward 分解 → 一支 Agent
舰队跨主机执行 Run → Briefing Agent 在夜间汇总 → 用户在手机上评审。

**移动端 + Hub 线（`v1.0.x`），阶段状态（截至 v1.0.822）：**

| 阶段 | 状态 |
|---|---|
| P0——Hub 基本原语（schema） | ✅ 已发布 |
| P1——结构化通信协议 | ✅ 已发布 |
| P2——应用 UI | ✅ 已发布 |
| P3——集成（trackio、A2A relay） | ✅ 已发布 |
| P4——研究 Demo | 🟡 后端功能完整；**硬件运行待完成** |

Demo 路径已借助免 GPU 的彩排工具（`seed-demo` + `mock-trainer`）
端到端跑通。Candidate A（nanoGPT-Shakespeare，优化器 × 模型规模
sweep）的硬件运行是 MVP 里程碑——其触发条件是连续两次设备演练
均无阻断性缺陷。

**桌面线（`desktop-v*` / `electron-v*`，独立 changelog）：** 工作台功能集
（WS2–WS8：外壳、Navigator、transcript、审批、Projects、管理、SSH 终端、
打包）已在 Tauri 外壳上发布；其 **Electron 继任者**已完成 M1（脚手架 +
Hub 桥接）、M2（原生能力移植：PTY、SSH/SFTP 与密钥、目录同步、vault
WASM）以及 M3.1–3.3（electron-builder 打包、electron-updater、自 Tauri
安装的首次启动迁移）。最终切换（M3.4）需维护者签名证书与首个正式发布，
Tauri 线将在一个重叠发布期后退役。记录见
[docs/changelog-desktop.md](docs/changelog-desktop.md)，计划见
[docs/plans/desktop-electron-migration.md](docs/plans/desktop-electron-migration.md)。

**尚待完成（demo 之后）：**

- **Briefing Agent 的夜间调度**——由 Steward 自动安排 Briefing（目前
  仍由用户手动触发）。
- **iOS TestFlight / App Store 分发**——Android APK 已发布；iOS 目前
  仅支持本地构建。
- **A2A 对端认证**——为反向隧道中继加入分 Agent 的 token，使跨团队
  调用得以端到端认证。
- **Domain packs / 应用市场**——内容包式的可扩展性（demo 之后；见
  [docs/discussions/post-mvp-domain-packs.md](docs/discussions/post-mvp-domain-packs.md)）。

实时跟踪：

- [docs/roadmap.md](docs/roadmap.md)——Now / Next / Later
- [docs/plans/research-demo-gaps.md](docs/plans/research-demo-gaps.md)——Demo 维度的详细差距
- [docs/changelog.md](docs/changelog.md)——移动端 + Hub 每次发布的具体变更
- [docs/changelog-desktop.md](docs/changelog-desktop.md)——桌面线的发布记录
- [docs/decisions/](docs/decisions/)——ADR（编号、追加式）

---

## 致谢

TermiPod 是 [@moezakura](https://github.com/moezakura) 所作 [MuxPod](https://github.com/moezakura/mux-pod)（Copyright 2025 mox）的衍生作品，遵循 [Apache License 2.0](LICENSE)。最初的 MuxPod 为 Android 提供了 SSH 连接与基础的 tmux 会话查看能力。此后 TermiPod 已大幅演进：跨平台支持、重新设计的输入体验、Agent 配置文件、SFTP、ProxyJump、SOCKS5、自定义键盘、Hub 控制面以及桌面工作台等等。TermiPod 是一个独立项目，与原作者并无关联。完整的署名见 [NOTICE](NOTICE)。

## 反馈

发现 Bug 或有功能建议？欢迎 [提交 issue](https://github.com/physercoe/termipod/issues)，或在应用内的 **设置 > 关于** 中发送反馈。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>移动端用 Flutter，桌面端用 React + Electron，Hub 用 Go。献给生活在终端里的开发者。</sub>
</p>
