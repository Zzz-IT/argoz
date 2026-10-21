package main

import (
	"bytes"
	"net"
	"testing"
)

func TestVlessAddrTypes(t *testing.T) {
	// IPv4
	buf := bytes.NewReader([]byte{0x01, 1, 2, 3, 4})
	if h, err := readVlessAddr(buf); err != nil || h != "1.2.3.4" {
		t.Fatalf("IPv4 解析错误: %q %v", h, err)
	}

	// 域名（VLESS 用 2，SOCKS5 才是 3）
	host := "example.com"
	buf = bytes.NewReader(append([]byte{0x02, byte(len(host))}, []byte(host)...))
	if h, err := readVlessAddr(buf); err != nil || h != host {
		t.Fatalf("域名解析错误: %q %v", h, err)
	}

	// IPv6（VLESS 用 3）
	ip := net.ParseIP("2001:db8::1").To16()
	buf = bytes.NewReader(append([]byte{0x03}, ip...))
	if h, err := readVlessAddr(buf); err != nil || h != "2001:db8::1" {
		t.Fatalf("IPv6 解析错误: %q %v", h, err)
	}

	// 不支持的类型
	buf = bytes.NewReader([]byte{0x09, 0, 0})
	if _, err := readVlessAddr(buf); err == nil {
		t.Fatal("未知地址类型应报错")
	}
}

// 端到端：目标用域名（localhost），验证 VLESS atyp=2 能连通
func TestVLESSEndToEndDomainTarget(t *testing.T) {
	echo := startEcho(t)
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n9", Name: "t", Protocol: "vless", Path: "/dom", UUID: uuid})

	conn, br := wsDial(t, addr, "/dom")
	defer conn.Close()

	_, portStr, _ := net.SplitHostPort(echo)
	var port int
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	u, _ := parseUUID(uuid)
	header := []byte{0x00}
	header = append(header, u[:]...)
	header = append(header, 0x00, 0x01) // addons 长度 0，cmd=TCP
	header = append(header, byte(port>>8), byte(port))
	header = append(header, 0x02, byte(len("localhost")))
	header = append(header, []byte("localhost")...)

	wsWrite(t, conn, opBinary, append(header, []byte("domain-ok")...))

	_, resp := wsRead(t, br)
	if len(resp) != 2 || resp[0] != 0 || resp[1] != 0 {
		t.Fatalf("VLESS 应答头异常: %x", resp)
	}
	_, data := wsRead(t, br)
	if string(data) != "domain-ok" {
		t.Fatalf("域名目标回显异常: %q", data)
	}
}
