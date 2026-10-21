package core

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

const testWSKey = "MDEyMzQ1Njc4OWFiY2RlZg==" // base64("0123456789abcdef")

func makeWSFrame(fin bool, op byte, payload []byte, masked bool, rsv byte) []byte {
	first := op | rsv
	if fin {
		first |= 0x80
	}
	out := []byte{first}
	n := len(payload)
	var mb byte
	if masked {
		mb = 0x80
	}
	switch {
	case n < 126:
		out = append(out, byte(n)|mb)
	case n <= 65535:
		out = append(out, 126|mb, byte(n>>8), byte(n))
	default:
		out = append(out, 127|mb)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		out = append(out, b[:]...)
	}
	if masked {
		mask := []byte{0x11, 0x22, 0x33, 0x44}
		out = append(out, mask...)
		for i, b := range payload {
			out = append(out, b^mask[i&3])
		}
	} else {
		out = append(out, payload...)
	}
	return out
}

func makeOversizedFrame() []byte {
	out := []byte{0x80 | opBinary, 0x80 | 127}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(maxWSFrameSize)+1)
	out = append(out, b[:]...)
	out = append(out, 0x11, 0x22, 0x33, 0x44)
	return out
}

func TestWSFrameHeaderValidation(t *testing.T) {
	cases := []struct {
		name    string
		frame   []byte
		wantErr bool
	}{
		{"valid", makeWSFrame(true, opBinary, []byte("abc"), true, 0), false},
		{"unmasked", makeWSFrame(true, opBinary, []byte("abc"), false, 0), true},
		{"rsv", makeWSFrame(true, opBinary, []byte("abc"), true, 0x40), true},
		{"fragmented control", makeWSFrame(false, opPing, nil, true, 0), true},
		{"orphan continuation", makeWSFrame(true, opContinuation, []byte("x"), true, 0), true},
		{"oversized", makeOversizedFrame(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &wsConn{br: bufio.NewReader(bytes.NewReader(tc.frame))}
			_, _, err := c.readFrameHeader()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestWSWriteAfterClose(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	c := &wsConn{nc: c1, br: bufio.NewReader(c2)}
	go func() { _, _ = io.Copy(io.Discard, c2) }()

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, errWSClosed) {
		t.Fatalf("Close 后写入应返回 errWSClosed，got %v", err)
	}
}

func TestWSFrameFragmentation(t *testing.T) {
	first := makeWSFrame(false, opBinary, []byte("abc"), true, 0)
	second := makeWSFrame(true, opContinuation, []byte("def"), true, 0)
	c := &wsConn{br: bufio.NewReader(bytes.NewReader(append(first, second...)))}

	op, plen, err := c.readFrameHeader()
	if err != nil || op != opBinary || plen != 3 {
		t.Fatalf("first frame: op=%d plen=%d err=%v", op, plen, err)
	}
	if _, err := io.CopyN(io.Discard, c.br, 3); err != nil {
		t.Fatal(err)
	}

	op, plen, err = c.readFrameHeader()
	if err != nil || op != opContinuation || plen != 3 {
		t.Fatalf("continuation: op=%d plen=%d err=%v", op, plen, err)
	}
}

func TestWSFrameRejectsNewMessageWhileFragmented(t *testing.T) {
	first := makeWSFrame(false, opBinary, []byte("a"), true, 0)
	second := makeWSFrame(true, opBinary, []byte("b"), true, 0)
	c := &wsConn{br: bufio.NewReader(bytes.NewReader(append(first, second...)))}

	if _, _, err := c.readFrameHeader(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, c.br, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.readFrameHeader(); !errors.Is(err, errWSProtocol) {
		t.Fatalf("应拒绝分片未结束的新数据帧，got %v", err)
	}
}

func TestWSFragmentedEndToEnd(t *testing.T) {
	echo := startEcho(t)
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n5", Name: "t", Protocol: "vless", Path: "/frag", UUID: uuid})

	conn, br := wsDial(t, addr, "/frag")
	defer conn.Close()

	payload := append(vlessHeader(uuid, echo), []byte("fragment-ok")...)
	mid := len(payload) / 2
	wsWriteFrame(t, conn, false, opBinary, payload[:mid])
	wsWriteFrame(t, conn, true, opContinuation, payload[mid:])

	_, resp := wsRead(t, br)
	if !bytes.Equal(resp, []byte{0x00, 0x00}) {
		t.Fatalf("VLESS 应答头错误: %x", resp)
	}
	_, data := wsRead(t, br)
	if string(data) != "fragment-ok" {
		t.Fatalf("分片回显异常: %q", data)
	}
}

func TestWSLargeFrameEndToEnd(t *testing.T) {
	echo := startEcho(t)
	uuid := newUUID()
	addr := startNode(t, Node{ID: "n6", Name: "t", Protocol: "vless", Path: "/large", UUID: uuid})

	conn, br := wsDial(t, addr, "/large")
	defer conn.Close()

	body := bytes.Repeat([]byte("x"), 70*1024)
	wsWriteFrame(t, conn, true, opBinary, append(vlessHeader(uuid, echo), body...))

	_, resp := wsRead(t, br)
	if !bytes.Equal(resp, []byte{0x00, 0x00}) {
		t.Fatalf("VLESS 应答头错误: %x", resp)
	}
	got := make([]byte, 0, len(body))
	for len(got) < len(body) {
		_, part := wsRead(t, br)
		got = append(got, part...)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("大帧回显长度不匹配: got %d want %d", len(got), len(body))
	}
}

func rawWSRequest(addr, path string) string {
	return fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, addr, testWSKey)
}

func TestWSMaxConns(t *testing.T) {
	uuid := newUUID()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rt := &nodeRuntime{
		node:     Node{ID: "n7", Name: "t", Protocol: "vless", Path: "/max", UUID: uuid},
		settings: defaultSettings(),
		state:    "up",
	}
	rt.settings.MaxConns = 1
	rt.startServer(ln)
	t.Cleanup(rt.stopServer)

	addr := ln.Addr().String()
	first, _ := wsDial(t, addr, "/max")
	defer first.Close()

	second, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Write([]byte(rawWSRequest(addr, "/max"))); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("超过 MaxConns 应返回 503，实际 %d", resp.StatusCode)
	}
}

func TestWSHandshakeTimeout(t *testing.T) {
	old := getWSHandshakeTimeout()
	setWSHandshakeTimeout(100 * time.Millisecond)
	defer setWSHandshakeTimeout(old)

	uuid := newUUID()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rt := &nodeRuntime{
		node:     Node{ID: "n8", Name: "t", Protocol: "vless", Path: "/timeout", UUID: uuid},
		settings: defaultSettings(),
		state:    "up",
	}
	rt.startServer(ln)
	t.Cleanup(rt.stopServer)

	conn, br := wsDial(t, ln.Addr().String(), "/timeout")
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	for {
		op, _, err := readFrameRaw(br)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("协议握手超时后连接未关闭")
			}
			break
		}
		if op == opClose {
			break
		}
	}
}

func TestStopServerClosesWS(t *testing.T) {
	uuid := newUUID()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rt := &nodeRuntime{
		node:     Node{ID: "n9", Name: "t", Protocol: "vless", Path: "/stop", UUID: uuid},
		settings: defaultSettings(),
		state:    "up",
	}
	rt.startServer(ln)
	t.Cleanup(rt.stopServer)

	conn, br := wsDial(t, ln.Addr().String(), "/stop")
	defer conn.Close()

	rt.stopServer()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := readFrameRaw(br)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("stopServer 后连接未关闭")
			}
			return
		}
	}
}
