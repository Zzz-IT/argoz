//go:build linux

package sys

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	cgroupV2Dir    = "/sys/fs/cgroup"
	cgroupV1Dir    = "/sys/fs/cgroup/memory"
	procCgroupFile = "/proc/self/cgroup"
)

func readProcMemInfo() (totalMB, availableMB int) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	return ParseMemInfo(string(data))
}

// readCgroupMemory 优先 cgroup v2，回退 cgroup v1；全部读取失败返回 0。
func readCgroupMemory() (maxMB, currentMB int, oomKills int64) {
	v2Roots, v1Roots := cgroupRoots()
	for _, root := range v2Roots {
		raw, err := os.ReadFile(filepath.Join(root, "memory.max"))
		if err != nil {
			continue
		}
		maxMB = ParseCgroupMB(string(raw))
		if cur, err := os.ReadFile(filepath.Join(root, "memory.current")); err == nil {
			currentMB = ParseCgroupMB(string(cur))
		}
		if ev, err := os.ReadFile(filepath.Join(root, "memory.events")); err == nil {
			oomKills = ParseOOMKills(string(ev))
		}
		return maxMB, currentMB, oomKills
	}
	for _, root := range v1Roots {
		raw, err := os.ReadFile(filepath.Join(root, "memory.limit_in_bytes"))
		if err != nil {
			continue
		}
		maxMB = ParseCgroupMB(string(raw))
		if cur, err := os.ReadFile(filepath.Join(root, "memory.usage_in_bytes")); err == nil {
			currentMB = ParseCgroupMB(string(cur))
		}
		if oom, err := os.ReadFile(filepath.Join(root, "memory.oom_control")); err == nil {
			oomKills = ParseOOMKills(string(oom))
		}
		return maxMB, currentMB, oomKills
	}
	return 0, 0, 0
}

// cgroupRoots 先尝试标准挂载点，再尝试 /proc/self/cgroup 暴露的命名空间路径。
func cgroupRoots() (v2Roots, v1Roots []string) {
	v2Roots = appendUniquePath(nil, cgroupV2Dir)
	v1Roots = appendUniquePath(nil, cgroupV1Dir)
	if data, err := os.ReadFile(procCgroupFile); err == nil {
		v2Path, v1Path := ParseCgroupPath(string(data))
		v2Roots = appendUniquePath(v2Roots, filepath.Join(cgroupV2Dir, strings.TrimPrefix(v2Path, "/")))
		v1Roots = appendUniquePath(v1Roots, filepath.Join(cgroupV1Dir, strings.TrimPrefix(v1Path, "/")))
	}
	return v2Roots, v1Roots
}

func appendUniquePath(list []string, p string) []string {
	p = filepath.Clean(p)
	for _, v := range list {
		if v == p {
			return list
		}
	}
	return append(list, p)
}

// DiskInodes 返回 dir 所在文件系统的可用/总 inode 数；失败返回 0,0。
func DiskInodes(dir string) (free, total int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	return int64(st.Ffree), int64(st.Files)
}

// diskFreeMB 返回 dir 所在文件系统的可用空间（MB）；失败返回 0。
func diskFreeMB(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	block := uint64(st.Bsize)
	if st.Frsize > 0 {
		block = uint64(st.Frsize)
	}
	if block == 0 {
		return 0
	}
	return int64(uint64(st.Bavail) * block / bytesPerMB)
}
