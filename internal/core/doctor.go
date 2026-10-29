package core

import (
	"argoz/internal/doctor"
	"argoz/internal/sys"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Doctor 返回只读自检报告。所有修复都必须由用户显式调用 Repair。
func (e *Engine) Doctor() (DoctorReport, error) {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()

	report := doctor.Run(e.dataDir, cfg)
	out := DoctorReport{
		Healthy:        report.Healthy,
		Summary:        report.Summary,
		MemTotalMB:     report.MemTotalMB,
		MemAvailableMB: report.MemAvailableMB,
		LowMemory:      report.LowMemory,
		Checks:         make([]DoctorCheck, 0, len(report.Checks)),
	}
	for _, c := range report.Checks {
		out.Checks = append(out.Checks, DoctorCheck{
			Name:   c.Name,
			Status: string(c.Status),
			Detail: c.Detail,
			Repair: c.Repair,
		})
	}
	return out, nil
}

// Repair 执行用户显式选择的本地修复动作。网络下载只允许 action=cloudflared。
func (e *Engine) Repair(action string) ([]string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	var lines []string
	switch action {
	case "chmod":
		lines = append(lines, e.repairPermissions()...)
	case "locks":
		lines = append(lines, e.repairLocks()...)
	case "config":
		lines = append(lines, e.repairConfigDir()...)
	case "env":
		sys.CheckRuntimeEnv()
		lines = append(lines, "已执行 Alpine 运行时依赖检查；请查看日志确认实际结果")
	case "cloudflared":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		p, err := EnsureCloudflared(ctx)
		if err != nil {
			return lines, err
		}
		lines = append(lines, "cloudflared 可用: "+p)
	case "all":
		lines = append(lines, e.repairPermissions()...)
		lines = append(lines, e.repairLocks()...)
		lines = append(lines, e.repairConfigDir()...)
	default:
		return nil, fmt.Errorf("不支持的修复动作: %s", action)
	}
	return lines, nil
}

func (e *Engine) repairPermissions() []string {
	var lines []string
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, exe)
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		paths = append(paths, p)
	} else {
		paths = append(paths, filepath.Join("/usr/local/bin", "cloudflared"))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		if st.Mode().Perm()&0o111 == 0o111 {
			lines = append(lines, p+" 已具备执行权限")
			continue
		}
		if err := os.Chmod(p, 0o755); err != nil {
			lines = append(lines, fmt.Sprintf("%s chmod 失败: %v", p, err))
			continue
		}
		lines = append(lines, p+" 已设置 0755 权限")
	}
	if len(lines) == 0 {
		lines = append(lines, "未找到可修复的 argoz/cloudflared 文件")
	}
	return lines
}

func (e *Engine) repairLocks() []string {
	var lines []string
	lock := filepath.Join(e.dataDir, "argoz.lock")
	if b, err := os.ReadFile(lock); err == nil {
		var pid int
		_, _ = fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
		if pid > 0 && pid != os.Getpid() && !pidAlive(pid) {
			if err := os.Remove(lock); err == nil {
				lines = append(lines, "已清理陈旧实例锁")
			}
		}
	}
	sock := filepath.Join(e.dataDir, "argoz.sock")
	if _, err := os.Stat(sock); err == nil {
		// 只有确实无法连接时才删除，避免误删当前 RPC。
		conn, dialErr := net.DialTimeout("unix", sock, 500*time.Millisecond)
		if dialErr != nil {
			if rmErr := os.Remove(sock); rmErr == nil {
				lines = append(lines, "已清理无法连接的 RPC socket")
			}
		} else {
			_ = conn.Close()
			lines = append(lines, "RPC socket 正在使用，未删除")
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "未发现需要清理的 lock/socket")
	}
	return lines
}

func (e *Engine) repairConfigDir() []string {
	lines := []string{}
	if err := os.MkdirAll(e.dataDir, 0o750); err != nil {
		return []string{"配置目录创建失败: " + err.Error()}
	}
	if err := os.Chmod(e.dataDir, 0o750); err != nil {
		lines = append(lines, "配置目录 chmod 失败: "+err.Error())
	} else {
		lines = append(lines, "配置目录权限已设为 0750")
	}

	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	configPath := filepath.Join(e.dataDir, "config.json")
	data, readErr := os.ReadFile(configPath)
	switch {
	case readErr == nil && json.Valid(data):
		lines = append(lines, "config.json 合法，无需重建")
	case readErr == nil:
		backup := configPath + ".bak." + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, data, 0o600); err != nil {
			lines = append(lines, "config.json 非法，备份失败: "+err.Error())
		} else {
			lines = append(lines, "config.json 非法，已备份: "+backup)
			if cfg != nil && cfg.Save() == nil {
				lines = append(lines, "已用当前内存配置重建 config.json")
			} else {
				lines = append(lines, "重建 config.json 失败，请检查目录权限")
			}
		}
	case os.IsNotExist(readErr):
		if cfg != nil && cfg.Save() == nil {
			lines = append(lines, "config.json 不存在，已用当前内存配置重建")
		} else {
			lines = append(lines, "config.json 不存在，重建失败")
		}
	default:
		lines = append(lines, "读取 config.json 失败: "+readErr.Error())
	}
	return lines
}
