package sys

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Kind int

const (
	KindNone Kind = iota
	KindSystemd
	KindOpenRC
)

func DetectServiceKind() Kind {
	if runtime.GOOS != "linux" {
		return KindNone
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		return KindSystemd
	}
	if _, err := exec.LookPath("rc-update"); err == nil {
		return KindOpenRC
	}
	return KindNone
}

func shellArg(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func systemdArg(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func renderSystemdUnit(bin, dir string) string {
	unit := strings.Replace(systemdServiceTemplate,
		"ExecStart=/usr/local/bin/argoz -d",
		"ExecStart="+systemdArg(bin)+" -d -dir "+systemdArg(dir), 1)
	return strings.ReplaceAll(unit, "ReadWritePaths=/usr/local/etc/argoz", "ReadWritePaths="+systemdArg(dir))
}

func renderOpenRC(bin, dir string) string {
	script := strings.Replace(openrcServiceTemplate,
		`command="/usr/local/bin/argoz"`,
		`command="`+bin+`"`, 1)
	return strings.Replace(script, `command_args="-d"`, `command_args="-d -dir `+shellArg(dir)+`"`, 1)
}

func RunServiceCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %w（%s）", name, strings.Join(args, " "), err, msg)
	}
	return nil
}

type ServiceOptions struct {
	Binary  string
	DataDir string
	NoStart bool
}

func ServiceInstall(opts ServiceOptions) error {
	if runtime.GOOS != "linux" {
		return errors.New("当前系统不支持 systemd/OpenRC 服务托管")
	}
	if os.Geteuid() != 0 {
		return errors.New("安装服务需要 root 权限")
	}

	bin, err := filepath.Abs(opts.Binary)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		return fmt.Errorf("argoz 二进制不存在或不可用: %s", bin)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	switch DetectServiceKind() {
	case KindSystemd:
		path := "/etc/systemd/system/argoz.service"
		if err := os.WriteFile(path, []byte(renderSystemdUnit(bin, dir)), 0o644); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", path, err)
		}
		if err := RunServiceCommand("systemctl", "daemon-reload"); err != nil {
			return err
		}
		if err := RunServiceCommand("systemctl", "enable", "argoz"); err != nil {
			return err
		}
		if !opts.NoStart {
			if err := RunServiceCommand("systemctl", "restart", "argoz"); err != nil {
				return err
			}
		}
		fmt.Printf("已安装 systemd 服务: %s\n", path)
		ReportShortcut(bin)
		return nil

	case KindOpenRC:
		path := "/etc/init.d/argoz"
		if err := os.WriteFile(path, []byte(renderOpenRC(bin, dir)), 0o755); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", path, err)
		}
		if err := RunServiceCommand("rc-update", "add", "argoz", "default"); err != nil {
			return err
		}
		if !opts.NoStart {
			if err := RunServiceCommand("rc-service", "argoz", "restart"); err != nil {
				return err
			}
		}
		fmt.Printf("已安装 OpenRC 服务: %s\n", path)
		ReportShortcut(bin)
		return nil

	default:
		return errors.New("未检测到 systemd 或 OpenRC，请使用 nohup / supervisor 手动托管")
	}
}

func ServiceStatus() error {
	switch DetectServiceKind() {
	case KindSystemd:
		if _, err := os.Stat("/etc/systemd/system/argoz.service"); err != nil {
			fmt.Println("systemd 服务未安装")
			return nil
		}
		out, err := exec.Command("systemctl", "is-active", "argoz").CombinedOutput()
		status := strings.TrimSpace(string(out))
		if status == "" {
			status = "unknown"
		}
		fmt.Printf("systemd: %s\n", status)
		if err != nil {
			return nil
		}
		return nil

	case KindOpenRC:
		if _, err := os.Stat("/etc/init.d/argoz"); err != nil {
			fmt.Println("OpenRC 服务未安装")
			return nil
		}
		out, err := exec.Command("rc-service", "argoz", "status").CombinedOutput()
		status := strings.TrimSpace(string(out))
		if status == "" {
			status = "unknown"
		}
		fmt.Printf("OpenRC: %s\n", status)
		if err != nil {
			return nil
		}
		return nil

	default:
		return errors.New("当前系统未检测到 systemd/OpenRC")
	}
}

func ServiceUninstall() error {
	if runtime.GOOS != "linux" {
		return errors.New("当前系统不支持服务卸载")
	}
	if os.Geteuid() != 0 {
		return errors.New("卸载服务需要 root 权限")
	}
	var lines []string
	RemoveService(func(f string, a ...any) {
		lines = append(lines, fmt.Sprintf(f, a...))
	})
	if len(lines) == 0 {
		fmt.Println("没有发现已安装的 systemd/OpenRC 服务")
		return nil
	}
	for _, line := range lines {
		fmt.Println("· " + line)
	}
	if RemoveShortcutAny() {
		fmt.Println("· 已删除快捷命令 az")
	}
	return nil
}

// reportShortcut 创建 az 快捷命令并输出结果
func ReportShortcut(bin string) {
	p, err := EnsureShortcut(bin)
	if err != nil {
		fmt.Printf("提示: %v\n", err)
		return
	}
	if p != "" {
		fmt.Printf("已创建快捷命令: %s（直接输入 az 唤出面板）\n", shortcutName)
	}
}

func RunServiceCommandLine(args []string, defaultDir string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("用法: argoz service install|status|uninstall [-bin PATH] [-dir DIR] [--no-start]")
		return nil
	}
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("service install", flag.ContinueOnError)
		bin := fs.String("bin", "", "argoz 二进制路径（默认当前可执行文件）")
		dir := fs.String("dir", defaultDir, "数据目录")
		noStart := fs.Bool("no-start", false, "只安装并 enable，不立即启动")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		b := strings.TrimSpace(*bin)
		if b == "" {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("定位当前二进制失败: %w", err)
			}
			b = exe
		}
		return ServiceInstall(ServiceOptions{Binary: b, DataDir: *dir, NoStart: *noStart})

	case "status":
		return ServiceStatus()

	case "uninstall":
		return ServiceUninstall()

	default:
		return fmt.Errorf("未知 service 子命令: %s（可用: install/status/uninstall）", args[0])
	}
}
