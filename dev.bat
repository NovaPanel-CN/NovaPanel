@echo off
chcp 65001 >nul
title NovaPanel 开发模式

set NOVAPANEL_ROOT=%~dp0
set NODE_BIN=%NOVAPANEL_ROOT%tools\node
set PATH=%NODE_BIN%;%PATH%

:: 检查系统 Go
where go >nul 2>nul
if errorlevel 1 (
    echo [错误] 未找到系统 Go！
    echo 请安装 Go: https://golang.google.cn/dl/
    pause
    exit /b 1
)

:: 检查 Node.js
if not exist "%NODE_BIN%\node.exe" (
    echo [错误] 未找到内置 Node.js！
    pause
    exit /b 1
)

echo ========================================
echo   NovaPanel 开发模式
echo   Go: 系统安装版
echo   Node.js: 内置版 (22.14.0 LTS)
echo ========================================
echo.

echo [检查] Go 版本:
go version
echo.

echo [检查] Node.js 版本:
"%NODE_BIN%\node.exe" -v
echo.

:: ============================================================
::  安装 Vue 依赖
:: ============================================================
if not exist "%NOVAPANEL_ROOT%vue-backend\node_modules" (
    echo [安装] 首次运行，安装 Vue 后端依赖...
    cd /d "%NOVAPANEL_ROOT%vue-backend"
    call "%NODE_BIN%\npm.cmd" install
    cd /d "%NOVAPANEL_ROOT%"
    echo.
)

:: ============================================================
::  启动服务（跳过 tools 目录）
:: ============================================================
echo [1/4] 安装 Go 依赖...
cd /d "%NOVAPANEL_ROOT%"
go mod tidy -e 2>nul
echo.

echo [2/4] 启动 Go Daemon（远程节点 :8078）...
start "NovaPanel Daemon" cmd /c "cd /d %NOVAPANEL_ROOT%go-daemon && go run main.go"
timeout /t 2 >nul

echo [3/4] 启动 Vue 后端（API :8079）...
start "NovaPanel Vue API" cmd /c "cd /d %NOVAPANEL_ROOT%vue-backend && node server.js"
timeout /t 2 >nul

echo [4/4] 启动 Go Web 前端（面板 :8080）...
start "NovaPanel Web" cmd /c "cd /d %NOVAPANEL_ROOT% && go run ./go-web/main.go"
timeout /t 2 >nul

start "" "http://127.0.0.1:8080"

echo.
echo ========================================
echo   开发环境启动完成！
echo   Go Web 前端: http://127.0.0.1:8080
echo   Vue 后端: http://127.0.0.1:8079
echo   Go Daemon: http://127.0.0.1:8078
echo   用户数据: %NOVAPANEL_ROOT%go-daemon\data\users.json
echo ========================================
echo.
pause >nul
exit