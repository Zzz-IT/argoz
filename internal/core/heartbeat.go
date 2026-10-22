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

// StartHeartbeat 定期写心跳文件并上报 systemd watchdog，返回停止函数
func StartHeartbeat(dir string) func() {
	path := HeartbeatPath(dir)
	sdNotify("READY=1")

	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(heartbeatInterval)
		defer t.Stop()
		for {
			ts := strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"
			_ = atomicWrite(path, []byte(ts), 0o600)
			sdNotify("WATCHDOG=1")
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
