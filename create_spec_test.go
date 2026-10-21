package main

import (
	"net"
	"testing"
	"time"
)

func TestCreateWithManualParams(t *testing.T) {
	e, dir := newTestEngine(t)

	spec := NodeSpec{
		Name:     "手填节点",
		Protocol: "vless",
		Mode:     "temp",
		Port:     21510,
		Path:     "/mypath",
		UUID:     "8f3a1c2b-1111-4222-8333-444455556666",
	}
	n, err := e.Create(spec)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if n.Port != 21510 {
		t.Fatalf("端口应为 21510，实际 %d", n.Port)
	}
	if n.Path != "/mypath" {
		t.Fatalf("路径应为 /mypath，实际 %s", n.Path)
	}
	if n.UUID != "8f3a1c2b-1111-4222-8333-444455556666" {
		t.Fatalf("UUID 未按填写值保存: %s", n.UUID)
	}

	// 监听应真的在 21510
	c, err := net.DialTimeout("tcp", "127.0.0.1:21510", time.Second)
	if err != nil {
		t.Fatalf("手填端口未监听: %v", err)
	}
	c.Close()

	// 配置落盘
	got, _ := e.Node(n.ID)
	if got.Port != 21510 || got.Path != "/mypath" {
		t.Fatalf("配置未落盘: %+v", got)
	}
	_ = dir
}

func TestCreateRejectsBusyPort(t *testing.T) {
	e, _ := newTestEngine(t)
	if _, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp"}); err != nil {
		t.Fatal(err)
	}
	nodes := e.Nodes()
	first := nodes[0]
	_, err := e.Create(NodeSpec{Name: "b", Protocol: "vless", Mode: "temp", Port: first.Port})
	if err == nil {
		t.Fatal("占用中的端口应被拒绝")
	}
}

func TestCreateRejectsBadPath(t *testing.T) {
	e, _ := newTestEngine(t)
	if _, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp", Path: "nope"}); err == nil {
		t.Fatal("路径缺少 / 应被拒绝")
	}
	if _, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp", Path: "/has space"}); err == nil {
		t.Fatal("路径含空格应被拒绝")
	}
}

func TestCreateRejectsBadUUID(t *testing.T) {
	e, _ := newTestEngine(t)
	if _, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp", UUID: "not-a-uuid"}); err == nil {
		t.Fatal("非法 UUID 应被拒绝")
	}
}

func TestCreateNormalizesUUID(t *testing.T) {
	e, _ := newTestEngine(t)
	n, err := e.Create(NodeSpec{Name: "a", Protocol: "vless", Mode: "temp", UUID: "8f3a1c2b111142228333444455556666"})
	if err != nil {
		t.Fatal(err)
	}
	if n.UUID != "8f3a1c2b-1111-4222-8333-444455556666" {
		t.Fatalf("UUID 未规范化: %s", n.UUID)
	}
}

func TestCreateTrojanPassword(t *testing.T) {
	e, _ := newTestEngine(t)
	n, err := e.Create(NodeSpec{Name: "t", Protocol: "trojan", Mode: "temp", Password: "short"})
	if err == nil {
		t.Fatal("过短密码应被拒绝")
	}
	n, err = e.Create(NodeSpec{Name: "t", Protocol: "trojan", Mode: "temp", Password: "0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if n.Password != "0123456789abcdef" {
		t.Fatalf("密码未按填写值保存: %s", n.Password)
	}
}
