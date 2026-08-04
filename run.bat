@echo off
chcp 65001 >nul
title Velunex Panel Run
setlocal enabledelayedexpansion

set NOVAPANEL_ROOT=%~dp0

:: ===== 检测系统语言 =====
set "LANG_CODE=en"
for /f "tokens=2,*" %%a in ('reg query "HKCU\Control Panel\International" /v LocaleName 2^>nul') do set "SYS_LANG=%%b"
echo !SYS_LANG! | findstr /i /r "zh-CN zh-Hans" >nul && set "LANG_CODE=zh_cn"
echo !SYS_LANG! | findstr /i /r "zh-TW zh-HK zh-Hant zh-MO" >nul && set "LANG_CODE=zh_tw"

:: ===== 加载对应语言 =====
call :msg_!LANG_CODE!
set "NOVAPANEL_LANG=!LANG_CODE!"

echo ========================================
echo   !MSG_TITLE!
echo   !MSG_DAEMON!:  8079
echo   !MSG_WEB!:     8080
echo   !MSG_DATA!:    go-daemon\data\
echo ========================================
echo.

:: ===== 检查 Go =====
where go >nul 2>nul
if errorlevel 1 (
    echo [!MSG_ERROR!] !MSG_GO_NOT_FOUND!
    echo !MSG_INSTALL_FROM!: https://golang.google.cn/dl/
    pause
    exit /b 1
)

echo [!MSG_CHECK!] !MSG_GO_VERSION!:
go version
echo.

:: ===== 启动服务 =====
echo [1/3] !MSG_CLEANING_PORTS! 8079/8080...
call :killport 8079
call :killport 8080
echo.

echo [2/3] !MSG_TIDYING_DEPS!...
cd /d "%NOVAPANEL_ROOT%"
go mod tidy
echo.

echo [3/3] !MSG_STARTING_SERVICES!...
start "Velunex Panel Daemon" cmd /c "cd /d "%NOVAPANEL_ROOT%go-daemon" && go run daemon_app.go"
ping -n 3 127.0.0.1 >nul

start "Velunex Panel Web" cmd /k "cd /d "%NOVAPANEL_ROOT%go-web" && go run web_app.go mcsmanager_client.go"
ping -n 3 127.0.0.1 >nul

start "" "http://127.0.0.1:8080"

echo.
echo ========================================
echo   !MSG_STARTED!
echo   !MSG_DAEMON!:  http://127.0.0.1:8079
echo   !MSG_WEB!:     http://127.0.0.1:8080
echo   !MSG_USERS!:   %NOVAPANEL_ROOT%go-daemon\data\users.json
echo ========================================
echo !MSG_CLOSE_IN_2S!
ping -n 3 127.0.0.1 >nul
exit

:: ===== 语言: English =====
:msg_en
set "MSG_TITLE=Velunex Panel Run"
set "MSG_DAEMON=Daemon"
set "MSG_WEB=Web"
set "MSG_DATA=Data"
set "MSG_ERROR=Error"
set "MSG_GO_NOT_FOUND=Go not found!"
set "MSG_INSTALL_FROM=Install from"
set "MSG_CHECK=Check"
set "MSG_GO_VERSION=Go version"
set "MSG_CLEANING_PORTS=Cleaning ports"
set "MSG_TIDYING_DEPS=Tidying Go deps"
set "MSG_STARTING_SERVICES=Starting Velunex Panel services"
set "MSG_STARTED=Started!"
set "MSG_USERS=Users"
set "MSG_CLOSE_IN_2S=Window will close in 2 seconds..."
set "MSG_PORT_FREE=port"
set "MSG_PORT_FREE_SUFFIX=is free"
set "MSG_KILLED=killed PID"
set "MSG_KILLED_SUFFIX=on port"
goto :eof

:: ===== 语言: 简体中文 =====
:msg_zh_cn
set "MSG_TITLE=Velunex Panel 运行"
set "MSG_DAEMON=守护进程"
set "MSG_WEB=面板"
set "MSG_DATA=数据"
set "MSG_ERROR=错误"
set "MSG_GO_NOT_FOUND=未找到 Go 环境！"
set "MSG_INSTALL_FROM=安装地址"
set "MSG_CHECK=检查"
set "MSG_GO_VERSION=Go 版本"
set "MSG_CLEANING_PORTS=清理端口"
set "MSG_TIDYING_DEPS=整理 Go 依赖"
set "MSG_STARTING_SERVICES=正在启动 Velunex Panel 服务"
set "MSG_STARTED=启动成功！"
set "MSG_USERS=用户"
set "MSG_CLOSE_IN_2S=窗口将在 2 秒后关闭..."
set "MSG_PORT_FREE=端口"
set "MSG_PORT_FREE_SUFFIX=空闲"
set "MSG_KILLED=已终止 PID"
set "MSG_KILLED_SUFFIX=端口"
goto :eof

:: ===== 语言: 繁體中文 =====
:msg_zh_tw
set "MSG_TITLE=Velunex Panel 執行"
set "MSG_DAEMON=守護進程"
set "MSG_WEB=面板"
set "MSG_DATA=資料"
set "MSG_ERROR=錯誤"
set "MSG_GO_NOT_FOUND=找不到 Go 環境！"
set "MSG_INSTALL_FROM=安裝位址"
set "MSG_CHECK=檢查"
set "MSG_GO_VERSION=Go 版本"
set "MSG_CLEANING_PORTS=清理連接埠"
set "MSG_TIDYING_DEPS=整理 Go 相依套件"
set "MSG_STARTING_SERVICES=正在啟動 Velunex Panel 服務"
set "MSG_STARTED=啟動成功！"
set "MSG_USERS=使用者"
set "MSG_CLOSE_IN_2S=視窗將在 2 秒後關閉..."
set "MSG_PORT_FREE=連接埠"
set "MSG_PORT_FREE_SUFFIX=空閒"
set "MSG_KILLED=已終止 PID"
set "MSG_KILLED_SUFFIX=連接埠"
goto :eof

:: ===== 子程序: 终止占用端口的进程 =====
:killport
set "KILLPORT=%~1"
if "%KILLPORT%"=="" goto :eof
set "KILLED=0"
for /f "tokens=5" %%a in ('netstat -ano ^| findstr "LISTENING" ^| findstr ":%KILLPORT% " ^| findstr /v "\["') do (
    taskkill /F /PID %%a >nul 2>nul
    echo   !MSG_KILLED! %%a !MSG_KILLED_SUFFIX! %KILLPORT%
    set "KILLED=1"
)
if "!KILLED!"=="0" echo   !MSG_PORT_FREE! %KILLPORT% !MSG_PORT_FREE_SUFFIX!
goto :eof
