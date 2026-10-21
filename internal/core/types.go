package core

type DaemonInfo struct {
	PID        int    `json:"pid"`
	UptimeSec  int64  `json:"uptime_sec"`
	MemBytes   int64  `json:"mem_bytes"`
	Goroutines int    `json:"goroutines"`
	Version    string `json:"version"`
	Nodes      int    `json:"nodes"`
	DataDir    string `json:"data_dir"`
}

type UpdateInfo struct {
	Current     string   `json:"current"`
	Latest      string   `json:"latest"`
	HasUpdate   bool     `json:"has_update"`
	PublishedAt string   `json:"published_at"`
	SizeBytes   int64    `json:"size_bytes"`
	Notes       []string `json:"notes"`
}

type UpdateResult struct {
	Replaced  bool   `json:"replaced"`
	Path      string `json:"path"`
	Restarted bool   `json:"restarted"`
	Hint      string `json:"hint"`
}

// NodePatch 面板可修改的字段（空值表示不改）
type NodePatch struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port,omitempty"`
	Path       string `json:"path,omitempty"`
	Credential string `json:"credential,omitempty"`
	Token      string `json:"token,omitempty"`
	Domain     string `json:"domain,omitempty"`
}
