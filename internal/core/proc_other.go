//go:build !linux

package core

import "syscall"

func ChildDeathAttr() *syscall.SysProcAttr {
	return nil
}
