package sys

import (
	"os"
	"os/exec"
	"runtime"
)

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
