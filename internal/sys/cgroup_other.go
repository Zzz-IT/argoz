//go:build !linux

package sys

// 非 Linux 平台没有 /proc/meminfo 与 cgroup 文件：返回 Known=false 的合理默认值。

func readProcMemInfo() (totalMB, availableMB int) { return 0, 0 }

func readCgroupMemory() (maxMB, currentMB int, oomKills int64) { return 0, 0, 0 }

// DiskInodes 非 Linux 平台返回 0,0（未知）。
func DiskInodes(dir string) (free, total int64) { return 0, 0 }

func diskFreeMB(dir string) int64 { return 0 }
