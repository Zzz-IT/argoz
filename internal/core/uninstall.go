package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// UninstallPlan 卸载方案
type UninstallPlan struct {
	KeepData          bool   // 保留配置与日志
	RemoveCloudflared bool   // 同时删除 cloudflared
	BinaryPath        string // 空则取 os.Executable()
	SkipService       bool   // 测试用：跳过服务文件操作
}

// Summary 给用户看的摘要
func (p UninstallPlan) Summary() string {
	data := "删除数据目录（配置/日志/导出文件）"
	if p.KeepData {
		data = "保留数据目录"
	}
	cf := "保留 cloudflared"
	if p.RemoveCloudflared {
		cf = "删除 cloudflared"
	}
	return fmt.Sprintf("停服务并删除服务文件、删除 argoz 二进制、%s、%s", data, cf)
}

// Uninstall 执行卸载（调用前请先确认），返回执行明细
func (e *Engine) Uninstall(p UninstallPlan) ([]string, error) {
	e.Shutdown()
	CloseLog()

	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }

	if !p.SkipService {
		RemoveService(logf)
	}

	// 二进制
	bin := p.BinaryPath
	if bin == "" {
		if exe, err := os.Executable(); err == nil {
			bin = exe
		} else {
			bin = "/usr/local/bin/argoz"
		}
	}
	if err := os.Remove(bin); err != nil && !os.IsNotExist(err) {
		return lines, fmt.Errorf("删除二进制失败（可能正被占用）: %w", err)
	}
	logf("已删除二进制 %s", bin)
	if removeShortcut(bin) {
		logf("已删除快捷命令 az")
	}

	// cloudflared
	if p.RemoveCloudflared {
		cf := filepath.Join("/usr/local/bin", "cloudflared")
		if runtime.GOOS == "windows" {
			cf = filepath.Join(e.dataDir, "cloudflared.exe")
		}
		if err := os.Remove(cf); err == nil {
			logf("已删除 cloudflared")
		}
	}

	// 数据目录
	if !p.KeepData {
		if err := os.RemoveAll(e.dataDir); err != nil {
			return lines, fmt.Errorf("删除数据目录失败: %w", err)
		}
		logf("已删除数据目录 %s", e.dataDir)
	} else {
		logf("已保留数据目录 %s", e.dataDir)
	}

	return lines, nil
}

// showUninstall 经典菜单里的卸载流程；返回 true 表示已执行卸载（调用方应退出）
