//go:build !linux

package core

// hasSupervisor 非 Linux 平台不检测
func hasSupervisor() bool { return false }
