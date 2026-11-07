package core

import (
	"argoz/internal/sys"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	memoryReserveMB = 16

	// lowMemConnBudgetBytes 是低内存模式下每条连接的粗略内存预算（含双向 relay 缓冲等）。
	lowMemConnBudgetBytes = 40 << 10
	// oomDegradeMaxConns 是 OOM 自适应降级后的连接预算硬上限。
	oomDegradeMaxConns = 8
	// memCacheTTL 是热路径可用内存缓存的有效期，避免每次请求都读 /proc 与 cgroup 文件。
	memCacheTTL = 5 * time.Second
)

var (
	lowMemoryProfile   atomic.Bool
	memSnapshotAt      atomic.Int64
	memAvailableMB     atomic.Int64
	memInfoCache       atomic.Value // 保存 sys.MemInfo，供热路径只读复用
	configuredMaxConns atomic.Int64 // ConfigureMemory 传入的 MaxConns，0 表示未初始化
	oomConnCap         atomic.Int64 // >0 时覆盖连接预算上限（OOM 降级）
	lastOOMKills       atomic.Int64 // daemon 运行期上次观测到的 cgroup oom_kill 累计

	oomMu          sync.Mutex // 保护 OOM 基线与幂等判定
	oomBaselineSet bool

	oomHookMu      sync.RWMutex
	oomDegradeHook func() // 由 engine 注册：遍历所有 runtime 关闭 UDP 会话
)

// ConfigureMemory 根据 cgroup 有效内存上限与设置启用低内存运行时策略。
// 该函数应在 daemon 启动、加载配置之后尽早调用；只调整内存中的 Settings，不写回配置。
func ConfigureMemory(s Settings) Settings {
	info := currentMemInfo()
	limit := info.EffectiveLimitMB()
	low := s.EffectiveLowMemory(limit)
	lowMemoryProfile.Store(low)
	configuredMaxConns.Store(int64(s.MaxConns))

	// 启动时的 oom_kill 累计作为基线：进程重启前的历史事件不重复触发降级。
	oomMu.Lock()
	lastOOMKills.Store(info.OOMKills)
	oomBaselineSet = true
	oomMu.Unlock()
	oomConnCap.Store(0)

	if !low {
		return s
	}
	// 低内存模式下收紧 GC：GOGC 降到 50 减少峰值占用，堆上限按有效内存上限推导，
	// 而不是写死 40MB —— 写死的值在容器被限到 64MB 时容易顶到 cgroup 上限被 OOM kill，
	// 被限到 256MB 时又过于保守、白白多触发 GC。留 25% 余量给 GC 自身与运行时开销。
	heapLimit := runtimeDebugHeapLimit(limit)
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(heapLimit)
	// 子进程 cloudflared 会继承这两个环境变量；env 的粒度是字节串，取与 heapLimit 一致的值。
	_ = os.Setenv("GOMEMLIMIT", fmt.Sprintf("%dMiB", heapLimit>>20))
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
	LogInfo("低内存模式已启用：有效上限 %dMB（cgroup %dMB / 宿主 %dMB），可用 %dMB，MaxConns=%d，UDP 会话=%d",
		limit, info.CgroupMaxMB, info.TotalMB, info.EffectiveAvailableMB(), s.MaxConns, s.MaxUDPSessions)
	return s
}

// LowMemoryActive 返回当前是否处于低内存运行模式。
func LowMemoryActive() bool { return lowMemoryProfile.Load() }

// runtimeDebugHeapLimit 由有效内存上限（MB）推导 Go 堆上限（字节）：
// 取上限的 75% 并夹到 [16MiB, 512MiB]，保证极小容器不会把自己撑爆，
// 大容器也不会因为固定常量而过度约束 GC。
func runtimeDebugHeapLimit(limitMB int) int64 {
	heap := int64(limitMB) * 3 / 4 << 20
	const (
		minHeap = 16 << 20
		maxHeap = 512 << 20
	)
	if heap < minHeap {
		return minHeap
	}
	if heap > maxHeap {
		return maxHeap
	}
	return heap
}

// currentMemInfo 实时读取并缓存最近一次内存快照。
func currentMemInfo() sys.MemInfo {
	info := sys.ReadMemInfo()
	memInfoCache.Store(info)
	memAvailableMB.Store(int64(info.EffectiveAvailableMB()))
	memSnapshotAt.Store(time.Now().UnixNano())
	return info
}

// lastMemInfo 返回最近一次读取的内存快照；热路径专用，不产生文件 I/O。
func lastMemInfo() sys.MemInfo {
	if v := memInfoCache.Load(); v != nil {
		if info, ok := v.(sys.MemInfo); ok {
			return info
		}
	}
	return sys.ReadMemInfo()
}

// CurrentMemInfo 返回实时读取的 cgroup / /proc/meminfo / statfs 快照。
// 同时刷新包内缓存，供热路径（ConnectionBudgetOK / memoryPressure）复用。
func CurrentMemInfo() sys.MemInfo { return currentMemInfo() }

// memorySnapshot 返回有效内存上限与有效可用内存（MB），保留给包内旧调用方与测试。
func memorySnapshot() (totalMB, availableMB int) {
	info := currentMemInfo()
	return info.EffectiveLimitMB(), info.EffectiveAvailableMB()
}

// memoryPressure 在低内存模式下检查有效可用内存，避免把 64MB 机器直接打到 OOM。
// 使用 5s 缓存，避免每个 WS 请求都读 /proc 与 cgroup 文件；阈值仍为 16MB。
func memoryPressure() bool {
	if !lowMemoryProfile.Load() {
		return false
	}
	now := time.Now().UnixNano()
	if now-memSnapshotAt.Load() < int64(memCacheTTL) {
		avail := memAvailableMB.Load()
		return avail > 0 && avail < memoryReserveMB
	}
	info := currentMemInfo()
	avail := info.EffectiveAvailableMB()
	return avail > 0 && avail < memoryReserveMB
}

// MemorySnapshot 返回最近一次读取的有效内存上限 / 有效可用内存（MB）。
// 签名保持不变；内部改用 sys.ReadMemInfo()（cgroup 限制优先）。
func MemorySnapshot() (int, int) {
	info := currentMemInfo()
	return info.EffectiveLimitMB(), info.EffectiveAvailableMB()
}

// effectiveMaxConns 返回运行期连接预算上限：OOM 降级后收敛到 8。
func effectiveMaxConns() int {
	base := int(configuredMaxConns.Load())
	if base <= 0 {
		base = 128
	}
	if cap := int(oomConnCap.Load()); cap > 0 && cap < base {
		return cap
	}
	return base
}

// ConnectionBudgetOK 判断当前活跃连接数是否仍在低内存预算之内。
// 非低内存模式恒为 true；低内存模式按每连接约 40KB 估算，
// 预算 = (有效可用内存 - 16MB 保留) / 40KB，并以运行期 MaxConns 上限收敛。
// 可用内存未知时退化为按有效上限的一半估算；完全未知时不误杀连接。
// 只读缓存快照，适合 ServeHTTP 热路径调用。
func ConnectionBudgetOK(active int64) bool {
	if !lowMemoryProfile.Load() {
		return true
	}
	active = max(active, 0)
	info := lastMemInfo()
	avail := info.EffectiveAvailableMB()
	if avail <= 0 {
		limit := info.EffectiveLimitMB()
		if limit <= 0 {
			return true
		}
		avail = limit / 2
	}
	budgetMB := avail - memoryReserveMB
	if budgetMB <= 0 {
		return false
	}
	budget := int64(budgetMB) * (1 << 20) / lowMemConnBudgetBytes
	if budget < 1 {
		return false
	}
	if cap := int64(effectiveMaxConns()); budget > cap {
		budget = cap
	}
	return active < budget
}

// RegisterOOMDegradeHook 注册 OOM 降级时的额外清理动作（通常是关闭全部 UDP 会话）。
// 传 nil 可清空；线程安全，可在 daemon 启动阶段重复调用覆盖。
func RegisterOOMDegradeHook(fn func()) {
	oomHookMu.Lock()
	oomDegradeHook = fn
	oomHookMu.Unlock()
}

// SetOOMDegradeHook 是 RegisterOOMDegradeHook 的别名，方便按习惯调用。
func SetOOMDegradeHook(fn func()) { RegisterOOMDegradeHook(fn) }

// RegisterOOMDegradeEngine 注册引擎级 UDP 清理钩子：OOM 降级时关闭所有节点的 UDP 会话。
// 在 NewEngine 之后调用一次即可；同包可见 e.rt，无需改动 engine.go 的其它逻辑。
func RegisterOOMDegradeEngine(e *Engine) {
	if e == nil {
		return
	}
	RegisterOOMDegradeHook(func() {
		e.mu.RLock()
		rts := make([]*nodeRuntime, 0, len(e.rt))
		for _, rt := range e.rt {
			rts = append(rts, rt)
		}
		e.mu.RUnlock()
		for _, rt := range rts {
			rt.closeUDPSessions()
		}
	})
}

// runOOMDegradeHook 执行已注册的清理动作，并隔离钩子自身的 panic，避免打断 health 循环。
func runOOMDegradeHook() (msg string) {
	oomHookMu.RLock()
	hook := oomDegradeHook
	oomHookMu.RUnlock()
	if hook == nil {
		return "UDP 会话清理钩子未注册（已跳过）"
	}
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprintf("UDP 会话清理钩子异常: %v", r)
		}
	}()
	hook()
	return "已关闭全部 UDP 会话"
}

// OOMDegrade 检测 cgroup oom_kill 累计增长并执行一次自适应降级。
// 动作：切换到低内存策略、把连接预算上限降到 8、关闭全部 UDP 会话、写一条 LogWarn。
// 幂等：同一个 oom 计数只触发一次；首次调用或 ConfigureMemory 会先建立基线。
// 供 health 循环/手工 oom-recover 调用，返回可读动作列表（无新事件时返回 nil）。
func OOMDegrade() []string {
	info := currentMemInfo()
	kills := info.OOMKills

	oomMu.Lock()
	if !oomBaselineSet {
		oomBaselineSet = true
		lastOOMKills.Store(kills)
		oomMu.Unlock()
		return nil
	}
	last := lastOOMKills.Load()
	if kills <= 0 || kills <= last {
		if kills < last {
			// cgroup 重建导致计数复位，重新对齐基线。
			lastOOMKills.Store(kills)
		}
		oomMu.Unlock()
		return nil
	}
	lastOOMKills.Store(kills)
	oomMu.Unlock()

	actions := []string{
		fmt.Sprintf("检测到 cgroup OOM 事件（oom_kill %d -> %d）", last, kills),
		"已切换到低内存运行策略",
		fmt.Sprintf("MaxConns 连接预算上限已降到 %d", oomDegradeMaxConns),
	}
	lowMemoryProfile.Store(true)
	oomConnCap.Store(int64(oomDegradeMaxConns))
	actions = append(actions, runOOMDegradeHook())
	LogWarn("OOM 自适应降级：%s", strings.Join(actions, "；"))
	return actions
}
