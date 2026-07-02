package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ===== MCSManager Daemon 客户端 =====
// MCSManager daemon 使用 Socket.IO v4 协议（EIO=4）
// 认证方式：连接时 URL 参数携带 key

// MCSMNodeInfo MCSManager 节点信息
type MCSMNodeInfo struct {
	Available  bool    `json:"available"`
	Version    string  `json:"version"`
	IP         string  `json:"ip"`
	Port       int     `json:"port"`
	Key        string  `json:"key"`
	CPUUsage   float64 `json:"cpuUsage"`   // 0-100
	MemUsage   float64 `json:"memUsage"`   // 0-100
	MemTotal   float64 `json:"memTotal"`   // GB
	MemUsed    float64 `json:"memUsed"`    // GB
	Running    int     `json:"running"`
	Total      int     `json:"total"`
	Hostname   string  `json:"hostname"`
	SystemType string  `json:"systemType"`
	Platform   string  `json:"platform"`
	Uptime     int64   `json:"uptime"`
	Error      string  `json:"error"`
}

// MCSMClient MCSManager daemon 客户端
type MCSMClient struct {
	mu                sync.Mutex
	ip                string
	port              int
	key               string
	conn              *websocket.Conn
	connected         bool
	authenticated     bool // 是否已通过 auth 认证
	infoPollerRunning bool // 确保 infoPoller 只启动一次
	stopChan          chan bool
	info              MCSMNodeInfo
	eioVersion        int // 3 或 4，对应 MCSManager v9.x / v10.x
}

// NewMCSMClient 创建新的 MCSManager daemon 客户端
func NewMCSMClient(ip string, port int, key string) *MCSMClient {
	return &MCSMClient{
		ip:         ip,
		port:       port,
		key:        key,
		stopChan:   make(chan bool, 1),
		eioVersion: 4, // 默认 v4（MCSManager v10.x），握手失败时自动降级到 v3
		info: MCSMNodeInfo{
			IP:   ip,
			Port: port,
			Key:  key,
		},
	}
}

// socketIOHandshake Socket.IO 握手（自动兼容 EIO=3 和 EIO=4）
// EIO=4 用于 MCSManager v10.x daemon（daemon v4.x）
// EIO=3 用于 MCSManager v9.x daemon（daemon v3.x）
func (c *MCSMClient) socketIOHandshake() (string, error) {
	// 先尝试 EIO=4（v10.x），失败后降级到 EIO=3（v9.x）
	for _, eio := range []int{4, 3} {
		sid, err := c.tryHandshake(eio)
		if err == nil {
			c.eioVersion = eio
			log.Printf("[MCSM] 握手成功 (EIO=%d, MCSManager v%s), sid: %s",
				eio, map[int]string{3: "9.x", 4: "10.x"}[eio], sid)
			return sid, nil
		}
		log.Printf("[MCSM] EIO=%d 握手失败: %v，尝试降级...", eio, err)
	}
	return "", fmt.Errorf("Socket.IO 握手失败（EIO=4 和 EIO=3 均失败）")
}

// tryHandshake 尝试指定 EIO 版本的握手
func (c *MCSMClient) tryHandshake(eio int) (string, error) {
	url := fmt.Sprintf("http://%s:%d/socket.io/?EIO=%d&transport=polling&key=%s",
		c.ip, c.port, eio, c.key)

	client := http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("连接失败: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	text := string(body)
	if len(text) < 2 || text[0] != '0' {
		return "", fmt.Errorf("非 Socket.IO 响应: %s", text)
	}

	jsonPart := text[1:]
	// EIO=3 握手响应可能有冒号: 0:{...}，EIO=4 没有: 0{...}
	if strings.HasPrefix(jsonPart, ":") {
		jsonPart = jsonPart[1:]
	}

	var handshake struct {
		Sid          string   `json:"sid"`
		Upgrades     []string `json:"upgrades"`
		PingInterval int      `json:"pingInterval"`
		PingTimeout  int      `json:"pingTimeout"`
	}
	if err := json.Unmarshal([]byte(jsonPart), &handshake); err != nil {
		return "", fmt.Errorf("解析握手响应失败: %v", err)
	}

	if handshake.Sid == "" {
		return "", fmt.Errorf("sid 为空")
	}

	return handshake.Sid, nil
}

// Connect 连接到 MCSManager daemon
// 注意：此函数不持有锁，connectDirect/connectWithPolling 内部自行管理锁
func (c *MCSMClient) Connect() error {
	// 快速检查是否已连接（加锁保护读取）
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// 方式1：尝试直接 WebSocket 连接（不做 HTTP polling 握手）
	err := c.connectDirect(4)
	if err != nil {
		log.Printf("[MCSM] 直接连接 (EIO=4) 失败: %v，尝试 EIO=3...", err)
		err = c.connectDirect(3)
	}
	if err != nil {
		log.Printf("[MCSM] 直接连接 (EIO=3) 失败: %v，尝试 polling 升级方式...", err)
		// 方式2：fallback 到 HTTP polling 握手 + WebSocket 升级
		err = c.connectWithPolling()
	}

	if err == nil {
		// 连接成功，统一启动一个 receiveLoop（避免重复启动）
		go c.receiveLoop()
	}
	return err
}

// connectDirect 直接 WebSocket 连接（不经过 HTTP polling）
// 此函数内部自行管理锁：网络操作不持锁，状态设置持锁
func (c *MCSMClient) connectDirect(eioVersion int) error {
	wsURL := fmt.Sprintf("ws://%s:%d/socket.io/?EIO=%d&transport=websocket&key=%s",
		c.ip, c.port, eioVersion, c.key)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket 连接失败: %v", err)
	}

	// 设置读取超时
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	// 等待 Engine.IO OPEN 消息
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("读取 OPEN 失败: %v", err)
	}
	msgStr := string(msg)
	log.Printf("[MCSM] 直接连接收到: %s", msgStr)

	if msgStr[0] != '0' {
		conn.Close()
		return fmt.Errorf("期望 OPEN 消息，收到: %s", msgStr)
	}

	// 解析 OPEN 获取 sid
	var openData struct {
		Sid          string `json:"sid"`
		PingInterval int    `json:"pingInterval"`
		PingTimeout  int    `json:"pingTimeout"`
	}
	if len(msgStr) > 1 {
		json.Unmarshal([]byte(msgStr[1:]), &openData)
	}
	if openData.Sid == "" {
		conn.Close()
		return fmt.Errorf("OPEN 消息中没有 sid")
	}
	log.Printf("[MCSM] 直接连接 sid: %s", openData.Sid)

	// 取消读取超时
	conn.SetReadDeadline(time.Time{})

	// 加锁设置状态并发送认证
	c.mu.Lock()
	defer c.mu.Unlock()

	// 防止并发：如果已有连接，关闭新连接
	if c.connected {
		conn.Close()
		return fmt.Errorf("已有其他连接成功")
	}

	c.eioVersion = eioVersion
	c.conn = conn
	c.connected = true
	c.authenticated = false
	c.info.Available = true
	c.info.Error = ""

	// 发送 Socket.IO CONNECT，等收到确认（40）后再发 auth（在 handleMessage 中处理）
	conn.WriteMessage(websocket.TextMessage, []byte("40"))

	mcsmVer := "v10.x"
	if eioVersion == 3 {
		mcsmVer = "v9.x"
	}
	log.Printf("[MCSM] 已连接到 MCSManager daemon %s:%d (MCSManager %s, EIO=%d, 直接连接)，等待认证...", c.ip, c.port, mcsmVer, eioVersion)

	return nil
}

// sendAuth 发送 auth 事件进行认证（调用者必须持有 c.mu 锁）
// MCSManager daemon 协议：auth 事件的 data 字段为 key 字符串
// 认证成功后 daemon 回复 42["auth", {uuid, status:200, event:"auth", data:true}]
func (c *MCSMClient) sendAuth() {
	if c.conn == nil {
		return
	}
	authUUID := fmt.Sprintf("%d", time.Now().UnixNano())
	// Packet 结构: {uuid, status, event, data}，data 是 key 字符串
	authPacket := fmt.Sprintf(`{"uuid":"%s","status":200,"event":"auth","data":%s}`,
		authUUID, mustJSONString(c.key))
	msg := `42["auth",` + authPacket + `]`
	c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	log.Printf("[MCSM] 已发送 auth 认证请求 (uuid=%s)", authUUID)
}

// requestInfoOverview 请求系统概览信息
// 必须在 auth 认证成功后调用
func (c *MCSMClient) requestInfoOverview() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || c.conn == nil || !c.authenticated {
		return
	}
	uuid := fmt.Sprintf("%d", time.Now().UnixNano())
	reqPacket := fmt.Sprintf(`{"uuid":"%s","status":200,"event":"info/overview","data":null}`, uuid)
	c.conn.WriteMessage(websocket.TextMessage, []byte(`42["info/overview",`+reqPacket+`]`))
}

// mustJSONString 将字符串转为 JSON 字符串字面量（带引号），安全转义特殊字符
func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// connectWithPolling 使用 HTTP polling 握手 + WebSocket 升级
func (c *MCSMClient) connectWithPolling() error {
	// 1. Socket.IO 握手
	sid, err := c.socketIOHandshake()
	if err != nil {
		return err
	}

	// 2. WebSocket 连接
	wsURL := fmt.Sprintf("ws://%s:%d/socket.io/?EIO=%d&transport=websocket&sid=%s&key=%s",
		c.ip, c.port, c.eioVersion, sid, c.key)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("WebSocket 连接失败: %v", err)
	}

	// 3. 处理升级 probe 流程
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	upgradeDone := false
	for !upgradeDone {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			return fmt.Errorf("升级阶段读取消息失败: %v", err)
		}
		msgStr := string(msg)
		log.Printf("[MCSM] 升级阶段收到: %s", msgStr)

		if strings.Contains(msgStr, "probe") {
			if strings.HasPrefix(msgStr, "2probe") {
				conn.WriteMessage(websocket.TextMessage, []byte("3probe"))
			}
		} else if strings.HasPrefix(msgStr, "0") || msgStr == "6" {
			upgradeDone = true
		} else if strings.HasPrefix(msgStr, "4") {
			upgradeDone = true
			c.handleMessage(msgStr)
		}
	}
	conn.SetReadDeadline(time.Time{})

	// 加锁设置状态并发送认证
	c.mu.Lock()
	defer c.mu.Unlock()

	// 防止并发：如果已有连接，关闭新连接
	if c.connected {
		conn.Close()
		return fmt.Errorf("已有其他连接成功")
	}

	c.conn = conn
	c.connected = true
	c.authenticated = false
	c.info.Available = true
	c.info.Error = ""

	// 4. 发送 Socket.IO CONNECT，等收到确认后再发 auth（在 handleMessage 中处理）
	conn.WriteMessage(websocket.TextMessage, []byte("40"))

	mcsmVer := "v10.x"
	if c.eioVersion == 3 {
		mcsmVer = "v9.x"
	}
	log.Printf("[MCSM] 已连接到 MCSManager daemon %s:%d (MCSManager %s, EIO=%d, polling 升级)，等待认证...", c.ip, c.port, mcsmVer, c.eioVersion)

	// 5. 注意：receiveLoop 由 Connect() 统一启动，避免重复
	return nil
}

// infoPoller 定时请求系统信息（每 5 秒）
func (c *MCSMClient) infoPoller() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			c.mu.Lock()
			authed := c.authenticated
			c.mu.Unlock()
			if !authed {
				continue
			}
			c.requestInfoOverview()
		}
	}
}

// receiveLoop 消息接收循环（带自动重连）
func (c *MCSMClient) receiveLoop() {
	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		c.mu.Lock()
		if !c.connected || c.conn == nil {
			c.mu.Unlock()
			// 连接断开，等待 10 秒后自动重连
			select {
			case <-c.stopChan:
				return
			case <-time.After(10 * time.Second):
			}
			log.Printf("[MCSM] 尝试重新连接 %s:%d...", c.ip, c.port)
			c.reconnect()
			continue
		}
		conn := c.conn
		c.mu.Unlock()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			if c.connected {
				log.Printf("[MCSM] 连接断开: %v，10秒后重连...", err)
				c.connected = false
				c.authenticated = false
				c.info.Available = false
				c.info.Error = err.Error()
				if c.conn != nil {
					c.conn.Close()
					c.conn = nil
				}
			}
			c.mu.Unlock()
			continue
		}

		c.handleMessage(string(msg))
	}
}

// reconnect 重新连接
// 此函数由 receiveLoop 调用，connectDirect/connectWithPolling 内部自行管理锁
func (c *MCSMClient) reconnect() {
	// 清理旧连接（加锁）
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()

	// 尝试直接连接方式（内部自行加锁设置状态）
	err := c.connectDirect(c.eioVersion)
	if err == nil {
		log.Printf("[MCSM] 重连成功（直接连接）%s:%d", c.ip, c.port)
		return
	}
	log.Printf("[MCSM] 重连直接连接失败: %v", err)

	// fallback 到 polling 方式（内部自行加锁设置状态）
	err = c.connectWithPolling()
	if err == nil {
		log.Printf("[MCSM] 重连成功（polling 升级）%s:%d", c.ip, c.port)
		return
	}
	log.Printf("[MCSM] 重连失败: %v", err)
}

// handleMessage 处理收到的 Socket.IO 消息
func (c *MCSMClient) handleMessage(msg string) {
	if len(msg) < 1 {
		return
	}

	packetType := msg[0]
	switch packetType {
	case '0': // OPEN
		log.Printf("[MCSM] 收到 OPEN: %s", msg)
	case '2': // PING
		// 回复 PONG
		c.mu.Lock()
		if c.conn != nil {
			c.conn.WriteMessage(websocket.TextMessage, []byte("3"))
		}
		c.mu.Unlock()
	case '3': // PONG
		// 忽略
	case '4': // MESSAGE
		if len(msg) < 2 {
			return
		}
		subType := msg[1]
		switch subType {
		case '0': // CONNECT 确认
			log.Printf("[MCSM] Socket.IO 连接确认，发送 auth 认证请求...")
			// 收到 CONNECT 确认后发送 auth 认证
			c.mu.Lock()
			c.sendAuth()
			c.mu.Unlock()
		case '2': // EVENT
			log.Printf("[MCSM] 原始 EVENT 数据: %s", msg[2:])
			c.handleEvent(msg[2:])
		}
	}
}

// handleEvent 处理 Socket.IO 事件
func (c *MCSMClient) handleEvent(data string) {
	// 事件格式: ["event_name", {uuid, status, event, data}]
	var event []json.RawMessage
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}

	if len(event) < 2 {
		return
	}

	var eventName string
	json.Unmarshal(event[0], &eventName)

	log.Printf("[MCSM] 收到事件: %s", eventName)

	// MCSManager daemon 响应包结构: {uuid, status, event, data}
	// 真正的数据在 data 字段里
	var packet struct {
		UUID   string          `json:"uuid"`
		Status int             `json:"status"`
		Event  string          `json:"event"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(event[1], &packet); err != nil {
		// 可能是旧格式直接传 data，尝试直接解析
		packet.Data = event[1]
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch eventName {
	case "auth":
		// 认证响应: data 为 true/false
		var authResult bool
		if err := json.Unmarshal(packet.Data, &authResult); err != nil {
			// data 可能是其他类型，尝试解析为字符串
			var s string
			if err2 := json.Unmarshal(packet.Data, &s); err2 == nil {
				authResult = (s == "true")
			}
		}
		if packet.Status == 200 && authResult {
			c.authenticated = true
			c.info.Error = ""
			log.Printf("[MCSM] ✅ 认证成功 %s:%d", c.ip, c.port)
			// 认证成功后立即请求系统信息
			c.requestInfoOverviewLocked()
			// 启动定时请求（确保只启动一次，避免重连后重复）
			if !c.infoPollerRunning {
				c.infoPollerRunning = true
				go func() {
					c.infoPoller()
					c.mu.Lock()
					c.infoPollerRunning = false
					c.mu.Unlock()
				}()
			}
		} else {
			c.authenticated = false
			c.info.Error = "认证失败：密钥错误"
			log.Printf("[MCSM] ❌ 认证失败：密钥错误 %s:%d", c.ip, c.port)
		}

	case "info/overview", "system", "status":
		// 系统信息（info/overview 是 MCSManager daemon 的标准事件名）
		// 如果 status 不是 200，说明请求失败（如未认证）
		if packet.Status != 200 {
			log.Printf("[MCSM] info/overview 请求失败: status=%d, data=%s", packet.Status, string(packet.Data))
			return
		}
		var sysInfo struct {
			Version string  `json:"version"`
			Process struct {
				CPU    float64 `json:"cpu"`
				Memory float64 `json:"memory"`
			} `json:"process"`
			Instance struct {
				Running int `json:"running"`
				Total   int `json:"total"`
			} `json:"instance"`
			System struct {
				Type     string  `json:"type"`
				Hostname string  `json:"hostname"`
				Platform string  `json:"platform"`
				Release  string  `json:"release"`
				Uptime   int64   `json:"uptime"`
				CpuUsage float64 `json:"cpuUsage"`
				MemUsage float64 `json:"memUsage"`
				Totalmem float64 `json:"totalmem"`
				Freemem  float64 `json:"freemem"`
			} `json:"system"`
		}
		if err := json.Unmarshal(packet.Data, &sysInfo); err == nil {
			c.info.Version = sysInfo.Version
			c.info.CPUUsage = sysInfo.System.CpuUsage * 100
			c.info.MemUsage = sysInfo.System.MemUsage * 100
			c.info.MemTotal = sysInfo.System.Totalmem / (1024 * 1024 * 1024) // 转为 GB
			c.info.MemUsed = (sysInfo.System.Totalmem - sysInfo.System.Freemem) / (1024 * 1024 * 1024)
			c.info.Running = sysInfo.Instance.Running
			c.info.Total = sysInfo.Instance.Total
			c.info.Hostname = sysInfo.System.Hostname
			c.info.SystemType = sysInfo.System.Type
			c.info.Platform = sysInfo.System.Platform
			c.info.Uptime = sysInfo.System.Uptime
			log.Printf("[MCSM] 系统信息已更新: 版本=%s, CPU=%.1f%%, 内存=%.1f%%, 总内存=%.2fGB, 已用=%.2fGB, 实例=%d/%d, 平台=%s",
				sysInfo.Version, c.info.CPUUsage, c.info.MemUsage, c.info.MemTotal, c.info.MemUsed,
				c.info.Running, c.info.Total, c.info.Platform)
		} else {
			log.Printf("[MCSM] 解析系统信息失败: %v, 原始数据: %s", err, string(packet.Data))
		}

	case "instance/list":
		// 实例列表
		var instances struct {
			Running int `json:"running"`
			Total   int `json:"total"`
		}
		if err := json.Unmarshal(packet.Data, &instances); err == nil {
			c.info.Running = instances.Running
			c.info.Total = instances.Total
		}
	}
}

// requestInfoOverviewLocked 在已持有锁的情况下请求系统概览信息
func (c *MCSMClient) requestInfoOverviewLocked() {
	if !c.connected || c.conn == nil || !c.authenticated {
		return
	}
	uuid := fmt.Sprintf("%d", time.Now().UnixNano())
	reqPacket := fmt.Sprintf(`{"uuid":"%s","status":200,"event":"info/overview","data":null}`, uuid)
	c.conn.WriteMessage(websocket.TextMessage, []byte(`42["info/overview",`+reqPacket+`]`))
}

// Disconnect 断开连接
func (c *MCSMClient) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return
	}

	c.connected = false
	c.info.Available = false
	select {
	case c.stopChan <- true:
	default:
	}

	if c.conn != nil {
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.conn.Close()
		c.conn = nil
	}

	log.Printf("[MCSM] 已断开 MCSManager daemon %s:%d", c.ip, c.port)
}

// GetInfo 获取节点信息
func (c *MCSMClient) GetInfo() MCSMNodeInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// IsConnected 检查是否已连接
func (c *MCSMClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// ===== MCSManager 节点管理器 =====
var mcsmClients = make(map[string]*MCSMClient)
var mcsmClientsMu sync.RWMutex

// AddMCSMNode 添加并连接 MCSManager 节点
// 注意：此函数不阻塞等待连接完成，连接在后台异步进行
// 这样多个节点可以并行连接，互不阻塞
func AddMCSMNode(id, ip string, port int, key string) (*MCSMClient, error) {
	// 1. 锁内：清理旧连接并注册新客户端（快速操作，不涉及网络）
	mcsmClientsMu.Lock()
	if old, exists := mcsmClients[id]; exists {
		delete(mcsmClients, id)
		mcsmClientsMu.Unlock()
		old.Disconnect() // 不持锁做断开（涉及网络关闭）
		mcsmClientsMu.Lock()
	}
	client := NewMCSMClient(ip, port, key)
	mcsmClients[id] = client
	mcsmClientsMu.Unlock()

	// 2. 锁外：异步连接（网络操作，可能耗时 10 秒+）
	// 多个节点可并行连接，互不阻塞
	go func() {
		err := client.Connect()
		if err != nil {
			log.Printf("[MCSM] 首次连接失败: %v，启动自动重连...", err)
			// 即使首次连接失败，也启动 receiveLoop 来自动重连
			go client.receiveLoop()
		}
	}()

	return client, nil
}

// RemoveMCSMNode 移除 MCSManager 节点
func RemoveMCSMNode(id string) {
	mcsmClientsMu.Lock()
	defer mcsmClientsMu.Unlock()

	if client, exists := mcsmClients[id]; exists {
		client.Disconnect()
		delete(mcsmClients, id)
	}
}

// GetMCSMNodeInfo 获取 MCSManager 节点信息
func GetMCSMNodeInfo(id string) (MCSMNodeInfo, bool) {
	mcsmClientsMu.RLock()
	defer mcsmClientsMu.RUnlock()

	if client, exists := mcsmClients[id]; exists {
		return client.GetInfo(), true
	}
	return MCSMNodeInfo{}, false
}

// GetAllMCSMNodesInfo 获取所有 MCSManager 节点信息
func GetAllMCSMNodesInfo() []MCSMNodeInfo {
	mcsmClientsMu.RLock()
	defer mcsmClientsMu.RUnlock()

	var infos []MCSMNodeInfo
	for _, client := range mcsmClients {
		infos = append(infos, client.GetInfo())
	}
	return infos
}

// ===== HTTP API 处理函数 =====

// handleMCSMAddNode 添加 MCSManager 节点
func handleMCSMAddNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID   string `json:"id"`
		IP   string `json:"ip"`
		Port int    `json:"port"`
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}

	if req.IP == "" || req.Port == 0 || req.Key == "" {
		sendJSON(w, map[string]interface{}{"success": false, "message": "IP、端口、密钥不能为空"})
		return
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("mcsm-%s-%d", req.IP, req.Port)
	}

	client, err := AddMCSMNode(req.ID, req.IP, req.Port, req.Key)
	if err != nil {
		sendJSON(w, map[string]interface{}{
			"success": false,
			"message": "连接 MCSManager daemon 失败: " + err.Error(),
		})
		return
	}

	info := client.GetInfo()
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "MCSManager 节点连接成功",
		"node":    info,
	})
}

// handleMCSMRemoveNode 移除 MCSManager 节点
func handleMCSMRemoveNode(w http.ResponseWriter, r *http.Request) {
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

	RemoveMCSMNode(req.ID)
	sendJSON(w, map[string]interface{}{
		"success": true,
		"message": "MCSManager 节点已移除",
	})
}

// handleMCSMNodes 获取所有 MCSManager 节点信息
func handleMCSMNodes(w http.ResponseWriter, r *http.Request) {
	infos := GetAllMCSMNodesInfo()
	if infos == nil {
		infos = []MCSMNodeInfo{}
	}
	sendJSON(w, map[string]interface{}{
		"success": true,
		"nodes":   infos,
	})
}

// handleMCSMTestNode 测试 MCSManager 节点连接（不保持连接，自动兼容 v9.x/v10.x）
func handleMCSMTestNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
		Key  string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, map[string]interface{}{"success": false, "message": "参数解析失败"})
		return
	}

	// 自动尝试 EIO=4（v10.x）和 EIO=3（v9.x）
	for _, eio := range []int{4, 3} {
		url := fmt.Sprintf("http://%s:%d/socket.io/?EIO=%d&transport=polling&key=%s", req.IP, req.Port, eio, req.Key)
		client := http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		text := string(body)
		if len(text) > 0 && text[0] == '0' {
			jsonPart := text[1:]
			if strings.HasPrefix(jsonPart, ":") {
				jsonPart = jsonPart[1:]
			}
			var handshake struct {
				Sid string `json:"sid"`
			}
			if err := json.Unmarshal([]byte(jsonPart), &handshake); err == nil && handshake.Sid != "" {
				mcsmVer := "v10.x"
				if eio == 3 {
					mcsmVer = "v9.x"
				}
				sendJSON(w, map[string]interface{}{
					"success": true,
					"message": "MCSManager daemon 连接成功 (" + mcsmVer + ")",
					"version": mcsmVer,
				})
				return
			}
		}
	}

	sendJSON(w, map[string]interface{}{
		"success": false,
		"message": "MCSManager daemon 连接失败（v9.x/v10.x 均无法连接），请检查 IP、端口、密钥",
	})
}
