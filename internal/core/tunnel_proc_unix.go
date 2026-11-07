//go:build unix

package core

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让 cloudflared 子进程自成独立进程组，
// 配合 orphan_linux.go 的进程组 kill 一起清理孤儿；组组长 pid 即子进程 pid，
// Kill(-pid) 可一次杀掉整组。
func setProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
