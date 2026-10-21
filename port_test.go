package main

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestUpdatePortRebinds(t *testing.T) {
	e, _ := newTestEngine(t)
	n, err := e.Create(NodeSpec{Name: "端口测试", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatal(err)
	}
	old := n.Port
	newPort := old + 7
	t.Logf("旧端口 %d -> 新端口 %d", old, newPort)

	if err := e.Update(n.ID, func(x *Node) error { x.Port = newPort; return nil }); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	got, _ := e.Node(n.ID)
	t.Logf("更新后配置端口: %d", got.Port)
	if got.Port != newPort {
		t.Fatalf("配置端口未更新: %d", got.Port)
	}

	// 旧端口应关闭
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", old), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatalf("旧端口 %d 仍在监听", old)
	}
	// 新端口应可连
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", newPort), time.Second)
	if err != nil {
		t.Fatalf("新端口 %d 不可连接: %v", newPort, err)
	}
	c.Close()

	// 再次更新（同一个端口号）不应报错
	if err := e.Update(n.ID, func(x *Node) error { x.Port = newPort; return nil }); err != nil {
		t.Fatalf("重复更新失败: %v", err)
	}
}
