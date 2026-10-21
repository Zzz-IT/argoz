package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// instanceLock 单实例锁：防止同时跑多个 argoz 抢同一批端口
type instanceLock struct {
	path string
}

// acquireLock 在数据目录创建锁文件；已有存活实例则返回错误
func AcquireLock(dir, name string) (*instanceLock, error) {
	if name == "" {
		name = "argoz.lock"
	}
	p := filepath.Join(dir, name)

	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			if werr := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); werr != nil {
				_ = os.Remove(p)
				return nil, fmt.Errorf("写入实例锁失败: %w", werr)
			}
			return &instanceLock{path: p}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("创建实例锁失败: %w", err)
		}

		data, _ := os.ReadFile(p)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 && pidAlive(pid) {
			return nil, fmt.Errorf("已有 argoz 实例在运行（pid %d），请先停止它：kill %d", pid, pid)
		}

		// 陈旧锁（进程已不存在）或自己上次残留：接管
		_ = os.Remove(p)
	}

	return nil, fmt.Errorf("无法获取实例锁 %s，请检查权限或手动删除后重试", p)
}

func (l *instanceLock) Release() {
	if l != nil && l.path != "" {
		_ = os.Remove(l.path)
	}
}

// pidAlive 判断进程是否存活（平台相关实现见 lock_pid_*.go）
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return pidAliveOS(pid)
}
