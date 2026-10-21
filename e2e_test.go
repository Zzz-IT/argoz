package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---------- 极简 WS 客户端（仅测试用） ----------

func wsDial(t *testing.T, addr, path string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, addr, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("写握手失败: %v", err)
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("读握手响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("期望 101，实际 %d", resp.StatusCode)
	}
	return conn, br
}

func wsWrite(t *testing.T, conn net.Conn, op byte, payload []byte) {
	t.Helper()
	wsWriteFrame(t, conn, true, op, payload)
}

func wsWriteFrame(t *testing.T, conn net.Conn, fin bool, op byte, payload []byte) {
	t.Helper()
	var first byte
	if fin {
		first = 0x80 | op
	} else {
		first = op
	}
	hdr := []byte{first}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n)|0x80)
	case n <= 65535:
		hdr = append(hdr, 126|0x80, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127|0x80)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, b[:]...)
	}
	mask := []byte{0x11, 0x22, 0x33, 0x44}
	hdr = append(hdr, mask...)
	out := make([]byte, n)
	for i := range payload {
		out[i] = payload[i] ^ mask[i&3]
	}
	if _, err := conn.Write(append(hdr, out...)); err != nil {
		t.Fatalf("写帧失败: %v", err)
	}
}

func wsRead(t *testing.T, br *bufio.Reader) (byte, []byte) {
	t.Helper()
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		t.Fatalf("读帧头失败: %v", err)
	}
	op := h[0] & 0x0f
	n := int(h[1] & 0x7f)
	if n == 126 {
		b := make([]byte, 2)
		if _, err := io.ReadFull(br, b); err != nil {
			t.Fatalf("读长度失败: %v", err)
		}
		n = int(binary.BigEndian.Uint16(b))
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(br, payload); err != nil {
			t.Fatalf("读负载失败: %v", err)
		}
	}
	return op, payload
}

// ---------- 测试脚手架 ----------

func startEcho(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("回显服务监听失败: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func startNode(t *testing.T, n Node) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("节点监听失败: %v", err)
	}
	rt := &nodeRuntime{node: n, settings: defaultSettings(), state: "up"}
	rt.startServer(ln)
	t.Cleanup(rt.stopServer)
	return ln.Addr().String()
}

func hostPort(addr string) (string, int) {
	h, p, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(p, "%d", &port)
	return h, port
}

func vlessHeader(uuid string, target string) []byte {
	h, p := hostPort(target)
	u, _ := parseUUID(uuid)
	buf := []byte{0x00}
	buf = append(buf, u[:]...)
	buf = append(buf, 0x00) // addons 长度
	buf = append(buf, 0x01) // cmd = TCP
	buf = append(buf, byte(p>>8), byte(p))
	ip := net.ParseIP(h).To4()
	buf = append(buf, 0x01)
	buf = append(buf, ip...)
	return buf
}

// ---------- 测试用例 ----------

func TestVLESSEndToEnd(t *testing.T) {
	echo := startEcho(t)
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n1", Name: "t", Protocol: "vless", Path: "/abc", UUID: uuid})

	conn, br := wsDial(t, addr, "/abc")
	defer conn.Close()

	payload := append(vlessHeader(uuid, echo), []byte("hello-argoz")...)
	wsWrite(t, conn, opBinary, payload)

	_, resp := wsRead(t, br)
	if !bytes.Equal(resp, []byte{0x00, 0x00}) {
		t.Fatalf("VLESS 应答头错误: %x", resp)
	}
	_, data := wsRead(t, br)
	if string(data) != "hello-argoz" {
		t.Fatalf("回显不匹配: %q", data)
	}

	// 验证流式：再发一段，应原样回来（同一 WS 连接内多帧）
	wsWrite(t, conn, opBinary, []byte("second-chunk"))
	_, data2 := wsRead(t, br)
	if string(data2) != "second-chunk" {
		t.Fatalf("第二段回显不匹配: %q", data2)
	}
}

func TestTrojanEndToEnd(t *testing.T) {
	echo := startEcho(t)
	password := "pass-123456"
	addr := startNode(t, Node{ID: "n2", Name: "t", Protocol: "trojan", Path: "/xyz", Password: password})

	conn, br := wsDial(t, addr, "/xyz")
	defer conn.Close()

	sum := sha224Hex(password)
	h, p := hostPort(echo)
	ip := net.ParseIP(h).To4()
	header := append([]byte(sum), '\r', '\n', 0x01, 0x01)
	header = append(header, ip...)
	header = append(header, byte(p>>8), byte(p), '\r', '\n')
	payload := append(header, []byte("trojan-hello")...)

	wsWrite(t, conn, opBinary, payload)
	_, data := wsRead(t, br)
	if string(data) != "trojan-hello" {
		t.Fatalf("Trojan 回显不匹配: %q", data)
	}
}

func TestWrongPathRejected(t *testing.T) {
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n3", Name: "t", Protocol: "vless", Path: "/right", UUID: uuid})

	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	req := fmt.Sprintf("GET /wrong HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", addr, key)
	conn.Write([]byte(req))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("读响应失败: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("错误路径应返回 404，实际 %d", resp.StatusCode)
	}
}

func TestWrongUUIDRejected(t *testing.T) {
	echo := startEcho(t)
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n4", Name: "t", Protocol: "vless", Path: "/p", UUID: uuid})

	conn, br := wsDial(t, addr, "/p")
	defer conn.Close()

	wrong := newUUID()
	wsWrite(t, conn, opBinary, append(vlessHeader(wrong, echo), []byte("x")...))

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		op, payload, err := readFrameRaw(br)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("认证失败后连接未关闭")
			}
			break
		}
		if op == opBinary || op == opText {
			t.Fatalf("认证失败后收到数据帧: %x", payload)
		}
		if op == opClose {
			break
		}
	}
}

func readFrameRaw(br *bufio.Reader) (byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		return 0, nil, err
	}
	n := int(h[1] & 0x7f)
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(br, payload); err != nil {
			return 0, nil, err
		}
	}
	return h[0] & 0x0f, payload, nil
}

func TestRenderURIAndYAML(t *testing.T) {
	n := Node{
		Name:     "Argo-VLESS-1",
		Protocol: "vless",
		UUID:     "8f3a1c2b-1111-4222-8333-444455556666",
		Path:     "/a1b2c3d4e5f6",
		Tunnel:   TunnelCfg{Domain: "xxx-yyy.trycloudflare.com"},
	}
	s := defaultSettings()

	uri := n.URI(s)
	for _, want := range []string{"vless://8f3a1c2b", "xxx-yyy.trycloudflare.com:443", "type=ws", "path=%2Fa1b2c3d4e5f6", "sni=cloudflare-ech.com"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("URI 缺少 %q: %s", want, uri)
		}
	}

	yaml := n.YAML(s)
	for _, want := range []string{"type: vless", "uuid: 8f3a1c2b", "network: ws", "ech-opts:", "path: /a1b2c3d4e5f6"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("YAML 缺少 %q:\n%s", want, yaml)
		}
	}

	s.ECH = false
	if !strings.Contains(n.URI(s), "sni=xxx-yyy.trycloudflare.com") {
		t.Fatal("关闭 ECH 后 SNI 应为真实域名")
	}
	if strings.Contains(n.YAML(s), "ech-opts") {
		t.Fatal("关闭 ECH 后不应出现 ech-opts")
	}
}

func TestParseUUIDFormats(t *testing.T) {
	u := "8f3a1c2b-1111-4222-8333-444455556666"
	a, err := parseUUID(u)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseUUID(strings.ReplaceAll(u, "-", ""))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("两种 UUID 写法应等价")
	}
	if _, err := parseUUID("not-a-uuid"); err == nil {
		t.Fatal("非法 UUID 应报错")
	}
}

func TestExtractToken(t *testing.T) {
	raw := "cloudflared.exe service install eyJhIjoiY2YifQ=="
	if got := extractToken(raw); got != "eyJhIjoiY2YifQ==" {
		t.Fatalf("Token 提取错误: %s", got)
	}
	if got := extractToken("eyJhIjoiY2YifQ=="); got != "eyJhIjoiY2YifQ==" {
		t.Fatalf("纯 Token 处理错误: %s", got)
	}
}

func sha224Hex(s string) string {
	sum := sha256.Sum224([]byte(s))
	return hex.EncodeToString(sum[:])
}
