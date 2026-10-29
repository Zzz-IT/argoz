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
	Healthy        bool    `json:"healthy"`
	Checks         []Check `json:"checks"`
	Summary        string  `json:"summary"`
	MemTotalMB     int     `json:"mem_total_mb"`
	MemAvailableMB int     `json:"mem_available_mb"`
	LowMemory      bool    `json:"low_memory"`
}

const (
	repairChmod       = "chmod"
	repairCloudflared = "cloudflared"
	repairLocks       = "locks"
	repairEnv         = "env"
	repairConfig      = "config"
	repairAll         = "all"

	diskWarnMB  = 100
	diskErrorMB = 32
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
	)

	healthy, summary := summarise(checks)
	low := mem.Known && mem.TotalMB <= config.LowMemoryThresholdMB
	if cfg != nil && cfg.Settings.EffectiveLowMemory(mem.TotalMB) {
		low = true
	}
	return Report{
		Healthy:        healthy,
		Checks:         checks,
		Summary:        summary,
		MemTotalMB:     mem.TotalMB,
		MemAvailableMB: mem.AvailableMB,
		LowMemory:      low,
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
	TotalMB     int
	AvailableMB int
	Known       bool
}

func readMemoryInfo() memoryInfo {
	if runtime.GOOS != "linux" {
		return memoryInfo{}
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memoryInfo{}
	}
	values := make(map[string]int)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb < 0 {
			continue
		}
		values[key] = int(kb / 1024)
	}
	total, ok := values["MemTotal"]
	if !ok || total <= 0 {
		return memoryInfo{}
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"]
	}
	return memoryInfo{TotalMB: total, AvailableMB: available, Known: true}
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
	if cfg != nil {
		detail += "，MemoryMode=" + config.NormalizeMemoryMode(cfg.Settings.MemoryMode)
	}
	if mem.TotalMB <= config.LowMemoryThresholdMB {
		return Check{
			Name:   "内存",
			Status: StatusWarn,
			Detail: detail + fmt.Sprintf("；总内存不超过 %d MB，建议低内存模式", config.LowMemoryThresholdMB),
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
