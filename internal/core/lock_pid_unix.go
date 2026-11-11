//go:build !windows

package core

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// procStatInfo 读取 Linux /proc/<pid>/stat 的状态和 starttime。
// 非 Linux 或读取失败返回 ok=false；调用方应退回 signal 0 探测。
func procStatInfo(pid int) (state byte, start uint64, ok bool) {
	if runtime.GOOS != "linux" || pid <= 0 {
		return 0, 0, false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, false
	}
	text := string(raw)
	// comm 可能包含空格和括号，必须从最后一个 ')' 之后解析。
	idx := strings.LastIndex(text, ")")
	if idx < 0 || idx+1 >= len(text) {
		return 0, 0, false
	}
	fields := strings.Fields(text[idx+1:])
	if len(fields) < 20 || len(fields[0]) == 0 {
		return 0, 0, false
	}
	start, err = strconv.ParseUint(fields[19], 10, 64) // /proc/pid/stat 第 22 字段
	if err != nil {
		// 状态仍然可信，starttime 不可用；僵尸判定比 PID 复用检测更重要。
		return fields[0][0], 0, true
	}
	return fields[0][0], start, true
}

// pidStartTime 返回 Linux 进程启动 ticks；用于识别 PID 复用后的陈旧锁。
func pidStartTime(pid int) (uint64, bool) {
	_, start, ok := procStatInfo(pid)
	return start, ok
}

// pidAliveOS Unix：signal 0 探测进程是否存在；Linux 上额外排除僵尸进程。
func pidAliveOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	if state, _, ok := procStatInfo(pid); ok && (state == 'Z' || state == 'X') {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// 无权限发信号也说明进程存在
	return strings.Contains(strings.ToLower(err.Error()), "operation not permitted")
}
