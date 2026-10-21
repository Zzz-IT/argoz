package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var domainRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

const stableRun = 10 * time.Minute

// backoffPolicy 分层重试：快退避 -> 分钟级 -> 小时级
type backoffPolicy struct {
	fast  []time.Duration
	slow  time.Duration
	slowN int
	ultra time.Duration
}

func defaultPolicy() backoffPolicy {
	return backoffPolicy{
		fast:  []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second},
		slow:  3 * time.Minute,
		slowN: 20,
		ultra: 60 * time.Minute,
	}
}

// next 计算下一次重试间隔，返回新的计数状态
func (p backoffPolicy) next(fastIdx, slowCount int, hard bool) (time.Duration, int, int) {
	// 永久性错误（Token 无效 / 隧道被删）快速跳到小时级
	if hard && fastIdx >= 1 {
		return jitterFn(p.ultra), fastIdx, slowCount
	}
	if fastIdx < len(p.fast) {
		return jitterFn(p.fast[fastIdx]), fastIdx + 1, slowCount
	}
	if slowCount < p.slowN {
		return jitterFn(p.slow), fastIdx, slowCount + 1
	}
	return jitterFn(p.ultra), fastIdx, slowCount
}

// jitterFn 给间隔加 ±20% 抖动，避免多节点同时重连
var jitterFn = func(d time.Duration) time.Duration {
	delta := int64(float64(d) * 0.2)
	if delta <= 0 {
		return d
	}
	n := time.Now().UnixNano()
	if n < 0 {
		n = -n
	}
	return d - time.Duration(delta) + time.Duration(n%(2*delta))
}

// isHardFail 判定是否为「重试也没用」的永久性错误
func isHardFail(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range []string{"1033", "tunnel not found", "no such tunnel", "unauthorized", "invalid tunnel secret", "invalid token", "failed to unmarshal"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// Tunnel 一条隧道的监督器（cloudflared 子进程）
type Tunnel struct {
	eng *Engine
	id  string

	mu        sync.Mutex
	cancel    context.CancelFunc
	state     string
	lastErr   string
	nextRetry time.Time
	restarts  int

	wg sync.WaitGroup
}

func NewTunnel(e *Engine, id string) *Tunnel {
	return &Tunnel{eng: e, id: id, state: "down"}
}

func (t *Tunnel) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	t.cancel = cancel
	t.state = "starting"
	t.nextRetry = time.Time{}
	t.mu.Unlock()

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.run(ctx)
	}()
}

func (t *Tunnel) Stop() {
	t.mu.Lock()
	cancel := t.cancel
	t.state = "down"
	t.nextRetry = time.Time{}
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	t.wg.Wait()
}

// Info 返回面板需要的状态快照
func (t *Tunnel) Info() (state, errMsg string, next time.Time, restarts int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.lastErr, t.nextRetry, t.restarts
}

func (t *Tunnel) name() string {
	if n, ok := t.eng.Node(t.id); ok {
		return n.Name
	}
	return t.id
}

func (t *Tunnel) run(ctx context.Context) {
	p := defaultPolicy()
	fastIdx, slowCount := 0, 0

	for {
		if ctx.Err() != nil {
			return
		}

		t.mu.Lock()
		t.state = "starting"
		t.nextRetry = time.Time{}
		t.mu.Unlock()

		start := time.Now()
		err := t.runOnce(ctx)
		ranFor := time.Since(start)
		if ctx.Err() != nil {
			return
		}

		msg := "cloudflared 进程退出"
		if err != nil && err.Error() != "" {
			msg = err.Error()
		}
		hard := isHardFail(msg)

		// 稳定运行超过 10 分钟 -> 计数清零，下次故障从 2s 重来
		if ranFor >= stableRun {
			fastIdx, slowCount = 0, 0
		}

		delay, fi, sc := p.next(fastIdx, slowCount, hard)
		fastIdx, slowCount = fi, sc

		t.mu.Lock()
		t.lastErr = msg
		t.restarts++
		t.nextRetry = time.Now().Add(delay)
		if delay >= p.slow {
			t.state = "degraded"
		} else {
			t.state = "down"
		}
		t.mu.Unlock()

		t.eng.SetNodeError(t.id, msg)
		t.eng.persistLastError(t.id, msg)

		LogWarn("节点 [%s] 隧道异常：%s；%s 后重试", t.name(), msg, roundDelay(delay))

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func roundDelay(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d 分钟", int(d.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%d 秒", int(d.Round(time.Second).Seconds()))
}

func (t *Tunnel) runOnce(ctx context.Context) error {
	node, ok := t.eng.Node(t.id)
	if !ok {
		return errors.New("节点不存在")
	}

	bin, err := cloudflaredLookup(ctx)
	if err != nil {
		return err
	}

	args, err := tunnelArgs(node)
	if err != nil {
		return err
	}
	LogDebug("执行: %s %s", bin, strings.Join(args, " "))

	cmd := commandContext(ctx, bin, args...)
	cmd.SysProcAttr = ChildDeathAttr()
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("启动 cloudflared 失败: %w", err)
	}

	pidPath := tunnelPIDPath(t.eng, t.id)
	if err := writeTunnelPID(pidPath, cmd.Process.Pid); err != nil {
		LogWarn("写入 cloudflared pid 文件失败: %v", err)
	}
	defer removeTunnelPID(pidPath)

	waitCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		waitCh <- err
	}()

	// 45 秒内没有注册到 Cloudflare 边缘就杀掉重来（避免"进程活着但根本没连上"）
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(45 * time.Second):
			t.mu.Lock()
			ok := t.state == "up"
			t.mu.Unlock()
			if !ok && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)

	var tail []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		tail = append(tail, line)
		if len(tail) > 40 {
			tail = tail[1:]
		}

		// cloudflared 打印 "Registered tunnel connection" 才代表真的连上了边缘
		low := strings.ToLower(line)
		if strings.Contains(low, "registered tunnel connection") || strings.Contains(low, "connection registered") {
			t.mu.Lock()
			t.state = "up"
			t.lastErr = ""
			t.nextRetry = time.Time{}
			t.mu.Unlock()
		}

		if node.Tunnel.Mode == "temp" {
			if m := domainRe.FindString(line); m != "" {
				domain := strings.TrimPrefix(m, "https://")
				if domain != node.Tunnel.Domain {
					t.eng.SetDomain(t.id, domain)
				}
				// 抓到域名说明隧道已建立
				t.mu.Lock()
				if t.state != "up" {
					t.state = "up"
					t.lastErr = ""
					t.nextRetry = time.Time{}
				}
				t.mu.Unlock()
			}
		}
	}

	waitErr := <-waitCh
	if msg := summariseCloudflared(tail); msg != "" {
		if waitErr != nil {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("%s", msg)
	}
	if waitErr != nil {
		return fmt.Errorf("cloudflared 退出: %w", waitErr)
	}
	return nil
}

// tunnelArgs 构造 cloudflared 参数。
// 注意：--no-autoupdate 是全局 flag，必须放在子命令之前，
// 否则 cloudflared 会报 "flag provided but not defined" 并打印 usage。
func tunnelArgs(n Node) ([]string, error) {
	if n.Tunnel.Mode == "fixed" {
		token := strings.TrimSpace(n.Tunnel.Token)
		if token == "" {
			return nil, errors.New("固定隧道缺少 Token")
		}
		return []string{"--no-autoupdate", "tunnel", "run", "--token", token}, nil
	}
	return []string{"--no-autoupdate", "tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", n.Port)}, nil
}

// summariseCloudflared 从输出尾部挑一行最有诊断价值的错误
func summariseCloudflared(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	keys := []string{"incorrect usage", "flag provided but not defined", "unknown flag", "failed", "error", "err=", "cannot", "no such"}
	for _, l := range lines {
		low := strings.ToLower(l)
		for _, k := range keys {
			if strings.Contains(low, k) {
				return strings.TrimSpace(l)
			}
		}
	}
	// 退化为第一条非空行（cloudflared 的首行通常带版本与运行模式）
	return strings.TrimSpace(lines[0])
}

// cloudflaredLookup / commandContext 可被测试替换
var (
	cloudflaredLookup = EnsureCloudflared
	commandContext    = exec.CommandContext
)

// cfMu 串行化 cloudflared 的查找/下载，避免多节点并发重复下载
var (
	cfMu       sync.Mutex
	cfLastErr  error
	cfLastFail time.Time
)

// EnsureCloudflared 查找或安装 cloudflared
func EnsureCloudflared(ctx context.Context) (string, error) {
	cfMu.Lock()
	defer cfMu.Unlock()

	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	candidates := []string{filepath.Join("/usr/local/bin", name)}
	if runtime.GOOS == "windows" {
		candidates = []string{filepath.Join(DefaultDataDir(), name)}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Size() > 1<<20 {
			return p, nil
		}
	}

	// 30 秒内失败过则直接复用错误，避免多节点反复拉取
	if cfLastErr != nil && time.Since(cfLastFail) < 30*time.Second {
		return "", cfLastErr
	}

	dest := candidates[0]
	if err := downloadCloudflared(ctx, dest); err != nil {
		cfLastErr = fmt.Errorf("未找到 cloudflared 且自动安装失败: %w", err)
		cfLastFail = time.Now()
		return "", cfLastErr
	}
	cfLastErr = nil
	LogOK("cloudflared 已安装: %s", dest)
	return dest, nil
}

func downloadCloudflared(ctx context.Context, dest string) error {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("不支持的架构: %s", arch)
	}
	url := fmt.Sprintf("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-%s", arch)

	LogInfo("正在下载 cloudflared (%s) ...", arch)
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "argoz")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态码 %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Chmod(dest, 0o755)
}
