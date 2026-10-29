//go:build linux

package doctor

import (
	"fmt"
	"syscall"
)

func diskAvailableMB(path string) (int, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	bytes := uint64(st.Bavail) * uint64(st.Bsize)
	return int(bytes / (1024 * 1024)), nil
}

func probeDirectoryWritable(path string) (bool, string) {
	if err := syscall.Access(path, 2); err != nil { // W_OK
		return false, fmt.Sprintf("当前用户无写权限: %v", err)
	}
	return true, "当前用户具备写权限"
}
