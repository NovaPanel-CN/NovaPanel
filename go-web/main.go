package main

import (
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
	HTTP_PORT = 8080
	DAEMON_PORT = 8079
)

// ========== WebSocket 相关 ==========
var (
	clients   = make(map[*websocket.Conn]bool)
	clientsMu sync.RWMutex
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

var (
	lastModMap = make(map[string]time.Time)
	watcherMu  sync.Mutex
)

var projectRoot string

// ========== 系统信息结构 ==========
type SysInfo struct {
	OS           string  `json:"os"`
	OSVersion    string  `json:"osVersion"`
	Hostname     string  `json:"hostname"`
	CurrentUser  string  `json:"currentUser"`
	Uptime       string  `json:"uptime"`
	CpuUsage     float64 `json:"cpuUsage"`
	CpuCores     int     `json:"cpuCores"`
	MemTotal     float64 `json:"memTotal"`
	MemUsed      float64 `json:"memUsed"`
	MemPercent   float64 `json:"memPercent"`
	DiskTotal    float64 `json:"diskTotal"`
	DiskUsed     float64 `json:"diskUsed"`
	DiskPercent  float64 `json:"diskPercent"`
	NetSent      string  `json:"netSent"`
	NetRecv      string  `json:"netRecv"`
	ProcessCount int     `json:"processCount"`
	LastUpdate   string  `json:"lastUpdate"`
}

// ========== 节点数据结构 ==========
type Node struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	IP         string  `json:"ip"`
	Port       int     `json:"port"`
	Version    string  `json:"version"`
	Status     string  `json:"status"`
	CPU        float64 `json:"cpu"`
	MemUsed    float64 `json:"memUsed"`
	MemTotal   float64 `json:"memTotal"`
	MemPercent float64 `json:"memPercent"`
	Running    int     `json:"running"`
	Total      int     `json:"total"`
	LastUpdate string  `json:"lastUpdate"`
}

// ========== 服务器状态 ==========
type ServerState struct {
	mu          sync.RWMutex
	running     bool
	startTime   time.Time
	cmd         *exec.Cmd
	memoryUsage float64
}

var serverState = &ServerState{}

type StatusResponse struct {
	Running bool    `json:"running"`
	Memory  float64 `json:"memory"`
	Uptime  string  `json:"uptime"`
	Players int     `json:"players"`
}

type ActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ========== 节点列表 ==========
var nodes = []Node{
	{
		ID:         "node1",
		Name:       "主节点",
		IP:         "127.0.0.1",
		Port:       8078,
		Version:    "1.0.0",
		Status:     "unknown",
		CPU:        0,
		MemUsed:    0,
		MemTotal:   0,
		MemPercent: 0,
		Running:    0,
		Total:      0,
		LastUpdate: "",
	},
}

var nodesMu sync.RWMutex

// ========== 主函数 ==========

func main() {
	workDir, err := os.Getwd()
	if err != nil {
		log.Printf("⚠️ 获取工作目录失败: %v", err)
		workDir = "."
	}

	if filepath.Base(workDir) == "go-web" {
		projectRoot = filepath.Dir(workDir)
	} else {
		projectRoot = workDir
	}

	log.Printf("📂 项目根目录: %s", projectRoot)

	staticPath := filepath.Join(projectRoot, "go-web", "static")
	if _, err := os.Stat(staticPath); os.IsNotExist(err) {
		staticPath = filepath.Join(projectRoot, "static")
		if _, err := os.Stat(staticPath); os.IsNotExist(err) {
			log.Printf("⚠️ 找不到 static 目录！")
			staticPath = "./static"
		}
	}
	log.Printf("📂 静态文件目录: %s", staticPath)

	fs := http.FileServer(http.Dir(staticPath))
	http.Handle("/", fs)

	http.HandleFunc("/ws", handleWebSocket)

	// ===== 用户 API（代理到 Daemon） =====
	http.HandleFunc("/api/register", proxyToDaemon)
	http.HandleFunc("/api/login", proxyToDaemon)

	// ===== 系统 API =====
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/start", handleStart)
	http.HandleFunc("/api/stop", handleStop)
	http.HandleFunc("/api/logs", handleLogs)
	http.HandleFunc("/api/sysinfo", handleSysInfo)

	// ===== 节点管理 API =====
	http.HandleFunc("/api/nodes", handleNodes)
	http.HandleFunc("/api/node/add", handleNodeAdd)
	http.HandleFunc("/api/node/delete", handleNodeDelete)
	http.HandleFunc("/api/node/refresh", handleNodeRefresh)

	// ===== 健康检查 =====
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	go startFileWatcher()
	go startNodeMonitor()

	addr := fmt.Sprintf(":%d", HTTP_PORT)
	log.Printf("🚀 NovaPanel Web 启动于 http://127.0.0.1%s", addr)
	log.Printf("🔌 WebSocket: ws://127.0.0.1%s/ws", addr)
	log.Printf("🔗 Daemon 代理: http://127.0.0.1:%d", DAEMON_PORT)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("启动失败:", err)
	}
}

// ========== 代理到 Daemon ==========

func proxyToDaemon(w http.ResponseWriter, r *http.Request) {
	targetURL := fmt.Sprintf("http://127.0.0.1:%d%s", DAEMON_PORT, r.URL.Path)

	var body io.Reader
	if r.Body != nil {
		body = r.Body
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, body)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "代理请求失败"})
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "连接 Daemon 失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 复制响应
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ========== 服务器状态 API ==========

func handleStatus(w http.ResponseWriter, r *http.Request) {
	serverState.mu.RLock()
	defer serverState.mu.RUnlock()
	uptime := time.Since(serverState.startTime)
	resp := StatusResponse{
		Running: serverState.running,
		Memory:  serverState.memoryUsage,
		Uptime:  formatUptime(uptime),
		Players: 3,
	}
	sendJSON(w, resp)
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()

	if serverState.running {
		sendJSON(w, ActionResponse{Success: false, Message: "服务已在运行中"})
		return
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("ping", "127.0.0.1", "-t")
	} else {
		cmd = exec.Command("sleep", "3600")
	}

	if err := cmd.Start(); err != nil {
		sendJSON(w, ActionResponse{Success: false, Message: "启动失败: " + err.Error()})
		return
	}

	serverState.cmd = cmd
	serverState.running = true
	serverState.startTime = time.Now()
	go monitorMemory()

	log.Println("✅ 服务已启动 (PID:", cmd.Process.Pid, ")")
	sendJSON(w, ActionResponse{Success: true, Message: "服务启动成功"})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()

	if !serverState.running {
		sendJSON(w, ActionResponse{Success: false, Message: "服务未运行"})
		return
	}

	if serverState.cmd != nil && serverState.cmd.Process != nil {
		if err := serverState.cmd.Process.Kill(); err != nil {
			sendJSON(w, ActionResponse{Success: false, Message: "停止失败: " + err.Error()})
			return
		}
	}

	serverState.running = false
	serverState.cmd = nil
	serverState.memoryUsage = 0

	log.Println("⏹️ 服务已停止")
	sendJSON(w, ActionResponse{Success: true, Message: "服务已停止"})
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, map[string]interface{}{
		"logs": []string{
			"[14:32:01] 服务器启动",
			"[14:32:05] 玩家 Steve 加入游戏",
			"[14:35:12] 玩家 Alex 加入游戏",
			"[14:40:33] 服务器保存中...",
			"[14:45:20] 玩家 Herobrine 加入了游戏 😱",
		},
	})
}

// ========== 系统信息 API ==========

func handleSysInfo(w http.ResponseWriter, r *http.Request) {
	info := getSystemInfo()
	sendJSON(w, info)
}

// ========== 系统信息获取函数 ==========

func getSystemInfo() SysInfo {
	info := SysInfo{}

	info.OS = runtime.GOOS
	info.OSVersion = getOSVersion()

	hostname, _ := os.Hostname()
	info.Hostname = hostname

	info.CurrentUser = os.Getenv("USERNAME")
	if info.CurrentUser == "" {
		info.CurrentUser = os.Getenv("USER")
	}
	if info.CurrentUser == "" {
		info.CurrentUser = "未知"
	}

	info.CpuCores = runtime.NumCPU()
	info.CpuUsage = getCPUUsage()
	info.MemTotal, info.MemUsed, info.MemPercent = getMemoryInfo()
	info.DiskTotal, info.DiskUsed, info.DiskPercent = getDiskInfo()
	info.Uptime = getSystemUptime()
	info.ProcessCount = getProcessCount()

	info.NetSent = fmt.Sprintf("%.1f MB", float64(10+time.Now().Unix()%50))
	info.NetRecv = fmt.Sprintf("%.1f MB", float64(20+time.Now().Unix()%80))

	info.LastUpdate = time.Now().Format("2006-01-02 15:04:05")

	return info
}

func getOSVersion() string {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			"(Get-CimInstance Win32_OperatingSystem).Version")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return "未知"
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
	}
	return float64(10 + time.Now().Unix()%20)
}

func getMemoryInfo() (total, used, percent float64) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command",
			"Get-CimInstance Win32_ComputerSystem | Select-Object -ExpandProperty TotalPhysicalMemory")
		out, err := cmd.Output()
		if err == nil {
			totalBytes, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
			if err == nil && totalBytes > 0 {
				total = totalBytes / 1024 / 1024 / 1024
			}
		}
		cmd = exec.Command("powershell", "-Command",
			"Get-CimInstance Win32_OperatingSystem | Select-Object -ExpandProperty FreePhysicalMemory")
		out, err = cmd.Output()
		if err == nil {
			freeMB, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
			if err == nil && freeMB > 0 && total > 0 {
				used = total - (freeMB / 1024)
				if used < 0 {
					used = 0
				}
				percent = (used / total) * 100
				return
			}
		}
	} else {
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
				return
			}
		}
	}
	if total <= 0 {
		total = 16.0
	}
	used = 2.1 + float64(time.Now().Unix()%3)
	if used > total {
		used = total * 0.8
	}
	percent = (used / total) * 100
	return
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

func getSystemUptime() string {
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
					return formatUptimeSimple(uptime)
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
				return formatUptimeSimple(uptime)
			}
		}
	}
	return "0时 0分"
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

func formatUptimeSimple(d time.Duration) string {
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

// ========== 节点管理 API ==========

func handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodesMu.RLock()
	defer nodesMu.RUnlock()
	sendJSON(w, nodes)
}

func handleNodeAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}

	if req.IP == "" || req.Port == 0 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "IP 和端口不能为空"})
		return
	}

	nodesMu.Lock()
	defer nodesMu.Unlock()

	for _, n := range nodes {
		if n.IP == req.IP && n.Port == req.Port {
			sendJSON(w, map[string]interface{}{"success": false, "message": "节点已存在"})
			return
		}
	}

	name := req.Name
	if name == "" {
		name = fmt.Sprintf("%s:%d", req.IP, req.Port)
	}

	newNode := Node{
		ID:         fmt.Sprintf("node%d", len(nodes)+1),
		Name:       name,
		IP:         req.IP,
		Port:       req.Port,
		Version:    "1.0.0",
		Status:     "unknown",
		CPU:        0,
		MemUsed:    0,
		MemTotal:   0,
		MemPercent: 0,
		Running:    0,
		Total:      0,
		LastUpdate: "",
	}

	nodes = append(nodes, newNode)

	go connectToNode(newNode)

	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "节点添加成功",
		"node":    newNode,
	})
}

func handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}

	nodesMu.Lock()
	defer nodesMu.Unlock()

	found := false
	for i, n := range nodes {
		if n.ID == req.ID {
			nodes = append(nodes[:i], nodes[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		sendJSON(w, map[string]interface{}{"success": false, "message": "节点不存在"})
		return
	}

	sendJSON(w, map[string]interface{}{"success": true, "message": "节点已删除"})
}

func handleNodeRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}

	nodesMu.RLock()
	var targetNode *Node
	for i := range nodes {
		if nodes[i].ID == req.ID {
			targetNode = &nodes[i]
			break
		}
	}
	nodesMu.RUnlock()

	if targetNode == nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "节点不存在"})
		return
	}

	go connectToNode(*targetNode)

	sendJSON(w, map[string]interface{}{"success": true, "message": "刷新中..."})
}

// ========== 连接节点（修复并发写入） ==========

func connectToNode(node Node) {
	wsAddr := fmt.Sprintf("ws://%s:%d/ws", node.IP, node.Port)

	log.Printf("🔗 正在连接节点: %s (%s)", node.Name, wsAddr)

	conn, _, err := websocket.DefaultDialer.Dial(wsAddr, nil)
	if err != nil {
		log.Printf("⚠️ 连接节点失败: %v", err)
		updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
		return
	}
	defer conn.Close()

	log.Printf("✅ 已连接节点: %s", node.Name)
	updateNodeStatus(node.ID, "online", 0, 0, 0, 0, 0, 0)

	writeMutex := &sync.Mutex{}

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			writeMutex.Lock()
			err := conn.WriteJSON(map[string]string{"type": "ping"})
			writeMutex.Unlock()
			if err != nil {
				log.Printf("⚠️ Ping 失败: %v", err)
				return
			}
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		writeMutex.Lock()
		err := conn.WriteJSON(map[string]string{"type": "get_system"})
		writeMutex.Unlock()
		if err != nil {
			log.Printf("⚠️ 发送请求失败: %v", err)
			updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
			return
		}

		var resp map[string]interface{}
		if err := conn.ReadJSON(&resp); err != nil {
			log.Printf("⚠️ 读取响应失败: %v", err)
			updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
			return
		}

		if data, ok := resp["data"].(map[string]interface{}); ok {
			cpu, _ := data["cpuUsage"].(float64)
			memTotal, _ := data["memTotal"].(float64)
			memUsed, _ := data["memUsed"].(float64)
			memPercent, _ := data["memPercent"].(float64)

			if memTotal <= 0 {
				memTotal = 16.0
				memUsed = 2.1
				memPercent = 13.0
			}

			updateNodeStatus(node.ID, "online", cpu, memUsed, memTotal, memPercent, -1, -1)
		}

		writeMutex.Lock()
		err = conn.WriteJSON(map[string]string{"type": "get_instances"})
		writeMutex.Unlock()
		if err != nil {
			continue
		}

		var instResp map[string]interface{}
		if err := conn.ReadJSON(&instResp); err != nil {
			continue
		}

		if data, ok := instResp["data"].([]interface{}); ok {
			running := 0
			for _, item := range data {
				if inst, ok := item.(map[string]interface{}); ok {
					if inst["status"] == "running" {
						running++
					}
				}
			}
			updateNodeInstances(node.ID, running, len(data))
		}
	}
}

func updateNodeStatus(id string, status string, cpu, memUsed, memTotal, memPercent float64, running, total int) {
	nodesMu.Lock()
	defer nodesMu.Unlock()

	for i, n := range nodes {
		if n.ID == id {
			nodes[i].Status = status
			if status == "online" {
				nodes[i].CPU = cpu
				nodes[i].MemUsed = memUsed
				nodes[i].MemTotal = memTotal
				nodes[i].MemPercent = memPercent
				nodes[i].Version = "1.0.0"
			} else {
				nodes[i].CPU = 0
				nodes[i].MemUsed = 0
				nodes[i].MemPercent = 0
			}
			if running >= 0 {
				nodes[i].Running = running
			}
			if total >= 0 {
				nodes[i].Total = total
			}
			nodes[i].LastUpdate = time.Now().Format("2006-01-02 15:04:05")
			break
		}
	}
}

func updateNodeInstances(id string, running, total int) {
	nodesMu.Lock()
	defer nodesMu.Unlock()

	for i, n := range nodes {
		if n.ID == id {
			nodes[i].Running = running
			nodes[i].Total = total
			break
		}
	}
}

// ========== 节点监控 ==========

func startNodeMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		nodesMu.RLock()
		nodeList := make([]Node, len(nodes))
		copy(nodeList, nodes)
		nodesMu.RUnlock()

		for _, node := range nodeList {
			if node.Status == "unknown" || node.Status == "offline" {
				go connectToNode(node)
			}
		}
	}
}

// ========== 辅助函数 ==========

func monitorMemory() {
	for {
		if !serverState.running {
			break
		}
		serverState.mu.Lock()
		serverState.memoryUsage = float64(2 + time.Now().Unix()%3)
		serverState.mu.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func formatUptime(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ========== WebSocket 热重载 ==========

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	clientsMu.Lock()
	clients[conn] = true
	clientsMu.Unlock()

	log.Printf("📱 浏览器已连接 (当前连接数: %d)", len(clients))

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}

	clientsMu.Lock()
	delete(clients, conn)
	clientsMu.Unlock()
	log.Printf("📱 浏览器断开连接 (当前连接数: %d)", len(clients))
}

func notifyReload() {
	clientsMu.RLock()
	defer clientsMu.RUnlock()

	if len(clients) == 0 {
		return
	}

	msg := []byte(`{"command":"reload"}`)
	for conn := range clients {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("⚠️ 通知刷新失败: %v", err)
		}
	}
	log.Printf("🔄 已通知 %d 个浏览器刷新", len(clients))
}

func startFileWatcher() {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		changed := false
		staticDir := filepath.Join(projectRoot, "go-web", "static")

		_ = filepath.Walk(staticDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if ext != ".html" && ext != ".css" && ext != ".js" {
				return nil
			}

			modTime := info.ModTime()
			watcherMu.Lock()
			lastMod, exists := lastModMap[path]
			if exists && modTime.After(lastMod) {
				changed = true
				log.Printf("📝 文件变化: %s", path)
			}
			lastModMap[path] = modTime
			watcherMu.Unlock()
			return nil
		})

		if changed {
			notifyReload()
		}
	}
}