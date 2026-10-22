//go:build !linux && !windows

package core

// execSelf 非 Linux 平台不支持原地替换
func execSelf() error { return errReloadUnsupported }

var errReloadUnsupported = errUnsupportedPlatform{}

type errUnsupportedPlatform struct{}

func (errUnsupportedPlatform) Error() string { return "当前平台不支持原地重启" }
