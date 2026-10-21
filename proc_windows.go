//go:build windows

package main

import "syscall"

const detachedProcess = 0x00000008

// detachAttr 让子进程不随终端退出
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess}
}
