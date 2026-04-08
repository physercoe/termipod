<p align="center">
  <img src="docs/logo/logo.svg" alt="TermiPod" width="140" height="140">
</p>

<h1 align="center">TermiPod</h1>

<p align="center">
  <b>tmux 会话，装进口袋。</b><br>
  <sub>Android 移动优先 tmux 客户端 — SSH 连接、管理会话，随时随地保持高效。</sub>
</p>

<p align="center">
  <a href="https://github.com/physercoe/mux-pod/releases"><img src="https://img.shields.io/github/v/release/physercoe/mux-pod?style=flat-square&color=00c0d1" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/physercoe/mux-pod?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/platform-Android-3DDC84?style=flat-square&logo=android&logoColor=white" alt="Platform">
  <img src="https://img.shields.io/badge/Flutter-3.24+-02569B?style=flat-square&logo=flutter&logoColor=white" alt="Flutter">
</p>

<p align="center">
  <a href="README.md">🇺🇸 English</a> &nbsp;|&nbsp;
  <a href="README.ja.md">🇯🇵 日本語</a>
</p>

---

> **TermiPod** 是 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod) 的分支，新增了 i18n 支持（英语/中文）、输入 UX 重新设计、代码片段、CLI 代理集成等功能。

---

## 为什么选择 TermiPod？

需要检查长时间运行的进程、重启服务或查看日志，但不在电脑旁？

**TermiPod 将你的 Android 手机变成 tmux 遥控器。**

- **零服务器配置** — 只要有 `sshd` 即可。无需安装任何额外软件。
- **为移动端而设计** — 不是硬塞进手机的终端，而是为触控精心设计的 UI。
- **默认安全** — SSH 密钥存储在 Android Keystore 中，凭据不会离开设备。
- **多语言** — 开箱即用的英语和简体中文，跟随系统语言。

---

## TermiPod 的新功能

与上游 [MuxPod](https://github.com/moezakura/mux-pod) 相比：

- **i18n** — 完整的英语和简体中文本地化，自动检测系统语言
- **输入 UX 重新设计** — 改进的特殊键栏、代码片段集成、命令输入
- **代码片段** — 保存常用命令，快速粘贴
- **CLI 代理支持** — 为 Claude Code / Kimi Code 工作流优化（S-RET、DirectInput）
- **Bug 修复** — 调整大小时滚动、返回按钮处理、版本显示等

---

## 快速开始

### 安装

从 [**Releases**](https://github.com/physercoe/mux-pod/releases) 下载最新 APK。

### 从源码构建

```bash
git clone https://github.com/physercoe/mux-pod.git
cd mux-pod
flutter pub get
flutter build apk --release
```

### 连接

1. **添加服务器** — 在服务器标签页点击 +，输入主机/端口/用户名
2. **认证** — 选择密码或 SSH 密钥（可在密钥标签页生成）
3. **导航** — 展开服务器 > 选择会话 > 点击窗口 > 选择窗格
4. **操作** — 使用触控手势、特殊键栏或 DirectInput 模式

---

## 系统要求

| 组件 | 要求 |
|------|------|
| **设备** | Android 8.0+（API 26） |
| **服务器** | 任意 SSH 服务器 |
| **tmux** | 任意版本（2.9+ 已测试） |

---

## 致谢

TermiPod 基于 [@moezakura](https://github.com/moezakura) 的 [MuxPod](https://github.com/moezakura/mux-pod) 构建。感谢出色的基础工作。

## 许可证

[Apache License 2.0](LICENSE)

---

<p align="center">
  <sub>使用 Flutter 构建</sub>
</p>
