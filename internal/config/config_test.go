package config

import "testing"

func TestDefaultUDPDisabled(t *testing.T) {
	s := DefaultSettings()
	if s.UDPEnabled {
		t.Fatal("UDP 默认应关闭")
	}
	if s.UDPIdle != "60s" || s.MaxUDPSessions != 32 {
		t.Fatalf("UDP 默认值异常: idle=%s sessions=%d", s.UDPIdle, s.MaxUDPSessions)
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
