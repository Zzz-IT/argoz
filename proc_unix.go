//go:build !windows

package main

import "syscall"

// detachAttr 让子进程脱离当前终端会话
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
