package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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

	_ "image/gif"

	"github.com/gorilla/websocket"
)

const (
	HTTP_PORT = 8080
	WEB_VER   = "1.0.0"
)

var projectRoot string

// ========== 用户数据结构 ==========
type User struct {
	UUID         string `json:"uuid"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Permission   int    `json:"permission"` // -1=禁封 1=普通用户 10=管理员
	Avatar       string `json:"avatar"`     // 头像文件名（如 admin.png）
	Email        string `json:"email"`      // 邮箱
	Bio          string `json:"bio"`        // 个人描述
	CreatedAt    string `json:"createdAt"`
	LastLogin    string `json:"lastLogin"`
	TwoFASecret  string `json:"twoFASecret"`  // 2FA 密钥（Base32）
	TwoFAEnabled bool   `json:"twoFAEnabled"` // 是否开启 2FA
}

type UsersDB struct {
	Users map[string]User `json:"users"`
	mu    sync.RWMutex
}

var userDB = &UsersDB{
	Users: make(map[string]User),
}

// generateUUID 生成 UUID v4（MCSManager 风格：32 位无连字符）
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x", b)
}

// getAvatarURL 根据头像文件名生成完整 URL
func getAvatarURL(avatar string) string {
	if avatar != "" {
		return "/avatars/" + avatar
	}
	return ""
}

// permissionName 权限等级转文字
func permissionName(p int) string {
	switch p {
	case -1:
		return "禁封"
	case 10:
		return "管理员"
	case 1:
		return "普通用户"
	default:
		return "普通用户"
	}
}

// ========== 系统信息结构 ==========
type SysInfo struct {
	OS             string  `json:"os"`
	OSVersion      string  `json:"osVersion"`
	Hostname       string  `json:"hostname"`
	CurrentUser    string  `json:"currentUser"`
	Uptime         string  `json:"uptime"`
	UptimeSeconds  int64   `json:"uptimeSeconds"`
	CpuUsage       float64 `json:"cpuUsage"`
	CpuCores       int     `json:"cpuCores"`
	MemTotal       float64 `json:"memTotal"`
	MemUsed        float64 `json:"memUsed"`
	MemPercent     float64 `json:"memPercent"`
	DiskTotal      float64 `json:"diskTotal"`
	DiskUsed       float64 `json:"diskUsed"`
	DiskPercent    float64 `json:"diskPercent"`
	NetSent        string  `json:"netSent"`
	NetRecv        string  `json:"netRecv"`
	ProcessCount   int     `json:"processCount"`
	LastUpdate     string  `json:"lastUpdate"`
	LastUpdateUnix int64   `json:"lastUpdateUnix"`
}

var sysInfoCache struct {
	sync.Mutex
	value   SysInfo
	updated time.Time
}

// ========== 节点数据结构 ==========
type Node struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	IP         string    `json:"ip"`
	Port       int       `json:"port"`
	Type       string    `json:"type"`   // "novapanel" 或 "mcsmanager"
	APIKey     string    `json:"apiKey"` // MCSManager 的验证密钥
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	Platform   string    `json:"platform"` // 操作系统平台
	DaemonID   string    `json:"daemonId"` // Daemon ID
	CPU        float64   `json:"cpu"`
	MemUsed    float64   `json:"memUsed"`
	MemTotal   float64   `json:"memTotal"`
	MemPercent float64   `json:"memPercent"`
	Running    int       `json:"running"`
	Total      int       `json:"total"`
	LastUpdate string    `json:"lastUpdate"`
	CPUHistory []float64 `json:"cpuHistory"` // CPU 10分钟历史（60个点，每10秒一个）
	MemHistory []float64 `json:"memHistory"` // 内存 10分钟历史
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

func startupText(key string) string {
	lang := os.Getenv("NOVAPANEL_LANG")
	texts := map[string][3]string{
		"started": {"Web panel started successfully", "控制面板端已启动", "控制面板端已啟動"},
		"address": {"Access URL", "访问地址", "存取位址"},
		"close":   {"Press Ctrl+C to stop", "关闭此程序请使用 Ctrl+C 快捷键", "關閉此程式請使用 Ctrl+C 快捷鍵"},
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

// ========== 节点列表 ==========
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

var nodes = []Node{}
var nodesMu sync.RWMutex

// ========== 节点数据持久化 ==========
func getNodesDataPath() string {
	return filepath.Join(projectRoot, "go-daemon", "data", "nodes.json")
}

func loadNodesData() {
	nodesMu.Lock()
	defer nodesMu.Unlock()

	path := getNodesDataPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("📝 节点数据文件不存在，将创建新文件")
			nodes = []Node{}
			saveNodesDataLocked()
			return
		}
		log.Printf("⚠️ 读取节点数据失败: %v", err)
		return
	}

	var loaded []Node
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("⚠️ 解析节点数据失败: %v", err)
		return
	}
	nodes = loaded
	log.Printf("✅ 加载了 %d 个节点", len(nodes))

	// 自动连接所有已保存的节点
	for i := range nodes {
		if nodes[i].Type == "mcsmanager" {
			go func(n Node) {
				// AddMCSMNode 异步连接，立即返回
				client, _ := AddMCSMNode(n.ID, n.IP, n.Port, n.APIKey)
				// 状态保持 connecting，由 startMCSMStatusSync 在连接成功后更新为 online
				if client != nil {
					go startMCSMStatusSync(client, n.ID)
				}
			}(nodes[i])
		} else {
			// NovaPanel 节点
			go connectToNode(nodes[i])
		}
	}
}

// startMCSMStatusSync 启动 MCSManager 节点状态同步循环
// 每 5 秒从 MCSM 客户端获取最新信息并更新到 nodes 数组
func startMCSMStatusSync(c *MCSMClient, nodeID string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		info := c.GetInfo()
		nodesMu.Lock()
		for i := range nodes {
			if nodes[i].ID == nodeID {
				if info.Available {
					nodes[i].Status = "online"
					nodes[i].Version = info.Version
					nodes[i].CPU = info.CPUUsage
					nodes[i].MemUsed = info.MemUsed
					nodes[i].MemTotal = info.MemTotal
					nodes[i].MemPercent = info.MemUsage
					nodes[i].Running = info.Running
					nodes[i].Total = info.Total
					nodes[i].Platform = info.Platform
					if nodes[i].DaemonID == "" {
						nodes[i].DaemonID = nodeID
					}
					// 记录历史数据（最多 60 个点，10 分钟）
					if nodes[i].CPUHistory == nil {
						nodes[i].CPUHistory = []float64{}
					}
					nodes[i].CPUHistory = append(nodes[i].CPUHistory, info.CPUUsage)
					if len(nodes[i].CPUHistory) > 60 {
						nodes[i].CPUHistory = nodes[i].CPUHistory[1:]
					}
					if nodes[i].MemHistory == nil {
						nodes[i].MemHistory = []float64{}
					}
					nodes[i].MemHistory = append(nodes[i].MemHistory, info.MemUsage)
					if len(nodes[i].MemHistory) > 60 {
						nodes[i].MemHistory = nodes[i].MemHistory[1:]
					}
					nodes[i].LastUpdate = time.Now().Format("2006-01-02 15:04:05")
				} else {
					nodes[i].Status = "connecting"
				}
				break
			}
		}
		nodesMu.Unlock()
	}
}

func saveNodesDataLocked() {
	path := getNodesDataPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(nodes, "", "  ")
	if err != nil {
		log.Printf("⚠️ 序列化节点数据失败: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("⚠️ 保存节点数据失败: %v", err)
		return
	}
}

// ========== WebSocket 相关 ==========
var (
	clients   = make(map[*websocket.Conn]bool)
	clientsMu sync.RWMutex
	upgrader  = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

var (
	lastModMap = make(map[string]time.Time)
	watcherMu  sync.Mutex
)

// ========== 用户数据持久化 ==========
func getUserDataPath() string {
	if projectRoot != "" {
		return filepath.Join(projectRoot, "go-daemon", "data", "users.json")
	}
	// 兜底：尝试向上查找
	dir, err := os.Getwd()
	if err != nil {
		return "./go-daemon/data/users.json"
	}
	if filepath.Base(dir) == "go-web" {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, "go-daemon", "data", "users.json")
}

// ========== 全局配置（含 MCSManager 风格 dataKey） ==========
type GlobalConfig struct {
	Version   string `json:"version"`
	DataKey   string `json:"dataKey"`
	CreatedAt string `json:"createdAt"`
}

var (
	globalConfig   = &GlobalConfig{}
	globalConfigMu sync.RWMutex
)

func getGlobalConfigPath() string {
	if projectRoot != "" {
		return filepath.Join(projectRoot, "go-daemon", "data", "config", "global.json")
	}
	return "./go-daemon/data/config/global.json"
}

// generateDataKey 生成 MCSManager 风格密钥（24 字节随机数 → 48 位 hex）
func generateDataKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x%x", time.Now().UnixNano(), time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

func loadGlobalConfig() {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()
	path := getGlobalConfigPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 首次启动：立即生成 dataKey 并保存
			globalConfig.DataKey = generateDataKey()
			globalConfig.Version = "1.0.0"
			globalConfig.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
			saveGlobalConfigLocked()
			log.Printf("� 首次启动，已生成全局 dataKey: %s", globalConfig.DataKey)
			return
		}
		log.Printf("⚠️ 读取全局配置失败: %v", err)
		return
	}
	if err := json.Unmarshal(data, globalConfig); err != nil {
		log.Printf("⚠️ 解析全局配置失败: %v", err)
		return
	}
	// 兜底：如果文件存在但没有 dataKey（旧版本或异常），立即补一个
	if globalConfig.DataKey == "" {
		globalConfig.DataKey = generateDataKey()
		if globalConfig.Version == "" {
			globalConfig.Version = "1.0.0"
		}
		if globalConfig.CreatedAt == "" {
			globalConfig.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
		}
		saveGlobalConfigLocked()
		log.Printf("🔑 检测到缺失 dataKey，已补生成: %s", globalConfig.DataKey)
	} else {
		log.Printf("✅ 加载全局配置，dataKey: %s", globalConfig.DataKey)
	}
}

func saveGlobalConfigLocked() {
	path := getGlobalConfigPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(globalConfig, "", " ")
	if err != nil {
		log.Printf("⚠️ 序列化全局配置失败: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("⚠️ 保存全局配置失败: %v", err)
		return
	}
	log.Printf("💾 全局配置已保存到 %s", path)
}

// ========== 面板设置（持久化） ==========
type PanelSettings struct {
	PanelName         string `json:"panelName"`
	Port              int    `json:"port"`
	SessionTimeout    int    `json:"sessionTimeout"`
	Debug             bool   `json:"debug"`
	HotReload         bool   `json:"hotReload"`
	CrossDomain       bool   `json:"crossDomain"`
	Gzip              bool   `json:"gzip"`
	PageSize          int    `json:"pageSize"`
	MaxLoginAttempts  int    `json:"maxLoginAttempts"`
	BanDuration       int    `json:"banDuration"`
	AllowFileManager  bool   `json:"allowFileManager"`
	LoginLimitEnabled bool   `json:"loginLimitEnabled"`
}

var (
	panelSettings   = &PanelSettings{}
	panelSettingsMu sync.RWMutex
)

func getSettingsPath() string {
	if projectRoot != "" {
		return filepath.Join(projectRoot, "go-daemon", "data", "config", "settings.json")
	}
	return "./go-daemon/data/config/settings.json"
}

func loadPanelSettings() {
	panelSettingsMu.Lock()
	defer panelSettingsMu.Unlock()
	path := getSettingsPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 首次启动：写入默认设置
			panelSettings.PanelName = "Velunex Panel"
			panelSettings.Port = HTTP_PORT
			panelSettings.SessionTimeout = 10
			panelSettings.Debug = false
			panelSettings.HotReload = true
			panelSettings.CrossDomain = false
			panelSettings.Gzip = true
			panelSettings.PageSize = 10
			panelSettings.MaxLoginAttempts = 5
			panelSettings.BanDuration = 20
			panelSettings.AllowFileManager = true
			panelSettings.LoginLimitEnabled = true
			savePanelSettingsLocked()
			log.Printf("📝 首次启动，已生成默认面板设置")
			return
		}
		log.Printf("⚠️ 读取面板设置失败: %v", err)
		return
	}
	if err := json.Unmarshal(data, panelSettings); err != nil {
		log.Printf("⚠️ 解析面板设置失败: %v", err)
		return
	}
	// 兜底默认值
	if panelSettings.PanelName == "" {
		panelSettings.PanelName = "Velunex Panel"
	}
	if panelSettings.Port == 0 {
		panelSettings.Port = HTTP_PORT
	}
	if panelSettings.SessionTimeout == 0 {
		panelSettings.SessionTimeout = 10
	}
	if panelSettings.MaxLoginAttempts == 0 {
		panelSettings.MaxLoginAttempts = 5
	}
	if panelSettings.BanDuration == 0 {
		panelSettings.BanDuration = 20
	}
	if panelSettings.PageSize == 0 {
		panelSettings.PageSize = 10
	}
	// AllowFileManager 和 LoginLimitEnabled 默认为 true（零值为 false，需要特殊处理）
	// 通过检查一个标记字段来判断是否是旧配置
	log.Printf("✅ 加载面板设置: %s (端口 %d)", panelSettings.PanelName, panelSettings.Port)
}

func savePanelSettingsLocked() {
	path := getSettingsPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(panelSettings, "", "  ")
	if err != nil {
		log.Printf("⚠️ 序列化面板设置失败: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("⚠️ 保存面板设置失败: %v", err)
		return
	}
	log.Printf("💾 面板设置已保存到 %s", path)
}

func loadUserData() {
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	path := getUserDataPath()
	log.Printf("📂 用户数据路径: %s", path)
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("📝 用户数据文件不存在，将创建新文件")
			userDB.Users = make(map[string]User)
			saveUserDataLocked()
			return
		}
		log.Printf("⚠️ 读取用户数据失败: %v", err)
		userDB.Users = make(map[string]User)
		return
	}
	var loaded UsersDB
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("⚠️ 解析用户数据失败: %v", err)
		userDB.Users = make(map[string]User)
		return
	}
	if loaded.Users == nil {
		loaded.Users = make(map[string]User)
	}
	// 兼容旧数据：为已有用户补充 UUID/Permission
	changed := false
	for name, u := range loaded.Users {
		if u.UUID == "" {
			u.UUID = generateUUID()
			changed = true
		}
		if u.Permission == 0 {
			// 第一个用户默认管理员
			if len(loaded.Users) == 1 {
				u.Permission = 10
			} else {
				u.Permission = 1
			}
			changed = true
		}
		// 旧的三档权限合并为两档：管理员(2) → 管理员(10)
		if u.Permission == 2 {
			u.Permission = 10
			changed = true
		}
		loaded.Users[name] = u
	}
	userDB.Users = loaded.Users
	log.Printf("✅ 加载了 %d 个用户", len(userDB.Users))
	if changed {
		saveUserDataLocked()
	}
}

func saveUserDataLocked() {
	path := getUserDataPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(userDB, "", " ")
	if err != nil {
		log.Printf("⚠️ 序列化用户数据失败: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("⚠️ 保存用户数据失败: %v", err)
		return
	}
	log.Printf("💾 用户数据已保存到 %s", path)
}

// ========== 用户 API ==========
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.Username == "" || req.Password == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号和密码不能为空"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	if _, exists := userDB.Users[req.Username]; exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号已存在"})
		return
	}
	// 第一个用户为管理员，其余为普通用户
	permission := 1
	ifFirstInstall := false
	if len(userDB.Users) == 0 {
		permission = 10
		ifFirstInstall = true
	}
	uuid := generateUUID()
	userDB.Users[req.Username] = User{
		UUID:       uuid,
		Username:   req.Username,
		Password:   req.Password,
		Permission: permission,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		LastLogin:  "",
	}
	saveUserDataLocked()
	// 首次安装：生成全局 dataKey（仅一次）
	if ifFirstInstall {
		globalConfigMu.Lock()
		if globalConfig.DataKey == "" {
			globalConfig.DataKey = generateDataKey()
			globalConfig.Version = "1.0.0"
			globalConfig.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
			saveGlobalConfigLocked()
			log.Printf("🔑 首次安装，已生成全局 dataKey: %s", globalConfig.DataKey)
		}
		globalConfigMu.Unlock()
	}
	log.Printf("✅ 新用户注册: %s (权限: %s, UUID: %s)", req.Username, permissionName(permission), uuid)
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "注册成功",
		"user": map[string]string{
			"username":  req.Username,
			"createdAt": userDB.Users[req.Username].CreatedAt,
		},
	})
}

// ========== IP 登录失败锁定 ==========
type ipLoginRecord struct {
	FailCount int
	LastFail  time.Time
	BanUntil  time.Time
}

var (
	ipLoginRecords   = make(map[string]*ipLoginRecord)
	ipLoginRecordsMu sync.Mutex
)

// getClientIP 获取客户端真实 IP（兼容反向代理）
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}

// isIPBanned 检查 IP 是否被锁定，返回是否锁定及剩余时间
func isIPBanned(ip string) (bool, time.Duration) {
	ipLoginRecordsMu.Lock()
	defer ipLoginRecordsMu.Unlock()
	record, exists := ipLoginRecords[ip]
	if !exists {
		return false, 0
	}
	if time.Now().Before(record.BanUntil) {
		return true, record.BanUntil.Sub(time.Now())
	}
	if !record.BanUntil.IsZero() {
		delete(ipLoginRecords, ip)
	}
	return false, 0
}

// recordLoginFailure 记录登录失败，返回是否触发锁定及当前失败次数
func recordLoginFailure(ip string) (bool, int) {
	ipLoginRecordsMu.Lock()
	defer ipLoginRecordsMu.Unlock()
	panelSettingsMu.RLock()
	maxAttempts := panelSettings.MaxLoginAttempts
	banDuration := panelSettings.BanDuration
	panelSettingsMu.RUnlock()
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if banDuration <= 0 {
		banDuration = 20
	}
	record, exists := ipLoginRecords[ip]
	if !exists {
		record = &ipLoginRecord{}
		ipLoginRecords[ip] = record
	}
	// 锁定过期后重置计数
	if !record.BanUntil.IsZero() && time.Now().After(record.BanUntil) {
		record.FailCount = 0
		record.BanUntil = time.Time{}
	}
	record.FailCount++
	record.LastFail = time.Now()
	if record.FailCount >= maxAttempts {
		record.BanUntil = time.Now().Add(time.Duration(banDuration) * time.Minute)
		log.Printf("🔒 IP %s 登录失败 %d 次，已锁定 %d 分钟", ip, record.FailCount, banDuration)
		return true, record.FailCount
	}
	return false, record.FailCount
}

// clearLoginFailure 登录成功时清除失败记录
func clearLoginFailure(ip string) {
	ipLoginRecordsMu.Lock()
	defer ipLoginRecordsMu.Unlock()
	delete(ipLoginRecords, ip)
}

// loginFailResponse 记录失败并生成响应消息
func loginFailResponse(clientIP, reason string) map[string]interface{} {
	panelSettingsMu.RLock()
	loginLimitEnabled := panelSettings.LoginLimitEnabled
	maxAttempts := panelSettings.MaxLoginAttempts
	banDuration := panelSettings.BanDuration
	panelSettingsMu.RUnlock()
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if banDuration <= 0 {
		banDuration = 20
	}
	// 如果登录限制未启用，直接返回失败消息
	if !loginLimitEnabled {
		return map[string]interface{}{
			"success": false,
			"message": reason,
		}
	}
	banned, count := recordLoginFailure(clientIP)
	if banned {
		return map[string]interface{}{
			"success": false,
			"message": fmt.Sprintf("%s次数过多（已达 %d 次），该 IP 已被锁定 %d 分钟", reason, maxAttempts, banDuration),
		}
	}
	remaining := maxAttempts - count
	return map[string]interface{}{
		"success": false,
		"message": fmt.Sprintf("%s，剩余尝试次数 %d 次", reason, remaining),
	}
}

// ========== TOTP 2FA 算法（RFC 6238） ==========
func generateTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return base32.StdEncoding.EncodeToString(b)
}

func generateTOTPCode(secret string, timestamp int64) (string, error) {
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return "", err
	}
	counter := timestamp / 30
	buf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		buf[i] = byte(counter & 0xFF)
		counter >>= 8
	}
	h := hmac.New(sha1.New, key)
	h.Write(buf)
	hash := h.Sum(nil)
	offset := int(hash[len(hash)-1]) & 0x0F
	code := ((int(hash[offset]) & 0x7F) << 24) |
		(int(hash[offset+1])&0xFF)<<16 |
		(int(hash[offset+2])&0xFF)<<8 |
		(int(hash[offset+3]) & 0xFF)
	return fmt.Sprintf("%06d", code%1000000), nil
}

func verifyTOTPCode(secret, code string) bool {
	now := time.Now().Unix()
	for _, offset := range []int64{-1, 0, 1} {
		c, err := generateTOTPCode(secret, now+offset*30)
		if err == nil && c == code {
			return true
		}
	}
	return false
}

func generateOTPAuthURL(secret, account string) string {
	return fmt.Sprintf("otpauth://totp/Velunex%%20Panel:%s?secret=%s&issuer=Velunex%%20Panel", account, secret)
}

// 2FA 登录临时令牌
var (
	twoFATempTokens   = make(map[string]string)
	twoFATempTokensMu sync.Mutex
)

func generate2FATempToken(username string) string {
	b := make([]byte, 16)
	rand.Read(b)
	token := fmt.Sprintf("%x", b)
	twoFATempTokensMu.Lock()
	twoFATempTokens[token] = username
	twoFATempTokensMu.Unlock()
	return token
}

func consume2FATempToken(token string) (string, bool) {
	twoFATempTokensMu.Lock()
	defer twoFATempTokensMu.Unlock()
	username, exists := twoFATempTokens[token]
	if exists {
		delete(twoFATempTokens, token)
	}
	return username, exists
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 检查 IP 是否被锁定（仅当登录限制启用时）
	clientIP := getClientIP(r)
	panelSettingsMu.RLock()
	loginLimitEnabled := panelSettings.LoginLimitEnabled
	panelSettingsMu.RUnlock()
	if loginLimitEnabled {
		if banned, remaining := isIPBanned(clientIP); banned {
			minutes := int(remaining.Minutes())
			seconds := int(remaining.Seconds()) % 60
			var msg string
			if minutes > 0 {
				msg = fmt.Sprintf("该 IP 已被临时锁定，请 %d 分 %d 秒后再试", minutes, seconds)
			} else {
				msg = fmt.Sprintf("该 IP 已被临时锁定，请 %d 秒后再试", seconds)
			}
			sendJSON(w, map[string]interface{}{"success": false, "message": msg})
			return
		}
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[req.Username]
	if !exists {
		sendJSON(w, loginFailResponse(clientIP, "账号不存在"))
		return
	}
	if user.Permission == -1 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "您的账号已被禁封，请联系管理员"})
		return
	}
	if user.Password != req.Password {
		sendJSON(w, loginFailResponse(clientIP, "密码错误"))
		return
	}
	// 登录成功，清除该 IP 的失败记录
	clearLoginFailure(clientIP)
	// 检查是否开启 2FA
	if user.TwoFAEnabled && user.TwoFASecret != "" {
		tempToken := generate2FATempToken(req.Username)
		sendJSON(w, map[string]interface{}{
			"success":   false,
			"need2FA":   true,
			"tempToken": tempToken,
			"message":   "请输入 2FA 动态验证码",
		})
		return
	}
	// 更新最后登录时间
	user.LastLogin = time.Now().Format("2006-01-02 15:04:05")
	userDB.Users[req.Username] = user
	saveUserDataLocked()
	log.Printf("✅ 用户登录: %s", req.Username)
	// 设置 cookie，10 分钟过期
	expire := time.Now().Add(10 * time.Minute)
	http.SetCookie(w, &http.Cookie{
		Name:     "novapanel_session",
		Value:    req.Username,
		Expires:  expire,
		Path:     "/",
		HttpOnly: true,
	})
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "登录成功",
		"user": map[string]interface{}{
			"username":   req.Username,
			"uuid":       user.UUID,
			"avatar":     getAvatarURL(user.Avatar),
			"email":      user.Email,
			"bio":        user.Bio,
			"permission": user.Permission,
			"createdAt":  user.CreatedAt,
			"lastLogin":  user.LastLogin,
		},
	})
}

// ========== 安装状态检查 API ==========
// 返回是否已存在用户（用于前端决定显示安装引导还是登录页）
func handleInstallStatus(w http.ResponseWriter, r *http.Request) {
	userDB.mu.RLock()
	defer userDB.mu.RUnlock()
	sendJSON(w, map[string]interface{}{
		"success":  true,
		"hasAdmin": len(userDB.Users) > 0,
	})
}

// ========== 会话检查 API ==========
func handleCheckSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 检查用户是否仍然存在
	userDB.mu.RLock()
	defer userDB.mu.RUnlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	// 续期 cookie（再延长 10 分钟）
	http.SetCookie(w, &http.Cookie{
		Name:     "novapanel_session",
		Value:    cookie.Value,
		Expires:  time.Now().Add(10 * time.Minute),
		Path:     "/",
		HttpOnly: true,
	})
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "会话有效",
		"user": map[string]interface{}{
			"username":   cookie.Value,
			"uuid":       user.UUID,
			"avatar":     getAvatarURL(user.Avatar),
			"email":      user.Email,
			"bio":        user.Bio,
			"permission": user.Permission,
			"createdAt":  user.CreatedAt,
			"lastLogin":  user.LastLogin,
		},
	})
}

// ========== 登出 API ==========
func handleLogout(w http.ResponseWriter, r *http.Request) {
	// 清除 cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "novapanel_session",
		Value:    "",
		Expires:  time.Unix(0, 0),
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	sendJSON(w, map[string]interface{}{"success": true, "message": "已登出"})
}

// ========== 用户管理 API ==========

// UserPublicInfo 返回给前端的用户信息（不含密码）
type UserPublicInfo struct {
	UUID         string `json:"uuid"`
	Username     string `json:"username"`
	Permission   int    `json:"permission"`
	PermName     string `json:"permName"`
	CreatedAt    string `json:"createdAt"`
	LastLogin    string `json:"lastLogin"`
	TwoFAEnabled bool   `json:"twoFAEnabled"`
	Avatar       string `json:"avatar"`
}

// handleUserList 获取用户列表
func handleUserList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userDB.mu.RLock()
	defer userDB.mu.RUnlock()
	var list []UserPublicInfo
	for _, u := range userDB.Users {
		list = append(list, UserPublicInfo{
			UUID:         u.UUID,
			Username:     u.Username,
			Permission:   u.Permission,
			PermName:     permissionName(u.Permission),
			CreatedAt:    u.CreatedAt,
			LastLogin:    u.LastLogin,
			TwoFAEnabled: u.TwoFAEnabled,
			Avatar:       getAvatarURL(u.Avatar),
		})
	}
	if list == nil {
		list = []UserPublicInfo{}
	}
	sendJSON(w, map[string]interface{}{
		"success": true,
		"data":    list,
	})
}

// handleUserCreate 新建用户（管理员创建）
func handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Permission int    `json:"permission"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.Username == "" || req.Password == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号和密码不能为空"})
		return
	}
	if req.Permission != -1 && req.Permission != 1 && req.Permission != 10 {
		req.Permission = 1
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	if _, exists := userDB.Users[req.Username]; exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号已存在"})
		return
	}
	uuid := generateUUID()
	userDB.Users[req.Username] = User{
		UUID:       uuid,
		Username:   req.Username,
		Password:   req.Password,
		Permission: req.Permission,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		LastLogin:  "",
	}
	saveUserDataLocked()
	log.Printf("✅ 管理员创建用户: %s (权限: %s)", req.Username, permissionName(req.Permission))
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "用户创建成功",
	})
}

// handleUserDelete 删除用户
func handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	u, exists := userDB.Users[req.Username]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if u.Permission == 10 {
		// 不允许删除管理员（防止锁死）
		sendJSON(w, map[string]interface{}{"success": false, "message": "不允许删除管理员"})
		return
	}
	delete(userDB.Users, req.Username)
	saveUserDataLocked()
	log.Printf("🗑️ 删除用户: %s", req.Username)
	sendJSON(w, map[string]interface{}{"success": true, "message": "用户已删除"})
}

// handleUserUpdate 更新用户（密码/权限）
func handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		Permission int    `json:"permission"`
		UpdatePass bool   `json:"updatePass"`
		UpdatePerm bool   `json:"updatePerm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	u, exists := userDB.Users[req.Username]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if req.UpdatePass && req.Password != "" {
		u.Password = req.Password
	}
	if req.UpdatePerm {
		if req.Permission != -1 && req.Permission != 1 && req.Permission != 10 {
			sendJSON(w, map[string]interface{}{"success": false, "message": "权限等级无效"})
			return
		}
		u.Permission = req.Permission
	}
	userDB.Users[req.Username] = u
	saveUserDataLocked()
	log.Printf("✏️ 更新用户: %s", req.Username)
	sendJSON(w, map[string]interface{}{"success": true, "message": "用户已更新"})
}

// ========== 获取当前用户信息 API ==========
func handleUserInfo(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	defer userDB.mu.RUnlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	// 计算节点数
	nodesMu.RLock()
	nodeCount := len(nodes)
	nodesMu.RUnlock()

	var avatarURL string
	if user.Avatar != "" {
		avatarURL = "/avatars/" + user.Avatar
	}
	roleName := "普通用户"
	if user.Permission == 10 {
		roleName = "管理员"
	} else if user.Permission == -1 {
		roleName = "已禁封"
	}
	statusName := "正常"
	if user.Permission == -1 {
		statusName = "已禁封"
	}
	sendJSON(w, map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"username":   user.Username,
			"uuid":       user.UUID,
			"avatar":     avatarURL,
			"email":      user.Email,
			"bio":        user.Bio,
			"permission": user.Permission,
			"role":       roleName,
			"status":     statusName,
			"createdAt":  user.CreatedAt,
			"lastLogin":  user.LastLogin,
			"nodeCount":  nodeCount,
		},
	})
}

// ========== 上传头像 API ==========
func handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	username := cookie.Value
	// 限制上传大小 5MB
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	file, header, err := r.FormFile("avatar")
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "读取文件失败: " + err.Error()})
		return
	}
	defer file.Close()
	// 验证文件类型
	contentType := header.Header.Get("Content-Type")
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "仅支持 PNG、JPG、GIF 格式"})
		return
	}
	// 确保目录存在
	avatarDir := filepath.Join(projectRoot, "go-daemon", "data", "avatars")
	os.MkdirAll(avatarDir, 0755)
	// 读取并验证图片
	imgData, _, err := image.DecodeConfig(file)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "无效的图片文件"})
		return
	}
	// 限制图片尺寸 512x512
	if imgData.Width > 512 || imgData.Height > 512 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "图片尺寸不能超过 512x512"})
		return
	}
	// 重置 reader
	file.Seek(0, 0)
	// 保存为 PNG
	ext := ".png"
	savePath := filepath.Join(avatarDir, username+ext)
	outFile, err := os.Create(savePath)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "保存失败"})
		return
	}
	defer outFile.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "图片解码失败"})
		return
	}
	png.Encode(outFile, img)
	// 更新用户数据
	userDB.mu.Lock()
	if u, exists := userDB.Users[username]; exists {
		u.Avatar = username + ext
		userDB.Users[username] = u
		saveUserDataLocked()
	}
	userDB.mu.Unlock()

	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "头像更新成功",
		"avatar":  "/avatars/" + username + ext,
	})
}

// ========== 更新个人资料 API ==========
func handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Email string `json:"email"`
		Bio   string `json:"bio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	u, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	u.Email = req.Email
	u.Bio = req.Bio
	userDB.Users[cookie.Value] = u
	saveUserDataLocked()
	log.Printf("✏️ 用户更新资料: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "资料已更新"})
}

// ========== 修改密码 API ==========
func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "旧密码和新密码不能为空"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	u, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if u.Password != req.OldPassword {
		sendJSON(w, map[string]interface{}{"success": false, "message": "旧密码错误"})
		return
	}
	u.Password = req.NewPassword
	userDB.Users[cookie.Value] = u
	saveUserDataLocked()
	log.Printf("🔑 用户修改密码: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "密码修改成功，请重新登录"})
}

// ========== 面板设置 API ==========
func handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	panelSettingsMu.RLock()
	defer panelSettingsMu.RUnlock()
	globalConfigMu.RLock()
	dataKey := globalConfig.DataKey
	createdAt := globalConfig.CreatedAt
	globalConfigMu.RUnlock()
	sendJSON(w, map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"panelName":         panelSettings.PanelName,
			"port":              panelSettings.Port,
			"sessionTimeout":    panelSettings.SessionTimeout,
			"debug":             panelSettings.Debug,
			"hotReload":         panelSettings.HotReload,
			"crossDomain":       panelSettings.CrossDomain,
			"gzip":              panelSettings.Gzip,
			"pageSize":          panelSettings.PageSize,
			"maxLoginAttempts":  panelSettings.MaxLoginAttempts,
			"banDuration":       panelSettings.BanDuration,
			"allowFileManager":  panelSettings.AllowFileManager,
			"loginLimitEnabled": panelSettings.LoginLimitEnabled,
			"dataKey":           dataKey,
			"panelCreatedAt":    createdAt,
		},
	})
}

func handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 仅管理员可修改设置
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	var req PanelSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.PanelName == "" {
		req.PanelName = "Velunex Panel"
	}
	if req.Port < 1 || req.Port > 65535 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "端口范围 1-65535"})
		return
	}
	if req.SessionTimeout < 1 {
		req.SessionTimeout = 10
	}
	if req.MaxLoginAttempts < 1 {
		req.MaxLoginAttempts = 5
	}
	if req.BanDuration < 1 {
		req.BanDuration = 20
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		req.PageSize = 10
	}
	panelSettingsMu.Lock()
	panelSettings.PanelName = req.PanelName
	panelSettings.Port = req.Port
	panelSettings.SessionTimeout = req.SessionTimeout
	panelSettings.Debug = req.Debug
	panelSettings.HotReload = req.HotReload
	panelSettings.CrossDomain = req.CrossDomain
	panelSettings.Gzip = req.Gzip
	panelSettings.PageSize = req.PageSize
	panelSettings.MaxLoginAttempts = req.MaxLoginAttempts
	panelSettings.BanDuration = req.BanDuration
	panelSettings.AllowFileManager = req.AllowFileManager
	panelSettings.LoginLimitEnabled = req.LoginLimitEnabled
	savePanelSettingsLocked()
	panelSettingsMu.Unlock()
	log.Printf("⚙️ 管理员更新面板设置: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "设置已保存（端口等部分配置需重启面板生效）"})
}

// 重置 DataKey（仅管理员）
func handleResetDataKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	globalConfigMu.Lock()
	globalConfig.DataKey = generateDataKey()
	saveGlobalConfigLocked()
	newKey := globalConfig.DataKey
	globalConfigMu.Unlock()
	log.Printf("🔄 管理员重置 DataKey: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "DataKey 已重置，请同步更新所有守护进程的密钥",
		"dataKey": newKey,
	})
}

// ========== 数据导出 API ==========
func handleExportData(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	// 从路径解析导出类型：/api/export/users 或 /api/export/nodes
	parts := strings.Split(r.URL.Path, "/")
	exportType := ""
	if len(parts) > 0 {
		exportType = parts[len(parts)-1]
	}
	w.Header().Set("Content-Type", "application/json")
	switch exportType {
	case "users":
		w.Header().Set("Content-Disposition", `attachment; filename="users.json"`)
		path := getUserDataPath()
		data, err := os.ReadFile(path)
		if err != nil {
			sendJSON(w, map[string]interface{}{"success": false, "message": "读取用户数据失败"})
			return
		}
		w.Write(data)
		log.Printf("📥 导出用户数据: %s", cookie.Value)
	case "nodes":
		w.Header().Set("Content-Disposition", `attachment; filename="nodes.json"`)
		path := getNodesDataPath()
		data, err := os.ReadFile(path)
		if err != nil {
			sendJSON(w, map[string]interface{}{"success": false, "message": "读取节点数据失败"})
			return
		}
		w.Write(data)
		log.Printf("📥 导出节点数据: %s", cookie.Value)
	default:
		sendJSON(w, map[string]interface{}{"success": false, "message": "未知的导出类型"})
	}
}

func handleImportData(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	if r.Method != http.MethodPost {
		sendJSON(w, map[string]interface{}{"success": false, "message": "仅支持 POST 请求"})
		return
	}
	// 从路径解析导入类型：/api/import/users 或 /api/import/nodes
	parts := strings.Split(r.URL.Path, "/")
	importType := ""
	if len(parts) > 0 {
		importType = parts[len(parts)-1]
	}
	// 限制上传大小 10MB
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "读取文件失败: " + err.Error()})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "读取文件内容失败"})
		return
	}
	// 验证是否为有效的 JSON
	var tmp interface{}
	if err := json.Unmarshal(data, &tmp); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "文件不是有效的 JSON 格式"})
		return
	}
	switch importType {
	case "users":
		path := getUserDataPath()
		if err := os.WriteFile(path, data, 0644); err != nil {
			sendJSON(w, map[string]interface{}{"success": false, "message": "写入用户数据失败"})
			return
		}
		// 重新加载用户数据
		loadUserData()
		log.Printf("📤 导入用户数据: %s", cookie.Value)
		sendJSON(w, map[string]interface{}{"success": true, "message": "用户数据导入成功"})
	case "nodes":
		path := getNodesDataPath()
		if err := os.WriteFile(path, data, 0644); err != nil {
			sendJSON(w, map[string]interface{}{"success": false, "message": "写入节点数据失败"})
			return
		}
		// 重新加载节点数据
		loadNodesData()
		log.Printf("📤 导入节点数据: %s", cookie.Value)
		sendJSON(w, map[string]interface{}{"success": true, "message": "节点数据导入成功"})
	default:
		sendJSON(w, map[string]interface{}{"success": false, "message": "未知的导入类型"})
	}
}

// ========== 2FA API ==========
func handle2FASetup(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	secret := generateTOTPSecret()
	user.TwoFASecret = secret
	userDB.Users[cookie.Value] = user
	saveUserDataLocked()
	sendJSON(w, map[string]interface{}{
		"success": true,
		"secret":  secret,
		"qrUrl":   generateOTPAuthURL(secret, cookie.Value),
	})
}

func handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if user.TwoFASecret == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "请先调用设置接口生成密钥"})
		return
	}
	if !verifyTOTPCode(user.TwoFASecret, req.Code) {
		sendJSON(w, map[string]interface{}{"success": false, "message": "验证码错误"})
		return
	}
	user.TwoFAEnabled = true
	userDB.Users[cookie.Value] = user
	saveUserDataLocked()
	log.Printf("🔐 用户开启 2FA: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "2FA 已开启"})
}

func handle2FADisable(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if user.TwoFAEnabled && user.TwoFASecret != "" && !verifyTOTPCode(user.TwoFASecret, req.Code) {
		sendJSON(w, map[string]interface{}{"success": false, "message": "验证码错误"})
		return
	}
	user.TwoFAEnabled = false
	user.TwoFASecret = ""
	userDB.Users[cookie.Value] = user
	saveUserDataLocked()
	log.Printf("🔓 用户关闭 2FA: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "2FA 已关闭"})
}

func handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	defer userDB.mu.RUnlock()
	user, exists := userDB.Users[cookie.Value]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	sendJSON(w, map[string]interface{}{
		"success": true,
		"enabled": user.TwoFAEnabled,
	})
}

func handleLogin2FA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TempToken string `json:"tempToken"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	username, ok := consume2FATempToken(req.TempToken)
	if !ok {
		sendJSON(w, map[string]interface{}{"success": false, "message": "临时令牌无效或已过期"})
		return
	}
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[username]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if !verifyTOTPCode(user.TwoFASecret, req.Code) {
		tempToken := generate2FATempToken(username)
		sendJSON(w, map[string]interface{}{
			"success":   false,
			"need2FA":   true,
			"tempToken": tempToken,
			"message":   "验证码错误",
		})
		return
	}
	user.LastLogin = time.Now().Format("2006-01-02 15:04:05")
	userDB.Users[username] = user
	saveUserDataLocked()
	log.Printf("✅ 用户登录（2FA）: %s", username)
	expire := time.Now().Add(10 * time.Minute)
	http.SetCookie(w, &http.Cookie{
		Name:     "novapanel_session",
		Value:    username,
		Expires:  expire,
		Path:     "/",
		HttpOnly: true,
	})
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "登录成功",
		"user": map[string]interface{}{
			"username":   username,
			"uuid":       user.UUID,
			"avatar":     getAvatarURL(user.Avatar),
			"email":      user.Email,
			"bio":        user.Bio,
			"permission": user.Permission,
			"createdAt":  user.CreatedAt,
			"lastLogin":  user.LastLogin,
		},
	})
}

// ========== 自定义主题与背景 ==========
type ThemeConfig struct {
	Preset         string            `json:"preset"`
	Colors         map[string]string `json:"colors"`
	Background     string            `json:"background"`
	ControlOpacity float64           `json:"controlOpacity"`
}

var (
	themeConfig   = &ThemeConfig{Preset: "default", Colors: map[string]string{}, ControlOpacity: 1}
	themeConfigMu sync.RWMutex
)

func getThemePath() string {
	if projectRoot != "" {
		return filepath.Join(projectRoot, "go-daemon", "data", "config", "theme.json")
	}
	return "./go-daemon/data/config/theme.json"
}

func loadThemeConfig() {
	themeConfigMu.Lock()
	defer themeConfigMu.Unlock()
	path := getThemePath()
	data, err := os.ReadFile(path)
	if err != nil {
		themeConfig.Preset = "default"
		themeConfig.Colors = map[string]string{}
		themeConfig.ControlOpacity = 1
		return
	}
	json.Unmarshal(data, themeConfig)
	if themeConfig.Colors == nil {
		themeConfig.Colors = map[string]string{}
	}
	if themeConfig.ControlOpacity == 0 {
		themeConfig.ControlOpacity = 1
	}
	log.Printf("🎨 加载主题配置: 预设=%s, 背景=%s", themeConfig.Preset, themeConfig.Background)
}

func saveThemeConfigLocked() {
	path := getThemePath()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.MarshalIndent(themeConfig, "", "  ")
	os.WriteFile(path, data, 0644)
}

func getBackgroundDir() string {
	if projectRoot != "" {
		return filepath.Join(projectRoot, "go-daemon", "data", "background")
	}
	return "./go-daemon/data/background"
}

func handleThemeGet(w http.ResponseWriter, r *http.Request) {
	themeConfigMu.RLock()
	defer themeConfigMu.RUnlock()
	sendJSON(w, map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"preset":         themeConfig.Preset,
			"colors":         themeConfig.Colors,
			"background":     themeConfig.Background,
			"controlOpacity": themeConfig.ControlOpacity,
		},
	})
}

func handleThemeSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	var req ThemeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	themeConfigMu.Lock()
	themeConfig.Preset = req.Preset
	if req.Colors != nil {
		themeConfig.Colors = req.Colors
	}
	themeConfig.Background = req.Background
	themeConfig.ControlOpacity = req.ControlOpacity
	saveThemeConfigLocked()
	themeConfigMu.Unlock()
	log.Printf("🎨 管理员更新主题配置: %s", cookie.Value)
	sendJSON(w, map[string]interface{}{"success": true, "message": "主题已保存"})
}

func handleBackgroundList(w http.ResponseWriter, r *http.Request) {
	bgDir := getBackgroundDir()
	files, err := os.ReadDir(bgDir)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": true, "data": []string{}})
		return
	}
	var list []string
	for _, f := range files {
		if !f.IsDir() {
			ext := strings.ToLower(filepath.Ext(f.Name()))
			if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" {
				list = append(list, f.Name())
			}
		}
	}
	sendJSON(w, map[string]interface{}{"success": true, "data": list})
}

func handleBackgroundUpload(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "读取文件失败"})
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif" && contentType != "image/webp" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "仅支持 PNG、JPG、GIF、WebP 格式"})
		return
	}
	bgDir := getBackgroundDir()
	os.MkdirAll(bgDir, 0755)
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".png"
	}
	filename := fmt.Sprintf("bg_%d%s", time.Now().UnixNano(), ext)
	savePath := filepath.Join(bgDir, filename)
	data, err := io.ReadAll(file)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "读取文件数据失败"})
		return
	}
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "保存失败"})
		return
	}
	log.Printf("🖼️ 上传背景: %s", filename)
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "上传成功",
		"name":    filename,
	})
}

func handleBackgroundDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists || u.Permission != 10 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "权限不足"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if strings.Contains(req.Name, "..") || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
		sendJSON(w, map[string]interface{}{"success": false, "message": "无效的文件名"})
		return
	}
	bgDir := getBackgroundDir()
	path := filepath.Join(bgDir, req.Name)
	if err := os.Remove(path); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "删除失败"})
		return
	}
	themeConfigMu.Lock()
	if themeConfig.Background == req.Name {
		themeConfig.Background = ""
		saveThemeConfigLocked()
	}
	themeConfigMu.Unlock()
	log.Printf("🗑️ 删除背景: %s", req.Name)
	sendJSON(w, map[string]interface{}{"success": true, "message": "已删除"})
}

// ========== 系统信息 API ==========
func handleSysInfo(w http.ResponseWriter, r *http.Request) {
	info := getSystemInfo()
	sendJSON(w, info)
}

// ========== 系统信息获取函数 ==========
func getSystemInfo() SysInfo {
	sysInfoCache.Lock()
	defer sysInfoCache.Unlock()
	if !sysInfoCache.updated.IsZero() && time.Since(sysInfoCache.updated) < 15*time.Second {
		return sysInfoCache.value
	}
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
	uptime := getSystemUptime()
	info.Uptime = formatUptimeSimple(uptime)
	info.UptimeSeconds = int64(uptime.Seconds())
	info.ProcessCount = getProcessCount()
	info.NetSent = fmt.Sprintf("%.1f MB", float64(10+time.Now().Unix()%50))
	info.NetRecv = fmt.Sprintf("%.1f MB", float64(20+time.Now().Unix()%80))
	info.LastUpdate = time.Now().Format("2006-01-02 15:04:05")
	info.LastUpdateUnix = time.Now().Unix()
	sysInfoCache.value = info
	sysInfoCache.updated = time.Now()
	return info
}

func getOSVersion() string {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command", "(Get-CimInstance Win32_OperatingSystem).Version")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return "未知"
}

func getCPUUsage() float64 {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("powershell", "-Command", "Get-Counter '\\Processor(_Total)\\% Processor Time' | Select-Object -ExpandProperty CounterSamples | Select-Object -ExpandProperty CookedValue")
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

// ========== 内存信息 ==========
// Windows: 使用 PowerShell 获取 Win32_OperatingSystem
// - TotalVisibleMemorySize 和 FreePhysicalMemory 均返回 KB
// Linux: 读取 /proc/meminfo
func getMemoryInfo() (total, used, percent float64) {
	if runtime.GOOS == "windows" {
		// 方法1: PowerShell（推荐；wmic 在新版 Windows 中已弃用）
		// 一次调用同时获取 TotalVisibleMemorySize 和 FreePhysicalMemory（单位均为 KB）
		cmd := exec.Command("powershell", "-Command", "Get-CimInstance Win32_OperatingSystem | ForEach-Object { $_.TotalVisibleMemorySize; $_.FreePhysicalMemory }")
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
		// 注意: wmic 输出列按字母顺序排列，即 "FreePhysicalMemory TotalVisibleMemorySize"
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
		// Linux
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
		cmd := exec.Command("powershell", "-Command", "Get-PSDrive -Name C | Select-Object -ExpandProperty Used; Get-PSDrive -Name C | Select-Object -ExpandProperty Free")
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
		cmd := exec.Command("powershell", "-Command", "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime")
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
		IP     string `json:"ip"`
		Port   int    `json:"port"`
		Name   string `json:"name"`
		Type   string `json:"type"`
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.IP == "" || req.Port == 0 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "IP 和端口不能为空"})
		return
	}
	// 默认类型为 novapanel
	nodeType := req.Type
	if nodeType == "" {
		nodeType = "novapanel"
	}
	if nodeType != "novapanel" && nodeType != "mcsmanager" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "节点类型无效"})
		return
	}
	if req.APIKey == "" {
		if nodeType == "mcsmanager" {
			sendJSON(w, map[string]interface{}{"success": false, "message": "MCSManager 节点密钥不能为空"})
		} else {
			sendJSON(w, map[string]interface{}{"success": false, "message": "Velunex Panel 节点密钥不能为空"})
		}
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
		Type:       nodeType,
		APIKey:     req.APIKey,
		Version:    "1.0.0",
		Status:     "connecting",
		CPU:        0,
		MemUsed:    0,
		MemTotal:   0,
		MemPercent: 0,
		Running:    0,
		Total:      0,
		LastUpdate: "",
	}
	nodes = append(nodes, newNode)
	saveNodesDataLocked() // 持久化保存
	nodesMu.Unlock()

	// 异步连接节点
	if nodeType == "mcsmanager" {
		go func(n Node) {
			// AddMCSMNode 现在是异步连接，立即返回
			client, _ := AddMCSMNode(n.ID, n.IP, n.Port, n.APIKey)
			// 状态保持 connecting，由 startMCSMStatusSync 在连接成功后更新为 online
			// 启动状态同步循环：定期从 MCSM 客户端更新节点信息
			if client != nil {
				go startMCSMStatusSync(client, n.ID)
			}
		}(newNode)
	} else {
		go connectToNode(newNode)
	}
	nodesMu.Lock()
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
	var deletedNode Node
	for i, n := range nodes {
		if n.ID == req.ID {
			deletedNode = n
			nodes = append(nodes[:i], nodes[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		sendJSON(w, map[string]interface{}{"success": false, "message": "节点不存在"})
		return
	}
	// 如果是 MCSManager 节点，断开连接
	if deletedNode.Type == "mcsmanager" {
		RemoveMCSMNode(req.ID)
	}
	saveNodesDataLocked() // 持久化保存
	sendJSON(w, map[string]interface{}{"success": true, "message": "节点已删除"})
}

// handleNodeUpdate 更新节点（地址/密钥）
func handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID     string `json:"id"`
		IP     string `json:"ip"`
		Port   int    `json:"port"`
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	nodesMu.Lock()
	var updatedNode Node
	found := false
	for i := range nodes {
		if nodes[i].ID == req.ID {
			// 断开旧连接
			if nodes[i].Type == "mcsmanager" {
				RemoveMCSMNode(req.ID)
			}
			// 按需更新字段（IP/Port/APIKey 任一非空即更新）
			if req.IP != "" {
				nodes[i].IP = req.IP
			}
			if req.Port > 0 {
				nodes[i].Port = req.Port
			}
			if req.APIKey != "" {
				nodes[i].APIKey = req.APIKey
			}
			nodes[i].Status = "connecting"
			nodes[i].CPUHistory = []float64{}
			nodes[i].MemHistory = []float64{}
			updatedNode = nodes[i]
			found = true
			saveNodesDataLocked()
			break
		}
	}
	nodesMu.Unlock()

	if !found {
		sendJSON(w, map[string]interface{}{"success": false, "message": "节点不存在"})
		return
	}

	// 重新连接
	if updatedNode.Type == "mcsmanager" {
		go func(n Node) {
			client, _ := AddMCSMNode(n.ID, n.IP, n.Port, n.APIKey)
			nodesMu.Lock()
			for j := range nodes {
				if nodes[j].ID == n.ID {
					nodes[j].Status = "connecting" // 等待认证完成后由同步循环更新为 online
					break
				}
			}
			nodesMu.Unlock()
			// 启动状态同步循环：定期从 MCSM 客户端更新节点信息
			if client != nil {
				go startMCSMStatusSync(client, n.ID)
			}
		}(updatedNode)
	} else {
		go connectToNode(updatedNode)
	}

	sendJSON(w, map[string]interface{}{"success": true, "message": "节点地址已更新"})
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
	// 根据节点类型选择刷新方式
	if targetNode.Type == "mcsmanager" {
		// MCSManager 节点：断开旧连接并重新连接
		go func(n Node) {
			RemoveMCSMNode(n.ID)
			client, _ := AddMCSMNode(n.ID, n.IP, n.Port, n.APIKey)
			nodesMu.Lock()
			for i := range nodes {
				if nodes[i].ID == n.ID {
					nodes[i].Status = "connecting"
					break
				}
			}
			nodesMu.Unlock()
			if client != nil {
				go startMCSMStatusSync(client, n.ID)
			}
		}(*targetNode)
	} else {
		// NovaPanel 节点：标记为从未连接，让 monitor 或直接连接处理
		go connectToNode(*targetNode)
	}
	sendJSON(w, map[string]interface{}{"success": true, "message": "刷新中..."})
}

// ========== 连接节点 ==========
func connectToNode(node Node) {
	// 自动重连循环
	for {
		wsAddr := fmt.Sprintf("ws://%s:%d/ws", node.IP, node.Port)
		log.Printf("🔗 正在连接节点: %s (%s)", node.Name, wsAddr)
		conn, _, err := websocket.DefaultDialer.Dial(wsAddr, nil)
		if err != nil {
			log.Printf("⚠️ 连接节点失败: %v，10秒后重试...", err)
			updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
			time.Sleep(10 * time.Second)
			continue
		}
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
		disconnected := false
		for !disconnected {
			writeMutex.Lock()
			err := conn.WriteJSON(map[string]string{"type": "get_system"})
			writeMutex.Unlock()
			if err != nil {
				log.Printf("⚠️ 发送请求失败: %v，5秒后重连...", err)
				updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
				disconnected = true
				break
			}
			var resp map[string]interface{}
			if err := conn.ReadJSON(&resp); err != nil {
				log.Printf("⚠️ 读取响应失败: %v，5秒后重连...", err)
				updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
				disconnected = true
				break
			}
			if data, ok := resp["data"].(map[string]interface{}); ok {
				cpu := 0.0
				if v, ok := data["cpuUsage"].(float64); ok {
					cpu = v
				}
				memTotal := 16.0
				memUsed := 0.0
				memPercent := 0.0
				if v, ok := data["memTotal"].(float64); ok && v > 0 {
					memTotal = v
				}
				if v, ok := data["memUsed"].(float64); ok && v > 0 {
					memUsed = v
				}
				if v, ok := data["memPercent"].(float64); ok && v > 0 {
					memPercent = v
				}
				if memUsed <= 0 && memTotal > 0 {
					memUsed = memTotal * 0.13
					memPercent = 13.0
				}
				updateNodeStatus(node.ID, "online", cpu, memUsed, memTotal, memPercent, -1, -1)
			}
			writeMutex.Lock()
			err = conn.WriteJSON(map[string]string{"type": "get_instances"})
			writeMutex.Unlock()
			if err != nil {
				log.Printf("⚠️ 发送实例请求失败: %v，5秒后重连...", err)
				updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
				disconnected = true
				break
			}
			var instResp map[string]interface{}
			if err := conn.ReadJSON(&instResp); err != nil {
				log.Printf("⚠️ 读取实例响应失败: %v，5秒后重连...", err)
				updateNodeStatus(node.ID, "offline", 0, 0, 0, 0, 0, 0)
				disconnected = true
				break
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
			// The previous code created a 5-second ticker but never waited for it,
			// causing an unrestricted get_system/get_instances request loop.
			// Wait before the next polling cycle to keep low-spec nodes responsive.
			if !disconnected {
				<-ticker.C
			}
		}
		conn.Close()
		ticker.Stop()
		if disconnected {
			time.Sleep(5 * time.Second)
		}
	}
}

// ========== 文件管理（NovaPanel 节点专用 WebSocket 连接） ==========

// nodeFileConns 每个节点的文件管理专用 WebSocket 连接（与状态同步连接独立）
var (
	nodeFileConns   = make(map[string]*websocket.Conn)
	nodeFileConnsMu sync.Mutex
)

// getNodeFileConnLocked 获取或创建节点的文件管理连接（调用者必须持有 nodeFileConnsMu 锁）
func getNodeFileConnLocked(nodeID string) (*websocket.Conn, error) {
	if conn, ok := nodeFileConns[nodeID]; ok {
		return conn, nil
	}
	// 查找节点
	nodesMu.RLock()
	var targetNode *Node
	for i := range nodes {
		if nodes[i].ID == nodeID {
			targetNode = &nodes[i]
			break
		}
	}
	nodesMu.RUnlock()
	if targetNode == nil {
		return nil, fmt.Errorf("节点不存在")
	}
	if targetNode.Type == "mcsmanager" {
		return nil, fmt.Errorf("MCSManager 节点暂不支持文件管理")
	}
	if targetNode.Status != "online" {
		return nil, fmt.Errorf("节点离线，请稍后重试")
	}
	wsAddr := fmt.Sprintf("ws://%s:%d/ws", targetNode.IP, targetNode.Port)
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsAddr, nil)
	if err != nil {
		return nil, fmt.Errorf("连接节点失败: %v", err)
	}
	nodeFileConns[nodeID] = conn
	log.Printf("📂 已建立文件管理连接: %s", targetNode.Name)
	return conn, nil
}

// closeNodeFileConn 关闭并清理节点的文件管理连接
func closeNodeFileConn(nodeID string) {
	if conn, ok := nodeFileConns[nodeID]; ok {
		conn.Close()
		delete(nodeFileConns, nodeID)
	}
}

// sendNodeFileRequest 向节点发送文件操作请求并等待响应（带 30 秒超时）
func sendNodeFileRequest(nodeID, msgType string, data interface{}) (map[string]interface{}, error) {
	nodeFileConnsMu.Lock()
	defer nodeFileConnsMu.Unlock()

	conn, err := getNodeFileConnLocked(nodeID)
	if err != nil {
		return nil, err
	}

	// 发送请求
	reqMsg := map[string]interface{}{"type": msgType, "data": data}
	if err := conn.WriteJSON(reqMsg); err != nil {
		closeNodeFileConn(nodeID)
		// 重试一次
		conn, err = getNodeFileConnLocked(nodeID)
		if err != nil {
			return nil, err
		}
		if err := conn.WriteJSON(reqMsg); err != nil {
			closeNodeFileConn(nodeID)
			return nil, fmt.Errorf("发送请求失败: %v", err)
		}
	}

	// 设置读取超时
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	defer conn.SetReadDeadline(time.Time{})

	var resp struct {
		Type    string      `json:"type"`
		Data    interface{} `json:"data"`
		Success bool        `json:"success"`
		Message string      `json:"message"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		closeNodeFileConn(nodeID)
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	if !resp.Success {
		msg := resp.Message
		if msg == "" {
			msg = "操作失败"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	// 将 Data 转为 map
	result := map[string]interface{}{
		"success": true,
		"data":    resp.Data,
	}
	return result, nil
}

// handleFileManage 统一文件管理 API
// POST /api/file  body: { "operation": "list|download|upload|delete|rename|mkdir|touch|read|write", "nodeId": "...", ... }
func handleFileManage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 验证登录
	cookie, err := r.Cookie("novapanel_session")
	if err != nil || cookie.Value == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	userDB.mu.RLock()
	u, exists := userDB.Users[cookie.Value]
	userDB.mu.RUnlock()
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "用户不存在"})
		return
	}
	if u.Permission == -1 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号已被禁封"})
		return
	}
	// 权限检查：普通用户需要 AllowFileManager 开启
	if u.Permission != 10 {
		panelSettingsMu.RLock()
		allow := panelSettings.AllowFileManager
		panelSettingsMu.RUnlock()
		if !allow {
			sendJSON(w, map[string]interface{}{"success": false, "message": "文件管理功能已被管理员禁用"})
			return
		}
	}

	var req struct {
		Operation string `json:"operation"`
		NodeID    string `json:"nodeId"`
		Path      string `json:"path"`
		OldPath   string `json:"oldPath"`
		NewPath   string `json:"newPath"`
		Content   string `json:"content"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}
	if req.NodeID == "" || req.Operation == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "缺少 nodeId 或 operation 参数"})
		return
	}

	// 消息类型映射
	var msgType string
	var data interface{}
	switch req.Operation {
	case "list":
		msgType = "file_list"
		data = map[string]interface{}{"path": req.Path, "offset": req.Offset, "limit": req.Limit}
	case "download":
		msgType = "file_download"
		data = map[string]string{"path": req.Path}
	case "upload":
		msgType = "file_upload"
		data = map[string]string{"path": req.Path, "content": req.Content}
	case "delete":
		msgType = "file_delete"
		data = map[string]string{"path": req.Path}
	case "rename":
		msgType = "file_rename"
		data = map[string]string{"oldPath": req.OldPath, "newPath": req.NewPath}
	case "mkdir":
		msgType = "file_mkdir"
		data = map[string]string{"path": req.Path}
	case "touch":
		msgType = "file_touch"
		data = map[string]string{"path": req.Path}
	case "read":
		msgType = "file_read"
		data = map[string]string{"path": req.Path}
	case "write":
		msgType = "file_write"
		data = map[string]string{"path": req.Path, "content": req.Content}
	default:
		sendJSON(w, map[string]interface{}{"success": false, "message": "不支持的操作: " + req.Operation})
		return
	}

	result, err := sendNodeFileRequest(req.NodeID, msgType, data)
	if err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	sendJSON(w, result)
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
				if nodes[i].Version == "" {
					nodes[i].Version = "1.0.0"
				}
				// 记录历史数据（最多60个点=10分钟）
				if nodes[i].CPUHistory == nil {
					nodes[i].CPUHistory = []float64{}
				}
				nodes[i].CPUHistory = append(nodes[i].CPUHistory, cpu)
				if len(nodes[i].CPUHistory) > 60 {
					nodes[i].CPUHistory = nodes[i].CPUHistory[1:]
				}
				if nodes[i].MemHistory == nil {
					nodes[i].MemHistory = []float64{}
				}
				nodes[i].MemHistory = append(nodes[i].MemHistory, memPercent)
				if len(nodes[i].MemHistory) > 60 {
					nodes[i].MemHistory = nodes[i].MemHistory[1:]
				}
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
// connectingNodes 记录已启动 connectToNode 的节点，避免重复启动导致 goroutine 泄漏
var connectingNodes = make(map[string]bool)
var connectingNodesMu sync.Mutex

// startNodeMonitor 定时检查节点状态，仅对从未连接过的 NovaPanel 节点启动连接
// 注意：connectToNode 内部已有无限重连循环，MCSManager 节点有 receiveLoop 自动重连
// 所以此监控器只负责启动首次连接，不重复启动已连接中的节点
func startNodeMonitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		nodesMu.RLock()
		nodeList := make([]Node, len(nodes))
		copy(nodeList, nodes)
		nodesMu.RUnlock()
		for _, node := range nodeList {
			// 只处理 NovaPanel 节点（MCSManager 节点有自己的重连机制）
			if node.Type == "mcsmanager" {
				continue
			}
			// 只对从未启动过连接的节点启动（status == "unknown"）
			// connectToNode 内部有无限重连循环，一旦启动就不需要再启动
			if node.Status != "unknown" {
				continue
			}
			connectingNodesMu.Lock()
			if connectingNodes[node.ID] {
				connectingNodesMu.Unlock()
				continue
			}
			connectingNodes[node.ID] = true
			connectingNodesMu.Unlock()
			go func(n Node) {
				connectToNode(n)
				// connectToNode 返回时（理论上不会返回，除非 stopChan 关闭）
				connectingNodesMu.Lock()
				delete(connectingNodes, n.ID)
				connectingNodesMu.Unlock()
			}(node)
		}
	}
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
	// 热重载仅用于开发；旧实现每 300ms 递归遍历整个 static 目录，
	// 在 HDD/VPS 上会造成持续磁盘 I/O 与 CPU 占用。
	panelSettingsMu.RLock()
	enabled := panelSettings.HotReload
	panelSettingsMu.RUnlock()
	if !enabled {
		log.Println("[INFO] Hot reload is disabled")
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		changed := false
		staticDir := filepath.Join(projectRoot, "go-web", "static")
		entries, err := os.ReadDir(staticDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := filepath.Ext(entry.Name())
			if ext != ".html" && ext != ".css" && ext != ".js" {
				continue
			}
			path := filepath.Join(staticDir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
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
		}
		if changed {
			notifyReload()
		}
	}
}

// corsMiddleware 根据面板设置中的 CrossDomain 开关为响应添加 CORS 头
// 启用后 HTTP 响应将包含 access-control-allow-origin: *，便于第三方开发扩展
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panelSettingsMu.RLock()
		crossDomain := panelSettings.CrossDomain
		panelSettingsMu.RUnlock()
		if crossDomain {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			// 处理预检请求
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

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
	loadGlobalConfig()
	loadPanelSettings()
	loadThemeConfig()
	loadUserData()
	loadNodesData() // 加载并自动连接已保存的节点
	staticPath := filepath.Join(projectRoot, "go-web", "static")
	if _, err := os.Stat(staticPath); os.IsNotExist(err) {
		staticPath = filepath.Join(projectRoot, "static")
		if _, err := os.Stat(staticPath); os.IsNotExist(err) {
			log.Printf("⚠️ 找不到 static 目录！")
			staticPath = "./static"
		}
	}
	log.Printf("📂 静态文件目录: %s", staticPath)

	// 修改路由处理：支持SPA路由重定向
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 检查是否是API请求
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// 先尝试提供实际存在的静态文件
		relPath := strings.TrimPrefix(r.URL.Path, "/")
		if relPath != "" {
			filePath := filepath.Join(staticPath, relPath)
			if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
				http.ServeFile(w, r, filePath)
				return
			}
		}
		// 文件不存在则返回 index.html（SPA 路由回退）
		http.ServeFile(w, r, filepath.Join(staticPath, "index.html"))
	})

	// WebSocket 路由
	http.HandleFunc("/ws", handleWebSocket)

	// API 路由
	http.HandleFunc("/api/register", handleRegister)
	http.HandleFunc("/api/login", handleLogin)
	http.HandleFunc("/api/check-session", handleCheckSession)
	http.HandleFunc("/api/install-status", handleInstallStatus)
	http.HandleFunc("/api/logout", handleLogout)
	http.HandleFunc("/api/user/list", handleUserList)
	http.HandleFunc("/api/user/create", handleUserCreate)
	http.HandleFunc("/api/user/delete", handleUserDelete)
	http.HandleFunc("/api/user/update", handleUserUpdate)
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/start", handleStart)
	http.HandleFunc("/api/stop", handleStop)
	http.HandleFunc("/api/logs", handleLogs)
	http.HandleFunc("/api/sysinfo", handleSysInfo)
	http.HandleFunc("/api/nodes", handleNodes)
	http.HandleFunc("/api/node/add", handleNodeAdd)
	http.HandleFunc("/api/node/delete", handleNodeDelete)
	http.HandleFunc("/api/node/update", handleNodeUpdate)
	http.HandleFunc("/api/node/refresh", handleNodeRefresh)
	// 文件管理
	http.HandleFunc("/api/file", handleFileManage)
	// 头像上传
	http.HandleFunc("/api/avatar/upload", handleAvatarUpload)
	// 头像静态文件服务
	avatarDir := filepath.Join(projectRoot, "go-daemon", "data", "avatars")
	os.MkdirAll(avatarDir, 0755)
	http.Handle("/avatars/", http.StripPrefix("/avatars/", http.FileServer(http.Dir(avatarDir))))
	// MCSManager daemon 兼容 API
	http.HandleFunc("/api/mcsm/nodes", handleMCSMNodes)
	http.HandleFunc("/api/mcsm/add", handleMCSMAddNode)
	http.HandleFunc("/api/mcsm/remove", handleMCSMRemoveNode)
	http.HandleFunc("/api/mcsm/test", handleMCSMTestNode)
	// 面板设置与数据导出
	http.HandleFunc("/api/settings", handleSettingsGet)
	http.HandleFunc("/api/settings/save", handleSettingsSave)
	http.HandleFunc("/api/settings/reset-key", handleResetDataKey)
	// 2FA 认证
	http.HandleFunc("/api/2fa/setup", handle2FASetup)
	http.HandleFunc("/api/2fa/verify", handle2FAVerify)
	http.HandleFunc("/api/2fa/disable", handle2FADisable)
	http.HandleFunc("/api/2fa/status", handle2FAStatus)
	http.HandleFunc("/api/login/2fa", handleLogin2FA)
	// 主题与背景
	http.HandleFunc("/api/theme", handleThemeGet)
	http.HandleFunc("/api/theme/save", handleThemeSave)
	http.HandleFunc("/api/background/list", handleBackgroundList)
	http.HandleFunc("/api/background/upload", handleBackgroundUpload)
	http.HandleFunc("/api/background/delete", handleBackgroundDelete)
	bgDir := getBackgroundDir()
	os.MkdirAll(bgDir, 0755)
	http.Handle("/backgrounds/", http.StripPrefix("/backgrounds/", http.FileServer(http.Dir(bgDir))))
	// 语言文件静态服务
	langDir := filepath.Join(projectRoot, "go-web", "lang")
	if projectRoot == "" {
		langDir = "./lang"
	}
	http.Handle("/lang/", http.StripPrefix("/lang/", http.FileServer(http.Dir(langDir))))
	http.HandleFunc("/api/export/", handleExportData)
	http.HandleFunc("/api/import/", handleImportData)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// 启动文件监听和节点监控
	go startFileWatcher()
	go startNodeMonitor()

	addr := fmt.Sprintf(":%d", HTTP_PORT)
	printStartupBanner("Web Panel", WEB_VER)
	fmt.Println(" ================================")
	log.Printf("[INFO] %s", startupText("started"))
	log.Printf("[INFO] %s: http://127.0.0.1%s", startupText("address"), addr)
	log.Printf("[INFO] WebSocket: ws://127.0.0.1%s/ws", addr)
	log.Printf("[INFO] 软件公网访问需开放端口 8080 与守护进程端口")
	log.Printf("[INFO] %s", startupText("close"))
	fmt.Println(" ================================")
	fmt.Println()

	if err := http.ListenAndServe(addr, corsMiddleware(http.DefaultServeMux)); err != nil {
		log.Fatal(startupText("failed")+":", err)
	}
}
