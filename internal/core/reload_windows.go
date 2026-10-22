//go:build windows

package core

// execSelf Windows 不支持 execve
func execSelf() error { return errReloadUnsupported }

var errReloadUnsupported = errUnsupportedPlatform{}

type errUnsupportedPlatform struct{}

func (errUnsupportedPlatform) Error() string { return "当前平台不支持原地重启" }
