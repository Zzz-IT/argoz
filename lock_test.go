package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLockRejectsSecondInstance(t *testing.T) {
	dir := t.TempDir()
	l1, err := acquireLock(dir, "")
	if err != nil {
		t.Fatalf("首次加锁失败: %v", err)
	}
	defer l1.Release()

	if _, err := acquireLock(dir, ""); err == nil {
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

	l, err := acquireLock(dir, "")
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
