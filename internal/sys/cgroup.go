package sys

import (
	"strconv"
	"strings"
)

// LowMemoryLimitMB 是 sys 包判定低内存的有效内存上限阈值（含）。
// 与 config.LowMemoryThresholdMB 保持一致；sys 不允许 import config，故在此独立定义。
const LowMemoryLimitMB = 96

const (
	bytesPerMB           = 1 << 20
	unlimitedCgroupBytes = int64(1) << 50 // v1 无限制哨兵约 2^63，超过此值视为无上限
)

// MemInfo 汇总 /proc/meminfo、cgroup 内存与当前工作目录的磁盘/inode 状态。
// 内存字段单位均为 MB；未知或无上限用 0 表示。
type MemInfo struct {
	TotalMB         int   // /proc/meminfo MemTotal MB；未知 0
	AvailableMB     int   // /proc/meminfo MemAvailable MB；未知 0
	CgroupMaxMB     int   // cgroup v2 memory.max / v1 memory.limit_in_bytes MB；无限制或未知 0
	CgroupCurrentMB int   // cgroup v2 memory.current / v1 memory.usage_in_bytes MB；未知 0
	OOMKills        int64 // memory.events 的 oom_kill 累计（v2）；v1 取 memory.oom_control 的 oom_kill 或 0
	DiskFreeMB      int64 // statfs(".") 可用空间 MB；未知 0
	InodesFree      int64 // statfs(".") 可用 inode；未知 0
	InodesTotal     int64 // statfs(".") 总 inode；未知 0
	LowMemory       bool  // 由 EffectiveLimitMB() 判定，<=LowMemoryLimitMB
	Known           bool  // 是否读到任何可信内存数据
}

// EffectiveLimitMB 返回运行时真正的内存上限：cgroup 限制优先，否则宿主总内存。
func (m MemInfo) EffectiveLimitMB() int {
	if m.CgroupMaxMB > 0 {
		return m.CgroupMaxMB
	}
	return m.TotalMB
}

// EffectiveAvailableMB 返回运行时真正可用的内存（MB）。
// cgroup 有上限时取 min(MemAvailable, Max-Current)；否则取 MemAvailable。
// MemAvailable 未知（0）时以 cgroup 剩余额度为准；已用超过上限按 0 处理。
func (m MemInfo) EffectiveAvailableMB() int {
	if m.CgroupMaxMB <= 0 {
		return m.AvailableMB
	}
	remaining := max(m.CgroupMaxMB-m.CgroupCurrentMB, 0)
	if m.AvailableMB <= 0 {
		return remaining
	}
	if remaining < m.AvailableMB {
		return remaining
	}
	return m.AvailableMB
}

// WithDerived 依据原始数值重算 Known / LowMemory 派生字段。
// 纯函数，便于离线构造与单元测试。
func (m MemInfo) WithDerived() MemInfo {
	m.Known = m.TotalMB > 0 || m.AvailableMB > 0 || m.CgroupMaxMB > 0 || m.CgroupCurrentMB > 0
	limit := m.EffectiveLimitMB()
	m.LowMemory = m.Known && limit > 0 && limit <= LowMemoryLimitMB
	return m
}

// ReadMemInfo 汇总 cgroup 内存、/proc/meminfo 与 statfs(".")。
// 纯只读；非 Linux 平台返回 Known=false 的合理默认值。
func ReadMemInfo() MemInfo {
	m := MemInfo{}
	m.TotalMB, m.AvailableMB = readProcMemInfo()
	m.CgroupMaxMB, m.CgroupCurrentMB, m.OOMKills = readCgroupMemory()
	m.DiskFreeMB = diskFreeMB(".")
	m.InodesFree, m.InodesTotal = DiskInodes(".")
	return m.WithDerived()
}

// ParseMemInfo 解析 /proc/meminfo 文本，返回 MemTotal / MemAvailable（MB）。
func ParseMemInfo(data string) (totalMB, availableMB int) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb < 0 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalMB = int(kb / 1024)
		case "MemAvailable:":
			availableMB = int(kb / 1024)
		}
	}
	return totalMB, availableMB
}

// ParseCgroupMB 解析 cgroup 内存字节数（memory.max / memory.current /
// memory.limit_in_bytes / memory.usage_in_bytes），返回 MB。
// "max"、v1 无限制哨兵、非正数与非法文本统一返回 0（无上限或未知）。
func ParseCgroupMB(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "max") {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n >= unlimitedCgroupBytes {
		return 0
	}
	return int(n / bytesPerMB)
}

// ParseOOMKills 从 cgroup v2 memory.events 或 v1 memory.oom_control 文本中读取 oom_kill 累计值。
func ParseOOMKills(data string) int64 {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "oom_kill" {
			continue
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err == nil && n >= 0 {
			return n
		}
		return 0
	}
	return 0
}

// ParseCgroupPath 解析 /proc/self/cgroup 文本，返回 v2 与 v1 memory 控制器的 cgroup 路径。
// 纯函数：未找到对应行时返回 "/"。
func ParseCgroupPath(data string) (v2Path, v1Path string) {
	var haveV2, haveV1 bool
	v2Path, v1Path = "/", "/"
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := strings.TrimSpace(parts[2])
		if path == "" {
			path = "/"
		}
		if parts[1] == "" {
			if !haveV2 {
				v2Path = path
				haveV2 = true
			}
			continue
		}
		for _, c := range strings.Split(parts[1], ",") {
			if c == "memory" && !haveV1 {
				v1Path = path
				haveV1 = true
			}
		}
	}
	return v2Path, v1Path
}
