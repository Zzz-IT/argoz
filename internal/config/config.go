package config

import (
	"argoz/internal/xutil"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	AppName    = "argoz"
	configName = "config.json"
)

// Version 发布时由 CI 通过 -ldflags "-X argoz/internal/config.Version=vX.Y.Z" 注入
var Version = "v1.3.4"

// AppVersion 发布时由 CI 通过 -ldflags "-X main.AppVersion=v1.0.0" 注入
var AppVersion = "v1.2.0"

// TunnelCfg Argo 隧道配置
type TunnelCfg struct {
	Mode   string `json:"mode"`             // temp | fixed
	Token  string `json:"token,omitempty"`  // fixed 模式使用
	Domain string `json:"domain,omitempty"` // 程序写入 / 用户填写
}

// Node 一个节点 = 一条隧道 + 一个本地 WS 监听
type Node struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"` // vless | trojan
	Port      int       `json:"port"`
	Path      string    `json:"path"`
	UUID      string    `json:"uuid,omitempty"`
	Password  string    `json:"password,omitempty"`
	Tunnel    TunnelCfg `json:"tunnel"`
	Created   string    `json:"created"`
	LastError string    `json:"last_error,omitempty"`
}

// Credential 返回该节点的凭据（vless 用 uuid，trojan 用 password）
func (n Node) Credential() string {
	if n.Protocol == "trojan" {
		return n.Password
	}
	return n.UUID
}

// Settings 全局设置
type Settings struct {
	Listen         string `json:"listen"`
	PortRange      string `json:"port_range"`
	MaxConns       int    `json:"max_conns"`
	Keepalive      string `json:"keepalive"`
	IdleTimeout    string `json:"idle_timeout"`
	UDPEnabled     bool   `json:"udp_enabled"`
	UDPIdle        string `json:"udp_idle"`
	MaxUDPSessions int    `json:"max_udp_sessions"`
	ECH            bool   `json:"ech"`
	LogLevel       string `json:"log_level"`
}

func (s Settings) KeepaliveDur() time.Duration {
	d, err := time.ParseDuration(s.Keepalive)
	if err != nil || d < 5*time.Second {
		return 30 * time.Second
	}
	return d
}

func (s Settings) IdleDur() time.Duration {
	d, err := time.ParseDuration(s.IdleTimeout)
	if err != nil || d < 10*time.Second {
		return 5 * time.Minute
	}
	return d
}

// UDPIdleDur UDP 会话空闲回收时间，默认 60s。
func (s Settings) UDPIdleDur() time.Duration {
	d, err := time.ParseDuration(s.UDPIdle)
	if err != nil || d < 10*time.Second {
		return 60 * time.Second
	}
	return d
}

// MaxUDPSessionsOrDefault 限制 UDP 会话数，默认 32，最大 128。
func (s Settings) MaxUDPSessionsOrDefault() int {
	n := s.MaxUDPSessions
	if n <= 0 {
		return 32
	}
	if n > 128 {
		return 128
	}
	return n
}

// PortBounds 解析端口段
func (s Settings) PortBounds() (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s.PortRange), "-")
	if len(parts) != 2 {
		return 0, 0, errors.New("端口段格式应为 起始-结束（如 1-65535，单个端口写 8080-8080）")
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || lo < 1 || hi > 65535 || lo > hi {
		return 0, 0, errors.New("端口段不合法（需在 1-65535 内，起始不得大于结束）")
	}
	return lo, hi, nil
}

// Config 全局配置
type Config struct {
	Version  int      `json:"version"`
	Settings Settings `json:"settings"`
	Nodes    []*Node  `json:"nodes"`

	path string
}

func DefaultSettings() Settings {
	return Settings{
		Listen:         "127.0.0.1",
		PortRange:      "1-65535",
		MaxConns:       128,
		Keepalive:      "30s",
		IdleTimeout:    "5m",
		UDPEnabled:     false,
		UDPIdle:        "60s",
		MaxUDPSessions: 32,
		ECH:            true,
		LogLevel:       "info",
	}
}

// DefaultDataDir 数据目录：优先 ARGOZ_DIR，Windows 下退化为当前目录
func DefaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv("ARGOZ_DIR")); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		return "argoz-data"
	}
	return "/usr/local/etc/argoz"
}

// LoadConfig 读取配置；不存在则创建；损坏则备份后重建
func LoadConfig(dir string) (*Config, error) {
	if dir == "" {
		dir = DefaultDataDir()
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	p := filepath.Join(dir, configName)

	cfg := &Config{Version: 1, Settings: DefaultSettings(), Nodes: []*Node{}, path: p}

	data, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("读取配置失败: %w", err)
		}
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		_ = os.WriteFile(p+".bak", data, 0o600)
		fmt.Println("[注意] 配置文件格式错误，已备份为 config.json.bak，本次以空配置启动")
		if err := cfg.Save(); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	loaded.path = p
	loaded.fixup()
	return &loaded, nil
}

func (c *Config) fixup() {
	d := DefaultSettings()
	if c.Version == 0 {
		c.Version = 1
	}
	// v2：默认端口段从 20000-20100 放开到全端口（只迁移一次）
	if c.Version < 2 {
		if c.Settings.PortRange == "20000-20100" {
			c.Settings.PortRange = d.PortRange
		}
		c.Version = 2
	}
	if c.Settings.Listen == "" {
		c.Settings.Listen = d.Listen
	}
	if c.Settings.PortRange == "" {
		c.Settings.PortRange = d.PortRange
	}
	if c.Settings.MaxConns <= 0 {
		c.Settings.MaxConns = d.MaxConns
	}
	if c.Settings.MaxConns > 4096 {
		c.Settings.MaxConns = 4096
	}
	if c.Settings.Keepalive == "" {
		c.Settings.Keepalive = d.Keepalive
	}
	if c.Settings.IdleTimeout == "" {
		c.Settings.IdleTimeout = d.IdleTimeout
	}
	if c.Settings.UDPIdle == "" {
		c.Settings.UDPIdle = d.UDPIdle
	}
	if c.Settings.MaxUDPSessions <= 0 {
		c.Settings.MaxUDPSessions = d.MaxUDPSessions
	}
	if c.Settings.MaxUDPSessions > 128 {
		c.Settings.MaxUDPSessions = 128
	}
	if c.Settings.LogLevel == "" {
		c.Settings.LogLevel = d.LogLevel
	}
	if c.Nodes == nil {
		c.Nodes = []*Node{}
	}
	for _, n := range c.Nodes {
		if n.Protocol == "" {
			n.Protocol = "vless"
		}
		if n.Path == "" {
			n.Path = xutil.RandomPath()
		}
		if n.Tunnel.Mode == "" {
			n.Tunnel.Mode = "temp"
		}
	}
}

// Save 原子保存配置
func (c *Config) Save() error {
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	return xutil.AtomicWrite(c.path, out, 0o600)
}

func (c *Config) Find(id string) *Node {
	for _, n := range c.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (c *Config) NextID() string {
	max := 0
	for _, n := range c.Nodes {
		if strings.HasPrefix(n.ID, "n") {
			if v, err := strconv.Atoi(n.ID[1:]); err == nil && v > max {
				max = v
			}
		}
	}
	return "n" + strconv.Itoa(max+1)
}

// Dir 返回配置所在目录
func (c *Config) Dir() string { return filepath.Dir(c.path) }
