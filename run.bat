@echo off
title NovaPanel Run

set NOVAPANEL_ROOT=%~dp0
set NODE_BIN=%NOVAPANEL_ROOT%tools\node
set PATH=%NODE_BIN%;%PATH%

echo ========================================
echo   NovaPanel Run
echo   Go Web:  8080
echo   Vue API: 8079
echo   Daemon:  8078
echo   Data:    go-daemon\data\
echo ========================================
echo.

:: Check Go
where go >nul 2>nul
if errorlevel 1 (
    echo [Error] Go not found!
    echo Install from: https://golang.google.cn/dl/
    pause
    exit /b 1
)

:: Check Node.js
if not exist "%NODE_BIN%\node.exe" (
    echo [Error] Node.js not found!
    pause
    exit /b 1
)

echo [Check] Go version:
go version
echo.

echo [Check] Node.js version:
"%NODE_BIN%\node.exe" -v
echo.

:: Install Vue deps (first run only)
if not exist "%NOVAPANEL_ROOT%vue-backend\node_modules" (
    echo [Install] First run, installing Vue backend deps...
    cd /d "%NOVAPANEL_ROOT%vue-backend"
    call "%NODE_BIN%\npm.cmd" install
    cd /d "%NOVAPANEL_ROOT%"
    echo.
)

:: Start services
echo [1/5] Cleaning ports 8078/8079/8080...
call :killport 8078
call :killport 8079
call :killport 8080
echo.

echo [2/5] Tidying Go deps...
cd /d "%NOVAPANEL_ROOT%"
go mod tidy
echo.

echo [3/5] Starting Go Daemon (:8078)...
start "NovaPanel Daemon" cmd /k "cd /d %NOVAPANEL_ROOT%go-daemon && go run main.go"
timeout /t 2 >nul

echo [4/5] Starting Vue API (:8079)...
start "NovaPanel Vue API" cmd /c "cd /d %NOVAPANEL_ROOT%vue-backend && node server.js"
timeout /t 2 >nul

echo [5/5] Starting Go Web (:8080)...
start "NovaPanel Web" cmd /c "cd /d %NOVAPANEL_ROOT% && go run ./go-web/main.go ./go-web/mcsmanager_client.go"
timeout /t 2 >nul

start "" "http://127.0.0.1:8080"

echo.
echo ========================================
echo   Started!
echo   Go Web:  http://127.0.0.1:8080
echo   Vue API: http://127.0.0.1:8079
echo   Daemon:  http://127.0.0.1:8078
echo   Users:   %NOVAPANEL_ROOT%go-daemon\data\users.json
echo ========================================
echo Window will close in 2 seconds...
timeout /t 2 >nul
exit

:: ===== Subroutine: kill process listening on given port =====
:killport
set "KILLPORT=%~1"
if "%KILLPORT%"=="" goto :eof
set "KILLED=0"
for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":%KILLPORT% " ^| findstr "LISTENING"') do (
    taskkill /F /PID %%a >nul 2>nul
    echo   killed PID %%a on port %KILLPORT%
    set "KILLED=1"
)
if "%KILLED%"=="0" echo   port %KILLPORT% is free
goto :eof
