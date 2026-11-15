package core

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAutoUpdateDue(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		st   autoUpdateState
		want bool
	}{
		{"从未检查过", autoUpdateState{}, true},
		{"还没到点", autoUpdateState{NextCheckAt: now.Add(time.Hour).Unix()}, false},
		{"刚好到点", autoUpdateState{NextCheckAt: now.Unix()}, true},
		{"早已过期", autoUpdateState{NextCheckAt: now.Add(-48 * time.Hour).Unix()}, true},
	}
	for _, tc := range cases {
		if got := autoUpdateDue(tc.st, now); got != tc.want {
			t.Fatalf("%s: autoUpdateDue=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

func TestAutoUpdateRetryDelay(t *testing.T) {
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{0, autoUpdateFirstRetry},
		{1, autoUpdateFirstRetry},
		{2, 2 * time.Hour},
		{3, 4 * time.Hour},
		{4, autoUpdateMaxRetry},
		{10, autoUpdateMaxRetry},
	}
	for _, tc := range cases {
		if got := autoUpdateRetryDelay(tc.streak); got != tc.want {
			t.Fatalf("streak=%d 退避应为 %s，实际 %s", tc.streak, tc.want, got)
		}
	}
}

func TestAutoUpdateNextCheck(t *testing.T) {
	now := time.Now()
	if got, want := autoUpdateNextCheck(now, 0), now.Add(autoUpdateInterval).Unix(); got != want {
		t.Fatalf("成功检查后应在 %s 后到期，实际 %d want %d", autoUpdateInterval, got, want)
	}
	if got, want := autoUpdateNextCheck(now, 1), now.Add(autoUpdateFirstRetry).Unix(); got != want {
		t.Fatalf("首次失败应在 %s 后重试，实际 %d want %d", autoUpdateFirstRetry, got, want)
	}
	if got, want := autoUpdateNextCheck(now, 9), now.Add(autoUpdateMaxRetry).Unix(); got != want {
		t.Fatalf("连续失败应封顶在 %s，实际 %d want %d", autoUpdateMaxRetry, got, want)
	}
}

func TestAutoUpdateStateRoundTrip(t *testing.T) {
	path := autoUpdateStatePath(t.TempDir())

	if got := loadAutoUpdateState(path); got != (autoUpdateState{}) {
		t.Fatalf("状态文件缺失时应返回零值，实际 %+v", got)
	}

	want := autoUpdateState{
		NextCheckAt:  1700000000,
		FailStreak:   2,
		LastError:    "boom",
		Pending:      "v1.5.0",
		PendingSince: 1700000000,
		PendingBoots: 1,
	}
	saveAutoUpdateState(path, want)
	if got := loadAutoUpdateState(path); got != want {
		t.Fatalf("读写不一致: got %+v want %+v", got, want)
	}
}

func TestDecidePendingOnBoot(t *testing.T) {
	cases := []struct {
		name    string
		st      autoUpdateState
		running string
		want    pendingDecision
		boots   int
	}{
		{
			name:    "无待验证",
			st:      autoUpdateState{},
			running: "v1.4.1",
			want:    pendingNone,
		},
		{
			name:    "已安装待重启",
			st:      autoUpdateState{Pending: "v1.5.0"},
			running: "v1.4.1",
			want:    pendingAwaitRestart,
			boots:   0,
		},
		{
			name:    "首次启动新版本",
			st:      autoUpdateState{Pending: "v1.5.0"},
			running: "v1.5.0",
			want:    pendingCountBoot,
			boots:   1,
		},
		{
			name:    "第二次启动仍未确认需回滚",
			st:      autoUpdateState{Pending: "v1.5.0", PendingBoots: 1},
			running: "v1.5.0",
			want:    pendingRollback,
			boots:   2,
		},
		{
			name:    "已被手工回滚",
			st:      autoUpdateState{Pending: "v1.5.0", PendingBoots: 1},
			running: "v1.4.1",
			want:    pendingStale,
			boots:   1,
		},
		{
			name:    "版本号带 v 前缀差异",
			st:      autoUpdateState{Pending: "1.5.0"},
			running: "v1.5.0",
			want:    pendingCountBoot,
			boots:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, boots := decidePendingOnBoot(tc.st, tc.running)
			if got != tc.want || boots != tc.boots {
				t.Fatalf("got (%v,%d) want (%v,%d)", got, boots, tc.want, tc.boots)
			}
		})
	}
}

func TestPendingConfirmedAndDeferExpired(t *testing.T) {
	st := autoUpdateState{Pending: "v1.5.0", PendingBoots: 1, PendingSince: time.Now().Add(-time.Minute).Unix()}

	if pendingConfirmed(st, "v1.5.0", autoUpdateConfirmDelay/2) {
		t.Fatal("稳定时长不足时不应确认")
	}
	if !pendingConfirmed(st, "v1.5.0", autoUpdateConfirmDelay) {
		t.Fatal("稳定达到阈值后应确认")
	}
	if pendingConfirmed(st, "v1.4.1", autoUpdateConfirmDelay) {
		t.Fatal("运行版本与待验证版本不一致时不应确认")
	}
	if pendingConfirmed(autoUpdateState{Pending: "v1.5.0"}, "v1.5.0", time.Hour) {
		t.Fatal("尚未启动过待验证版本时不应确认")
	}

	if pendingDeferExpired(st, time.Now()) {
		t.Fatal("刚安装不久不应判定超期")
	}
	expired := autoUpdateState{Pending: "v1.5.0", PendingSince: time.Now().Add(-autoUpdateMaxDefer).Unix()}
	if !pendingDeferExpired(expired, time.Now()) {
		t.Fatal("超过最长等待应判定超期")
	}
}

// 低内存模式（含运行期 OOM 降级）必须压过配置里的开关，
// 否则面板隐藏了入口、cloudflared 却仍被允许自行下载二进制。
func TestCloudflaredAutoUpdateHonorsLowMemory(t *testing.T) {
	prev := LowMemoryActive()
	defer lowMemoryProfile.Store(prev)

	on := Settings{AutoUpdateCloudflared: true}

	lowMemoryProfile.Store(false)
	if !cloudflaredAutoUpdateEnabled(on) {
		t.Fatal("正常模式且开关开启时应启用 cloudflared 自更新")
	}
	if cloudflaredAutoUpdateEnabled(Settings{}) {
		t.Fatal("开关关闭时不应启用 cloudflared 自更新")
	}

	lowMemoryProfile.Store(true)
	if cloudflaredAutoUpdateEnabled(on) {
		t.Fatal("低内存模式必须禁用 cloudflared 自更新（配置开着也不生效）")
	}
}

// 低内存模式下不能发起任何检查（也就不会产生状态文件）。
func TestAutoUpdateRoundSkipsLowMemory(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{cfg: &Config{}, dataDir: dir}
	e.cfg.Settings.AutoUpdateSelf = true

	prev := LowMemoryActive()
	lowMemoryProfile.Store(true)
	defer lowMemoryProfile.Store(prev)

	e.autoUpdateRound(context.Background())

	if _, err := os.Stat(autoUpdateStatePath(dir)); !os.IsNotExist(err) {
		t.Fatal("低内存模式不应发起检查")
	}
}

// 开关关闭时不发起检查。
func TestAutoUpdateRoundSkipsWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{cfg: &Config{}, dataDir: dir}

	prev := LowMemoryActive()
	lowMemoryProfile.Store(false)
	defer lowMemoryProfile.Store(prev)

	e.autoUpdateRound(context.Background())

	if _, err := os.Stat(autoUpdateStatePath(dir)); !os.IsNotExist(err) {
		t.Fatal("开关关闭时不应发起检查")
	}
}

// 状态必须跨进程保留：这是 supervisor 反复拉起时仍能按周期检查的前提。
func TestAutoUpdateDueSurvivesReload(t *testing.T) {
	path := autoUpdateStatePath(t.TempDir())
	now := time.Now()
	saveAutoUpdateState(path, autoUpdateState{NextCheckAt: autoUpdateNextCheck(now, 0)})

	if autoUpdateDue(loadAutoUpdateState(path), now) {
		t.Fatal("刚检查过的状态重新加载后不应立即到期")
	}
	if !autoUpdateDue(loadAutoUpdateState(path), now.Add(autoUpdateInterval)) {
		t.Fatal("到期后重新加载应判定为需要检查")
	}
}

// busySessions 必须把 UDP 会话算进来，否则只有 UDP 流量的机器会被误判为空闲。
func TestBusySessionsCountsUDP(t *testing.T) {
	e := &Engine{rt: map[string]*nodeRuntime{}, cfg: &Config{}}
	rt := &nodeRuntime{}
	rt.udpConns.Store(2)
	e.rt["n1"] = rt

	if got := e.busySessions(); got != 2 {
		t.Fatalf("UDP 会话应计入忙碌判定，实际 %d", got)
	}
	rt.conns.Store(3)
	if got := e.busySessions(); got != 5 {
		t.Fatalf("WS + UDP 应为 5，实际 %d", got)
	}
}
