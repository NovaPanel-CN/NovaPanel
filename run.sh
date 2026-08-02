#!/bin/bash

# ============================================================
# NovaPanel Linux 一键安装与管理脚本
# 功能：检测环境 → 安装依赖 → 编译 → systemd 开机自启 → 启动
# ============================================================

set -e

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
    echo -e "${YELLOW}📦 安装系统依赖...${NC}"
    case $pm in
        apt)
            sudo apt-get update -qq
            sudo apt-get install -y -qq wget curl tar xz-utils > /dev/null 2>&1 || true
            ;;
        yum|dnf)
            sudo $pm install -y -q wget curl tar xz > /dev/null 2>&1 || true
            ;;
        *)
            echo -e "${RED}❌ 不支持的包管理器，请手动安装 wget curl tar${NC}"
            exit 1
            ;;
    esac
    echo -e "${GREEN}✅ 系统依赖已就绪${NC}"
}

# ========== 安装 Go ==========

install_go() {
    echo -e "${YELLOW}📥 正在安装 Go ${GO_VERSION}...${NC}"
    local arch=$(get_arch)
    [ "$arch" = "unsupported" ] && { echo -e "${RED}❌ 不支持的系统架构${NC}"; exit 1; }

    local url
    if is_china; then
        echo -e "${CYAN}🌐 检测到中国大陆网络，使用镜像源下载 Go...${NC}"
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
        echo -e "${CYAN}🔧 已设置 GOPROXY=https://goproxy.cn,direct${NC}"
    fi

    echo -e "${GREEN}✅ Go ${GO_VERSION} 安装完成${NC}"
}

# ========== 安装 Node.js ==========

install_node() {
    echo -e "${YELLOW}📥 正在安装 Node.js ${NODE_VERSION}...${NC}"
    local arch=$(get_arch)
    local node_arch
    case $arch in
        amd64) node_arch="x64" ;;
        arm64) node_arch="arm64" ;;
        *) echo -e "${RED}❌ 不支持的系统架构${NC}"; exit 1 ;;
    esac

    local url
    if is_china; then
        echo -e "${CYAN}🌐 检测到中国大陆网络，使用镜像源下载 Node.js...${NC}"
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
        echo -e "${CYAN}🔧 已设置 npm 镜像源${NC}"
    fi

    echo -e "${GREEN}✅ Node.js ${NODE_VERSION} 安装完成${NC}"
}

# ========== 编译项目 ==========

build_project() {
    echo -e "${YELLOW}🔨 正在编译项目...${NC}"

    export PATH="$PATH:/usr/local/go/bin"
    export GOPATH="$HOME/go"
    export PATH="$PATH:$GOPATH/bin"

    # 编译面板
    echo -e "${CYAN}   编译 go-web...${NC}"
    cd "${SCRIPT_DIR}/go-web"
    /usr/local/go/bin/go build -o nova-panel .
    echo -e "${GREEN}   ✅ 面板编译完成${NC}"

    # 编译 Daemon
    echo -e "${CYAN}   编译 go-daemon...${NC}"
    cd "${SCRIPT_DIR}/go-daemon"
    /usr/local/go/bin/go build -o nova-daemon .
    echo -e "${GREEN}   ✅ Daemon 编译完成${NC}"

    cd "${SCRIPT_DIR}"
    echo -e "${GREEN}✅ 项目编译完成${NC}"
}

# ========== 创建 systemd 服务 ==========

create_services() {
    echo -e "${YELLOW}⚙️  正在创建 systemd 开机自启服务...${NC}"

    # 面板服务
    sudo tee /etc/systemd/system/nova-panel.service > /dev/null <<'EOF'
[Unit]
Description=NovaPanel Web Panel
After=network.target

[Service]
Type=simple
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    # 用 sed 填入实际路径（避免变量被 heredoc 吞掉）
    sudo sed -i "s|^Type=simple|Type=simple\nWorkingDirectory=${SCRIPT_DIR}/go-web\nExecStart=${SCRIPT_DIR}/go-web/nova-panel\nEnvironment=PATH=/usr/local/go/bin:/usr/local/node/bin:/usr/bin:/bin|" /etc/systemd/system/nova-panel.service

    # Daemon 服务
    sudo tee /etc/systemd/system/nova-daemon.service > /dev/null <<'EOF'
[Unit]
Description=NovaPanel Daemon
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
    echo -e "${GREEN}✅ systemd 服务已创建并启用开机自启${NC}"
}

# ========== 启动服务 ==========

start_services() {
    echo -e "${YELLOW}🚀 正在启动服务...${NC}"
    sudo systemctl start nova-daemon
    sleep 1
    sudo systemctl start nova-panel
    sleep 2
    echo -e "${GREEN}✅ 服务已启动${NC}"
}

# ========== 显示信息 ==========

show_info() {
    local ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -z "$ip" ] && ip="服务器IP"

    echo ""
    echo -e "${GREEN}${BOLD}╔══════════════════════════════════════╗${NC}"
    echo -e "${GREEN}${BOLD}║       NovaPanel 安装完成！           ║${NC}"
    echo -e "${GREEN}${BOLD}╚══════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${CYAN}  📌 面板地址:${NC}  http://${ip}:8080"
    echo -e "${CYAN}  📌 Daemon:${NC}    ws://${ip}:8079"
    echo ""
    echo -e "${YELLOW}${BOLD}━━━ 🛠 服务管理命令 ━━━${NC}"
    echo -e "${GREEN}  ▶ 启动面板:${NC}    sudo systemctl start nova-panel"
    echo -e "${GREEN}  ▶ 启动 Daemon:${NC}  sudo systemctl start nova-daemon"
    echo -e "${GREEN}  🔄 重启面板:${NC}    sudo systemctl restart nova-panel"
    echo -e "${GREEN}  🔄 重启 Daemon:${NC}  sudo systemctl restart nova-daemon"
    echo -e "${GREEN}  ⏹ 停止面板:${NC}    sudo systemctl stop nova-panel"
    echo -e "${GREEN}  ⏹ 停止 Daemon:${NC}  sudo systemctl stop nova-daemon"
    echo ""
    echo -e "${YELLOW}${BOLD}━━━ 📊 查看状态 ━━━${NC}"
    echo -e "${CYAN}  面板状态:${NC}      sudo systemctl status nova-panel"
    echo -e "${CYAN}  Daemon 状态:${NC}    sudo systemctl status nova-daemon"
    echo ""
    echo -e "${GREEN}  ✨ 服务已设置开机自启，可以安全关闭此终端！${NC}"
    echo ""
}

# ========== 主流程 ==========

main() {
    echo ""
    echo -e "${GREEN}${BOLD}╔══════════════════════════════════════╗${NC}"
    echo -e "${GREEN}${BOLD}║     NovaPanel Linux 安装脚本         ║${NC}"
    echo -e "${GREEN}${BOLD}╚══════════════════════════════════════╝${NC}"
    echo ""

    # Step 1: 安装系统依赖
    install_deps

    # Step 2: 检测并安装 Go
    if command_exists go; then
        echo -e "${GREEN}✅ Go 已安装:$(go version)${NC}"
    else
        echo -e "${YELLOW}⚠️  Go 未安装，开始安装...${NC}"
        install_go
    fi

    # Step 3: 检测并安装 Node.js
    if command_exists node; then
        echo -e "${GREEN}✅ Node.js 已安装: $(node --version)${NC}"
    else
        echo -e "${YELLOW}⚠️  Node.js 未安装，开始安装...${NC}"
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
