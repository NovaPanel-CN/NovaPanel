#!/bin/bash

# ============================================================
# Velunex Panel Linux 一键安装与管理脚本
# 功能：检测环境 → 安装依赖 → 编译 → systemd 开机自启 → 启动
# 支持：简体中文 / 繁體中文 / English（根据系统语言自动切换）
# ============================================================

set -e

# 强制 UTF-8 编码
export LANG="${LANG:-en_US.UTF-8}"
export LC_ALL="${LANG}"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# 项目根目录（脚本所在目录）
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 版本配置
GO_VERSION="1.21.13"
NODE_VERSION="v20.17.0"

# ========== 语言检测 ==========

detect_lang() {
    local lang=$(echo "${LANG:-en_US}" | tr '[:upper:]' '[:lower:]')
    case "$lang" in
        zh_cn*|zh_hans*) echo "zh_cn" ;;
        zh_tw*|zh_hk*|zh_hant*|zh_mo*) echo "zh_tw" ;;
        *) echo "en" ;;
    esac
}

# ========== 加载多语言消息 ==========

load_messages() {
    local lang=$(detect_lang)
    case "$lang" in
        zh_cn)
            MSG_TITLE="Velunex Panel Linux 安装脚本"
            MSG_INSTALLING_DEPS="安装系统依赖..."
            MSG_DEPS_READY="系统依赖已就绪"
            MSG_GO_INSTALLING="正在安装 Go"
            MSG_GO_INSTALLED="Go 安装完成"
            MSG_GO_FOUND="Go 已安装"
            MSG_GO_NOT_FOUND="Go 未安装，开始安装..."
            MSG_NODE_INSTALLING="正在安装 Node.js"
            MSG_NODE_INSTALLED="Node.js 安装完成"
            MSG_NODE_FOUND="Node.js 已安装"
            MSG_NODE_NOT_FOUND="Node.js 未安装，开始安装..."
            MSG_BUILDING="正在编译项目..."
            MSG_BUILD_WEB="编译 go-web..."
            MSG_BUILD_WEB_DONE="面板编译完成"
            MSG_BUILD_DAEMON="编译 go-daemon..."
            MSG_BUILD_DAEMON_DONE="Daemon 编译完成"
            MSG_BUILD_DONE="项目编译完成"
            MSG_CREATING_SERVICES="正在创建 systemd 开机自启服务..."
            MSG_SERVICES_CREATED="systemd 服务已创建并启用开机自启"
            MSG_STARTING_SERVICES="正在启动服务..."
            MSG_SERVICES_STARTED="服务已启动"
            MSG_INSTALL_DONE="Velunex Panel 安装完成！"
            MSG_PANEL_ADDR="面板地址"
            MSG_DAEMON_ADDR="Daemon"
            MSG_MGMT_CMDS="服务管理命令"
            MSG_START_PANEL="启动面板"
            MSG_START_DAEMON="启动 Daemon"
            MSG_RESTART_PANEL="重启面板"
            MSG_RESTART_DAEMON="重启 Daemon"
            MSG_STOP_PANEL="停止面板"
            MSG_STOP_DAEMON="停止 Daemon"
            MSG_VIEW_STATUS="查看状态"
            MSG_PANEL_STATUS="面板状态"
            MSG_DAEMON_STATUS="Daemon 状态"
            MSG_AUTOSTART_HINT="服务已设置开机自启，可以安全关闭此终端！"
            MSG_CHINA_NET="检测到中国大陆网络，使用镜像源下载"
            MSG_GOPROXY_SET="已设置 GOPROXY=https://goproxy.cn,direct"
            MSG_NPM_MIRROR_SET="已设置 npm 镜像源"
            MSG_UNSUPPORTED_ARCH="不支持的系统架构"
            MSG_UNSUPPORTED_PM="不支持的包管理器，请手动安装 wget curl tar"
            MSG_INSTALL_GO_PROXY="中国大陆设置 Go 代理"
            ;;
        zh_tw)
            MSG_TITLE="Velunex Panel Linux 安裝腳本"
            MSG_INSTALLING_DEPS="安裝系統相依套件..."
            MSG_DEPS_READY="系統相依套件已就緒"
            MSG_GO_INSTALLING="正在安裝 Go"
            MSG_GO_INSTALLED="Go 安裝完成"
            MSG_GO_FOUND="Go 已安裝"
            MSG_GO_NOT_FOUND="Go 未安裝，開始安裝..."
            MSG_NODE_INSTALLING="正在安裝 Node.js"
            MSG_NODE_INSTALLED="Node.js 安裝完成"
            MSG_NODE_FOUND="Node.js 已安裝"
            MSG_NODE_NOT_FOUND="Node.js 未安裝，開始安裝..."
            MSG_BUILDING="正在編譯專案..."
            MSG_BUILD_WEB="編譯 go-web..."
            MSG_BUILD_WEB_DONE="面板編譯完成"
            MSG_BUILD_DAEMON="編譯 go-daemon..."
            MSG_BUILD_DAEMON_DONE="Daemon 編譯完成"
            MSG_BUILD_DONE="專案編譯完成"
            MSG_CREATING_SERVICES="正在建立 systemd 開機自啟服務..."
            MSG_SERVICES_CREATED="systemd 服務已建立並啟用開機自啟"
            MSG_STARTING_SERVICES="正在啟動服務..."
            MSG_SERVICES_STARTED="服務已啟動"
            MSG_INSTALL_DONE="Velunex Panel 安裝完成！"
            MSG_PANEL_ADDR="面板位址"
            MSG_DAEMON_ADDR="Daemon"
            MSG_MGMT_CMDS="服務管理指令"
            MSG_START_PANEL="啟動面板"
            MSG_START_DAEMON="啟動 Daemon"
            MSG_RESTART_PANEL="重啟面板"
            MSG_RESTART_DAEMON="重啟 Daemon"
            MSG_STOP_PANEL="停止面板"
            MSG_STOP_DAEMON="停止 Daemon"
            MSG_VIEW_STATUS="檢視狀態"
            MSG_PANEL_STATUS="面板狀態"
            MSG_DAEMON_STATUS="Daemon 狀態"
            MSG_AUTOSTART_HINT="服務已設定開機自啟，可以安全關閉此終端機！"
            MSG_CHINA_NET="偵測到中國大陸網路，使用鏡像源下載"
            MSG_GOPROXY_SET="已設定 GOPROXY=https://goproxy.cn,direct"
            MSG_NPM_MIRROR_SET="已設定 npm 鏡像源"
            MSG_UNSUPPORTED_ARCH="不支援的系統架構"
            MSG_UNSUPPORTED_PM="不支援的套件管理器，請手動安裝 wget curl tar"
            MSG_INSTALL_GO_PROXY="中國大陸設定 Go 代理"
            ;;
        *)
            MSG_TITLE="Velunex Panel Linux Install Script"
            MSG_INSTALLING_DEPS="Installing system dependencies..."
            MSG_DEPS_READY="System dependencies ready"
            MSG_GO_INSTALLING="Installing Go"
            MSG_GO_INSTALLED="Go installed"
            MSG_GO_FOUND="Go already installed"
            MSG_GO_NOT_FOUND="Go not found, installing..."
            MSG_NODE_INSTALLING="Installing Node.js"
            MSG_NODE_INSTALLED="Node.js installed"
            MSG_NODE_FOUND="Node.js already installed"
            MSG_NODE_NOT_FOUND="Node.js not found, installing..."
            MSG_BUILDING="Building project..."
            MSG_BUILD_WEB="Building go-web..."
            MSG_BUILD_WEB_DONE="Panel build complete"
            MSG_BUILD_DAEMON="Building go-daemon..."
            MSG_BUILD_DAEMON_DONE="Daemon build complete"
            MSG_BUILD_DONE="Project build complete"
            MSG_CREATING_SERVICES="Creating systemd autostart services..."
            MSG_SERVICES_CREATED="systemd services created and enabled"
            MSG_STARTING_SERVICES="Starting services..."
            MSG_SERVICES_STARTED="Services started"
            MSG_INSTALL_DONE="Velunex Panel installation complete!"
            MSG_PANEL_ADDR="Panel URL"
            MSG_DAEMON_ADDR="Daemon"
            MSG_MGMT_CMDS="Service Management Commands"
            MSG_START_PANEL="Start Panel"
            MSG_START_DAEMON="Start Daemon"
            MSG_RESTART_PANEL="Restart Panel"
            MSG_RESTART_DAEMON="Restart Daemon"
            MSG_STOP_PANEL="Stop Panel"
            MSG_STOP_DAEMON="Stop Daemon"
            MSG_VIEW_STATUS="View Status"
            MSG_PANEL_STATUS="Panel status"
            MSG_DAEMON_STATUS="Daemon status"
            MSG_AUTOSTART_HINT="Services are set to autostart on boot. You can safely close this terminal!"
            MSG_CHINA_NET="China mainland network detected, using mirror source"
            MSG_GOPROXY_SET="GOPROXY set to https://goproxy.cn,direct"
            MSG_NPM_MIRROR_SET="npm mirror source set"
            MSG_UNSUPPORTED_ARCH="Unsupported system architecture"
            MSG_UNSUPPORTED_PM="Unsupported package manager, please install wget curl tar manually"
            MSG_INSTALL_GO_PROXY="Setting Go proxy for China mainland"
            ;;
    esac
}

# 加载语言
load_messages

# ========== 工具函数 ==========

command_exists() {
    command -v "$1" > /dev/null 2>&1
}

# 检测是否在中国大陆（通过尝试访问 Google，3 秒超时）
is_china() {
    if curl -s --connect-timeout 3 --max-time 5 https://www.google.com -o /dev/null 2>&1; then
        return 1  # 不在中国大陆
    else
        return 0  # 在中国大陆
    fi
}

# 获取系统架构
get_arch() {
    local arch=$(uname -m)
    case $arch in
        x86_64)       echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) echo "unsupported"; return 1 ;;
    esac
}

# 检测包管理器
get_package_manager() {
    if command_exists apt-get; then echo "apt"
    elif command_exists dnf; then   echo "dnf"
    elif command_exists yum; then   echo "yum"
    else echo "unknown"
    fi
}

# 安装系统依赖（wget curl tar xz）
install_deps() {
    local pm=$(get_package_manager)
    echo -e "${YELLOW}📦 ${MSG_INSTALLING_DEPS}${NC}"
    case $pm in
        apt)
            sudo apt-get update -qq
            sudo apt-get install -y -qq wget curl tar xz-utils > /dev/null 2>&1 || true
            ;;
        yum|dnf)
            sudo $pm install -y -q wget curl tar xz > /dev/null 2>&1 || true
            ;;
        *)
            echo -e "${RED}❌ ${MSG_UNSUPPORTED_PM}${NC}"
            exit 1
            ;;
    esac
    echo -e "${GREEN}✅ ${MSG_DEPS_READY}${NC}"
}

# ========== 安装 Go ==========

install_go() {
    echo -e "${YELLOW}📥 ${MSG_GO_INSTALLING} ${GO_VERSION}...${NC}"
    local arch=$(get_arch)
    [ "$arch" = "unsupported" ] && { echo -e "${RED}❌ ${MSG_UNSUPPORTED_ARCH}${NC}"; exit 1; }

    local url
    if is_china; then
        echo -e "${CYAN}🌐 ${MSG_CHINA_NET}${NC}"
        url="https://golang.google.cn/dl/go${GO_VERSION}.linux-${arch}.tar.gz"
    else
        url="https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz"
    fi

    wget -q --show-progress "$url" -O /tmp/go.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz

    export PATH="$PATH:/usr/local/go/bin"
    export GOPATH="$HOME/go"
    export PATH="$PATH:$GOPATH/bin"

    # 持久化环境变量到 ~/.bashrc
    if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
        echo 'export GOPATH=$HOME/go' >> ~/.bashrc
        echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
    fi

    # 中国大陆设置 Go 代理
    if is_china; then
        /usr/local/go/bin/go env -w GOPROXY=https://goproxy.cn,direct
        echo -e "${CYAN}🔧 ${MSG_GOPROXY_SET}${NC}"
    fi

    echo -e "${GREEN}✅ Go ${GO_VERSION} ${MSG_GO_INSTALLED}${NC}"
}

# ========== 安装 Node.js ==========

install_node() {
    echo -e "${YELLOW}📥 ${MSG_NODE_INSTALLING} ${NODE_VERSION}...${NC}"
    local arch=$(get_arch)
    local node_arch
    case $arch in
        amd64) node_arch="x64" ;;
        arm64) node_arch="arm64" ;;
        *) echo -e "${RED}❌ ${MSG_UNSUPPORTED_ARCH}${NC}"; exit 1 ;;
    esac

    local url
    if is_china; then
        echo -e "${CYAN}🌐 ${MSG_CHINA_NET}${NC}"
        url="https://npmmirror.com/mirrors/node/${NODE_VERSION}/node-${NODE_VERSION}-linux-${node_arch}.tar.xz"
    else
        url="https://nodejs.org/dist/${NODE_VERSION}/node-${NODE_VERSION}-linux-${node_arch}.tar.xz"
    fi

    wget -q --show-progress "$url" -O /tmp/node.tar.xz
    sudo rm -rf /usr/local/node
    sudo mkdir -p /usr/local/node
    sudo tar -xJf /tmp/node.tar.xz -C /usr/local/node --strip-components=1
    rm -f /tmp/node.tar.xz

    export PATH="$PATH:/usr/local/node/bin"

    if ! grep -q "/usr/local/node/bin" ~/.bashrc; then
        echo 'export PATH=$PATH:/usr/local/node/bin' >> ~/.bashrc
    fi

    # 中国大陆设置 npm 镜像
    if is_china; then
        /usr/local/node/bin/npm config set registry https://registry.npmmirror.com
        echo -e "${CYAN}🔧 ${MSG_NPM_MIRROR_SET}${NC}"
    fi

    echo -e "${GREEN}✅ Node.js ${NODE_VERSION} ${MSG_NODE_INSTALLED}${NC}"
}

# ========== 编译项目 ==========

build_project() {
    echo -e "${YELLOW}🔨 ${MSG_BUILDING}${NC}"

    export PATH="$PATH:/usr/local/go/bin"
    export GOPATH="$HOME/go"
    export PATH="$PATH:$GOPATH/bin"

    # 整理依赖
    cd "${SCRIPT_DIR}"
    go mod tidy 2>/dev/null || true

    # 编译面板
    echo -e "${CYAN}   ${MSG_BUILD_WEB}${NC}"
    cd "${SCRIPT_DIR}/go-web"
    go build -o nova-panel .
    echo -e "${GREEN}   ✅ ${MSG_BUILD_WEB_DONE}${NC}"

    # 编译 Daemon
    echo -e "${CYAN}   ${MSG_BUILD_DAEMON}${NC}"
    cd "${SCRIPT_DIR}/go-daemon"
    go build -o nova-daemon .
    echo -e "${GREEN}   ✅ ${MSG_BUILD_DAEMON_DONE}${NC}"

    cd "${SCRIPT_DIR}"
    echo -e "${GREEN}✅ ${MSG_BUILD_DONE}${NC}"
}

# ========== 创建 systemd 服务 ==========

create_services() {
    echo -e "${YELLOW}⚙️  ${MSG_CREATING_SERVICES}${NC}"

    # 面板服务
    sudo tee /etc/systemd/system/nova-panel.service > /dev/null <<'EOF'
[Unit]
Description=Velunex Panel Web Panel
After=network.target

[Service]
Type=simple
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo sed -i "s|^Type=simple|Type=simple\nWorkingDirectory=${SCRIPT_DIR}/go-web\nExecStart=${SCRIPT_DIR}/go-web/nova-panel\nEnvironment=PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin|" /etc/systemd/system/nova-panel.service

    # Daemon 服务
    sudo tee /etc/systemd/system/nova-daemon.service > /dev/null <<'EOF'
[Unit]
Description=Velunex Panel Daemon
After=network.target

[Service]
Type=simple
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    sudo sed -i "s|^Type=simple|Type=simple\nWorkingDirectory=${SCRIPT_DIR}/go-daemon\nExecStart=${SCRIPT_DIR}/go-daemon/nova-daemon\nEnvironment=PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin|" /etc/systemd/system/nova-daemon.service

    sudo systemctl daemon-reload
    sudo systemctl enable nova-panel nova-daemon
    echo -e "${GREEN}✅ ${MSG_SERVICES_CREATED}${NC}"
}

# ========== 启动服务 ==========

start_services() {
    echo -e "${YELLOW}🚀 ${MSG_STARTING_SERVICES}${NC}"
    sudo systemctl start nova-daemon
    sleep 1
    sudo systemctl start nova-panel
    sleep 2
    echo -e "${GREEN}✅ ${MSG_SERVICES_STARTED}${NC}"
}

# ========== 显示信息 ==========

show_info() {
    local ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -z "$ip" ] && ip="SERVER_IP"

    echo ""
    echo -e "${GREEN}${BOLD}========================================${NC}"
    echo -e "${GREEN}${BOLD}  ✅ ${MSG_INSTALL_DONE}${NC}"
    echo -e "${GREEN}${BOLD}========================================${NC}"
    echo ""
    echo -e "${CYAN}  📌 ${MSG_PANEL_ADDR}:${NC}  http://${ip}:8080"
    echo -e "${CYAN}  📌 ${MSG_DAEMON_ADDR}:${NC}    ws://${ip}:8079"
    echo ""
    echo -e "${YELLOW}${BOLD}━━━ 🛠 ${MSG_MGMT_CMDS} ━━━${NC}"
    echo -e "${GREEN}  ▶ ${MSG_START_PANEL}:${NC}      sudo systemctl start nova-panel"
    echo -e "${GREEN}  ▶ ${MSG_START_DAEMON}:${NC}    sudo systemctl start nova-daemon"
    echo -e "${GREEN}  🔄 ${MSG_RESTART_PANEL}:${NC}      sudo systemctl restart nova-panel"
    echo -e "${GREEN}  🔄 ${MSG_RESTART_DAEMON}:${NC}    sudo systemctl restart nova-daemon"
    echo -e "${GREEN}  ⏹ ${MSG_STOP_PANEL}:${NC}      sudo systemctl stop nova-panel"
    echo -e "${GREEN}  ⏹ ${MSG_STOP_DAEMON}:${NC}    sudo systemctl stop nova-daemon"
    echo ""
    echo -e "${YELLOW}${BOLD}━━━ 📊 ${MSG_VIEW_STATUS} ━━━${NC}"
    echo -e "${CYAN}  ${MSG_PANEL_STATUS}:${NC}        sudo systemctl status nova-panel"
    echo -e "${CYAN}  ${MSG_DAEMON_STATUS}:${NC}      sudo systemctl status nova-daemon"
    echo ""
    echo -e "${GREEN}  ✨ ${MSG_AUTOSTART_HINT}${NC}"
    echo ""
}

# ========== 主流程 ==========

main() {
    echo ""
    echo -e "${GREEN}${BOLD}========================================${NC}"
    echo -e "${GREEN}${BOLD}  🚀 ${MSG_TITLE}${NC}"
    echo -e "${GREEN}${BOLD}========================================${NC}"
    echo ""

    # Step 1: 安装系统依赖
    install_deps

    # Step 2: 检测并安装 Go
    if command_exists go; then
        echo -e "${GREEN}✅ ${MSG_GO_FOUND}: $(go version)${NC}"
    else
        echo -e "${YELLOW}⚠️  ${MSG_GO_NOT_FOUND}${NC}"
        install_go
    fi

    # Step 3: 检测并安装 Node.js
    if command_exists node; then
        echo -e "${GREEN}✅ ${MSG_NODE_FOUND}: $(node --version)${NC}"
    else
        echo -e "${YELLOW}⚠️  ${MSG_NODE_NOT_FOUND}${NC}"
        install_node
    fi

    # Step 4: 编译项目
    build_project

    # Step 5: 创建 systemd 服务
    create_services

    # Step 6: 启动服务
    start_services

    # Step 7: 显示信息
    show_info
}

main "$@"
