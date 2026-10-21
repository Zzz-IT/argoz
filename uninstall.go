package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// uninstallPlan 卸载方案
type uninstallPlan struct {
	KeepData          bool   // 保留配置与日志
	RemoveCloudflared bool   // 同时删除 cloudflared
	BinaryPath        string // 空则取 os.Executable()
	SkipService       bool   // 测试用：跳过服务文件操作
}

// Summary 给用户看的摘要
func (p uninstallPlan) Summary() string {
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

// RemoveService 注销并删除 systemd / OpenRC 服务文件
func RemoveService(logf func(string, ...any)) {
	if runtime.GOOS != "linux" {
		return
	}

	systemd := "/etc/systemd/system/argoz.service"
	if _, err := os.Stat(systemd); err == nil {
		if _, err := exec.LookPath("systemctl"); err == nil {
			_ = exec.Command("systemctl", "disable", "argoz").Run()
			_ = exec.Command("systemctl", "stop", "argoz").Run()
			_ = exec.Command("systemctl", "daemon-reload").Run()
		}
		if err := os.Remove(systemd); err == nil {
			logf("已删除 %s", systemd)
		}
	}

	openrc := "/etc/init.d/argoz"
	if _, err := os.Stat(openrc); err == nil {
		if _, err := exec.LookPath("rc-update"); err == nil {
			_ = exec.Command("rc-update", "del", "argoz", "default").Run()
			_ = exec.Command("rc-service", "argoz", "stop").Run()
		}
		if err := os.Remove(openrc); err == nil {
			logf("已删除 %s", openrc)
		}
	}
}

// Uninstall 执行卸载（调用前请先确认），返回执行明细
func (e *Engine) Uninstall(p uninstallPlan) ([]string, error) {
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
func showUninstall(e *Engine) bool {
	Clear()
	Title("卸载 argoz")
	fmt.Println("  将停止所有节点，并删除 systemd / OpenRC 服务与 argoz 二进制。")
	fmt.Println()
	fmt.Println("  1) 保留配置与日志（推荐）")
	fmt.Println("  2) 彻底删除（配置、日志、导出的链接文件）")
	choice := ReadLine("\n  请选择 (默认 1): ")
	keep := choice != "2"

	rmcf := ReadLine("  同时删除 cloudflared? (y/N): ") == "y"

	plan := uninstallPlan{KeepData: keep, RemoveCloudflared: rmcf}
	fmt.Printf("\n  即将执行：%s\n", plan.Summary())
	if ReadLine("  输入 DELETE 确认: ") != "DELETE" {
		LogWarn("已取消卸载")
		Pause("")
		return false
	}

	lines, err := e.Uninstall(plan)
	for _, l := range lines {
		fmt.Println("  · " + l)
	}
	if err != nil {
		LogErr("卸载未完成: %v", err)
		Pause("")
		return false
	}
	fmt.Println("\n  卸载完成，感谢使用 argoz。")
	return true
}
