package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	l1Interval    = 15 * time.Second // L1 本地探针间隔
	l3BusyEvery   = 3                // ×15s = 45s，有连接时做端到端探活
	l3IdleEvery   = 6                // ×15s = 90s，空闲时做端到端探活
	sweepEvery    = 240              // ×15s = 60min，全量巡检
	l1Strike      = 3                // L1 连续失败阈值
	l3Strike      = 2                // L3 连续失败阈值
	probeTimeout  = 3 * time.Second  // L1 超时
	tunnelTimeout = 8 * time.Second  // L3 超时
	runtimeEvery  = 5 * time.Minute  // L4 采样间隔
	runtimeWindow = 15 * time.Minute // L4 观察窗口
	leakThreshold = 200              // L4 goroutine/fd 增长阈值

	// 低内存模式下的探针降频倍数：L1 每 2×15s 一次（30s），L3 busy/idle/sweep 各 4×稀疏。
	// 目的是让探针本身不成为内存/CPU 压力来源，同时保持 L1/L3 判定语义不变。
	lowMemL1Scale = 2
	lowMemL3Scale = 4
)

// probeIntervalL1 返回 L1 探针周期：低内存时放大 2 倍，正常保持 l1Interval。
func probeIntervalL1() time.Duration {
	if LowMemoryActive() {
		return l1Interval * lowMemL1Scale
	}
	return l1Interval
}

// lowMemEvery 对探针轮次计数模数进行低内存放大：返回放大后的模数。
// 调用方仍按 n%probeEveryXxx(…) 判定触发时机，只是低内存时更稀疏。
func lowMemEvery(base int) int {
	if LowMemoryActive() {
		if base <= 0 {
			return 1
		}
		return base * lowMemL3Scale
	}
	return base
}

// healthState 探针计数器
type healthState struct {
	mu      sync.Mutex
	l1Fails map[string]int
	l3Fails map[string]int
	lastL3  map[string]time.Time
}

func newHealthState() *healthState {
	return &healthState{
		l1Fails: map[string]int{},
		l3Fails: map[string]int{},
		lastL3:  map[string]time.Time{},
	}
}

func (h *healthState) bump(m map[string]int, id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	m[id]++
	return m[id]
}

func (h *healthState) reset(m map[string]int, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m[id] = 0
}

func (e *Engine) bumpL1(id string) int { return e.health.bump(e.health.l1Fails, id) }
func (e *Engine) resetL1(id string)    { e.health.reset(e.health.l1Fails, id) }
func (e *Engine) bumpL3(id string) int { return e.health.bump(e.health.l3Fails, id) }
func (e *Engine) resetL3(id string)    { e.health.reset(e.health.l3Fails, id) }

// ---------- L1：本地监听探针 ----------

// probeL1 对本地监听发一次 HTTP 请求，任何合法 HTTP 响应都算存活
func probeL1(addr, path string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("连接失败: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: probe\r\nConnection: close\r\n\r\n", path)
	if _, err := conn.Write([]byte(req)); err != nil {
		return fmt.Errorf("写请求失败: %w", err)
	}

	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return fmt.Errorf("读响应失败: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "HTTP/1.") {
		return fmt.Errorf("非法响应: %q", strings.TrimSpace(line))
	}
	return nil
}

// ---------- L3：端到端域名探针 ----------

var probeClient = &http.Client{
	Timeout: tunnelTimeout,
	Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// probeL3 通过 Cloudflare 边缘访问域名，判断隧道是否真的通了
func probeL3(domain, path string, timeout time.Duration) error {
	if domain == "" {
		return nil
	}
	client := *probeClient
	client.Timeout = timeout

	req, err := http.NewRequest("GET", "https://"+domain+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "argoz-probe")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))

	// CF 侧 5xx 基本都意味着隧道/源站异常
	if resp.StatusCode >= 500 {
		if bytes.Contains(body, []byte("1033")) || bytes.Contains(body, []byte("not found")) {
			return fmt.Errorf("隧道不存在 (HTTP %d)", resp.StatusCode)
		}
		return fmt.Errorf("边缘返回 %d", resp.StatusCode)
	}
	return nil
}

// ---------- 探针调度 ----------

// oomEngineHookRegistered 保证 OOM 降级引擎钩子只注册一次，StartHealth 多次调用不会叠加。
var oomEngineHookRegistered atomic.Bool

func (e *Engine) StartHealth() {
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	// 首次 StartHealth 时注册引擎级 UDP 清理钩子；RegisterOOMDegradeEngine 是覆盖式写入，
	// 但用 once 语义避免多次 StartHealth 重复构造闭包带来的无谓开销。
	if oomEngineHookRegistered.CompareAndSwap(false, true) {
		RegisterOOMDegradeEngine(e)
	}

	go e.probeLoop(ctx)
	go e.runtimeLoop(ctx)
}

func (e *Engine) StopHealth() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) probeLoop(ctx context.Context) {
	t := time.NewTicker(probeIntervalL1())
	defer t.Stop()

	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		n++
		e.probeRound(true, false, true) // L1 每轮都做

		sweepN := lowMemEvery(sweepEvery)
		l3IdleN := lowMemEvery(l3IdleEvery)
		l3BusyN := lowMemEvery(l3BusyEvery)

		switch {
		case n%sweepN == 0:
			e.probeRound(false, true, true)
			LogInfo("每小时全量巡检完成（%d 个节点）", len(e.Statuses()))
		case n%l3IdleN == 0:
			e.probeRound(false, true, false)
		case n%l3BusyN == 0:
			e.probeRound(false, true, true)
		}
	}
}

// probeRound 跑一轮探针；doL1 本地探活，doL3 端到端，busyOnly 仅探测有活跃连接的节点
func (e *Engine) probeRound(doL1, doL3, busyOnly bool) {
	list := e.Statuses()
	if len(list) == 0 {
		return
	}
	settings := e.Settings()

	var wg sync.WaitGroup
	for _, st := range list {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.probeOne(st, settings, doL1, doL3, busyOnly)
		}()
	}
	wg.Wait()
}

func (e *Engine) probeOne(st NodeStatus, settings Settings, doL1, doL3, busyOnly bool) {
	if doL1 {
		addr := net.JoinHostPort(settings.Listen, strconv.Itoa(st.Node.Port))
		if err := probeL1(addr, st.Node.Path, probeTimeout); err != nil {
			if e.bumpL1(st.Node.ID) >= l1Strike {
				e.resetL1(st.Node.ID)
				LogWarn("节点 [%s] 本地探针连续失败（%v），重建监听", st.Node.Name, err)
				e.repairListener(st.Node.ID)
			}
		} else {
			e.resetL1(st.Node.ID)
		}
	}

	if !doL3 || st.State != "up" || st.Node.Tunnel.Domain == "" {
		return
	}
	if busyOnly && st.Conns == 0 {
		return
	}

	if err := probeL3(st.Node.Tunnel.Domain, st.Node.Path, tunnelTimeout); err != nil {
		if e.bumpL3(st.Node.ID) >= l3Strike {
			e.resetL3(st.Node.ID)
			LogWarn("节点 [%s] 端到端探活失败（%v），重启隧道", st.Node.Name, err)
			_ = e.RestartTunnel(st.Node.ID)
		}
	} else {
		e.resetL3(st.Node.ID)
	}
}

// repairListener 重建本地监听（端口可能漂移）
func (e *Engine) repairListener(id string) {
	rt := e.runtimeOf(id)
	if rt == nil {
		return
	}
	node, ok := e.Node(id)
	if !ok {
		return
	}
	settings := e.Settings()

	rt.stopServer()
	ln, port, err := e.bindNode(&node, settings)
	if err != nil {
		rt.setState("err", "重建监听失败: "+err.Error())
		LogErr("节点 [%s] 重建监听失败: %v", node.Name, err)
		return
	}
	if port != node.Port {
		old := node.Port
		node.Port = port
		e.mu.Lock()
		if p := e.cfg.Find(id); p != nil {
			p.Port = port
		}
		_ = e.cfg.Save()
		e.mu.Unlock()
		LogWarn("节点 [%s] 端口已迁移: %d -> %d", node.Name, old, port)
	}

	rt.mu.Lock()
	rt.node = node
	rt.state = "up"
	rt.mu.Unlock()

	rt.startServer(ln)
	LogOK("节点 [%s] 监听已重建 (%s:%d)", node.Name, settings.Listen, port)
}

// ---------- L4：运行时自检 ----------

type runtimeSample struct {
	at  time.Time
	gor int
	fd  int
}

// runtimeInterval 返回 L4 采样间隔；低内存时降低采样频率，减少自检本身的开销。
func runtimeInterval() time.Duration {
	if LowMemoryActive() {
		return 10 * time.Minute
	}
	return runtimeEvery
}

func (e *Engine) runtimeLoop(ctx context.Context) {
	t := time.NewTicker(runtimeInterval())
	defer t.Stop()

	var samples []runtimeSample
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		s := runtimeSample{at: time.Now(), gor: runtime.NumGoroutine(), fd: countFDs()}

		// OOM 事件驱动降级：cgroup oom_kill 计数增长时立即切换低内存策略并关闭 UDP 会话。
		if actions := OOMDegrade(); len(actions) > 0 {
			LogWarn("runtimeLoop 触发 OOM 降级：%s", strings.Join(actions, "；"))
		}

		cutoff := time.Now().Add(-runtimeWindow)
		kept := samples[:0]
		for _, x := range samples {
			if x.at.After(cutoff) {
				kept = append(kept, x)
			}
		}
		samples = append(kept, s)

		LogInfo("自检: goroutine=%d fd=%d", s.gor, s.fd)

		if len(samples) < 2 {
			continue
		}
		first := samples[0]
		dg := s.gor - first.gor
		df := s.fd - first.fd
		if dg >= leakThreshold || df >= leakThreshold {
			LogErr("检测到资源泄漏迹象（%s 内 goroutine %+d / fd %+d），触发自愈", runtimeWindow, dg, df)
			if e.daemon {
				LogErr("守护模式：请求主进程优雅退出，交由 systemd/OpenRC 拉起")
				// 不能在这里 os.Exit：会跳过 main 的 defer，留下 lock/socket/心跳。
				e.ReportFatal(fmt.Errorf("资源泄漏：goroutine %+d / fd %+d（%s）", dg, df, runtimeWindow))
				return
			}
			samples = nil
		}
	}
}

// countFDs 统计当前进程打开的文件描述符（仅 Linux 有效）
func countFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}
