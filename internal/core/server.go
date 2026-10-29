package core

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// wsHandshakeTimeoutNanos 是 WS 升级后到协议握手完成的读超时（纳秒，原子读写）
var wsHandshakeTimeoutNanos atomic.Int64

func init() { wsHandshakeTimeoutNanos.Store(int64(10 * time.Second)) }

// setWSHandshakeTimeout 设置握手超时（测试可调）
func setWSHandshakeTimeout(d time.Duration) { wsHandshakeTimeoutNanos.Store(int64(d)) }

// getWSHandshakeTimeout 读取握手超时
func getWSHandshakeTimeout() time.Duration { return time.Duration(wsHandshakeTimeoutNanos.Load()) }

// nodeRuntime 单个节点的运行态
type nodeRuntime struct {
	eng *Engine

	mu       sync.RWMutex
	node     Node
	settings Settings
	state    string // up | down | err
	lastErr  string

	srv *http.Server
	ln  net.Listener
	tun *Tunnel

	conns atomic.Int64
	total atomic.Int64
	up    atomic.Int64
	down  atomic.Int64

	startedAt time.Time

	wsMu     sync.Mutex
	wsConns  map[*wsConn]struct{}
	stopping bool

	udpMu       sync.Mutex
	udpSessions map[*udpSession]struct{}
	udpConns    atomic.Int64
	udpPackets  atomic.Int64
	udpBytes    atomic.Int64
}

func (rt *nodeRuntime) snapshot() (Node, Settings) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.node, rt.settings
}

func (rt *nodeRuntime) setState(s, errMsg string) {
	rt.mu.Lock()
	rt.state = s
	if errMsg != "" {
		rt.lastErr = errMsg
	}
	if s == "up" {
		rt.lastErr = ""
	}
	rt.mu.Unlock()
}

func (rt *nodeRuntime) status() (string, string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.state, rt.lastErr
}

func (rt *nodeRuntime) setError(errMsg string) {
	rt.mu.Lock()
	rt.lastErr = errMsg
	rt.mu.Unlock()
}

// trackWS 登记一条已升级的连接。节点正在停止时返回 false。
func (rt *nodeRuntime) trackWS(c *wsConn) bool {
	rt.wsMu.Lock()
	defer rt.wsMu.Unlock()
	if rt.stopping {
		return false
	}
	if rt.wsConns == nil {
		rt.wsConns = make(map[*wsConn]struct{})
	}
	rt.wsConns[c] = struct{}{}
	return true
}

func (rt *nodeRuntime) untrackWS(c *wsConn) {
	rt.wsMu.Lock()
	delete(rt.wsConns, c)
	rt.wsMu.Unlock()
}

// abortWSConns 立即关闭所有已登记的 WS 连接。
// http.Server.Shutdown 不会处理 hijacked 连接，必须自己维护。
func (rt *nodeRuntime) abortWSConns() {
	rt.wsMu.Lock()
	rt.stopping = true
	conns := make([]*wsConn, 0, len(rt.wsConns))
	for c := range rt.wsConns {
		conns = append(conns, c)
	}
	rt.wsConns = make(map[*wsConn]struct{})
	rt.wsMu.Unlock()

	for _, c := range conns {
		c.abort()
	}
}

// ServeHTTP 处理该节点的全部请求
func (rt *nodeRuntime) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	node, settings := rt.snapshot()

	if r.URL.Path != node.Path {
		http.NotFound(w, r)
		return
	}
	if !isWebSocketRequest(r) {
		http.NotFound(w, r)
		return
	}
	if memoryPressure() {
		http.Error(w, "low memory", http.StatusServiceUnavailable)
		return
	}

	// 先原子预占名额，避免并发握手同时通过 Load 检查。
	maxConns := settings.MaxConns
	if maxConns <= 0 {
		maxConns = 128
	}
	if rt.conns.Add(1) > int64(maxConns) {
		rt.conns.Add(-1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}

	ws, err := upgradeWS(w, r)
	if err != nil {
		rt.conns.Add(-1)
		return
	}
	if !rt.trackWS(ws) {
		ws.abort()
		rt.conns.Add(-1)
		return
	}
	defer rt.untrackWS(ws)

	rt.serve(ws, node, settings)
}

func (rt *nodeRuntime) serve(ws *wsConn, node Node, settings Settings) {
	defer ws.Close()

	rt.total.Add(1)
	defer rt.conns.Add(-1)

	// 升级完成后的协议握手必须有超时，防止只完成 101 不发送数据的连接占满 MaxConns。
	_ = ws.SetReadDeadline(time.Now().Add(getWSHandshakeTimeout()))

	stage := "handshake"
	var target string
	var vlessReq VlessRequest

	var err error
	switch node.Protocol {
	case "vless":
		vlessReq, err = vlessHandshakeRequest(ws, node)
		if err == nil {
			err = vlessResponse(ws)
		}
	case "trojan":
		target, err = trojanHandshake(ws, node)
	default:
		err = fmt.Errorf("未知协议: %s", node.Protocol)
	}
	if err != nil {
		rt.setError(fmt.Sprintf("%s: %v", stage, err))
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

	if vlessReq.Cmd == 0x02 {
		if !settings.UDPEnabled {
			rt.setError("UDP 转发未启用")
			return
		}
		rt.serveUDP(ws, vlessReq.Target, settings)
		return
	}
	if node.Protocol == "vless" {
		target = vlessReq.Target
	}

	stage = "dial"
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	up, err := d.Dial("tcp", target)
	if err != nil {
		rt.setError(fmt.Sprintf("连接目标 %s 失败: %v", target, err))
		return
	}
	defer up.Close()

	// 心跳
	stopPing := make(chan struct{})
	var pingWG sync.WaitGroup
	if keep := settings.KeepaliveDur(); keep > 0 {
		pingWG.Add(1)
		go func() {
			defer pingWG.Done()
			t := time.NewTicker(keep)
			defer t.Stop()
			for {
				select {
				case <-stopPing:
					return
				case <-t.C:
					if time.Since(ws.LastRecv()) > 2*keep {
						ws.abort()
						return
					}
					if err := ws.Ping(); err != nil {
						ws.abort()
						return
					}
				}
			}
		}()
	}

	upN, downN := relayStream(ws, up, settings.IdleDur())
	close(stopPing)
	pingWG.Wait()

	rt.up.Add(upN)
	rt.down.Add(downN)
}

// relayStream 双向中继（含空闲超时）
func relayStream(ws *wsConn, up net.Conn, idle time.Duration) (int64, int64) {
	var sUp, sDown atomic.Int64

	var wg sync.WaitGroup
	wg.Add(2)

	bufA := getRelayBuf()
	bufB := getRelayBuf()
	defer putRelayBuf(bufA)
	defer putRelayBuf(bufB)

	go func() {
		defer wg.Done()
		_, _ = io.CopyBuffer(&countWriter{w: up, n: &sUp}, ws, *bufA)
		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		_, _ = io.CopyBuffer(&countWriter{w: ws, n: &sDown}, up, *bufB)
		// 上游读端结束，直接关闭 WS；WebSocket 没有可靠的半关闭语义。
		_ = ws.Close()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	tick := idle / 4
	if tick < time.Second {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	lastUp, lastDown := int64(0), int64(0)
	lastChange := time.Now()

loop:
	for {
		select {
		case <-done:
			break loop
		case <-ticker.C:
			u, d := sUp.Load(), sDown.Load()
			if u != lastUp || d != lastDown {
				lastUp, lastDown = u, d
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > idle {
				ws.abort()
				up.Close()
				<-done
				break loop
			}
		}
	}

	ws.Close()
	up.Close()
	return sUp.Load(), sDown.Load()
}

// relayBufSize 单会话单方向中继缓冲；16KB 在吞吐与内存间取平衡
const relayBufSize = 16 << 10

var relayBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, relayBufSize)
		return &b
	},
}

func getRelayBuf() *[]byte  { return relayBufPool.Get().(*[]byte) }
func putRelayBuf(b *[]byte) { relayBufPool.Put(b) }

type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// startServer 在已分配的 listener 上启动 HTTP 服务
func (rt *nodeRuntime) startServer(ln net.Listener) {
	rt.wsMu.Lock()
	rt.stopping = false
	rt.wsMu.Unlock()

	mux := http.NewServeMux()
	mux.Handle("/", rt)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	rt.srv = srv
	rt.ln = ln
	rt.startedAt = time.Now()

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			rt.setError("监听异常: " + err.Error())
			rt.setState("err", "")
		}
	}()
}

// stopServer 优雅关闭；hijacked 的 WS 连接需要显式关闭。
func (rt *nodeRuntime) stopServer() {
	rt.abortWSConns()
	rt.closeUDPSessions()

	if rt.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = rt.srv.Shutdown(ctx)
		cancel()
	}
	if rt.ln != nil {
		_ = rt.ln.Close()
	}
}
