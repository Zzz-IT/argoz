package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// instanceLock 单实例锁：防止同时跑多个 argoz 抢同一批端口。
// lock 文件保持单行 pid（兼容旧版本/测试），另用 .meta 记录启动 ticks，识别 PID 复用。
type instanceLock struct {
	path string
	meta string
}

func lockMetaPath(lockPath string) string { return lockPath + ".meta" }

// acquireLock 在数据目录创建锁文件；已有存活实例则返回错误。
func AcquireLock(dir, name string) (*instanceLock, error) {
	if name == "" {
		name = "argoz.lock"
	}
	p := filepath.Join(dir, name)
	meta := lockMetaPath(p)

	for attempt := 0; attempt < 4; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			if werr := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); werr != nil {
				_ = os.Remove(p)
				return nil, fmt.Errorf("写入实例锁失败: %w", werr)
			}
			start, _ := pidStartTime(os.Getpid())
			_ = os.WriteFile(meta, []byte(fmt.Sprintf("%d %d\n", os.Getpid(), start)), 0o600)
			return &instanceLock{path: p, meta: meta}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("创建实例锁失败: %w", err)
		}

		data, _ := os.ReadFile(p)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 && pidAlive(pid) && lockOwnerMatches(meta, pid) {
			return nil, fmt.Errorf("已有 argoz 实例在运行（pid %d），请先停止它：kill %d", pid, pid)
		}

		// 进程已不存在 / 僵尸 / PID 被复用 / 元数据不匹配：接管陈旧锁。
		_ = os.Remove(p)
		_ = os.Remove(meta)
		if name == "argoz.lock" {
			// 只有确认锁主已死亡后才清理旧的 RPC 文件，避免误删活实例。
			_ = os.Remove(filepath.Join(dir, "argoz.sock"))
			_ = os.Remove(filepath.Join(dir, "argoz.addr"))
		}
	}

	return nil, fmt.Errorf("无法获取实例锁 %s，请检查权限或手动删除后重试", p)
}

// lockOwnerMatches 校验锁主仍是同一进程：旧版本没有 meta 时只按 pid 存活判断。
func lockOwnerMatches(metaPath string, pid int) bool {
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return true
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return true
	}
	metaPID, err1 := strconv.Atoi(fields[0])
	metaStart, err2 := strconv.ParseUint(fields[1], 10, 64)
	if err1 != nil || err2 != nil || metaPID != pid || metaStart == 0 {
		return true
	}
	cur, ok := pidStartTime(pid)
	if !ok || cur == 0 {
		return true
	}
	return cur == metaStart
}

func (l *instanceLock) Release() {
	if l != nil && l.path != "" {
		_ = os.Remove(l.path)
	}
	if l != nil && l.meta != "" {
		_ = os.Remove(l.meta)
	}
}

// pidAlive 判断进程是否存活（平台相关实现见 lock_pid_*.go）
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAliveOS(pid)
}
