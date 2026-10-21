package main

import "time"

// processStart 进程启动时间（面板显示 uptime）
var processStart = time.Now()

// Snapshot 面板渲染所需的全部数据（纯数据，无 UI 概念）
type Snapshot struct {
	Version  string
	Settings Settings
	Nodes    []NodeStatus
	Conns    int
	Up       int64
	Down     int64
	Started  time.Time
}

// Controller 面板与引擎之间的唯一契约
// 本地实现是 *Engine；将来换 unix socket 实现时 UI 无需改动
type Controller interface {
	Snapshot() Snapshot
	Settings() Settings
	SaveSettings(Settings) error
	Create(name, proto, mode, token, domain string) (Node, error)
	Update(id string, mutate func(*Node) error) error
	Delete(id string) error
	RestartTunnel(id string) error
	Node(id string) (Node, bool)
	TailLog(n int) ([]string, error)
}

func (e *Engine) Snapshot() Snapshot {
	_, conns, up, down := e.Totals()
	return Snapshot{
		Version:  AppVersion,
		Settings: e.Settings(),
		Nodes:    e.Statuses(),
		Conns:    conns,
		Up:       up,
		Down:     down,
		Started:  processStart,
	}
}

func (e *Engine) TailLog(n int) ([]string, error) {
	return tailLines(LogFilePath(), n)
}
