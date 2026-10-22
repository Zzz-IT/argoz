package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"argoz/internal/config"
	"argoz/internal/rpc"
)

const (
	reloadWindow = 10 * time.Minute
	reloadMax    = 2
)

func reloadLogPath(dir string) string { return filepath.Join(dir, "argoz.reload") }

// reloadAllowed 限制 10 分钟内最多自动重启 2 次，避免重启风暴
func reloadAllowed(dir string) bool {
	data, err := os.ReadFile(reloadLogPath(dir))
	if err != nil {
		return true
	}
	now := time.Now()
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		ts, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil {
			continue
		}
		if now.Sub(time.Unix(ts, 0)) < reloadWindow {
			count++
		}
	}
	return count < reloadMax
}

func markReload(dir string) {
	now := time.Now()
	lines := []string{strconv.FormatInt(now.Unix(), 10)}
	if data, err := os.ReadFile(reloadLogPath(dir)); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			ts, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
			if err != nil {
				continue
			}
			if now.Sub(time.Unix(ts, 0)) < reloadWindow {
				lines = append(lines, line)
			}
		}
	}
	_ = os.WriteFile(reloadLogPath(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// maybeAutoReload 检测到 daemon 版本落后时，自动原地重启到当前版本
func maybeAutoReload(e rpc.API) {
	dv := e.DaemonVersion()
	if dv == "" || e.DaemonProto() < 1 {
		return
	}
	if config.CompareVersion(dv, config.Version) >= 0 {
		return
	}
	if _, conns, _, _ := e.Totals(); conns > 0 {
		return
	}
	dir := e.DataDir()
	if !reloadAllowed(dir) {
		return
	}

	fmt.Printf("  检测到守护进程 %s，正在自动重启到 %s ...\n", dv, config.Version)
	markReload(dir)

	if err := e.Reload(); err != nil {
		fmt.Printf("  自动重启失败：%v\n", err)
		time.Sleep(900 * time.Millisecond)
		return
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if e.Info().Version == config.Version {
			fmt.Printf("  ✓ 守护进程已升级到 %s\n\n", config.Version)
			time.Sleep(700 * time.Millisecond)
			return
		}
	}
	fmt.Println("  自动重启未生效，请手动执行：argoz service install")
	time.Sleep(time.Second)
}
