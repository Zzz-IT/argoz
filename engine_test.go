package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()

	old := cloudflaredLookup
	cloudflaredLookup = func(ctx context.Context) (string, error) { return "", errors.New("测试环境未安装 cloudflared") }
	t.Cleanup(func() { cloudflaredLookup = old })

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	e := NewEngine(cfg, dir)
	t.Cleanup(e.Shutdown)

	// 用小端口段，便于精确测试分配
	s := cfg.Settings
	s.PortRange = "21500-21520"
	if err := e.SaveSettings(s); err != nil {
		t.Fatalf("保存设置失败: %v", err)
	}
	return e, dir
}

func TestEngineCreateUpdateDelete(t *testing.T) {
	e, dir := newTestEngine(t)

	node, err := e.Create(NodeSpec{Name: "测试节点", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if node.Port < 21500 || node.Port > 21520 {
		t.Fatalf("端口不在范围内: %d", node.Port)
	}
	if len(node.UUID) != 36 {
		t.Fatalf("UUID 生成异常: %q", node.UUID)
	}
	if len(node.Path) != 13 {
		t.Fatalf("路径长度异常: %q", node.Path)
	}

	// 落盘检查
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if !contains(string(data), node.ID) || !contains(string(data), node.Path) {
		t.Fatal("配置未落盘")
	}

	// 监听可用：用真实 WS 握手 + VLESS 头打到本地回显
	echo := startEcho(t)
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(node.Port))
	conn, br := wsDial(t, addr, node.Path)
	payload := append(vlessHeader(node.UUID, echo), []byte("engine-ok")...)
	wsWrite(t, conn, opBinary, payload)
	_, resp := wsRead(t, br)
	if string(resp) != "\x00\x00" {
		t.Fatalf("VLESS 应答异常: %x", resp)
	}
	_, data2 := wsRead(t, br)
	if string(data2) != "engine-ok" {
		t.Fatalf("回显异常: %q", data2)
	}
	conn.Close()

	// 修改 UUID：旧凭据失效、新凭据可用
	newU := newUUID()
	if err := e.Update(node.ID, func(n *Node) error { n.UUID = newU; return nil }); err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	conn2, br2 := wsDial(t, addr, node.Path)
	wsWrite(t, conn2, opBinary, append(vlessHeader(node.UUID, echo), []byte("old")...))
	conn2.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	gotBinary := false
	for {
		op, pl, err := readFrameRaw(br2)
		if err != nil {
			break
		}
		if op == opBinary && len(pl) > 0 {
			gotBinary = true
			break
		}
	}
	if gotBinary {
		t.Fatal("旧 UUID 不应通过认证")
	}
	conn2.Close()

	conn3, br3 := wsDial(t, addr, node.Path)
	wsWrite(t, conn3, opBinary, append(vlessHeader(newU, echo), []byte("new-uuid")...))
	wsRead(t, br3) // 应答头
	_, got := wsRead(t, br3)
	if string(got) != "new-uuid" {
		t.Fatalf("新 UUID 回显异常: %q", got)
	}
	conn3.Close()

	// 删除后端口释放
	if err := e.Delete(node.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if len(e.Nodes()) != 0 {
		t.Fatal("节点未从配置移除")
	}
	time.Sleep(100 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("删除后端口仍在监听")
	}
}

func TestPortConflictAutoShift(t *testing.T) {
	e, _ := newTestEngine(t)

	// 占住段首端口
	blocker, err := net.Listen("tcp", "127.0.0.1:21500")
	if err != nil {
		t.Skip("21500 已被系统占用，跳过")
	}
	defer blocker.Close()

	node, err := e.Create(NodeSpec{Name: "冲突测试", Protocol: "vless", Mode: "temp"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if node.Port == 21500 {
		t.Fatal("应自动避开被占用端口")
	}
	if node.Port < 21501 || node.Port > 21520 {
		t.Fatalf("端口异常: %d", node.Port)
	}
}

func TestPortRangeExhausted(t *testing.T) {
	e, _ := newTestEngine(t)

	s := e.Settings()
	s.PortRange = "21600-21601"
	if err := e.SaveSettings(s); err != nil {
		t.Fatal(err)
	}

	holder1, err := net.Listen("tcp", "127.0.0.1:21600")
	if err != nil {
		t.Skip("端口被占用，跳过")
	}
	defer holder1.Close()
	holder2, err := net.Listen("tcp", "127.0.0.1:21601")
	if err != nil {
		t.Skip("端口被占用，跳过")
	}
	defer holder2.Close()

	if _, err := e.Create(NodeSpec{Name: "无端口", Protocol: "vless", Mode: "temp"}); err == nil {
		t.Fatal("端口耗尽时应报错")
	}
}

func TestTrojanCreateAndLink(t *testing.T) {
	e, _ := newTestEngine(t)
	node, err := e.Create(NodeSpec{Name: "trojan节点", Protocol: "trojan", Mode: "fixed", Token: "eyJ0ZXN0IjoxfQ==", Domain: "argo.example.com"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if node.Password == "" {
		t.Fatal("trojan 应生成密码")
	}

	s := e.Settings()
	uri := node.URI(s)
	if !contains(uri, "trojan://") || !contains(uri, "argo.example.com:443") {
		t.Fatalf("链接异常: %s", uri)
	}
	yaml := node.YAML(s)
	if !contains(yaml, "type: trojan") || !contains(yaml, "password: "+node.Password) {
		t.Fatalf("YAML 异常: %s", yaml)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
