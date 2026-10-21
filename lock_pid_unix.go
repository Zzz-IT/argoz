//go:build !windows

package main

import (
	"os"
	"strings"
	"syscall"
)

// pidAliveOS Unix：signal 0 探测进程是否存在
func pidAliveOS(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// 无权限发信号也说明进程存在
	return strings.Contains(strings.ToLower(err.Error()), "operation not permitted")
}
