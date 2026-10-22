package core

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxUDPPacket = 65507
	udpBufSize   = 64 << 10
)

var udpWritePool = sync.Pool{
	New: func() any {
		b := make([]byte, udpBufSize+2)
		return &b
	},
}

type udpSession struct {
	conn *net.UDPConn
	last atomic.Int64
}

func (s *udpSession) touch() {
	s.last.Store(time.Now().UnixNano())
}

func (s *udpSession) idleFor() time.Duration {
	n := s.last.Load()
	if n == 0 {
		return 0
	}
	return time.Since(time.Unix(0, n))
}

func (rt *nodeRuntime) acquireUDPSession(conn *net.UDPConn, max int) (*udpSession, bool) {
	if max <= 0 {
		max = 32
	}
	if max > 128 {
		max = 128
	}
	rt.udpMu.Lock()
	defer rt.udpMu.Unlock()
	if rt.udpSessions == nil {
		rt.udpSessions = make(map[*udpSession]struct{})
	}
	if len(rt.udpSessions) >= max {
		return nil, false
	}
	s := &udpSession{conn: conn}
	s.touch()
	rt.udpSessions[s] = struct{}{}
	rt.udpConns.Add(1)
	return s, true
}

func (rt *nodeRuntime) releaseUDPSession(s *udpSession) {
	rt.udpMu.Lock()
	delete(rt.udpSessions, s)
	rt.udpMu.Unlock()
	rt.udpConns.Add(-1)
}

func (rt *nodeRuntime) closeUDPSessions() {
	rt.udpMu.Lock()
	sessions := make([]*udpSession, 0, len(rt.udpSessions))
	for s := range rt.udpSessions {
		sessions = append(sessions, s)
	}
	rt.udpSessions = make(map[*udpSession]struct{})
	rt.udpMu.Unlock()

	for _, s := range sessions {
		_ = s.conn.Close()
	}
}

// serveUDP 处理一条 VLESS UDP 会话。
// 一条 VLESS/WS 连接只对应一个目标地址，使用已连接 UDP socket。
func (rt *nodeRuntime) serveUDP(ws *wsConn, target string, settings Settings) {
	conn, err := net.DialTimeout("udp", target, 5*time.Second)
	if err != nil {
		rt.setError(fmt.Sprintf("UDP 连接目标 %s 失败: %v", target, err))
		return
	}
	uc, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		rt.setError("UDP 连接类型异常")
		return
	}
	defer uc.Close()

	// 64KB 已足够容纳单个 UDP 数据报；不要设置 1MB，128MB VPS 会爆。
	_ = uc.SetReadBuffer(udpBufSize)
	_ = uc.SetWriteBuffer(udpBufSize)

	s, ok := rt.acquireUDPSession(uc, settings.MaxUDPSessionsOrDefault())
	if !ok {
		rt.setError(fmt.Sprintf("UDP 会话数已达上限 %d", settings.MaxUDPSessionsOrDefault()))
		return
	}
	defer rt.releaseUDPSession(s)

	var wg sync.WaitGroup
	wg.Add(2)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = uc.Close()
			ws.abort()
		})
	}

	go func() {
		defer wg.Done()
		rt.udpStreamToSocket(ws, s)
		stop()
	}()
	go func() {
		defer wg.Done()
		rt.udpSocketToStream(ws, s)
		stop()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	idle := settings.UDPIdleDur()
	tick := idle / 4
	if tick < time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if s.idleFor() > idle {
				stop()
				<-done
				return
			}
		}
	}
}

func (rt *nodeRuntime) udpStreamToSocket(ws *wsConn, s *udpSession) {
	buf := make([]byte, udpBufSize)
	for {
		p, err := readDatagram(ws, buf)
		if err != nil {
			return
		}
		s.touch()
		if len(p) == 0 {
			continue
		}
		n, err := s.conn.Write(p)
		if err != nil {
			return
		}
		rt.udpPackets.Add(1)
		rt.udpBytes.Add(int64(n))
	}
}

func (rt *nodeRuntime) udpSocketToStream(ws *wsConn, s *udpSession) {
	buf := make([]byte, udpBufSize)
	for {
		n, err := s.conn.Read(buf)
		if err != nil {
			return
		}
		s.touch()
		if n == 0 {
			continue
		}
		if err := writeDatagram(ws, buf[:n]); err != nil {
			return
		}
		rt.udpPackets.Add(1)
		rt.udpBytes.Add(int64(n))
	}
}

// readDatagram 读取 VLESS UDP 的 [2B 长度][载荷]。
func readDatagram(r io.Reader, buf []byte) ([]byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(h[:]))
	if n > maxUDPPacket || n > len(buf) {
		return nil, fmt.Errorf("UDP 数据报过大: %d", n)
	}
	if n == 0 {
		return buf[:0], nil
	}
	if _, err := io.ReadFull(r, buf[:n]); err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// writeDatagram 写入 VLESS UDP 的 [2B 长度][载荷]。
func writeDatagram(w io.Writer, p []byte) error {
	if len(p) > maxUDPPacket {
		return fmt.Errorf("UDP 数据报过大: %d", len(p))
	}
	b := udpWritePool.Get().(*[]byte)
	buf := (*b)[:2+len(p)]
	binary.BigEndian.PutUint16(buf[:2], uint16(len(p)))
	copy(buf[2:], p)
	_, err := w.Write(buf)
	udpWritePool.Put(b)
	return err
}
