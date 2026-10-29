package core

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const memoryReserveMB = 16

var (
	lowMemoryProfile atomic.Bool
	memSnapshotAt    atomic.Int64
	memAvailableMB   atomic.Int64
)

// ConfigureMemory 根据设置和系统总内存启用低内存运行时策略。
// 该函数应在 daemon 启动、加载配置之后尽早调用。
func ConfigureMemory(s Settings) Settings {
	total, _ := memorySnapshot()
	low := s.EffectiveLowMemory(total)
	lowMemoryProfile.Store(low)
	if !low {
		return s
	}
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(40 << 20)
	// 子进程 cloudflared 会继承这两个环境变量。
	_ = os.Setenv("GOMEMLIMIT", "32MiB")
	_ = os.Setenv("GOGC", "50")
	// 旧配置可能仍带着普通机器默认值；运行期按低内存上限收敛，但不写回配置。
	if s.MaxConns <= 0 || s.MaxConns > 24 {
		s.MaxConns = 24
	}
	if s.MaxUDPSessions <= 0 || s.MaxUDPSessions > 8 {
		s.MaxUDPSessions = 8
	}
	if s.IdleTimeout == "" {
		s.IdleTimeout = "3m"
	}
	LogInfo("低内存模式已启用：总内存 %dMB，MaxConns=%d，UDP 会话=%d",
		total, s.MaxConns, s.MaxUDPSessions)
	return s
}

// memorySnapshot 读取 Linux /proc/meminfo；其他平台返回 0。
func memorySnapshot() (totalMB, availableMB int) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalMB = kb / 1024
		case "MemAvailable:":
			availableMB = kb / 1024
		}
	}
	return totalMB, availableMB
}

// memoryPressure 在低内存模式下检查可用内存，避免把 64MB 机器直接打到 OOM。
func memoryPressure() bool {
	if !lowMemoryProfile.Load() {
		return false
	}
	now := time.Now().UnixNano()
	if now-memSnapshotAt.Load() < int64(5*time.Second) {
		return memAvailableMB.Load() > 0 && memAvailableMB.Load() < memoryReserveMB
	}
	_, avail := memorySnapshot()
	memSnapshotAt.Store(now)
	memAvailableMB.Store(int64(avail))
	return avail > 0 && avail < memoryReserveMB
}

// MemorySnapshot 返回最近一次读取的总内存/可用内存（MB）。
func MemorySnapshot() (int, int) {
	total, avail := memorySnapshot()
	if avail > 0 {
		memAvailableMB.Store(int64(avail))
		memSnapshotAt.Store(time.Now().UnixNano())
	}
	return total, avail
}
