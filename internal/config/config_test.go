package config

import (
	"argoz/internal/sys"
	"testing"
)

func TestDefaultUDPDisabled(t *testing.T) {
	s := DefaultSettings()
	if s.UDPEnabled {
		t.Fatal("UDP 默认应关闭")
	}
	wantSessions := 32
	if s.MemoryMode == MemoryModeLow {
		wantSessions = 8
	}
	if s.UDPIdle != "60s" || s.MaxUDPSessions != wantSessions {
		t.Fatalf("UDP 默认值异常: idle=%s sessions=%d want=%d", s.UDPIdle, s.MaxUDPSessions, wantSessions)
	}
}

func TestDefaultPortRangeIsAllPorts(t *testing.T) {
	s := DefaultSettings()
	if s.PortRange != "1-65535" {
		t.Fatalf("默认端口段应为 1-65535，实际 %s", s.PortRange)
	}
	lo, hi, err := s.PortBounds()
	if err != nil || lo != 1 || hi != 65535 {
		t.Fatalf("端口段解析异常: %d-%d %v", lo, hi, err)
	}
}

func TestPortRangeMigratesFromOldDefault(t *testing.T) {
	c := &Config{Version: 1, Settings: Settings{PortRange: "20000-20100"}}
	c.fixup()
	if c.Settings.PortRange != "1-65535" {
		t.Fatalf("旧默认值应迁移为全端口，实际 %s", c.Settings.PortRange)
	}

	// 用户自定义的范围不应被改动
	c2 := &Config{Version: 2, Settings: Settings{PortRange: "10000-10100"}}
	c2.fixup()
	if c2.Settings.PortRange != "10000-10100" {
		t.Fatalf("自定义范围被误改: %s", c2.Settings.PortRange)
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.3.4", "v1.3.3", 1},
		{"v1.3.3", "v1.3.4", -1},
		{"v1.3.3", "v1.3.3", 0},
		{"1.3.10", "1.3.9", 1},
		{"v1.3", "v1.3.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersion(c.a, c.b); got != c.want {
			t.Fatalf("CompareVersion(%q,%q)=%d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}

func TestDefaultSettingsMemoryBaseline(t *testing.T) {
	low := defaultSettingsForTotalMemory(64)
	if low.MaxConns != 24 || low.MaxUDPSessions != 8 || low.UDPEnabled {
		t.Fatalf("64MB 低内存基线异常: max_conns=%d max_udp=%d udp=%v",
			low.MaxConns, low.MaxUDPSessions, low.UDPEnabled)
	}
	if low.Keepalive != "30s" || low.IdleTimeout != "3m" || low.MemoryMode != MemoryModeLow {
		t.Fatalf("64MB 低内存超时/模式异常: keepalive=%s idle=%s mode=%s",
			low.Keepalive, low.IdleTimeout, low.MemoryMode)
	}

	atLimit := defaultSettingsForTotalMemory(LowMemoryThresholdMB)
	if atLimit.MemoryMode != MemoryModeLow || atLimit.MaxConns != 24 {
		t.Fatalf("阈值 %dMB 应启用低内存基线，实际 mode=%s max_conns=%d",
			LowMemoryThresholdMB, atLimit.MemoryMode, atLimit.MaxConns)
	}

	normal := defaultSettingsForTotalMemory(LowMemoryThresholdMB + 1)
	if normal.MaxConns != 128 || normal.MaxUDPSessions != 32 || normal.IdleTimeout != "5m" {
		t.Fatalf("普通机器默认值被改变: max_conns=%d max_udp=%d idle=%s",
			normal.MaxConns, normal.MaxUDPSessions, normal.IdleTimeout)
	}
	if normal.MemoryMode != MemoryModeNormal {
		t.Fatalf("普通机器 MemoryMode 应为 normal，实际 %s", normal.MemoryMode)
	}
	if unknown := defaultSettingsForTotalMemory(0); unknown.MemoryMode != MemoryModeNormal {
		t.Fatalf("未知总内存应保留普通基线，实际 %s", unknown.MemoryMode)
	}
}

func TestEffectiveLowMemory(t *testing.T) {
	cases := []struct {
		mode    string
		totalMB int
		want    bool
	}{
		{"", 64, true},
		{MemoryModeAuto, 96, true},
		{MemoryModeAuto, 97, false},
		{MemoryModeAuto, 0, false},
		{MemoryModeNormal, 64, false},
		{MemoryModeLow, 64, true},
		{MemoryModeLow, 512, true},
		{" LOW ", 64, true},
		{"unknown", 64, true},
	}
	for _, tc := range cases {
		s := Settings{MemoryMode: tc.mode}
		if got := s.EffectiveLowMemory(tc.totalMB); got != tc.want {
			t.Fatalf("EffectiveLowMemory(mode=%q,total=%d)=%v，期望 %v",
				tc.mode, tc.totalMB, got, tc.want)
		}
	}
}

func TestFixupMemoryMode(t *testing.T) {
	c := &Config{Version: 2, Settings: Settings{PortRange: "10000-10100"}}
	c.fixup()
	if c.Settings.MemoryMode != MemoryModeAuto {
		t.Fatalf("旧配置空 MemoryMode 应归一化为 auto，实际 %s", c.Settings.MemoryMode)
	}

	c = &Config{Version: 2, Settings: Settings{PortRange: "10000-10100", MemoryMode: " LOW "}}
	c.fixup()
	if c.Settings.MemoryMode != MemoryModeLow {
		t.Fatalf("MemoryMode 应大小写/空白归一化，实际 %s", c.Settings.MemoryMode)
	}

	c = &Config{Version: 2, Settings: Settings{PortRange: "10000-10100", MemoryMode: "bogus"}}
	c.fixup()
	if c.Settings.MemoryMode != MemoryModeAuto {
		t.Fatalf("未知 MemoryMode 应退化为 auto，实际 %s", c.Settings.MemoryMode)
	}
}

// ---------- cgroup 内存感知（纯函数，不依赖运行环境） ----------

func TestParseMemInfo(t *testing.T) {
	data := "MemTotal:       16384 kB\nMemFree:         1024 kB\nMemAvailable:   8192 kB\n"
	total, avail := sys.ParseMemInfo(data)
	if total != 16 || avail != 8 {
		t.Fatalf("ParseMemInfo=%d/%d，期望 16/8", total, avail)
	}
	if total, avail := sys.ParseMemInfo("garbage\nMemTotal: nope kB\nMemAvailable: -1 kB\n"); total != 0 || avail != 0 {
		t.Fatalf("非法 meminfo 应返回 0/0，实际 %d/%d", total, avail)
	}
}

func TestParseCgroupMB(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"67108864", 64},   // cgroup v2 memory.max = 64MiB
		{"268435456", 256}, // 256MiB
		{"max", 0},         // v2 无限制
		{"MAX\n", 0},
		{"9223372036854771712", 0}, // v1 无限制哨兵
		{"", 0},
		{"bogus", 0},
		{"-1", 0},
		{"1048575", 0}, // 不足 1MB 按 0 处理
	}
	for _, tc := range cases {
		if got := sys.ParseCgroupMB(tc.raw); got != tc.want {
			t.Fatalf("ParseCgroupMB(%q)=%d，期望 %d", tc.raw, got, tc.want)
		}
	}
}

func TestParseOOMKills(t *testing.T) {
	// cgroup v2 memory.events
	ev := "low 0\nhigh 0\nmax 12\noom 3\noom_kill 3\n"
	if got := sys.ParseOOMKills(ev); got != 3 {
		t.Fatalf("v2 oom_kill=%d，期望 3", got)
	}
	// cgroup v1 memory.oom_control
	v1 := "oom_kill_disable 0\nunder_oom 0\noom_kill 7\n"
	if got := sys.ParseOOMKills(v1); got != 7 {
		t.Fatalf("v1 oom_kill=%d，期望 7", got)
	}
	for _, bad := range []string{"", "oom_kill bogus\n", "oom 3\n"} {
		if got := sys.ParseOOMKills(bad); got != 0 {
			t.Fatalf("ParseOOMKills(%q)=%d，期望 0", bad, got)
		}
	}
}

func TestParseCgroupPath(t *testing.T) {
	v2, v1 := sys.ParseCgroupPath("0::/\n")
	if v2 != "/" || v1 != "/" {
		t.Fatalf("v2 根路径解析异常: %q/%q", v2, v1)
	}
	v2, v1 = sys.ParseCgroupPath("5:memory:/docker/abc\n3:cpu,cpuacct:/docker/abc\n0::/user.slice\n")
	if v2 != "/user.slice" || v1 != "/docker/abc" {
		t.Fatalf("混合 v1/v2 路径解析异常: v2=%q v1=%q", v2, v1)
	}
}

func TestEffectiveLimitMB(t *testing.T) {
	cases := []struct {
		name string
		info sys.MemInfo
		want int
	}{
		{"无 cgroup 上限用宿主总量", sys.MemInfo{TotalMB: 16384}, 16384},
		{"cgroup 上限优先", sys.MemInfo{TotalMB: 16384, CgroupMaxMB: 64}, 64},
		{"cgroup 无限制回退宿主", sys.MemInfo{TotalMB: 512, CgroupMaxMB: 0}, 512},
		{"完全未知", sys.MemInfo{}, 0},
	}
	for _, tc := range cases {
		if got := tc.info.EffectiveLimitMB(); got != tc.want {
			t.Fatalf("%s: EffectiveLimitMB=%d，期望 %d", tc.name, got, tc.want)
		}
	}
}

func TestEffectiveAvailableMB(t *testing.T) {
	cases := []struct {
		name string
		info sys.MemInfo
		want int
	}{
		{"无 cgroup 上限用 MemAvailable", sys.MemInfo{TotalMB: 512, AvailableMB: 200}, 200},
		{"cgroup 剩余额度更少时取剩余", sys.MemInfo{TotalMB: 16384, AvailableMB: 8000, CgroupMaxMB: 64, CgroupCurrentMB: 40}, 24},
		{"cgroup 剩余额度更多时取 MemAvailable", sys.MemInfo{TotalMB: 256, AvailableMB: 20, CgroupMaxMB: 128, CgroupCurrentMB: 10}, 20},
		{"已用超过上限按 0", sys.MemInfo{AvailableMB: 100, CgroupMaxMB: 64, CgroupCurrentMB: 80}, 0},
		{"MemAvailable 未知时用 cgroup 剩余", sys.MemInfo{CgroupMaxMB: 96, CgroupCurrentMB: 30}, 66},
	}
	for _, tc := range cases {
		if got := tc.info.EffectiveAvailableMB(); got != tc.want {
			t.Fatalf("%s: EffectiveAvailableMB=%d，期望 %d", tc.name, got, tc.want)
		}
	}
}

func TestMemInfoLowMemoryDerived(t *testing.T) {
	cases := []struct {
		name string
		info sys.MemInfo
		want bool
	}{
		{"宿主 64MB", sys.MemInfo{TotalMB: 64, AvailableMB: 32}, true},
		{"阈值 96MB", sys.MemInfo{TotalMB: LowMemoryThresholdMB}, true},
		{"97MB 普通", sys.MemInfo{TotalMB: LowMemoryThresholdMB + 1}, false},
		{"宿主很大但 cgroup 限制 64MB", sys.MemInfo{TotalMB: 16384, AvailableMB: 8000, CgroupMaxMB: 64, CgroupCurrentMB: 10}, true},
		{"cgroup 限制 128MB 不触发", sys.MemInfo{TotalMB: 64, CgroupMaxMB: 128}, false},
		{"完全未知不误判", sys.MemInfo{}, false},
	}
	for _, tc := range cases {
		got := tc.info.WithDerived()
		if got.LowMemory != tc.want {
			t.Fatalf("%s: LowMemory=%v，期望 %v", tc.name, got.LowMemory, tc.want)
		}
	}
	if (sys.MemInfo{}).WithDerived().Known {
		t.Fatal("完全未知时 Known 应为 false")
	}
	if !(sys.MemInfo{TotalMB: 64}).WithDerived().Known {
		t.Fatal("读到 MemTotal 时 Known 应为 true")
	}
}

func TestCgroupLimitSelectsLowMemoryBaseline(t *testing.T) {
	// 模拟：宿主 16GB，但 cgroup 只有 64MB；默认配置必须走低内存基线。
	info := sys.MemInfo{
		TotalMB:         sys.ParseCgroupMB("17179869184"),
		CgroupMaxMB:     sys.ParseCgroupMB("67108864"),
		CgroupCurrentMB: sys.ParseCgroupMB("10485760"),
	}.WithDerived()
	if !info.LowMemory {
		t.Fatal("cgroup 64MB 限制应判定为低内存")
	}
	limit := info.EffectiveLimitMB()
	if limit != 64 {
		t.Fatalf("有效上限应为 64MB，实际 %dMB", limit)
	}
	settings := defaultSettingsForTotalMemory(limit)
	if settings.MemoryMode != MemoryModeLow || settings.MaxConns != 24 ||
		settings.MaxUDPSessions != 8 || settings.UDPEnabled || settings.IdleTimeout != "3m" {
		t.Fatalf("cgroup 限制未落到低内存基线: %v", settings)
	}
	if !settings.EffectiveLowMemory(limit) {
		t.Fatal("auto 模式应按 cgroup 有效上限启用低内存")
	}

	// 无 cgroup 上限时保持普通机器基线。
	normal := sys.MemInfo{TotalMB: 16384, AvailableMB: 8192}.WithDerived()
	if normal.LowMemory || normal.EffectiveLimitMB() != 16384 {
		t.Fatal("无 cgroup 上限时应按宿主总量判定")
	}
	ns := defaultSettingsForTotalMemory(normal.EffectiveLimitMB())
	if ns.MemoryMode != MemoryModeNormal || ns.MaxConns != 128 || ns.IdleTimeout != "5m" {
		t.Fatalf("普通机器基线被改变: %v", ns)
	}
}

func TestDetectTotalMemoryMBPrefersCgroup(t *testing.T) {
	// 纯只读探测：有 cgroup 上限时返回该上限，否则返回宿主总量；不依赖具体运行环境。
	info := sys.ReadMemInfo()
	got := detectTotalMemoryMB()
	if info.CgroupMaxMB > 0 && got != info.CgroupMaxMB {
		t.Fatalf("cgroup 上限 %dMB 应优先，实际 %dMB", info.CgroupMaxMB, got)
	}
	if info.CgroupMaxMB <= 0 && got != info.TotalMB {
		t.Fatalf("无 cgroup 上限时应回退 MemTotal %dMB，实际 %dMB", info.TotalMB, got)
	}
}
