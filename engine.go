package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Engine 节点编排核心
type Engine struct {
	mu      sync.RWMutex
	cfg     *Config
	rt      map[string]*nodeRuntime
	dataDir string

	cancel context.CancelFunc
	daemon bool
	health *healthState
}

func NewEngine(cfg *Config, dataDir string) *Engine {
	return &Engine{cfg: cfg, rt: map[string]*nodeRuntime{}, dataDir: dataDir, health: newHealthState()}
}

// SetDaemon 标记是否运行在守护模式（资源泄漏时允许退出交系统拉起）
func (e *Engine) SetDaemon(v bool) {
	e.mu.Lock()
	e.daemon = v
	e.mu.Unlock()
}

// ---------- 查询 ----------

func (e *Engine) Settings() Settings {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Settings
}

func (e *Engine) Nodes() []Node {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Node, 0, len(e.cfg.Nodes))
	for _, n := range e.cfg.Nodes {
		out = append(out, *n)
	}
	return out
}

func (e *Engine) Node(id string) (Node, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	n := e.cfg.find(id)
	if n == nil {
		return Node{}, false
	}
	return *n, true
}

// NodeStatus 面板展示用的快照
type NodeStatus struct {
	Node      Node
	State     string
	Err       string
	Conns     int64
	Total     int64
	Up        int64
	Down      int64
	NextRetry time.Time
	Restarts  int
}

func (e *Engine) Statuses() []NodeStatus {
	e.mu.RLock()
	nodes := make([]Node, 0, len(e.cfg.Nodes))
	for _, n := range e.cfg.Nodes {
		nodes = append(nodes, *n)
	}
	rts := make(map[string]*nodeRuntime, len(e.rt))
	for k, v := range e.rt {
		rts[k] = v
	}
	e.mu.RUnlock()

	out := make([]NodeStatus, 0, len(nodes))
	for _, n := range nodes {
		st := NodeStatus{Node: n, State: "down"}
		if rt, ok := rts[n.ID]; ok {
			state, errMsg := rt.status()
			st.State = state
			st.Err = errMsg
			st.Conns = rt.conns.Load()
			st.Total = rt.total.Load()
			st.Up = rt.up.Load()
			st.Down = rt.down.Load()

			if rt.tun != nil {
				ts, terr, next, restarts := rt.tun.Info()
				if state != "err" && ts != "" {
					st.State = ts
				}
				if terr != "" {
					st.Err = terr
				}
				st.NextRetry = next
				st.Restarts = restarts
			}
		}
		out = append(out, st)
	}
	return out
}

// Totals 全局流量与连接数
func (e *Engine) Totals() (nodes, conns int, up, down int64) {
	e.mu.RLock()
	rts := make([]*nodeRuntime, 0, len(e.rt))
	for _, v := range e.rt {
		rts = append(rts, v)
	}
	e.mu.RUnlock()

	for _, rt := range rts {
		nodes++
		conns += int(rt.conns.Load())
		up += rt.up.Load()
		down += rt.down.Load()
	}
	return
}

// ---------- 生命周期 ----------

// Start 启动全部节点（端口被占则自动顺延）
func (e *Engine) Start() {
	for _, n := range e.Nodes() {
		if err := e.startNode(n.ID); err != nil {
			LogErr("节点 [%s] 启动失败: %v", n.Name, err)
		}
	}
}

// Shutdown 停止全部节点
func (e *Engine) Shutdown() {
	e.StopHealth()
	e.mu.Lock()
	rts := make([]*nodeRuntime, 0, len(e.rt))
	for _, v := range e.rt {
		rts = append(rts, v)
	}
	e.mu.Unlock()

	for _, rt := range rts {
		if rt.tun != nil {
			rt.tun.Stop()
		}
		rt.stopServer()
	}
}

func (e *Engine) startNode(id string) error {
	e.mu.Lock()
	n := e.cfg.find(id)
	if n == nil {
		e.mu.Unlock()
		return errors.New("节点不存在")
	}
	node := *n
	settings := e.cfg.Settings
	e.mu.Unlock()

	ln, port, err := e.bindNode(&node, settings)
	if err != nil {
		return err
	}

	if port != node.Port {
		old := node.Port
		node.Port = port
		e.mu.Lock()
		if p := e.cfg.find(id); p != nil {
			p.Port = port
		}
		_ = e.cfg.Save()
		e.mu.Unlock()
		LogWarn("节点 [%s] 原端口 %d 被占用，已自动调整为 %d", node.Name, old, port)
	}

	rt := &nodeRuntime{eng: e, node: node, settings: settings, state: "down"}
	rt.startServer(ln)

	e.mu.Lock()
	e.rt[id] = rt
	e.mu.Unlock()

	rt.tun = NewTunnel(e, id)
	rt.tun.Start()

	return nil
}

// bindNode 优先用节点原端口，冲突则顺延
func (e *Engine) bindNode(n *Node, s Settings) (net.Listener, int, error) {
	addr := net.JoinHostPort(s.Listen, strconv.Itoa(n.Port))
	if ln, err := net.Listen("tcp", addr); err == nil {
		return ln, n.Port, nil
	}
	port, ln, err := e.allocPort(s, n.ID)
	if err != nil {
		return nil, 0, err
	}
	return ln, port, nil
}

// allocPort 在端口段内找一个既未被配置占用、又能真实监听的端口
func (e *Engine) allocPort(s Settings, excludeID string) (int, net.Listener, error) {
	lo, hi, err := s.PortBounds()
	if err != nil {
		return 0, nil, err
	}

	e.mu.RLock()
	used := map[int]bool{}
	for _, n := range e.cfg.Nodes {
		if n.ID == excludeID {
			continue
		}
		used[n.Port] = true
	}
	e.mu.RUnlock()

	for p := lo; p <= hi; p++ {
		if used[p] {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(s.Listen, strconv.Itoa(p)))
		if err != nil {
			continue
		}
		return p, ln, nil
	}
	return 0, nil, fmt.Errorf("端口段 %s 已用尽，请到设置中调整", s.PortRange)
}

// ---------- 增删改 ----------

// NodeSpec 新建节点的入参（Port/Path/UUID/Password 为空即自动生成）
type NodeSpec struct {
	Name     string
	Protocol string
	Mode     string
	Token    string
	Domain   string
	Port     int
	Path     string
	UUID     string
	Password string
}

// CheckPortFree 校验端口可用：既未被其他节点占用，也能真实绑定
func (e *Engine) CheckPortFree(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口需为 1-65535")
	}
	for _, n := range e.Nodes() {
		if n.Port == port {
			return fmt.Errorf("端口 %d 已被节点 [%s] 占用", port, n.Name)
		}
	}
	settings := e.Settings()
	ln, err := net.Listen("tcp", net.JoinHostPort(settings.Listen, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("端口 %d 无法监听（可能被系统占用）", port)
	}
	ln.Close()
	return nil
}

// FindByCredential 查找使用该凭据的节点名（用于重复告警）
func (e *Engine) FindByCredential(cred string) string {
	cred = strings.TrimSpace(cred)
	if cred == "" {
		return ""
	}
	for _, n := range e.Nodes() {
		if n.Credential() == cred {
			return n.Name
		}
	}
	return ""
}

// Create 创建节点：校验 -> 分配端口 -> 落盘(提交点) -> 起监听 -> 起隧道
func (e *Engine) Create(spec NodeSpec) (Node, error) {
	settings := e.Settings()

	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Node{}, errors.New("名称不能为空")
	}
	proto := strings.TrimSpace(spec.Protocol)
	if proto != "vless" && proto != "trojan" {
		return Node{}, fmt.Errorf("不支持的协议: %s", proto)
	}
	mode := strings.TrimSpace(spec.Mode)
	if mode == "" {
		mode = "temp"
	}

	path := strings.TrimSpace(spec.Path)
	if path != "" {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t") {
			return Node{}, errors.New("WS 路径需以 / 开头且不含空白")
		}
	}
	if path == "" {
		path = randomPath()
	}

	// 凭据：先校验再绑定端口，避免占用资源
	var uuid, password string
	if proto == "trojan" {
		password = strings.TrimSpace(spec.Password)
		if password == "" {
			password = randHex(16)
		} else if len(password) < 8 || len(password) > 64 || strings.ContainsAny(password, " \t") {
			return Node{}, errors.New("密码需为 8-64 位且不含空白")
		}
	} else {
		uuid = strings.TrimSpace(spec.UUID)
		if uuid == "" {
			uuid = newUUID()
		} else {
			if _, err := parseUUID(uuid); err != nil {
				return Node{}, fmt.Errorf("UUID 非法: %w", err)
			}
			uuid = normalizeUUID(uuid)
		}
	}

	// 端口：手填则严格校验，留空则自动分配
	var ln net.Listener
	var port int
	var err error
	if spec.Port != 0 {
		if err := e.CheckPortFree(spec.Port); err != nil {
			return Node{}, err
		}
		port = spec.Port
		ln, err = net.Listen("tcp", net.JoinHostPort(settings.Listen, strconv.Itoa(port)))
		if err != nil {
			return Node{}, fmt.Errorf("端口 %d 无法监听: %w", port, err)
		}
	} else {
		port, ln, err = e.allocPort(settings, "")
		if err != nil {
			return Node{}, err
		}
	}

	n := &Node{
		Name:     name,
		Protocol: proto,
		Port:     port,
		Path:     path,
		UUID:     uuid,
		Password: password,
		Tunnel:   TunnelCfg{Mode: mode, Token: strings.TrimSpace(spec.Token), Domain: strings.TrimSpace(spec.Domain)},
		Created:  time.Now().Format(time.RFC3339),
	}

	e.mu.Lock()
	n.ID = e.cfg.nextID()
	e.cfg.Nodes = append(e.cfg.Nodes, n)
	saveErr := e.cfg.Save()
	node := *n
	e.mu.Unlock()

	if saveErr != nil {
		ln.Close()
		return Node{}, fmt.Errorf("写配置失败: %w", saveErr)
	}

	rt := &nodeRuntime{eng: e, node: node, settings: settings, state: "down"}
	rt.startServer(ln)

	e.mu.Lock()
	e.rt[node.ID] = rt
	e.mu.Unlock()

	rt.tun = NewTunnel(e, node.ID)
	rt.tun.Start()

	return node, nil
}

// Update 修改节点信息，热生效
func (e *Engine) Update(id string, mutate func(*Node) error) error {
	e.mu.Lock()
	n := e.cfg.find(id)
	if n == nil {
		e.mu.Unlock()
		return errors.New("节点不存在")
	}
	before := *n
	if err := mutate(n); err != nil {
		e.mu.Unlock()
		return err
	}
	node := *n
	e.mu.Unlock()

	rt := e.runtimeOf(id)
	if rt == nil {
		return e.cfg.Save()
	}

	needRebind := before.Port != node.Port || before.Path == ""
	switch {
	case needRebind:
		rt.stopServer()
		ln, port, err := e.bindNode(&node, rt.settings)
		if err != nil {
			return err
		}
		node.Port = port
		e.mu.Lock()
		if p := e.cfg.find(id); p != nil {
			p.Port = port
		}
		_ = e.cfg.Save()
		e.mu.Unlock()
		rt.mu.Lock()
		rt.node = node
		rt.mu.Unlock()
		rt.startServer(ln)
		if rt.tun != nil {
			rt.tun.Stop()
			rt.tun = NewTunnel(e, id)
			rt.tun.Start()
		}
	default:
		rt.mu.Lock()
		rt.node = node
		rt.mu.Unlock()
		e.mu.Lock()
		_ = e.cfg.Save()
		e.mu.Unlock()
	}
	return nil
}

// Delete 删除节点
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	idx := -1
	for i, n := range e.cfg.Nodes {
		if n.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		e.mu.Unlock()
		return errors.New("节点不存在")
	}
	e.cfg.Nodes = append(e.cfg.Nodes[:idx], e.cfg.Nodes[idx+1:]...)
	rt := e.rt[id]
	delete(e.rt, id)
	saveErr := e.cfg.Save()
	e.mu.Unlock()

	if rt != nil {
		if rt.tun != nil {
			rt.tun.Stop()
		}
		rt.stopServer()
	}
	return saveErr
}

// SaveSettings 应用新设置
func (e *Engine) SaveSettings(s Settings) error {
	SetLevelByName(s.LogLevel)
	e.mu.Lock()
	e.cfg.Settings = s
	err := e.cfg.Save()
	rts := make([]*nodeRuntime, 0, len(e.rt))
	for _, v := range e.rt {
		rts = append(rts, v)
	}
	e.mu.Unlock()

	for _, rt := range rts {
		rt.mu.Lock()
		rt.settings = s
		rt.mu.Unlock()
	}
	return err
}

// SetDomain 隧道抓到新域名后回写配置
func (e *Engine) SetDomain(id, domain string) {
	e.mu.Lock()
	n := e.cfg.find(id)
	if n == nil || n.Tunnel.Domain == domain {
		e.mu.Unlock()
		return
	}
	old := n.Tunnel.Domain
	n.Tunnel.Domain = domain
	_ = e.cfg.Save()
	node := *n
	e.mu.Unlock()

	if rt := e.runtimeOf(id); rt != nil {
		rt.mu.Lock()
		rt.node = node
		rt.mu.Unlock()
	}
	if old == "" {
		LogOK("节点 [%s] 隧道已就绪: %s", node.Name, domain)
	} else {
		LogWarn("节点 [%s] 域名已更新: %s -> %s", node.Name, old, domain)
	}
}

// SetNodeError 记录节点最近错误
func (e *Engine) SetNodeError(id, msg string) {
	if rt := e.runtimeOf(id); rt != nil {
		rt.setError(msg)
	}
}

func (e *Engine) runtimeOf(id string) *nodeRuntime {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rt[id]
}

// UpdateLastError 把错误持久化到配置（供重启后显示）
func (e *Engine) persistLastError(id, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n := e.cfg.find(id); n != nil && n.LastError != msg {
		n.LastError = strings.TrimSpace(msg)
		_ = e.cfg.Save()
	}
}
