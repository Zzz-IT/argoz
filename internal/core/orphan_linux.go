//go:build linux

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// cleanupOrphanCloudflared 在 daemon 启动时清理上一次异常退出留下的 cloudflared。
// 先按本数据目录的 pid 文件清理（可覆盖已删除节点），再按当前配置精确匹配 /proc，
// 不使用 pkill -f 这种宽泛匹配。
func CleanupOrphanCloudflared(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	if os.Geteuid() != 0 {
		LogDebug("非 root 运行：只能清理与当前用户同属主的 cloudflared")
	}
	var cleaned []string
	seen := make(map[int]bool)

	// 1) pid 文件：节点可能已被删除，不能只依赖当前配置。
	runDir := filepath.Join(cfg.Dir(), "run")
	if entries, err := os.ReadDir(runDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
				continue
			}
			path := filepath.Join(runDir, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil || pid <= 1 || pid == os.Getpid() {
				_ = os.Remove(path)
				continue
			}
			if !pidAlive(pid) {
				_ = os.Remove(path)
				continue
			}
			args, err := readProcArgs(pid)
			if err != nil || len(args) == 0 || !isCloudflaredProcess(args[0]) || !hasArg(args, "--no-autoupdate") {
				_ = os.Remove(path)
				continue
			}
			if terminatePID(pid) {
				cleaned = append(cleaned, fmt.Sprintf("已清理遗留 cloudflared pid=%d（pidfile）", pid))
				seen[pid] = true
			}
			_ = os.Remove(path)
		}
	}

	// 2) 当前配置匹配：兼容旧版本没有 pid 文件、或升级前遗留的进程。
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return cleaned
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == os.Getpid() || seen[pid] {
			continue
		}
		args, err := readProcArgs(pid)
		if err != nil || len(args) == 0 {
			continue
		}
		for _, n := range cfg.Nodes {
			if n == nil || !orphanMatchesNode(args, *n) {
				continue
			}
			if terminatePID(pid) {
				cleaned = append(cleaned, fmt.Sprintf("已清理遗留 cloudflared pid=%d 节点=%s", pid, n.Name))
			}
			seen[pid] = true
			break
		}
	}
	return cleaned
}

func readProcArgs(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), nil
}

func terminatePID(pid int) bool {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return false
	}
	for i := 0; i < 20; i++ {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return true
}
