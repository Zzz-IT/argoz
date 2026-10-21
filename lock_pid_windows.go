//go:build windows

package main

import "syscall"

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess = kernel32.NewProc("OpenProcess")
)

const processQueryLimitedInformation = 0x1000

// pidAliveOS Windows：OpenProcess 探测进程是否存在
func pidAliveOS(pid int) bool {
	h, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if h == 0 {
		return false
	}
	_ = syscall.CloseHandle(syscall.Handle(h))
	return true
}
