package core

import (
	"net"
	"testing"
	"time"
)

// 用确定性抖动跑退避表
func withNoJitter(t *testing.T) {
	t.Helper()
	old := jitterFn
	jitterFn = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { jitterFn = old })
}

func TestBackoffSchedule(t *testing.T) {
	withNoJitter(t)
	p := defaultPolicy()

	fastIdx, slowCount := 0, 0
	var got []time.Duration

	// 前 3 次：2s / 4s / 8s
	for i := 0; i < len(p.fast); i++ {
		d, fi, sc := p.next(fastIdx, slowCount, false)
		fastIdx, slowCount = fi, sc
		got = append(got, d)
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("快退避第 %d 次应为 %s，实际 %s", i+1, want[i], got[i])
		}
	}

	// 接下来 20 次：一律 3 分钟
	for i := 0; i < p.slowN; i++ {
		d, fi, sc := p.next(fastIdx, slowCount, false)
		fastIdx, slowCount = fi, sc
		if d != p.slow {
			t.Fatalf("慢重试第 %d 次应为 %s，实际 %s", i+1, p.slow, d)
		}
	}

	// 之后：60 分钟
	for i := 0; i < 3; i++ {
		d, fi, sc := p.next(fastIdx, slowCount, false)
		fastIdx, slowCount = fi, sc
		if d != p.ultra {
			t.Fatalf("超慢重试第 %d 次应为 %s，实际 %s", i+1, p.ultra, d)
		}
	}
}

func TestBackoffHardFailJumpsToHourly(t *testing.T) {
	withNoJitter(t)
	p := defaultPolicy()

	// 第 1 次失败（hard）仍走 2s，给人一次机会
	d, fi, sc := p.next(0, 0, true)
	if d != 2*time.Second {
		t.Fatalf("首次 hard 失败应为 2s，实际 %s", d)
	}
	// 第 2 次 hard 失败直接跳到 60 分钟
	d2, _, _ := p.next(fi, sc, true)
	if d2 != p.ultra {
		t.Fatalf("hard 失败第二次应为 %s，实际 %s", p.ultra, d2)
	}
}

func TestJitterWithinTwentyPercent(t *testing.T) {
	for _, base := range []time.Duration{2 * time.Second, 30 * time.Second, 60 * time.Minute} {
		lo := time.Duration(float64(base) * 0.8)
		hi := time.Duration(float64(base) * 1.2)
		for i := 0; i < 200; i++ {
			got := jitterFn(base)
			if got < lo || got > hi {
				t.Fatalf("抖动越界: base=%s got=%s 允许区间 [%s,%s]", base, got, lo, hi)
			}
		}
	}
}

func TestIsHardFail(t *testing.T) {
	hard := []string{
		"failed to authenticate: invalid tunnel secret",
		"error 1033",
		"tunnel not found",
		"Unauthorized",
	}
	soft := []string{
		"connection reset by peer",
		"network is unreachable",
		"等待 Cloudflare 分配域名超时",
	}
	for _, s := range hard {
		if !isHardFail(s) {
			t.Fatalf("应判定为永久错误: %s", s)
		}
	}
	for _, s := range soft {
		if isHardFail(s) {
			t.Fatalf("不应判定为永久错误: %s", s)
		}
	}
}

func TestProbeL1HealthyAndBroken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	rt := &nodeRuntime{
		node:     Node{ID: "p1", Protocol: "vless", Path: "/p", UUID: newUUID()},
		settings: defaultSettings(),
	}
	rt.startServer(ln)
	addr := ln.Addr().String()

	if err := probeL1(addr, "/p", time.Second); err != nil {
		t.Fatalf("健康节点探针应通过: %v", err)
	}

	rt.stopServer()
	time.Sleep(50 * time.Millisecond)

	if err := probeL1(addr, "/p", 500*time.Millisecond); err == nil {
		t.Fatal("已关闭的监听应探测失败")
	}
}

func TestProbeL3InvalidDomain(t *testing.T) {
	// .invalid 是 RFC 保留后缀，必定解析失败
	if err := probeL3("argoz-probe.invalid", "/x", 800*time.Millisecond); err == nil {
		t.Fatal("无效域名应探测失败")
	}
}

func TestRelayBufferPool(t *testing.T) {
	b := getRelayBuf()
	if len(*b) != relayBufSize {
		t.Fatalf("缓冲大小应为 %d，实际 %d", relayBufSize, len(*b))
	}
	if relayBufSize != 16<<10 {
		t.Fatalf("缓冲应为 16KB，实际 %d", relayBufSize)
	}
	putRelayBuf(b)

	b2 := getRelayBuf()
	if len(*b2) != relayBufSize {
		t.Fatal("池化复用后大小异常")
	}
	putRelayBuf(b2)
}

func TestRetryHint(t *testing.T) {
	if got := retryHint(NodeStatus{}); got != "" {
		t.Fatalf("无重试计划时应为空，实际 %q", got)
	}
	st := NodeStatus{NextRetry: time.Now().Add(3*time.Minute + 30*time.Second)}
	got := retryHint(st)
	if !contains(got, "后重试") || !contains(got, "3m") {
		t.Fatalf("重试提示异常: %q", got)
	}
	if got := retryHint(NodeStatus{NextRetry: time.Now().Add(-time.Second)}); !contains(got, "即将重试") {
		t.Fatalf("过期重试提示异常: %q", got)
	}
}
