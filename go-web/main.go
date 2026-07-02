package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
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
)

var projectRoot string

// ========== 用户数据结构 ==========
type User struct {
	UUID       string `json:"uuid"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Permission int    `json:"permission"` // -1=禁封 1=普通用户 10=管理员
	CreatedAt  string `json:"createdAt"`
	LastLogin  string `json:"lastLogin"`
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
	OS         string  `json:"os"`
	OSVersion  string  `json:"osVersion"`
	Hostname   string  `json:"hostname"`
	CurrentUser string  `json:"currentUser"`
	Uptime     string  `json:"uptime"`
	CpuUsage   float64 `json:"cpuUsage"`
	CpuCores   int     `json:"cpuCores"`
	MemTotal   float64 `json:"memTotal"`
	MemUsed    float64 `json:"memUsed"`
	MemPercent float64 `json:"memPercent"`
	DiskTotal  float64 `json:"diskTotal"`
	DiskUsed   float64 `json:"diskUsed"`
	DiskPercent float64 `json:"diskPercent"`
	NetSent    string  `json:"netSent"`
	NetRecv    string  `json:"netRecv"`
	ProcessCount int    `json:"processCount"`
	LastUpdate  string  `json:"lastUpdate"`
}

// ========== 节点数据结构 ==========
type Node struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	IP         string    `json:"ip"`
	Port       int       `json:"port"`
	Type       string    `json:"type"`     // "novapanel" 或 "mcsmanager"
	APIKey     string    `json:"apiKey"`   // MCSManager 的验证密钥
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	Platform   string    `json:"platform"`   // 操作系统平台
	DaemonID   string    `json:"daemonId"`   // Daemon ID
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
	mu         sync.RWMutex
	running    bool
	startTime  time.Time
	cmd        *exec.Cmd
	memoryUsage float64
}

var serverState = &ServerState{}

type StatusResponse struct {
	Running bool   `json:"running"`
	Memory  float64 `json:"memory"`
	Uptime  string  `json:"uptime"`
	Players int    `json:"players"`
}

type ActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ========== 节点列表 ==========
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
	clients = make(map[*websocket.Conn]bool)
	clientsMu sync.RWMutex
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

var (
	lastModMap = make(map[string]time.Time)
	watcherMu sync.Mutex
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

func handleLogin(w http.ResponseWriter, r *http.Request) {
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
	userDB.mu.Lock()
	defer userDB.mu.Unlock()
	user, exists := userDB.Users[req.Username]
	if !exists {
		sendJSON(w, map[string]interface{}{"success": false, "message": "账号不存在"})
		return
	}
	if user.Permission == -1 {
		sendJSON(w, map[string]interface{}{"success": false, "message": "您的账号已被禁封，请联系管理员"})
		return
	}
	if user.Password != req.Password {
		sendJSON(w, map[string]interface{}{"success": false, "message": "密码错误"})
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
		"user": map[string]string{
			"username":  req.Username,
			"createdAt": user.CreatedAt,
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
		"user": map[string]string{
			"username":  cookie.Value,
			"createdAt": user.CreatedAt,
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
	UUID       string `json:"uuid"`
	Username   string `json:"username"`
	Permission int    `json:"permission"`
	PermName   string `json:"permName"`
	CreatedAt  string `json:"createdAt"`
	LastLogin  string `json:"lastLogin"`
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
			UUID:       u.UUID,
			Username:   u.Username,
			Permission: u.Permission,
			PermName:   permissionName(u.Permission),
			CreatedAt:  u.CreatedAt,
			LastLogin:  u.LastLogin,
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

func getSystemUptime() string {
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
	if nodeType == "mcsmanager" && req.APIKey == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "MCSManager 节点密钥不能为空"})
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
		ID:      fmt.Sprintf("node%d", len(nodes)+1),
		Name:    name,
		IP:      req.IP,
		Port:    req.Port,
		Type:    nodeType,
		APIKey:  req.APIKey,
		Version: "1.0.0",
		Status:  "connecting",
		CPU:     0,
		MemUsed: 0,
		MemTotal: 0,
		MemPercent: 0,
		Running: 0,
		Total:   0,
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
		conn.Close()
		ticker.Stop()
		if disconnected {
			time.Sleep(5 * time.Second)
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
	// MCSManager daemon 兼容 API
	http.HandleFunc("/api/mcsm/nodes", handleMCSMNodes)
	http.HandleFunc("/api/mcsm/add", handleMCSMAddNode)
	http.HandleFunc("/api/mcsm/remove", handleMCSMRemoveNode)
	http.HandleFunc("/api/mcsm/test", handleMCSMTestNode)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// 启动文件监听和节点监控
	go startFileWatcher()
	go startNodeMonitor()

	addr := fmt.Sprintf(":%d", HTTP_PORT)
	log.Printf("🚀 NovaPanel Web 启动于 http://127.0.0.1%s", addr)
	log.Printf("🔌 WebSocket: ws://127.0.0.1%s/ws", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("启动失败:", err)
	}
}
