package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestLockRejectsSecondInstance(t *testing.T) {
	dir := t.TempDir()
	l1, err := AcquireLock(dir, "")
	if err != nil {
		t.Fatalf("首次加锁失败: %v", err)
	}
	defer l1.Release()

	if _, err := AcquireLock(dir, ""); err == nil {
		t.Fatal("第二个实例应被拒绝")
	}
}

func TestLockTakesOverStale(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "argoz.lock")
	// 写一个不存在的 pid
	if err := os.WriteFile(p, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := AcquireLock(dir, "")
	if err != nil {
		t.Fatalf("陈旧锁应可接管: %v", err)
	}
	data, _ := os.ReadFile(p)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid != os.Getpid() {
		t.Fatalf("锁内 pid 应为当前进程 %d，实际 %d", os.Getpid(), pid)
	}
	l.Release()

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("Release 应删除锁文件")
	}
}

func TestLockTakesOverPIDReuse(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PID starttime 仅在 Linux 校验")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "argoz.lock")
	pid := os.Getpid()
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 同 PID 但错误的启动 tick：模拟 PID 复用后的陈旧锁。
	if err := os.WriteFile(p+".meta", []byte(strconv.Itoa(pid)+" 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(dir, "")
	if err != nil {
		t.Fatalf("PID 复用锁应可接管: %v", err)
	}
	data, _ := os.ReadFile(p)
	if strings.TrimSpace(string(data)) != strconv.Itoa(pid) {
		t.Fatalf("新锁 pid 不正确: %q", data)
	}
	l.Release()
}
