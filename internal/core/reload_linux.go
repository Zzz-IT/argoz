//go:build linux

package core

import (
	"os"
	"syscall"
)

// execSelf 用当前二进制原地替换进程镜像（PID 不变）
func execSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
