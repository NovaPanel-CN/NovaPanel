<h1 align="center">VelunexPanel</h1>

<p align="center">
<strong>全 Go 栈 · 零依赖部署 · 为 Minecraft 和控制台应用而生</strong>
</p>

> 轻量 · 高效 · 开箱即用的服务器管理面板（早期开发版）
<div align="center">
[![Preview](https://img.shields.io/badge/status-preview-orange.svg)]()
[![Go Version](https://img.shields.io/badge/Go-1.26.4-00ADD8?logo=go)](https://golang.org/)
[![Node Version](https://img.shields.io/badge/Node-22.14.0-339933?logo=node.js)](https://nodejs.org/)
</div>

<p align="center">
<img src="images/Panel-2026-08-04.png" alt="Panel" width="780">
</p>

---
<div align="center">

[English](README.md) - [简体中文](README_ZH.md) - [繁體中文](README_TW.md)

</div>
---

<table>
  <tr>
    <td align="center" style="background-color: #ffebee; color: #48c628; padding: 12px; border-radius: 6px;">
      <strong>🕰️ 最后更新时间:2026年08月04日</strong>
    </td>
  </tr>
</table>


<table>
  <tr>
    <td align="center" style="background-color: #ffebee; color: #c62828; padding: 12px; border-radius: 6px;">
      <strong>⚠️ 注意：本项目目前为开发早期（alpha），仍在积极开发中，部分功能可能不完善，欢迎反馈！</strong>
    </td>
  </tr>
</table>

<table>
  <tr>
    <td align="center" style="background-color: #ffebee; color: #c6bb28; padding: 12px; border-radius: 6px;">
      <strong>💡 Tips：2FA只支持手动输入而不支持扫码，望有缘人可以帮忙解决一下问题并pr到此项目</strong>
    </td>
  </tr>
</table>

---

## 📖 介绍

这是来自作者 **0721xun（现改名为missqwq）** 编写的一个基于 **MCSManager** 的 **VelunexPanel 轻量面板**，一款开箱即用的服务器管理面板，专为 **Minecraft 服务器**和**所有控制台程序**设计。

NovaPanel 致力于提供**轻量、高效、开箱即用**的管理体验，无需复杂的配置，下载即用。

---

## ✨ 特性

- 🚀 **轻量高效** - 基于 Go 构建，资源占用低
- 📦 **开箱即用** - 内置 Node.js 运行环境，无需手动安装
- 🔌 **分布式架构** - 支持远程节点管理，可横向扩展
- 🎮 **Minecraft 支持** - 专为 Minecraft 服务器优化
- 🖥️ **跨平台支持** - 支持 Windows server / linux ubuntu server
- 📶 **远程节点跨平台支持** - 支持 NovaPanel / MCSManager daemon
- 🔥 **热重载开发** - 修改代码自动刷新，开发体验流畅
- 📊 **实时监控** - 系统信息总览，CPU/内存/磁盘实时监控
- 🔐 **安全认证** - 账号密码登录，保障面板安全，支持2FA和防爆破，欢迎尝鲜
- 🌏 **语言支持** - 支持简体中文/繁体中文/english（目前）

---

## 🛠️ 技术栈

| 层级 | 技术 | 说明 |
|------|------|------|
| 前端 | Go | 现代化管理界面 |
| Web 后端 | Go | 高性能 Web 服务 |
| 远程节点 | Go | 分布式节点管理 |
| 文件管理 | Go | 实时文件管理 |
| API 服务 | Node.js + Express | 数据接口 |
| 通信协议 | WebSocket | 实时双向通信 |

---

## 📦 快速开始

### 环境要求

- Windows Server / linux ubuntu server
- **需要安装 Go**（推荐 1.21+）：https://golang.google.cn/dl/
- **Node.js 已内置**，无需额外安装

> ⚠️ 确保 Go 安装后已添加到系统 PATH（安装时勾选"Add to PATH"）

### 下载与启动

```bash
# windows克隆项目（需要先Fork本仓库）
git clone https://github.com/你的用户名/NovaPanel.git
cd NovaPanel

# 或者直接打开该链接下载即可
点我下载→ https://github.com/VelunexPanel/VelunexPanel/archive/refs/heads/main.zip


# windows正常使用请直接启动（Node.js 已内置）
run.bat

# linux克隆项目（需要先Fork本仓库）
cd /opt
git clone https://github.com/你的用户名/NovaPanel.git

# 或者直接打开该链接下载即可
点我下载→ https://github.com/VelunexPanel/VelunexPanel/archive/refs/heads/main.zip

# linux正常使用请直接启动（会自动安装和设置开机自启）
cd /opt/NovaPanel
run.sh