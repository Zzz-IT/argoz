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

// DoctorCheck 单项自检结果。
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | error
	Detail string `json:"detail"`
	Repair string `json:"repair,omitempty"`
}

// DoctorReport 自检汇总。
type DoctorReport struct {
	Healthy        bool          `json:"healthy"`
	Checks         []DoctorCheck `json:"checks"`
	Summary        string        `json:"summary"`
	MemTotalMB     int           `json:"mem_total_mb"`
	MemAvailableMB int           `json:"mem_available_mb"`
	LowMemory      bool          `json:"low_memory"`
}

// ArtifactStatus 二进制资源状态。
type ArtifactStatus struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Healthy   bool   `json:"healthy"`
	Cached    bool   `json:"cached"`
	Size      int64  `json:"size"`
	LastError string `json:"last_error,omitempty"`
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
