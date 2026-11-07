// Package doctor provides read-only diagnostics for argoz.
//
// It intentionally does not import internal/core: core imports doctor for its
// RPC-facing diagnostics, and doctor must never trigger downloads or mutate a
// running service. The only commands it executes are short-lived
// "<binary> --version" probes.
package doctor

import (
	"argoz/internal/config"
	"argoz/internal/sys"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Status 是单项诊断状态。
type Status string

const (
	StatusOK    Status = "ok"
	StatusWarn  Status = "warn"
	StatusError Status = "error"
)

// Check 是单项诊断结果。
// Repair 是机器可读的修复 action id（可为空），不是展示文本。
// 固定取值：chmod、cloudflared、locks、env、config、all。
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"` // ok | warn | error
	Detail string `json:"detail"`
	Repair string `json:"repair,omitempty"`
}

// Report 是完整自检报告。
type Report struct {
	// Healthy 仅在存在 error 级检查时为 false；warn 表示可运行但需要关注。
	Healthy         bool    `json:"healthy"`
	Checks          []Check `json:"checks"`
	Summary         string  `json:"summary"`
	MemTotalMB      int     `json:"mem_total_mb"`
	MemAvailableMB  int     `json:"mem_available_mb"`
	LowMemory       bool    `json:"low_memory"`
	CgroupMaxMB     int     `json:"cgroup_max_mb"` // 0 表示无限制或未知
	CgroupCurrentMB int     `json:"cgroup_current_mb"`
	OOMKills        int64   `json:"oom_kills"` // cgroup memory.events 累计
	InodesFree      int64   `json:"inodes_free"`
	InodesTotal     int64   `json:"inodes_total"`
}

const (
	repairChmod       = "chmod"
	repairCloudflared = "cloudflared"
	repairLocks       = "locks"
	repairEnv         = "env"
	repairConfig      = "config"
	repairAll         = "all"
	repairOOMRecover  = "oom-recover"

	diskWarnMB  = 100
	diskErrorMB = 32

	// inodeWarnInodes inode 可用数低于该阈值时告警；InodesTotal 为 0 表示未知不参与判定。
	inodeWarnInodes = 4096
	// cgroupLimitWarnPct cgroup 已用/上限超过该百分比时告警。
	cgroupLimitWarnPct = 80
	// egressProbeTimeout 出口连通性探测超时。
	egressProbeTimeout = 3 * time.Second
)

// Run 执行一次纯只读自检。dataDir 为空时优先使用 cfg.Dir()。
// cfg 可以为 nil；此时跳过节点端口检查。
func Run(dataDir string, cfg *config.Config) Report {
	dir := resolveDataDir(dataDir, cfg)
	mem := readMemoryInfo()

	checks := make([]Check, 0, 14)
	checks = append(checks,
		checkConfigDir(dir),
		checkConfigFile(dir),
		checkArgozBinary(dir),
		checkCloudflared(dir),
		checkRPC(dir),
	)
	checks = append(checks, checkDependencies()...)
	checks = append(checks,
		checkMemory(mem, cfg),
		checkDisk(dir),
		checkNodePorts(cfg),
		checkCgroupLimit(mem),
		checkOOMEvents(mem),
		checkInodes(mem),
		checkEgress(),
	)

	healthy, summary := summarise(checks)
	// 低内存按有效内存上限判定（cgroup 优先），与运行时 ConfigureMemory 保持一致，
	// 否则容器被限到 64MB 时自检会说「内存充足」而实际已启用低内存模式。
	limitMB := mem.EffectiveLimitMB()
	low := mem.Known && limitMB > 0 && limitMB <= config.LowMemoryThresholdMB
	if cfg != nil && cfg.Settings.EffectiveLowMemory(limitMB) {
		low = true
	}
	return Report{
		Healthy:         healthy,
		Checks:          checks,
		Summary:         summary,
		MemTotalMB:      mem.TotalMB,
		MemAvailableMB:  mem.AvailableMB,
		LowMemory:       low,
		CgroupMaxMB:     int(mem.CgroupMaxMB),
		CgroupCurrentMB: int(mem.CgroupCurrentMB),
		OOMKills:        mem.OOMKills,
		InodesFree:      mem.InodesFree,
		InodesTotal:     mem.InodesTotal,
	}
}

func resolveDataDir(dataDir string, cfg *config.Config) string {
	if dataDir != "" {
		return filepath.Clean(dataDir)
	}
	if cfg != nil {
		if dir := cfg.Dir(); dir != "" {
			return filepath.Clean(dir)
		}
	}
	return filepath.Clean(config.DefaultDataDir())
}

func checkConfigDir(dir string) Check {
	st, err := os.Stat(dir)
	if err != nil {
		return Check{
			Name:   "配置目录可写",
			Status: StatusError,
			Detail: fmt.Sprintf("无法访问配置目录 %s: %v", dir, err),
			Repair: repairConfig,
		}
	}
	if !st.IsDir() {
		return Check{
			Name:   "配置目录可写",
			Status: StatusError,
			Detail: fmt.Sprintf("配置路径不是目录: %s", dir),
			Repair: repairConfig,
		}
	}
	writable, detail := probeDirectoryWritable(dir)
	if !writable {
		return Check{
			Name:   "配置目录可写",
			Status: StatusError,
			Detail: fmt.Sprintf("配置目录不可写: %s（%s）", dir, detail),
			Repair: repairConfig,
		}
	}
	return Check{
		Name:   "配置目录可写",
		Status: StatusOK,
		Detail: fmt.Sprintf("%s（%s）", dir, detail),
	}
}

func checkConfigFile(dir string) Check {
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{
				Name:   "config.json 可读",
				Status: StatusWarn,
				Detail: fmt.Sprintf("配置文件尚不存在: %s（首次启动会自动创建）", path),
				Repair: repairConfig,
			}
		}
		return Check{
			Name:   "config.json 可读",
			Status: StatusError,
			Detail: fmt.Sprintf("无法读取配置文件 %s: %v", path, err),
			Repair: repairConfig,
		}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Check{
			Name:   "config.json 可读",
			Status: StatusWarn,
			Detail: fmt.Sprintf("配置文件为空: %s", path),
			Repair: repairConfig,
		}
	}
	if !json.Valid(data) {
		return Check{
			Name:   "config.json 可读",
			Status: StatusError,
			Detail: fmt.Sprintf("配置文件不是合法 JSON: %s", path),
			Repair: repairConfig,
		}
	}
	return Check{
		Name:   "config.json 可读",
		Status: StatusOK,
		Detail: fmt.Sprintf("%s（%d 字节，JSON 合法）", path, len(data)),
	}
}

func checkArgozBinary(dir string) Check {
	path, candidates := findArgozBinary(dir)
	if path == "" {
		return Check{
			Name:   "argoz 二进制",
			Status: StatusError,
			Detail: "未找到 argoz 二进制；已查找: " + strings.Join(candidates, ", "),
			Repair: repairAll,
		}
	}
	return inspectBinary("argoz 二进制", path, repairAll)
}

func checkCloudflared(dir string) Check {
	path, candidates := findCloudflared(dir)
	if path == "" {
		return Check{
			Name:   "cloudflared 二进制",
			Status: StatusWarn,
			Detail: "未找到 cloudflared（需要隧道节点时可自动安装）；已查找: " + strings.Join(candidates, ", "),
			Repair: repairCloudflared,
		}
	}
	return inspectBinary("cloudflared 二进制", path, repairCloudflared)
}

func inspectBinary(name, path, missingRepair string) Check {
	st, err := os.Stat(path)
	if err != nil {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("无法访问 %s: %v", path, err),
			Repair: missingRepair,
		}
	}
	if !st.Mode().IsRegular() {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("路径不是普通文件: %s", path),
			Repair: missingRepair,
		}
	}
	if st.Size() == 0 {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("文件为空或已损坏: %s", path),
			Repair: missingRepair,
		}
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("文件没有执行权限: %s（mode=%s）", path, st.Mode().Perm()),
			Repair: repairChmod,
		}
	}
	if runtime.GOOS == "linux" && !isELF(path) {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("文件不是 ELF 可执行文件: %s", path),
			Repair: missingRepair,
		}
	}
	version, err := binaryVersion(path)
	if err != nil {
		return Check{
			Name:   name,
			Status: StatusError,
			Detail: fmt.Sprintf("%s 执行 --version 失败: %v", path, err),
			Repair: missingRepair,
		}
	}
	detail := path
	if version != "" {
		detail += "（" + version + "）"
	}
	if runtime.GOOS != "linux" {
		detail += "；当前平台跳过 ELF 校验"
	}
	return Check{Name: name, Status: StatusOK, Detail: detail}
}

func findArgozBinary(dir string) (string, []string) {
	candidates := make([]string, 0, 5)
	if p, err := exec.LookPath("argoz"); err == nil {
		candidates = append(candidates, p)
	}
	if p, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		candidates = append(candidates, p)
	}
	name := "argoz"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates = append(candidates, filepath.Join(dir, name))
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/usr/local/bin/argoz")
	}
	return firstRegularFile(candidates)
}

func findCloudflared(dir string) (string, []string) {
	candidates := make([]string, 0, 5)
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, p)
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/usr/local/bin/"+name)
	}
	candidates = append(candidates, filepath.Join(dir, name))
	if defaultDir := config.DefaultDataDir(); filepath.Clean(defaultDir) != filepath.Clean(dir) {
		candidates = append(candidates, filepath.Join(defaultDir, name))
	}
	return firstRegularFile(candidates)
}

func firstRegularFile(candidates []string) (string, []string) {
	unique := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		cleaned := filepath.Clean(candidate)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		unique = append(unique, cleaned)
		if st, err := os.Stat(cleaned); err == nil && st.Mode().IsRegular() {
			return cleaned, unique
		}
	}
	return "", unique
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var header [4]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return false
	}
	return header[0] == 0x7f && header[1] == 'E' && header[2] == 'L' && header[3] == 'F'
}

func binaryVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("执行超时")
	}
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, firstLine(text))
	}
	return firstLine(text), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func checkRPC(dir string) Check {
	sockPath := filepath.Join(dir, "argoz.sock")
	addrPath := filepath.Join(dir, "argoz.addr")

	if st, err := os.Stat(sockPath); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return Check{
				Name:   "RPC socket",
				Status: StatusError,
				Detail: fmt.Sprintf("%s 存在但不是 socket，可能是残留文件", sockPath),
				Repair: repairLocks,
			}
		}
		conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err != nil {
			return Check{
				Name:   "RPC socket",
				Status: StatusError,
				Detail: fmt.Sprintf("%s 存在但无法连接（可能是残留 socket）: %v", sockPath, err),
				Repair: repairLocks,
			}
		}
		_ = conn.Close()
		return Check{Name: "RPC socket", Status: StatusOK, Detail: sockPath + " 可连接"}
	} else if !os.IsNotExist(err) {
		return Check{
			Name:   "RPC socket",
			Status: StatusError,
			Detail: fmt.Sprintf("无法访问 %s: %v", sockPath, err),
			Repair: repairLocks,
		}
	}

	addrData, err := os.ReadFile(addrPath)
	if err == nil {
		endpoint := strings.TrimSpace(string(addrData))
		if _, _, splitErr := net.SplitHostPort(endpoint); splitErr != nil {
			return Check{
				Name:   "RPC socket",
				Status: StatusError,
				Detail: fmt.Sprintf("%s 内容无效（可能是残留地址）: %q", addrPath, endpoint),
				Repair: repairLocks,
			}
		}
		conn, dialErr := net.DialTimeout("tcp", endpoint, 500*time.Millisecond)
		if dialErr != nil {
			return Check{
				Name:   "RPC socket",
				Status: StatusError,
				Detail: fmt.Sprintf("%s 指向的 RPC 地址无法连接（可能是残留地址）: %v", addrPath, dialErr),
				Repair: repairLocks,
			}
		}
		_ = conn.Close()
		return Check{Name: "RPC socket", Status: StatusOK, Detail: "TCP RPC 回退可用: " + endpoint}
	}
	if err != nil && !os.IsNotExist(err) {
		return Check{
			Name:   "RPC socket",
			Status: StatusError,
			Detail: fmt.Sprintf("无法读取 %s: %v", addrPath, err),
			Repair: repairLocks,
		}
	}
	return Check{
		Name:   "RPC socket",
		Status: StatusWarn,
		Detail: "RPC socket/addr 不存在（daemon 可能未运行）",
	}
}

func checkDependencies() []Check {
	if runtime.GOOS != "linux" {
		return []Check{{
			Name:   "系统依赖文件",
			Status: StatusWarn,
			Detail: fmt.Sprintf("当前平台 %s 不支持 CA/tzdata/ELF loader 检查", runtime.GOOS),
		}}
	}
	checks := []Check{checkCA(), checkTZData(), checkLoader()}
	envReport := sys.CheckRuntimeEnvReport()
	if envReport.Alpine {
		check := Check{
			Name:   "Alpine 运行时包",
			Status: StatusOK,
			Detail: envReport.Summary,
		}
		if len(envReport.Missing) > 0 {
			check.Status = StatusWarn
			check.Repair = repairEnv
			check.Detail = envReport.Summary
		}
		checks = append(checks, check)
	}
	return checks
}

func checkCA() Check {
	candidates := []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/ssl/cert.pem",
		"/etc/pki/tls/certs/ca-bundle.crt",
	}
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return Check{Name: "CA 证书", Status: StatusOK, Detail: path}
		}
	}
	return Check{
		Name:   "CA 证书",
		Status: StatusError,
		Detail: "未找到 CA 证书 bundle，HTTPS 探测/下载可能失败",
		Repair: repairEnv,
	}
}

func checkTZData() Check {
	path := "/usr/share/zoneinfo"
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return Check{Name: "时区数据", Status: StatusOK, Detail: path}
	}
	return Check{
		Name:   "时区数据",
		Status: StatusWarn,
		Detail: "未找到时区数据目录 /usr/share/zoneinfo",
		Repair: repairEnv,
	}
}

func checkLoader() Check {
	path := doctorLoaderPath()
	if path == "" {
		return Check{
			Name:   "动态加载器",
			Status: StatusWarn,
			Detail: fmt.Sprintf("当前架构 %s 没有预置 ELF loader 检查路径", runtime.GOARCH),
		}
	}
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return Check{Name: "动态加载器", Status: StatusOK, Detail: path}
	}
	return Check{
		Name:   "动态加载器",
		Status: StatusError,
		Detail: "缺少动态加载器: " + path,
		Repair: repairEnv,
	}
}

func doctorLoaderPath() string {
	switch runtime.GOARCH {
	case "amd64":
		return "/lib/ld-linux-x86-64.so.2"
	case "arm64":
		return "/lib/ld-linux-aarch64.so.1"
	case "386":
		return "/lib/ld-linux.so.2"
	default:
		return ""
	}
}

type memoryInfo struct {
	TotalMB         int
	AvailableMB     int
	Known           bool
	CgroupMaxMB     int
	CgroupCurrentMB int
	OOMKills        int64
	InodesFree      int64
	InodesTotal     int64
}

// EffectiveLimitMB 返回用于低内存判定的有效内存上限（MB）：cgroup 上限优先，
// 与 config.detectTotalMemoryMB / sys.MemInfo.EffectiveLimitMB 同一口径。
// 容器被限制到 64MB 而宿主有 16GB 时应按 64MB 判定，否则低内存模式永不触发。
func (m memoryInfo) EffectiveLimitMB() int {
	if m.CgroupMaxMB > 0 {
		return m.CgroupMaxMB
	}
	return m.TotalMB
}

// readMemoryInfo 读取 /proc/meminfo、cgroup 与 statfs 信息。
// 保留现有 Known/TotalMB/AvailableMB 语义不动，只在有 sys 可用时补齐 cgroup/inode 字段。
func readMemoryInfo() memoryInfo {
	info := sys.ReadMemInfo()
	out := memoryInfo{
		TotalMB:         info.TotalMB,
		AvailableMB:     info.AvailableMB,
		CgroupMaxMB:     info.CgroupMaxMB,
		CgroupCurrentMB: info.CgroupCurrentMB,
		OOMKills:        info.OOMKills,
		InodesFree:      info.InodesFree,
		InodesTotal:     info.InodesTotal,
	}
	// Known 沿用 sys.MemInfo.WithDerived 的口径：cgroup 数据也算已知，
	// 否则纯 cgroup 环境（读不到 /proc/meminfo）会被误判为「无法读取内存」。
	out.Known = info.Known
	return out
}

func checkMemory(mem memoryInfo, cfg *config.Config) Check {
	if !mem.Known {
		return Check{
			Name:   "内存",
			Status: StatusWarn,
			Detail: fmt.Sprintf("当前平台 %s 无法读取总内存/可用内存", runtime.GOOS),
		}
	}
	detail := fmt.Sprintf("总内存 %d MB，可用 %d MB", mem.TotalMB, mem.AvailableMB)
	if mem.CgroupMaxMB > 0 {
		detail += fmt.Sprintf("，cgroup 上限 %d MB（当前 %d MB）", mem.CgroupMaxMB, mem.CgroupCurrentMB)
	}
	if cfg != nil {
		detail += "，MemoryMode=" + config.NormalizeMemoryMode(cfg.Settings.MemoryMode)
	}
	// 用有效上限（cgroup 优先）判低内存，而不是裸 TotalMB，避免容器里误判。
	if limit := mem.EffectiveLimitMB(); limit > 0 && limit <= config.LowMemoryThresholdMB {
		return Check{
			Name:   "内存",
			Status: StatusWarn,
			Detail: detail + fmt.Sprintf("；有效内存上限 %d MB 不超过 %d MB，建议低内存模式", limit, config.LowMemoryThresholdMB),
		}
	}
	if mem.AvailableMB > 0 && mem.AvailableMB < 32 {
		return Check{Name: "内存", Status: StatusWarn, Detail: detail + "；可用内存偏低"}
	}
	return Check{Name: "内存", Status: StatusOK, Detail: detail}
}

func checkDisk(dir string) Check {
	if runtime.GOOS != "linux" {
		return Check{
			Name:   "磁盘空间",
			Status: StatusWarn,
			Detail: fmt.Sprintf("当前平台 %s 不支持 statfs 磁盘检查", runtime.GOOS),
		}
	}
	availableMB, err := diskAvailableMB(dir)
	if err != nil {
		return Check{
			Name:   "磁盘空间",
			Status: StatusWarn,
			Detail: fmt.Sprintf("无法统计 %s 的可用磁盘空间: %v", dir, err),
		}
	}
	detail := fmt.Sprintf("%s 可用 %d MB", dir, availableMB)
	switch {
	case availableMB < diskErrorMB:
		return Check{Name: "磁盘空间", Status: StatusError, Detail: detail + "；空间严重不足"}
	case availableMB < diskWarnMB:
		return Check{Name: "磁盘空间", Status: StatusWarn, Detail: detail + "；空间偏低"}
	default:
		return Check{Name: "磁盘空间", Status: StatusOK, Detail: detail}
	}
}

func checkNodePorts(cfg *config.Config) Check {
	if cfg == nil || len(cfg.Nodes) == 0 {
		return Check{Name: "节点端口监听", Status: StatusOK, Detail: "当前没有节点"}
	}
	host := normalizeLoopback(cfg.Settings.Listen)
	allListening := true
	details := make([]string, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		if node == nil {
			continue
		}
		if node.Port < 1 || node.Port > 65535 {
			allListening = false
			details = append(details, fmt.Sprintf("%s: 端口 %d 无效", node.Name, node.Port))
			continue
		}
		addr := net.JoinHostPort(host, strconv.Itoa(node.Port))
		conn, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
		if err != nil {
			allListening = false
			details = append(details, fmt.Sprintf("%s: %s 未监听", node.Name, addr))
			continue
		}
		_ = conn.Close()
		details = append(details, fmt.Sprintf("%s: %s 可连接", node.Name, addr))
	}
	if len(details) == 0 {
		return Check{Name: "节点端口监听", Status: StatusOK, Detail: "当前没有有效节点"}
	}
	if allListening {
		return Check{Name: "节点端口监听", Status: StatusOK, Detail: strings.Join(details, "；")}
	}
	return Check{
		Name:   "节点端口监听",
		Status: StatusWarn,
		Detail: strings.Join(details, "；"),
	}
}

// checkCgroupLimit 检查 cgroup 内存上限与当前用量：提示生效上限，接近上限时告警。
// 无 cgroup 限制（CgroupMaxMB=0）时提示未设置上限。
func checkCgroupLimit(mem memoryInfo) Check {
	if mem.CgroupMaxMB <= 0 {
		return Check{
			Name:   "cgroup 内存限制",
			Status: StatusOK,
			Detail: "未设置 cgroup 内存上限（按宿主内存使用）",
		}
	}
	used := mem.CgroupCurrentMB
	pct := 0
	if mem.CgroupMaxMB > 0 {
		pct = used * 100 / mem.CgroupMaxMB
	}
	detail := fmt.Sprintf("生效上限 %d MB，当前用量 %d MB（%d%%）", mem.CgroupMaxMB, used, pct)
	switch {
	case pct >= cgroupLimitWarnPct:
		return Check{
			Name:   "cgroup 内存限制",
			Status: StatusWarn,
			Detail: detail + "；接近 cgroup 上限，建议降低负载",
			Repair: repairOOMRecover,
		}
	default:
		return Check{Name: "cgroup 内存限制", Status: StatusOK, Detail: detail}
	}
}

// checkOOMEvents 检查 cgroup oom_kill 累计：>0 时告警，说明累计次数。
func checkOOMEvents(mem memoryInfo) Check {
	if mem.OOMKills <= 0 {
		return Check{Name: "OOM 事件", Status: StatusOK, Detail: "未检测到 cgroup OOM 事件"}
	}
	return Check{
		Name:   "OOM 事件",
		Status: StatusWarn,
		Detail: fmt.Sprintf("cgroup oom_kill 累计 %d 次，建议执行 OOM 后降低负载", mem.OOMKills),
		Repair: repairOOMRecover,
	}
}

// checkInodes 检查磁盘 inode 剩余量：低于阈值时告警，参考 checkDisk 的阈值风格。
func checkInodes(mem memoryInfo) Check {
	if mem.InodesTotal <= 0 && mem.InodesFree <= 0 {
		return Check{
			Name:   "磁盘 inode",
			Status: StatusWarn,
			Detail: "无法统计 inode 使用情况（平台不支持或文件系统未返回）",
		}
	}
	detail := fmt.Sprintf("可用 inode %d / 总 %d", mem.InodesFree, mem.InodesTotal)
	if mem.InodesFree > 0 && mem.InodesFree < inodeWarnInodes {
		return Check{Name: "磁盘 inode", Status: StatusWarn, Detail: detail + "；inode 偏低，可能无法创建新文件"}
	}
	return Check{Name: "磁盘 inode", Status: StatusOK, Detail: detail}
}

// checkEgress 做一个轻量的出口连通性探测：连接一个公网 DNS 的 53 端口即可。
// 不引入第三方依赖，超时/失败时降级为 warn 而不是 panic，因为离线环境很常见。
func checkEgress() Check {
	deadline := time.Now().Add(egressProbeTimeout)
	addrs := []string{
		"1.1.1.1:53",
		"8.8.8.8:53",
		"[2001:4860:4860::8888]:53",
	}
	var lastErr error
	for _, addr := range addrs {
		remain := time.Until(deadline)
		if remain <= 0 {
			break
		}
		conn, err := net.DialTimeout("tcp", addr, remain)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.Close()
		return Check{Name: "出口连通性", Status: StatusOK, Detail: "可连接公共 DNS " + addr}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用探测地址")
	}
	return Check{
		Name:   "出口连通性",
		Status: StatusWarn,
		Detail: fmt.Sprintf("无法连接公共 DNS（可能离线或防火墙拦截）: %v", lastErr),
	}
}

func normalizeLoopback(host string) string {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0":
		return "127.0.0.1"
	case "::", "[::]":
		return "::1"
	default:
		return host
	}
}

func summarise(checks []Check) (bool, string) {
	okCount, warnCount, errorCount := 0, 0, 0
	for _, check := range checks {
		switch check.Status {
		case StatusOK:
			okCount++
		case StatusWarn:
			warnCount++
		case StatusError:
			errorCount++
		}
	}
	healthy := errorCount == 0
	switch {
	case errorCount > 0 && warnCount > 0:
		return healthy, fmt.Sprintf("自检发现 %d 个错误、%d 个警告（%d 项正常）", errorCount, warnCount, okCount)
	case errorCount > 0:
		return healthy, fmt.Sprintf("自检发现 %d 个错误（%d 项正常）", errorCount, okCount)
	case warnCount > 0:
		return healthy, fmt.Sprintf("自检通过，但有 %d 个警告（%d 项正常）", warnCount, okCount)
	default:
		return healthy, fmt.Sprintf("自检通过，%d 项检查全部正常", okCount)
	}
}
