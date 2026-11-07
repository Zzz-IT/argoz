package core

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const heartbeatInterval = 10 * time.Second

// HeartbeatPath 返回心跳文件路径
func HeartbeatPath(dir string) string {
	return filepath.Join(dir, "argoz.heartbeat")
}

// sdNotify 向 systemd 发送就绪/看门狗信号（未启用 systemd 时静默跳过）
func sdNotify(msg string) {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return
	}
	if sock[0] == '@' {
		sock = string(byte(0)) + sock[1:]
	}
	c, err := net.Dial("unixgram", sock)
	if err != nil {
		return
	}
	defer c.Close()
	_, _ = c.Write([]byte(msg))
}

// StartHeartbeat 定期写心跳文件并上报 systemd watchdog，返回停止函数。
// 低内存 / OOM 状态下仍然必须写心跳文件：否则 HeartbeatStale 会误判 daemon 死亡，
// 触发 supervisor 重启形成重启风暴；只做观测 + 告警，不触发任何降级副作用。
func StartHeartbeat(dir string) func() {
	path := HeartbeatPath(dir)
	sdNotify("READY=1")

	var lastOOMKills int64 = -1

	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			ts := strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"
			// 心跳写入不能因为内存紧张而跳过；写失败时记录告警，但仍继续下一轮。
			if err := atomicWrite(path, []byte(ts), 0o600); err != nil {
				LogWarn("心跳文件写入失败（daemon 仍需保持运行）: %v", err)
			}
			sdNotify("WATCHDOG=1")

			// 仅观测 cgroup oom_kill 变化，用于告警；不在此处触发降级副作用（那是 runtimeLoop 的职责）。
			if info := CurrentMemInfo(); info.OOMKills != lastOOMKills {
				if lastOOMKills >= 0 && info.OOMKills > lastOOMKills {
					LogWarn("检测到 cgroup OOM 事件（oom_kill %d -> %d），心跳仍持续写入以避免 supervisor 误重启",
						lastOOMKills, info.OOMKills)
				}
				lastOOMKills = info.OOMKills
			}

			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()

	return func() {
		close(stop)
		_ = os.Remove(path)
	}
}

// HeartbeatStale 判断心跳是否过期（文件不存在时按进程运行时长判断）
func HeartbeatStale(dir string, started time.Time, limit time.Duration) bool {
	st, err := os.Stat(HeartbeatPath(dir))
	if err != nil {
		return time.Since(started) > 2*limit
	}
	return time.Since(st.ModTime()) > limit
}
