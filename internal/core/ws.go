package core

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

const (
	// maxWSFrameSize 限制单帧大小。relay 使用 16KB 缓冲，正常客户端远小于该值，
	// 但必须防止异常长度把一条连接拖死或触发整数溢出。
	maxWSFrameSize = 1 << 20 // 1 MiB
	wsWriteTimeout = 30 * time.Second
)

var (
	errWSClosed   = errors.New("websocket 已关闭")
	errWSProtocol = errors.New("websocket 协议错误")
)

// wsConn 把一条 WebSocket 连接包装成字节流（帧边界对上层透明）
type wsConn struct {
	nc  net.Conn
	br  *bufio.Reader
	wmu sync.Mutex

	remaining  int64
	mask       [4]byte
	maskOff    int
	masked     bool
	fragmented bool

	closeOnce   sync.Once
	writeClosed atomic.Bool
	lastRecv    atomic.Int64
}

func headerHasToken(h, tok string) bool {
	for _, p := range strings.Split(h, ",") {
		if strings.EqualFold(strings.TrimSpace(p), tok) {
			return true
		}
	}
	return false
}

func isWebSocketRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if !headerHasToken(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") {
		return false
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return false
	}
	return strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) == "13"
}

// upgradeWS 完成握手并劫持连接
func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !isWebSocketRequest(r) {
		return nil, errors.New("不是合法的 websocket 请求")
	}

	h := sha1.New()
	io.WriteString(h, strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))+wsMagic)
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("当前服务器不支持连接劫持")
	}
	nc, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	_ = nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		nc.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		nc.Close()
		return nil, err
	}
	_ = nc.SetWriteDeadline(time.Time{})

	c := &wsConn{nc: nc, br: brw.Reader}
	c.touch()
	return c, nil
}

// ---------- 读 ----------

func (c *wsConn) touch() {
	c.lastRecv.Store(time.Now().UnixNano())
}

// LastRecv 返回最近一次读到帧头的时间，用于判断对端是否还活着。
func (c *wsConn) LastRecv() time.Time {
	n := c.lastRecv.Load()
	if n == 0 {
		return time.Now()
	}
	return time.Unix(0, n)
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.nc.SetReadDeadline(t)
}

func (c *wsConn) unmask(b []byte) {
	if !c.masked {
		return
	}
	for i := range b {
		b[i] ^= c.mask[(c.maskOff+i)&3]
	}
	c.maskOff = (c.maskOff + len(b)) & 3
}

func (c *wsConn) readFrameHeader() (byte, int64, error) {
	var h [2]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return 0, 0, err
	}
	c.touch()

	fin := h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		return 0, 0, fmt.Errorf("%w: RSV 位非零", errWSProtocol)
	}

	op := h[0] & 0x0f
	switch op {
	case opContinuation, opText, opBinary, opClose, opPing, opPong:
	default:
		return 0, 0, fmt.Errorf("%w: 未知 opcode %d", errWSProtocol, op)
	}

	c.masked = h[1]&0x80 != 0
	plen := int64(h[1] & 0x7f)
	switch plen {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return 0, 0, err
		}
		plen = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return 0, 0, err
		}
		u := binary.BigEndian.Uint64(b[:])
		if u > uint64(maxWSFrameSize) {
			return 0, 0, fmt.Errorf("%w: 帧长度 %d 超过上限", errWSProtocol, u)
		}
		plen = int64(u)
	}
	if plen < 0 || plen > maxWSFrameSize {
		return 0, 0, fmt.Errorf("%w: 帧长度非法 %d", errWSProtocol, plen)
	}

	if op >= opClose {
		if !fin {
			return 0, 0, fmt.Errorf("%w: 控制帧不能分片", errWSProtocol)
		}
		if plen > 125 {
			return 0, 0, fmt.Errorf("%w: 控制帧过长 %d", errWSProtocol, plen)
		}
	}

	// 服务端收到的客户端帧必须带掩码。
	if !c.masked {
		return 0, 0, fmt.Errorf("%w: 客户端帧未加掩码", errWSProtocol)
	}

	// 维护分片状态，防止把孤立的 continuation 当成正常数据。
	if op == opContinuation {
		if !c.fragmented {
			return 0, 0, fmt.Errorf("%w: 没有起始分片", errWSProtocol)
		}
		if fin {
			c.fragmented = false
		}
	} else if op == opText || op == opBinary {
		if c.fragmented {
			return 0, 0, fmt.Errorf("%w: 上一条消息尚未结束", errWSProtocol)
		}
		if !fin {
			c.fragmented = true
		}
	}

	c.maskOff = 0
	if _, err := io.ReadFull(c.br, c.mask[:]); err != nil {
		return 0, 0, err
	}
	return op, plen, nil
}

func (c *wsConn) readControl(plen int64) ([]byte, error) {
	if plen <= 0 {
		return nil, nil
	}
	if plen > 125 {
		return nil, fmt.Errorf("控制帧长度非法: %d", plen)
	}
	buf := make([]byte, plen)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, err
	}
	c.unmask(buf)
	return buf, nil
}

// nextDataFrame 跳过控制帧，定位到下一个数据帧
func (c *wsConn) nextDataFrame() error {
	for {
		op, plen, err := c.readFrameHeader()
		if err != nil {
			return err
		}
		switch op {
		case opBinary, opText, opContinuation:
			if plen == 0 {
				continue
			}
			c.remaining = plen
			return nil
		case opPing:
			payload, err := c.readControl(plen)
			if err != nil {
				return err
			}
			if !c.writeClosed.Load() {
				_ = c.writeFrame(opPong, payload)
			}
		case opPong:
			if _, err := c.readControl(plen); err != nil {
				return err
			}
		case opClose:
			if plen == 1 {
				return fmt.Errorf("%w: close 帧长度非法", errWSProtocol)
			}
			_, _ = c.readControl(plen)
			_ = c.sendClose()
			return io.EOF
		default:
			return fmt.Errorf("%w: 未知 websocket opcode %d", errWSProtocol, op)
		}
	}
}

func (c *wsConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if c.remaining == 0 {
			if err := c.nextDataFrame(); err != nil {
				if errors.Is(err, errWSProtocol) {
					_ = c.sendCloseCode(1002, "")
				}
				return 0, err
			}
			continue
		}
		n := len(p)
		if int64(n) > c.remaining {
			n = int(c.remaining)
		}
		read, err := c.br.Read(p[:n])
		if read > 0 {
			c.unmask(p[:read])
			c.remaining -= int64(read)
			c.touch()
			return read, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// ---------- 写 ----------

func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()

	if len(payload) > maxWSFrameSize {
		return fmt.Errorf("%w: 发送帧过大", errWSProtocol)
	}

	var hdr [10]byte
	hdr[0] = 0x80 | op
	n := len(payload)
	var hl int
	switch {
	case n < 126:
		hdr[1] = byte(n)
		hl = 2
	case n <= 65535:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
		hl = 4
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
		hl = 10
	}

	bufs := net.Buffers{hdr[:hl]}
	if n > 0 {
		bufs = append(bufs, payload)
	}
	_ = c.nc.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_, err := bufs.WriteTo(c.nc)
	return err
}

func (c *wsConn) Write(p []byte) (int, error) {
	if c.writeClosed.Load() {
		return 0, errWSClosed
	}
	if err := c.writeFrame(opBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// sendCloseCode 发送关闭帧（只发一次）。调用后写方向关闭。
func (c *wsConn) sendCloseCode(code uint16, reason string) error {
	if c.writeClosed.Load() {
		return errWSClosed
	}
	var err error
	c.closeOnce.Do(func() {
		if c.writeClosed.Load() {
			return
		}
		c.writeClosed.Store(true)
		if len(reason) > 123 {
			reason = reason[:123]
		}
		payload := make([]byte, 2+len(reason))
		binary.BigEndian.PutUint16(payload, code)
		copy(payload[2:], reason)
		err = c.writeFrame(opClose, payload)
	})
	return err
}

func (c *wsConn) sendClose() error {
	return c.sendCloseCode(1000, "")
}

// Ping 发送心跳
func (c *wsConn) Ping() error {
	if c.writeClosed.Load() {
		return errWSClosed
	}
	return c.writeFrame(opPing, nil)
}

// Close 彻底关闭
func (c *wsConn) Close() error {
	_ = c.sendClose()
	c.writeClosed.Store(true)
	return c.nc.Close()
}

// abort 立即断开，不等待 close 握手，用于超时或节点停止。
func (c *wsConn) abort() {
	c.writeClosed.Store(true)
	_ = c.nc.Close()
}

func (c *wsConn) RemoteAddr() net.Addr { return c.nc.RemoteAddr() }
