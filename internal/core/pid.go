package core

import (
	"os"
	"path/filepath"
	"strconv"
)

// tunnelPIDPath 返回某节点的 cloudflared pid 文件路径。
func tunnelPIDPath(e *Engine, id string) string {
	if e == nil || e.dataDir == "" || id == "" {
		return ""
	}
	return filepath.Join(e.dataDir, "run", id+".pid")
}

func writeTunnelPID(path string, pid int) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return atomicWrite(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

func removeTunnelPID(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
