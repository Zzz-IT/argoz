package core

import (
	"os"
	"testing"
	"time"
)

func TestHeartbeatStale(t *testing.T) {
	dir := t.TempDir()
	started := time.Now()

	// 文件不存在且进程刚启动 → 不算过期
	if HeartbeatStale(dir, started, 90*time.Second) {
		t.Fatal("刚启动不应判定过期")
	}

	// 写心跳 → 不过期
	if err := atomicWrite(HeartbeatPath(dir), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if HeartbeatStale(dir, started, 90*time.Second) {
		t.Fatal("刚写的心跳不应过期")
	}

	// 回拨 3 分钟 → 过期
	old := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(HeartbeatPath(dir), old, old); err != nil {
		t.Fatal(err)
	}
	if !HeartbeatStale(dir, started, 90*time.Second) {
		t.Fatal("3 分钟未更新应判定过期")
	}
}

func TestHeartbeatFileWritten(t *testing.T) {
	dir := t.TempDir()
	stop := StartHeartbeat(dir)
	defer stop()

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(HeartbeatPath(dir)); err != nil {
		t.Fatalf("心跳文件未写入: %v", err)
	}
}
