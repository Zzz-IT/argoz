//go:build linux

package core

import "syscall"

// childDeathAttr 让子进程在父进程退出时收到信号，避免父进程被 kill -9 后留下孤儿。
// 仅 Linux 支持 Pdeathsig；其他平台返回 nil。
func ChildDeathAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
