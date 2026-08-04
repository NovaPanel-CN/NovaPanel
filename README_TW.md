<h1 align="center">VelunexPanel</h1>

<p align="center">
<strong>全 Go 堆疊 · 零依賴部署 · 為 Minecraft 和控制台應用而生</strong>
</p>

> 輕量 · 高效率 · 開箱即用的伺服器管理面板（早期開發版）
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
 <strong>🕰️ 最後更新時間:2026年08月04日</strong>
 </td>
 </tr>
</table>


<table>
 <tr>
 <td align="center" style="background-color: #ffebee; color: #c62828; padding: 12px; border-radius: 6px;">
 <strong>⚠️ 注意：本專案目前為開發早期（alpha），仍在積極開發中，部分功能可能不完善，歡迎回饋！ </strong>
 </td>
 </tr>
</table>

<table>
 <tr>
 <td align="center" style="background-color: #ffebee; color: #c6bb28; padding: 12px; border-radius: 6px;">
 <strong>💡 Tips：2FA只支援手動輸入而不支援掃碼，望有緣人可以幫忙解決一下問題並pr到此專案</strong>
 </td>
 </tr>
</table>

---

## 📖 介紹

這是來自作者 **0721xun（現改名為missqwq）** 編寫的一個基於 **MCSManager** 的 **VelunexPanel 輕量級面板**，一款開箱即用的伺服器管理面板，專為 **Minecraft 伺服器**和**所有控制台程式**設計。

NovaPanel 致力於提供**輕量、高效、開箱即用**的管理體驗，無需複雜的配置，下載即用。

---

## ✨ 特性

- 🚀 **輕量高效** - 基於 Go 構建，資源佔用低
- 📦 **開箱即用** - 內建 Node.js 運行環境，無需手動安裝
- 🔌 **分散式架構** - 支援遠端節點管理，可橫向擴展
- 🎮 **Minecraft 支援** - 專為 Minecraft 伺服器最佳化
- 🖥️ **跨平台支援** - 支援 Windows server / linux ubuntu server
- 📶 **遠端節點跨平台支援** - 支援 NovaPanel / MCSManager daemon
- 🔥 **熱重載開發** - 修改程式碼自動刷新，開發體驗流暢
- 📊 **即時監控** - 系統資訊總覽，CPU/記憶體/磁碟即時監控
- 🔐 **安全認證** - 帳號密碼登錄，保障面板安全，支援2FA和防爆破，歡迎嚐鮮
- 🌏 **語言支援** - 支援簡體中文/繁體中文/english（目前）

---

## 🛠️ 技術堆疊

| 層級 | 技術 | 說明 |
|------|------|------|
| 前端 | Go | 現代化管理介面 |
| Web 後端 | Go | 高效能 Web 服務 |
| 遠端節點 | Go | 分散式節點管理 |
| 文件管理 | Go | 即時文件管理 |
| API 服務 | Node.js + Express | 資料介面 |
| 通訊協定 | WebSocket | 即時雙向通訊 |

---

## 📦 快速開始

### 環境需求

- Windows Server / linux ubuntu server
- **需要安裝 Go**（建議 1.21+）：https://golang.google.cn/dl/
- **Node.js 已內建**，無需額外安裝

> ⚠️ 確保 Go 安裝後已新增至系統 PATH（安裝時勾選"Add to PATH"）

### 下載與啟動

『`bash
# windows克隆專案（需先Fork本倉庫）
git clone https://github.com/你的使用者名稱/NovaPanel.git
cd NovaPanel

# 或直接開啟該連結下載即可
點我下載→ https://github.com/VelunexPanel/VelunexPanel/archive/refs/heads/main.zip


# windows正常使用請直接啟動（Node.js 已內建）
run.bat

# linux克隆專案（需先Fork本倉庫）
cd /opt
git clone https://github.com/你的使用者名稱/NovaPanel.git

# 或直接開啟該連結下載即可
點我下載→ https://github.com/VelunexPanel/VelunexPanel/archive/refs/heads/main.zip

# linux正常使用請直接啟動（會自動安裝並設定開機自啟動）
cd /opt/NovaPanel
run.sh