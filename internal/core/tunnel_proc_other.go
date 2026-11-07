//go:build !unix

package core

import "os/exec"

// setProcessGroup 在 Windows 上无独立进程组语义，置为 no-op。
func setProcessGroup(cmd *exec.Cmd) {}
