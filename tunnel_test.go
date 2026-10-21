package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// 假 cloudflared：打印永久性错误并退出
func TestFakeCloudflaredHelper(t *testing.T) {
	if os.Getenv("ARGOZ_FAKE_CF") != "1" {
		t.Skip("helper process only")
	}
	fmt.Fprintln(os.Stderr, "failed to authenticate: invalid tunnel secret")
	os.Exit(1)
}

func stubFakeCloudflared(t *testing.T) {
	t.Helper()
	oldLookup, oldCmd := cloudflaredLookup, commandContext
	cloudflaredLookup = func(ctx context.Context) (string, error) { return "cloudflared-fake", nil }
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFakeCloudflaredHelper")
		cmd.Env = append(os.Environ(), "ARGOZ_FAKE_CF=1")
		return cmd
	}
	t.Cleanup(func() { cloudflaredLookup, commandContext = oldLookup, oldCmd })
}

// 验证：永久性错误 -> 第 2 次起直接进入 60 分钟档，状态为 degraded
func TestTunnelHardFailGoesHourly(t *testing.T) {
	e, _ := newTestEngine(t)
	stubFakeCloudflared(t)

	node, err := e.Create("退避测试", "vless", "temp", "", "")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	rt := e.runtimeOf(node.ID)
	if rt == nil || rt.tun == nil {
		t.Fatal("隧道未启动")
	}

	// 等两次失败：首次 ~0s 失败 -> 2s 后重试 -> 再失败 -> 60min
	deadline := time.Now().Add(8 * time.Second)
	var state string
	var next time.Time
	for time.Now().Before(deadline) {
		state, _, next, _ = rt.tun.Info()
		if state == "degraded" && !next.IsZero() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if state != "degraded" {
		t.Fatalf("永久性错误应进入 degraded，实际 %q", state)
	}
	d := time.Until(next)
	if d < 40*time.Minute || d > 75*time.Minute {
		t.Fatalf("下一次重试应在 60 分钟档（含抖动），实际 %s", d)
	}
}

// 验证：普通错误 -> 2s/4s 档，状态为 down
func TestTunnelSoftFailFastRetry(t *testing.T) {
	e, _ := newTestEngine(t)

	oldLookup, oldCmd := cloudflaredLookup, commandContext
	cloudflaredLookup = func(ctx context.Context) (string, error) { return "cloudflared-fake", nil }
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFakeCloudflaredHelperSoft")
		cmd.Env = append(os.Environ(), "ARGOZ_FAKE_CF_SOFT=1")
		return cmd
	}
	t.Cleanup(func() { cloudflaredLookup, commandContext = oldLookup, oldCmd })

	node, err := e.Create("软错测试", "vless", "temp", "", "")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	rt := e.runtimeOf(node.ID)

	deadline := time.Now().Add(6 * time.Second)
	var state string
	var next time.Time
	for time.Now().Before(deadline) {
		state, _, next, _ = rt.tun.Info()
		if !next.IsZero() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state == "degraded" {
		t.Fatal("软错误不应立即进入 degraded")
	}
	if d := time.Until(next); d > 10*time.Second {
		t.Fatalf("软错误应快速重试，实际 %s", d)
	}
}

func TestFakeCloudflaredHelperSoft(t *testing.T) {
	if os.Getenv("ARGOZ_FAKE_CF_SOFT") != "1" {
		t.Skip("helper process only")
	}
	fmt.Fprintln(os.Stderr, "connection reset by peer")
	os.Exit(1)
}
