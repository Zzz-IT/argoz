package config

import "testing"

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
