//go:build !windows

package rpc

import "syscall"

// detachAttr 让子进程脱离当前终端会话
func DetachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
