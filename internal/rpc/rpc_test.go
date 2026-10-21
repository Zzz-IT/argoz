package rpc

import (
	"argoz/internal/config"
	"argoz/internal/core"
	"testing"
	"time"
)

func TestRPCClientServer(t *testing.T) {
	e, dir := newTestEngine(t)

	srv, err := Start(e, dir)
	if err != nil {
		t.Fatalf("启动 RPC 服务失败: %v", err)
	}
	defer srv.Shutdown()

	// 等控制通道就绪
	var c *rpcClient
	for i := 0; i < 30; i++ {
		c, err = dialRPC(dir)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接 daemon 失败: %v", err)
	}
	defer c.Close()

	// 创建
	n, err := c.Create(NodeSpec{Name: "rpc节点", Protocol: "vless", Mode: "temp", Port: 21512})
	if err != nil {
		t.Fatalf("远程创建失败: %v", err)
	}
	if n.Port != 21512 {
		t.Fatalf("端口异常: %d", n.Port)
	}

	// 列表
	if got := c.Statuses(); len(got) != 1 || got[0].Node.Name != "rpc节点" {
		t.Fatalf("列表异常: %+v", got)
	}

	// 修改（客户端 diff -> patch）
	if err := c.Update(n.ID, func(x *Node) error { x.Port = 21513; return nil }); err != nil {
		t.Fatalf("远程修改失败: %v", err)
	}
	got, ok := c.Node(n.ID)
	if !ok || got.Port != 21513 {
		t.Fatalf("修改未生效: %+v", got)
	}

	// 端口占用检查
	if err := c.CheckPortFree(21513, ""); err == nil {
		t.Fatal("已占用端口应报错")
	}

	// 设置
	s := c.Settings()
	s.MaxConns = 64
	if err := c.SaveSettings(s); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	if c.Settings().MaxConns != 64 {
		t.Fatal("设置未生效")
	}

	// 删除
	if err := c.Delete(n.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(c.Nodes()) != 0 {
		t.Fatal("节点未删除")
	}
}

func TestRPCTotals(t *testing.T) {
	e, dir := newTestEngine(t)
	srv, err := Start(e, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	c, err := dialRPC(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Create(NodeSpec{Name: "x", Protocol: "vless", Mode: "temp"}); err != nil {
		t.Fatal(err)
	}
	nodes, conns, up, down := c.Totals()
	if nodes != 1 || conns != 0 || up != 0 || down != 0 {
		t.Fatalf("总量异常: %d %d %d %d", nodes, conns, up, down)
	}
}

func newTestEngine(t *testing.T) (*core.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.LoadConfig(dir)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	e := core.NewEngine(cfg, dir)
	e.SetDaemon(true)
	t.Cleanup(func() { e.Shutdown() })
	return e, dir
}

func TestHelloHandshake(t *testing.T) {
	e, dir := newTestEngine(t)
	srv, err := Start(e, dir)
	if err != nil {
		t.Fatalf("启动 RPC 服务失败: %v", err)
	}
	defer srv.Shutdown()

	var c *rpcClient
	for i := 0; i < 30; i++ {
		c, err = dialRPC(dir)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer c.Close()

	if c.DaemonProto() != ProtoVersion {
		t.Fatalf("协议版本应为 %d，实际 %d", ProtoVersion, c.DaemonProto())
	}
	if c.DaemonVersion() == "" {
		t.Fatal("握手未返回版本号")
	}
}
