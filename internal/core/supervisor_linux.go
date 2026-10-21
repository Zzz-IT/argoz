//go:build linux

package core

import (
	"os"
	"strconv"
	"strings"
)

// hasSupervisor 判断父进程是否是 argoz --supervise
func hasSupervisor() bool {
	ppid := os.Getppid()
	if ppid <= 1 {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/cmdline")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ReplaceAll(string(raw), "\x00", " "), "--supervise")
}
