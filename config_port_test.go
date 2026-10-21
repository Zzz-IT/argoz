package main

import "testing"

func TestDefaultPortRangeIsAllPorts(t *testing.T) {
	s := defaultSettings()
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

func TestUpdatePortConflictRejected(t *testing.T) {
	e, _ := newTestEngine(t)
	a, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Create(NodeSpec{Name: "b", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.Update(a.ID, func(n *Node) error { n.Port = b.Port; return nil }); err == nil {
		t.Fatal("端口冲突应报错，而不是静默顺延")
	}
	got, _ := e.Node(a.ID)
	if got.Port != a.Port {
		t.Fatalf("冲突后应回滚到原端口 %d，实际 %d", a.Port, got.Port)
	}
}
