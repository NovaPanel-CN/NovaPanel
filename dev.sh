#!/bin/bash
# NovaPanel Dev Mode — Linux startup script
# Ports:  Go Web :8080 | Vue API :8079 | Go Daemon :8078

set -e

NOVAPANEL_ROOT="$(cd "$(dirname "$0")" && pwd)"
NODE_BIN="$NOVAPANEL_ROOT/tools/node"
export PATH="$NODE_BIN:$PATH"

echo "========================================"
echo "  NovaPanel Dev Mode"
echo "  Go Web:  8080"
echo "  Vue API: 8079"
echo "  Daemon:  8078"
echo "  Data:    go-daemon/data/"
echo "========================================"
echo ""

# ---- Check Go ----
if ! command -v go &>/dev/null; then
    echo "[Error] Go not found!"
    echo "Install from: https://golang.google.cn/dl/"
    exit 1
fi

# ---- Check Node.js ----
NODE_BIN="$NOVAPANEL_ROOT/tools/node"
NODE_EXE="$NODE_BIN/node"
if [ ! -x "$NODE_EXE" ] && [ ! -x "$NODE_EXE.js" ]; then
    # Try without .js — just the node binary
    if [ ! -x "$NODE_EXE" ]; then
        echo "[Error] Node.js not found at $NODE_EXE!"
        exit 1
    fi
fi

echo "[Check] Go version:"
go version
echo ""

echo "[Check] Node.js version:"
"$NODE_EXE" -v
echo ""

# ---- Vue deps ----
if [ ! -d "$NOVAPANEL_ROOT/vue-backend/node_modules" ]; then
    echo "[Install] First run, installing Vue backend deps..."
    cd "$NOVAPANEL_ROOT/vue-backend"
    npm install
    cd "$NOVAPANEL_ROOT"
    echo ""
fi

# ---- Kill processes on target ports ----
killport() {
    local PORT="$1"
    local PIDS

    # Try lsof first (macOS / many Linux)
    PIDS=$(lsof -ti ":$PORT" 2>/dev/null || true)

    # Fallback: ss (modern Linux, part of iproute2)
    if [ -z "$PIDS" ]; then
        PIDS=$(ss -tlnp "sport = :$PORT" 2>/dev/null | \
            awk 'NR>1 {gsub(/.*pid=/,"",$NF); gsub(/,.*/,"",$NF); print $NF}' | \
            grep -E '^[0-9]+$' || true)
    fi

    if [ -n "$PIDS" ]; then
        for PID in $PIDS; do
            kill "$PID" 2>/dev/null || true
            echo "  killed PID $PID on port $PORT"
        done
        # Give processes a moment to release the port
        sleep 1
    else
        echo "  port $PORT is free"
    fi
}

echo "[1/5] Cleaning ports 8078/8079/8080..."
killport 8078
killport 8079
killport 8080
echo ""

echo "[2/5] Tidying Go deps..."
cd "$NOVAPANEL_ROOT"
go mod tidy
echo ""

echo "[3/5] Starting Go Daemon (:8078)..."
cd "$NOVAPANEL_ROOT/go-daemon"
go run main.go &
DAEMON_PID=$!
cd "$NOVAPANEL_ROOT"
sleep 2

echo "[4/5] Starting Vue API (:8079)..."
cd "$NOVAPANEL_ROOT/vue-backend"
node server.js &
VUE_PID=$!
cd "$NOVAPANEL_ROOT"
sleep 2

echo "[5/5] Starting Go Web (:8080)..."
go run ./go-web/main.go ./go-web/mcsmanager_client.go &
WEB_PID=$!
sleep 2

# ---- Open browser ----
if command -v xdg-open &>/dev/null; then
    xdg-open "http://127.0.0.1:8080" 2>/dev/null || true
elif command -v gnome-open &>/dev/null; then
    gnome-open "http://127.0.0.1:8080" 2>/dev/null || true
fi

echo ""
echo "========================================"
echo "  Dev started!"
echo "  Go Web:  http://127.0.0.1:8080  (PID $WEB_PID)"
echo "  Vue API: http://127.0.0.1:8079  (PID $VUE_PID)"
echo "  Daemon:  http://127.0.0.1:8078  (PID $DAEMON_PID)"
echo "  Users:   $NOVAPANEL_ROOT/go-daemon/data/users.json"
echo "========================================"
echo ""
echo "Run the following to stop all services:"
echo "  kill $DAEMON_PID $VUE_PID $WEB_PID"
echo ""

# Trap Ctrl+C to clean up background processes
trap 'echo ""; echo "[Shutdown] Stopping background services..."; kill $DAEMON_PID $VUE_PID $WEB_PID 2>/dev/null; echo "[Done]"; exit 0' INT TERM

# Wait for any child to exit (stay alive)
wait