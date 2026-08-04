package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DAEMON_PORT = 8079
	DAEMON_NAME = "Velunex Panel Daemon"
	DAEMON_VER  = "1.0.0"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	instances   = make(map[string]*Instance)
	instancesMu sync.RWMutex
)

type SystemInfo struct {
	OS            string  `json:"os"`
	Hostname      string  `json:"hostname"`
	CpuUsage      float64 `json:"cpuUsage"`
	CpuCores      int     `json:"cpuCores"`
	MemTotal      float64 `json:"memTotal"`
	MemUsed       float64 `json:"memUsed"`
	MemPercent    float64 `json:"memPercent"`
	DiskTotal     float64 `json:"diskTotal"`
	DiskUsed      float64 `json:"diskUsed"`
	DiskPercent   float64 `json:"diskPercent"`
	Uptime        string  `json:"uptime"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
	ProcessCount  int     `json:"processCount"`
}

var systemInfoCache struct {
	sync.Mutex
	value   SystemInfo
	updated time.Time
}

type Instance struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Port        int    `json:"port"`
	Memory      int    `json:"memory"`
	StartTime   string `json:"startTime"`
	Uptime      string `json:"uptime"`
	PlayerCount int    `json:"playerCount"`
	MaxPlayers  int    `json:"maxPlayers"`
}

type WSMessage struct {
	Type    string      `json:"type"`
	Data    interface{} `json:"data"`
	Success bool        `json:"success"`
	Message string      `json:"message"`
}

func startupText(key string) string {
	lang := os.Getenv("NOVAPANEL_LANG")
	texts := map[string][3]string{
		"started": {"Daemon started successfully", "守护进程现已成功启动", "守護行程現已成功啟動"},
		"system":  {"Operating system", "操作系统", "作業系統"},
		"config":  {"Configuration directory", "配置文件", "設定檔目錄"},
		"close":   {"Press Ctrl+C to stop", "你可以使用 Ctrl+C 快捷键即可关闭程序", "可使用 Ctrl+C 快捷鍵關閉程式"},
		"failed":  {"Startup failed", "启动失败", "啟動失敗"},
	}
	i := 0
	if lang == "zh_cn" {
		i = 1
	} else if lang == "zh_tw" {
		i = 2
	}
	return texts[key][i]
}

func printStartupBanner(component, version string) {
	fmt.Println()
	fmt.Println(`V     V  EEEEEEE  L        U     U  N     N  EEEEEEE  X     X`)
	fmt.Println(` V   V   E        L        U     U  NN    N  E         X   X `)
	fmt.Println(`  V V    EEEEE    L        U     U  N N   N  EEEEE      X X  `)
	fmt.Println(`   V     E        L        U     U  N  N  N  E           X   `)
	fmt.Println(`   V     EEEEEEE  LLLLLLL   UUUUU   N   N N  EEEEEEE   X     `)
	fmt.Println()
	fmt.Printf("                    Velunex Panel | %s\n", component)
	fmt.Printf("                    Version %s\n", version)
	fmt.Println("                    Copyright (C) 2026 Velunex Panel")
	fmt.Println()
}

func main() {
	initInstances()

	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte("Velunex Panel Daemon v1.0.0"))
			return
		}
		http.NotFound(w, r)
	})

	addr := fmt.Sprintf(":%d", DAEMON_PORT)
	printStartupBanner("Daemon", DAEMON_VER)
	fmt.Println(" ================================")
	log.Printf("[INFO] %s", startupText("started"))
	log.Printf("[INFO] WebSocket: ws://127.0.0.1%s/ws", addr)
	log.Printf("[INFO] %s: %s", startupText("system"), runtime.GOOS)
	log.Printf("[INFO] %s: go-daemon/data/", startupText("config"))
	log.Printf("[INFO] %s", startupText("close"))
	fmt.Println(" ================================")
	fmt.Println()

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(startupText("failed")+":", err)
	}
}

func initInstances() {
	instancesMu.Lock()
	defer instancesMu.Unlock()

	instances["server1"] = &Instance{
		ID:          "server1",
		Name:        "生存服",
		Status:      "running",
		Port:        25565,
		Memory:      1024,
		StartTime:   time.Now().Add(-2 * time.Hour).Format("2006-01-02 15:04:05"),
		Uptime:      "2h 15m",
		PlayerCount: 3,
		MaxPlayers:  20,
	}
	instances["server2"] = &Instance{
		ID:          "server2",
		Name:        "创造服",
		Status:      "stopped",
		Port:        25566,
		Memory:      2048,
		StartTime:   "",
		Uptime:      "",
		PlayerCount: 0,
		MaxPlayers:  20,
	}
	instances["server3"] = &Instance{
		ID:          "server3",
		Name:        "空岛服",
		Status:      "running",
		Port:        25567,
		Memory:      512,
		StartTime:   time.Now().Add(-45 * time.Minute).Format("2006-01-02 15:04:05"),
		Uptime:      "45m",
		PlayerCount: 7,
		MaxPlayers:  10,
	}
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("🔗 面板已连接 (IP: %s)", r.RemoteAddr)

	conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// WriteControl is safe to call concurrently with the request-response
			// writes below. WriteMessage is not, and would panic when a client
			// request happened at the same time as this keepalive ping.
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				log.Printf("⚠️ Ping 失败: %v", err)
				return
			}
		}
	}()

	for {
		var msg WSMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("⚠️ WebSocket 异常关闭: %v", err)
			} else {
				log.Printf("读取消息失败: %v", err)
			}
			break
		}

		log.Printf("📨 收到消息: %s", msg.Type)

		switch msg.Type {
		case "ping":
			conn.WriteJSON(WSMessage{Type: "pong", Success: true, Message: "pong"})

		case "get_system":
			sysInfo := getSystemInfo()
			conn.WriteJSON(WSMessage{Type: "system_info", Data: sysInfo, Success: true})

		case "get_instances":
			instancesMu.RLock()
			list := make([]*Instance, 0, len(instances))
			for _, inst := range instances {
				list = append(list, inst)
			}
			instancesMu.RUnlock()
			conn.WriteJSON(WSMessage{Type: "instances_list", Data: list, Success: true})

		case "file_list", "file_download", "file_upload", "file_delete",
			"file_rename", "file_mkdir", "file_touch", "file_read", "file_write":
			handleFileOperation(conn, &msg)

		default:
			log.Printf("⚠️ 未知消息类型: %s", msg.Type)
		}
	}
}

// ========== 文件管理 ==========

// 文件操作最大大小限制（50MB）
const fileMaxSize = 50 * 1024 * 1024

// FileInfo 文件信息
type FileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	ModTime string `json:"modTime"`
	Mode    string `json:"mode"`
}

// FileRequest 文件操作请求
type FileRequest struct {
	Path    string `json:"path"`
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
	Content string `json:"content"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
}

// resolvePath 安全地解析路径，返回绝对路径
// 兼容 Linux（/）和 Windows（C:\）路径格式
func resolvePath(p string) (string, error) {
	if p == "" {
		p = "/"
	}
	// 统一路径分隔符后清理
	cleaned := filepath.Clean(p)
	if cleaned == "." {
		cleaned = "/"
	}
	// 转为绝对路径
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("路径解析失败: %v", err)
	}
	return abs, nil
}

// handleFileOperation 统一处理文件操作请求
func handleFileOperation(conn *websocket.Conn, msg *WSMessage) {
	// 解析请求数据
	dataBytes, err := json.Marshal(msg.Data)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: msg.Type, Success: false, Message: "参数解析失败"})
		return
	}
	var req FileRequest
	if err := json.Unmarshal(dataBytes, &req); err != nil {
		conn.WriteJSON(WSMessage{Type: msg.Type, Success: false, Message: "参数解析失败"})
		return
	}

	switch msg.Type {
	case "file_list":
		handleFileList(conn, &req)
	case "file_download":
		handleFileDownload(conn, &req)
	case "file_upload":
		handleFileUpload(conn, &req)
	case "file_delete":
		handleFileDelete(conn, &req)
	case "file_rename":
		handleFileRename(conn, &req)
	case "file_mkdir":
		handleFileMkdir(conn, &req)
	case "file_touch":
		handleFileTouch(conn, &req)
	case "file_read":
		handleFileRead(conn, &req)
	case "file_write":
		handleFileWrite(conn, &req)
	}
}

// handleFileList 列出目录内容
func handleFileList(conn *websocket.Conn, req *FileRequest) {
	// Windows 上根目录枚举所有盘符
	if runtime.GOOS == "windows" && (req.Path == "" || req.Path == "/" || req.Path == "\\") {
		drives := listWindowsDrives()
		conn.WriteJSON(WSMessage{
			Type:    "file_list",
			Success: true,
			Data: map[string]interface{}{
				"path":  "/",
				"files": drives,
			},
		})
		return
	}

	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_list", Success: false, Message: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_list", Success: false, Message: "路径不存在或无法访问: " + err.Error()})
		return
	}

	// 如果是文件，返回其所在目录
	if !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	limit := req.Limit
	// Keep directory listings safe by default for low-spec hosts. The web UI
	// defaults to 16 and may request a user-selected value up to 50.
	if limit <= 0 {
		limit = 16
	}
	if limit > 50 {
		limit = 50
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	dir, err := os.Open(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_list", Success: false, Message: "读取目录失败: " + err.Error()})
		return
	}
	defer dir.Close()

	files := make([]FileInfo, 0, limit+1)
	skipped := 0
	for len(files) < limit+1 {
		entries, readErr := dir.ReadDir(min(limit+1-len(files), 128))
		if readErr != nil && readErr != io.EOF {
			break
		}
		for _, entry := range entries {
			if skipped < offset {
				skipped++
				continue
			}
			info, statErr := entry.Info()
			if statErr != nil {
				continue
			}
			files = append(files, FileInfo{
				Name: entry.Name(), Path: filepath.Join(absPath, entry.Name()), Size: info.Size(),
				IsDir: entry.IsDir(), ModTime: info.ModTime().Format("2006-01-02 15:04:05"), Mode: info.Mode().String(),
			})
			if len(files) >= limit+1 {
				break
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	hasMore := len(files) > limit
	if hasMore {
		files = files[:limit]
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_list",
		Success: true,
		Data: map[string]interface{}{
			"path":    absPath,
			"files":   files,
			"hasMore": hasMore,
		},
	})
}

// listWindowsDrives 枚举 Windows 所有可用盘符（C/D/E...）
func listWindowsDrives() []FileInfo {
	var drives []FileInfo
	for c := 'C'; c <= 'Z'; c++ {
		drive := string(c) + ":\\"
		if _, err := os.Stat(drive); err == nil {
			drives = append(drives, FileInfo{
				Name:    string(c) + ":",
				Path:    string(c) + ":/",
				Size:    0,
				IsDir:   true,
				ModTime: "",
				Mode:    "drwxr-xr-x",
			})
		}
	}
	return drives
}

// handleFileDownload 下载文件（base64 编码）
func handleFileDownload(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_download", Success: false, Message: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_download", Success: false, Message: "文件不存在: " + err.Error()})
		return
	}
	if info.IsDir() {
		conn.WriteJSON(WSMessage{Type: "file_download", Success: false, Message: "不能下载文件夹"})
		return
	}
	if info.Size() > fileMaxSize {
		conn.WriteJSON(WSMessage{Type: "file_download", Success: false, Message: "文件过大（超过 50MB 限制）"})
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_download", Success: false, Message: "读取文件失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_download",
		Success: true,
		Data: map[string]interface{}{
			"name":    filepath.Base(absPath),
			"path":    absPath,
			"size":    info.Size(),
			"content": base64.StdEncoding.EncodeToString(data),
		},
	})
}

// handleFileUpload 上传文件（base64 编码）
func handleFileUpload(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_upload", Success: false, Message: err.Error()})
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_upload", Success: false, Message: "文件内容解码失败: " + err.Error()})
		return
	}
	if int64(len(data)) > fileMaxSize {
		conn.WriteJSON(WSMessage{Type: "file_upload", Success: false, Message: "文件过大（超过 50MB 限制）"})
		return
	}

	// 确保父目录存在
	os.MkdirAll(filepath.Dir(absPath), 0755)

	if err := os.WriteFile(absPath, data, 0644); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_upload", Success: false, Message: "写入文件失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_upload",
		Success: true,
		Data:    map[string]interface{}{"path": absPath, "size": len(data)},
	})
}

// handleFileDelete 删除文件或目录
func handleFileDelete(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_delete", Success: false, Message: err.Error()})
		return
	}

	// 防止删除根目录
	if absPath == "/" || absPath == filepath.Dir(absPath) {
		conn.WriteJSON(WSMessage{Type: "file_delete", Success: false, Message: "不能删除根目录"})
		return
	}

	if err := os.RemoveAll(absPath); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_delete", Success: false, Message: "删除失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_delete",
		Success: true,
		Data:    map[string]interface{}{"path": absPath},
	})
}

// handleFileRename 重命名或移动文件/目录
func handleFileRename(conn *websocket.Conn, req *FileRequest) {
	oldPath, err := resolvePath(req.OldPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_rename", Success: false, Message: err.Error()})
		return
	}
	newPath, err := resolvePath(req.NewPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_rename", Success: false, Message: err.Error()})
		return
	}

	if _, err := os.Stat(oldPath); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_rename", Success: false, Message: "源路径不存在: " + err.Error()})
		return
	}

	os.MkdirAll(filepath.Dir(newPath), 0755)
	if err := os.Rename(oldPath, newPath); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_rename", Success: false, Message: "重命名失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_rename",
		Success: true,
		Data:    map[string]interface{}{"oldPath": oldPath, "newPath": newPath},
	})
}

// handleFileMkdir 新建文件夹
func handleFileMkdir(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_mkdir", Success: false, Message: err.Error()})
		return
	}

	if err := os.MkdirAll(absPath, 0755); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_mkdir", Success: false, Message: "创建文件夹失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_mkdir",
		Success: true,
		Data:    map[string]interface{}{"path": absPath},
	})
}

// handleFileTouch 新建空文件
func handleFileTouch(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_touch", Success: false, Message: err.Error()})
		return
	}

	if _, err := os.Stat(absPath); err == nil {
		conn.WriteJSON(WSMessage{Type: "file_touch", Success: false, Message: "文件已存在"})
		return
	}

	os.MkdirAll(filepath.Dir(absPath), 0755)
	f, err := os.Create(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_touch", Success: false, Message: "创建文件失败: " + err.Error()})
		return
	}
	f.Close()

	conn.WriteJSON(WSMessage{
		Type:    "file_touch",
		Success: true,
		Data:    map[string]interface{}{"path": absPath},
	})
}

// handleFileRead 读取文件文本内容（用于在线编辑）
func handleFileRead(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_read", Success: false, Message: err.Error()})
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_read", Success: false, Message: "文件不存在: " + err.Error()})
		return
	}
	if info.IsDir() {
		conn.WriteJSON(WSMessage{Type: "file_read", Success: false, Message: "不能读取文件夹内容"})
		return
	}
	if info.Size() > fileMaxSize {
		conn.WriteJSON(WSMessage{Type: "file_read", Success: false, Message: "文件过大（超过 50MB 限制）"})
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_read", Success: false, Message: "读取文件失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_read",
		Success: true,
		Data: map[string]interface{}{
			"name":    filepath.Base(absPath),
			"path":    absPath,
			"size":    info.Size(),
			"content": string(data),
		},
	})
}

// handleFileWrite 写入文件文本内容（用于在线编辑保存）
func handleFileWrite(conn *websocket.Conn, req *FileRequest) {
	absPath, err := resolvePath(req.Path)
	if err != nil {
		conn.WriteJSON(WSMessage{Type: "file_write", Success: false, Message: err.Error()})
		return
	}

	if int64(len(req.Content)) > fileMaxSize {
		conn.WriteJSON(WSMessage{Type: "file_write", Success: false, Message: "内容过大（超过 50MB 限制）"})
		return
	}

	os.MkdirAll(filepath.Dir(absPath), 0755)
	if err := os.WriteFile(absPath, []byte(req.Content), 0644); err != nil {
		conn.WriteJSON(WSMessage{Type: "file_write", Success: false, Message: "写入文件失败: " + err.Error()})
		return
	}

	conn.WriteJSON(WSMessage{
		Type:    "file_write",
		Success: true,
		Data:    map[string]interface{}{"path": absPath, "size": len(req.Content)},
	})
}

func getSystemInfo() SystemInfo {
	systemInfoCache.Lock()
	defer systemInfoCache.Unlock()
	if !systemInfoCache.updated.IsZero() && time.Since(systemInfoCache.updated) < 15*time.Second {
		return systemInfoCache.value
	}
	info := SystemInfo{}
	info.OS = runtime.GOOS
	hostname, _ := os.Hostname()
	info.Hostname = hostname
	info.CpuCores = runtime.NumCPU()
	info.CpuUsage = getCPUUsage()
	info.MemTotal, info.MemUsed, info.MemPercent = getMemoryInfo()
	info.DiskTotal, info.DiskUsed, info.DiskPercent = getDiskInfo()
	uptime := getSystemUptime()
	info.Uptime = formatUptime(uptime)
	info.UptimeSeconds = int64(uptime.Seconds())
	info.ProcessCount = getProcessCount()
	systemInfoCache.value = info
	systemInfoCache.updated = time.Now()
	return info
}

func getCPUUsage() float64 {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			"Get-Counter '\\Processor(_Total)\\% Processor Time' | Select-Object -ExpandProperty CounterSamples | Select-Object -ExpandProperty CookedValue")
		out, err := cmd.Output()
		if err == nil {
			val, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
			if err == nil && val >= 0 {
				return val
			}
		}
	} else {
		data, err := os.ReadFile("/proc/loadavg")
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 1 {
				load, _ := strconv.ParseFloat(fields[0], 64)
				cpuPercent := (load / float64(runtime.NumCPU())) * 100
				if cpuPercent > 0 && cpuPercent <= 100 {
					return cpuPercent
				}
			}
		}
	}
	return float64(10 + time.Now().Unix()%20)
}

// ========== 内存信息 ==========
// Windows: 使用 PowerShell 获取 Win32_OperatingSystem
//   - TotalVisibleMemorySize 和 FreePhysicalMemory 均返回 KB
//
// Linux:   读取 /proc/meminfo
func getMemoryInfo() (total, used, percent float64) {
	log.Printf("📊 正在获取内存信息...")

	if runtime.GOOS == "windows" {
		// 方法1: PowerShell（推荐；wmic 在新版 Windows 中已弃用）
		// 一次调用同时获取 TotalVisibleMemorySize 和 FreePhysicalMemory（单位均为 KB）
		cmd := exec.Command("powershell", "-Command",
			"Get-CimInstance Win32_OperatingSystem | ForEach-Object { $_.TotalVisibleMemorySize; $_.FreePhysicalMemory }")
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Fields(strings.TrimSpace(string(out)))
			if len(lines) >= 2 {
				totalKB, err1 := strconv.ParseFloat(lines[0], 64)
				freeKB, err2 := strconv.ParseFloat(lines[1], 64)
				if err1 == nil && err2 == nil && totalKB > 0 {
					total = totalKB / 1024 / 1024 // KB → GB
					used = (totalKB - freeKB) / 1024 / 1024
					if used < 0 {
						used = 0
					}
					percent = (used / total) * 100
					log.Printf("📊 PowerShell 内存数据: 总计=%.2fGB, 已用=%.2fGB, 使用率=%.1f%%", total, used, percent)
					return total, used, percent
				}
			}
		}

		// 方法2: wmic 备用
		// 注意: wmic 输出列按字母顺序排列，即 "FreePhysicalMemory  TotalVisibleMemorySize"
		// 因此 fields[0] 是 FreePhysicalMemory，fields[1] 是 TotalVisibleMemorySize
		cmd = exec.Command("wmic", "OS", "get", "FreePhysicalMemory,TotalVisibleMemorySize")
		out, err = cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" || strings.Contains(line, "FreePhysicalMemory") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					freeKB, err1 := strconv.ParseFloat(fields[0], 64)
					totalKB, err2 := strconv.ParseFloat(fields[1], 64)
					if err1 == nil && err2 == nil && totalKB > 0 {
						total = totalKB / 1024 / 1024
						used = (totalKB - freeKB) / 1024 / 1024
						if used < 0 {
							used = 0
						}
						percent = (used / total) * 100
						log.Printf("📊 wmic 内存数据: 总计=%.2fGB, 已用=%.2fGB, 使用率=%.1f%%", total, used, percent)
						return total, used, percent
					}
				}
			}
		}
	} else {
		// Linux: 读取 /proc/meminfo
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			lines := strings.Split(string(data), "\n")
			var totalKB, availableKB float64
			for _, line := range lines {
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						totalKB, _ = strconv.ParseFloat(fields[1], 64)
					}
				}
				if strings.HasPrefix(line, "MemAvailable:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						availableKB, _ = strconv.ParseFloat(fields[1], 64)
					}
				}
			}
			if totalKB > 0 {
				total = totalKB / 1024 / 1024
				used = (totalKB - availableKB) / 1024 / 1024
				if used < 0 {
					used = 0
				}
				percent = (used / total) * 100
				return total, used, percent
			}
		}
	}

	// 如果所有方法都失败，使用模拟数据
	if total <= 0 {
		total = 16.0
	}
	used = 2.1 + float64(time.Now().Unix()%3)
	if used > total {
		used = total * 0.8
	}
	percent = (used / total) * 100
	log.Printf("📊 内存数据 (默认): 总计=%.2fGB, 已用=%.2fGB, 使用率=%.1f%%", total, used, percent)
	return total, used, percent
}

func getDiskInfo() (total, used, percent float64) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			"Get-PSDrive -Name C | Select-Object -ExpandProperty Used; Get-PSDrive -Name C | Select-Object -ExpandProperty Free")
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			var values []string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					values = append(values, line)
				}
			}
			if len(values) >= 2 {
				usedBytes, err1 := strconv.ParseFloat(values[0], 64)
				freeBytes, err2 := strconv.ParseFloat(values[1], 64)
				if err1 == nil && err2 == nil {
					used = usedBytes / 1024 / 1024 / 1024
					total = (usedBytes + freeBytes) / 1024 / 1024 / 1024
					if total > 0 {
						percent = (used / total) * 100
					}
					return
				}
			}
		}
	} else {
		cmd := exec.Command("df", "-k", "/")
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 4 {
					totalKB, err1 := strconv.ParseFloat(fields[1], 64)
					usedKB, err2 := strconv.ParseFloat(fields[2], 64)
					if err1 == nil && err2 == nil && totalKB > 0 {
						total = totalKB / 1024 / 1024
						used = usedKB / 1024 / 1024
						if used < 0 {
							used = 0
						}
						percent = (used / total) * 100
						return
					}
				}
			}
		}
	}
	if total <= 0 {
		total = 256.0
	}
	used = 128.0 + float64(time.Now().Unix()%20)
	if used > total {
		used = total * 0.7
	}
	percent = (used / total) * 100
	return
}

func getSystemUptime() time.Duration {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			"(Get-CimInstance Win32_OperatingSystem).LastBootUpTime")
		out, err := cmd.Output()
		if err == nil {
			bootTimeStr := strings.TrimSpace(string(out))
			if len(bootTimeStr) >= 14 {
				timeStr := bootTimeStr[:14]
				bootTime, err := time.Parse("20060102150405", timeStr)
				if err == nil {
					uptime := time.Since(bootTime)
					return uptime
				}
			}
		}
	} else {
		data, err := os.ReadFile("/proc/uptime")
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 0 {
				seconds, _ := strconv.ParseFloat(fields[0], 64)
				uptime := time.Duration(seconds) * time.Second
				return uptime
			}
		}
	}
	return 0
}

func getProcessCount() int {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command", "(Get-Process).Count")
		out, err := cmd.Output()
		if err == nil {
			count, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err == nil && count > 0 {
				return count
			}
		}
	} else {
		cmd := exec.Command("ps", "-e", "--no-headers")
		out, err := cmd.Output()
		if err == nil {
			return strings.Count(string(out), "\n")
		}
	}
	return 0
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d天 %d时 %d分", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d时 %d分", hours, minutes)
	}
	return fmt.Sprintf("%d分", minutes)
}
