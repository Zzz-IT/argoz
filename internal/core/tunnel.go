package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"argoz/internal/artifact"
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
		ultra: 10 * time.Minute,
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

// isHardFail 判定是否为「重试也没用」的永久性隧道配置错误
func isHardFail(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range []string{"1033", "tunnel not found", "no such tunnel", "unauthorized", "invalid tunnel secret", "invalid token", "failed to unmarshal"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

type tunnelFailureKind int

const (
	tunnelFailureSoft tunnelFailureKind = iota
	tunnelFailureBinaryFatal
	tunnelFailureOOM
	tunnelFailureStartupTimeout
)

// tunnelFailure keeps the display message and the underlying wait/start error
// together so the supervisor can classify fatal binary and OOM failures
// without parsing a flattened error string.
type tunnelFailure struct {
	kind tunnelFailureKind
	msg  string
	err  error
}

func (e *tunnelFailure) Error() string { return e.msg }
func (e *tunnelFailure) Unwrap() error { return e.err }

// classifyFailureText is deliberately narrow: ordinary network errors remain
// soft failures and continue through the existing backoff ladder.
func classifyFailureText(s string) tunnelFailureKind {
	m := strings.ToLower(s)
	for _, k := range []string{
		"permission denied", "operation not permitted", "access is denied", "exec format error",
		"executable file format", "invalid elf", "cannot execute binary file", "not a valid win32 application",
		"segmentation fault", "sigsegv", "signal: segmentation fault",
	} {
		if strings.Contains(m, k) {
			return tunnelFailureBinaryFatal
		}
	}
	for _, k := range []string{
		"out of memory", "cannot allocate memory", "oom-kill", "oom kill",
		"killed process", "signal: killed", "exit status 137", "exit status 9", "memory allocation failed",
		"runtime: out of memory",
	} {
		if strings.Contains(m, k) {
			return tunnelFailureOOM
		}
	}
	return tunnelFailureSoft
}

func classifyTunnelFailure(err error) tunnelFailureKind {
	if err == nil {
		return tunnelFailureSoft
	}
	if artifact.IsFatalBinaryError(err) {
		return tunnelFailureBinaryFatal
	}
	var tf *tunnelFailure
	if errors.As(err, &tf) {
		switch tf.kind {
		case tunnelFailureBinaryFatal:
			return tunnelFailureBinaryFatal
		case tunnelFailureOOM:
			return tunnelFailureOOM
		case tunnelFailureStartupTimeout:
			return tunnelFailureStartupTimeout
		}
	}
	return classifyFailureText(err.Error())
}

func oomRetryDelay(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	d := 3 * time.Minute
	for i := 1; i < n; i++ {
		d *= 2
	}
	if d > 24*time.Minute {
		d = 24 * time.Minute
	}
	return jitterFn(d)
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
	oomCount := 0

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
		kind := classifyTunnelFailure(err)

		// 权限/格式/段错误属于二进制本身损坏，重试不可能自愈。
		// 保留状态与错误，等待用户修复/重装后通过 RestartTunnel 手动启动。
		if kind == tunnelFailureBinaryFatal {
			t.mu.Lock()
			t.lastErr = msg
			t.nextRetry = time.Time{}
			t.state = "err"
			t.restarts++
			t.mu.Unlock()
			t.eng.SetNodeError(t.id, msg)
			t.eng.persistLastError(t.id, msg)
			LogErr("节点 [%s] cloudflared 二进制错误，已停止自动重试: %s；请重装/修复 cloudflared 后手动重启该节点", t.name(), msg)
			return
		}

		// 稳定运行超过 10 分钟 -> 计数清零，下次故障从 2s 重来
		if ranFor >= stableRun {
			fastIdx, slowCount = 0, 0
			oomCount = 0
		}

		var delay time.Duration
		switch kind {
		case tunnelFailureOOM:
			oomCount++
			delay = oomRetryDelay(oomCount)
			// 释放已经积累的快速重试计数，OOM 恢复需要更长时间。
			fastIdx, slowCount = 0, 0
		default:
			hard := isHardFail(msg)
			var fi, sc int
			delay, fi, sc = p.next(fastIdx, slowCount, hard)
			fastIdx, slowCount = fi, sc
		}

		t.mu.Lock()
		t.lastErr = msg
		t.restarts++
		t.nextRetry = time.Now().Add(delay)
		if kind == tunnelFailureOOM || delay >= p.slow {
			t.state = "degraded"
		} else {
			t.state = "down"
		}
		t.mu.Unlock()

		t.eng.SetNodeError(t.id, msg)
		t.eng.persistLastError(t.id, msg)

		if kind == tunnelFailureOOM {
			LogWarn("节点 [%s] cloudflared 疑似 OOM/被系统杀死，延长退避：%s；%s 后重试", t.name(), msg, roundDelay(delay))
		} else {
			LogWarn("节点 [%s] 隧道异常：%s；%s 后重试", t.name(), msg, roundDelay(delay))
		}

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
	defer pr.Close()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		kind := classifyFailureText(err.Error())
		return &tunnelFailure{kind: kind, msg: fmt.Sprintf("启动 cloudflared 失败: %v", err), err: err}
	}

	pidPath := tunnelPIDPath(t.eng, t.id)
	if err := writeTunnelPID(pidPath, cmd.Process.Pid); err != nil {
		LogWarn("写入 cloudflared pid 文件失败: %v", err)
	}
	defer removeTunnelPID(pidPath)

	waitCh := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		waitCh <- err
	}()

	// 45 秒内没有注册到 Cloudflare 边缘就杀掉重来（避免"进程活着但根本没连上"）。
	// 单独记录这是启动超时，不能把随之而来的 signal: killed 误判为 OOM。
	var startupTimedOut atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		case <-time.After(45 * time.Second):
			t.mu.Lock()
			up := t.state == "up"
			t.mu.Unlock()
			if !up && cmd.Process != nil {
				startupTimedOut.Store(true)
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
	fullText := strings.Join(tail, "\n")
	if startupTimedOut.Load() {
		return &tunnelFailure{
			kind: tunnelFailureStartupTimeout,
			msg:  "cloudflared 45 秒内未注册到 Cloudflare 边缘，已重启",
		}
	}
	if msg := summariseCloudflared(tail); msg != "" {
		kind := classifyFailureText(fullText + "\n" + errorString(waitErr))
		if (kind == tunnelFailureBinaryFatal || kind == tunnelFailureOOM) && waitErr != nil && !strings.Contains(msg, waitErr.Error()) {
			msg += "；" + waitErr.Error()
		}
		return &tunnelFailure{kind: kind, msg: msg, err: waitErr}
	}
	if waitErr != nil {
		kind := classifyFailureText(fullText + "\n" + waitErr.Error())
		return &tunnelFailure{kind: kind, msg: fmt.Sprintf("cloudflared 退出: %v", waitErr), err: waitErr}
	}
	return nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	keys := []string{"incorrect usage", "flag provided but not defined", "unknown flag", "failed", "error", "err=", "cannot", "no such", "out of memory", "oom", "segmentation fault", "sigsegv"}
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

func cloudflaredBinaryName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// cloudflaredSpec 构造 cloudflared 的统一下载规格。version 为空或 latest
// 时跟随 GitHub 最新发布，否则使用精确 tag，避免检查与下载之间换版。
func cloudflaredSpec(version string) (artifact.Spec, error) {
	if runtime.GOOS != "linux" {
		return artifact.Spec{}, fmt.Errorf("cloudflared 自动安装仅支持 Linux（当前 %s）", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return artifact.Spec{}, fmt.Errorf("不支持的架构: %s", runtime.GOARCH)
	}
	version = strings.TrimSpace(version)
	asset := "cloudflared-linux-" + runtime.GOARCH
	base := strings.TrimRight(githubWebBase, "/") + "/cloudflare/cloudflared/releases"
	baseURL := base + "/latest/download/" + asset
	cacheVersion := "latest"
	if version != "" && !strings.EqualFold(version, "latest") {
		cacheVersion = version
		baseURL = base + "/download/" + version + "/" + asset
	}
	return artifact.Spec{
		Name:    "cloudflared",
		Version: cacheVersion,
		Asset:   asset,
		URL:     baseURL,
		MinSize: 1 << 20,
		MaxSize: 256 << 20,
		Format:  artifact.FormatELF,
	}, nil
}

func cloudflaredCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	if p, err := exec.LookPath(cloudflaredBinaryName()); err == nil {
		add(p)
	}
	if runtime.GOOS == "windows" {
		add(filepath.Join(DefaultDataDir(), cloudflaredBinaryName()))
	} else {
		add(filepath.Join("/usr/local/bin", cloudflaredBinaryName()))
		add(filepath.Join(DefaultDataDir(), cloudflaredBinaryName()))
	}
	return out
}

func validateCloudflaredAt(ctx context.Context, path string) error {
	opts := artifact.ValidateOptions{
		Format:         artifact.FormatELF,
		MinSize:        1 << 20,
		MaxSize:        256 << 20,
		RunVersion:     true,
		VersionArgs:    []string{"--version"},
		VersionTimeout: 12 * time.Second,
	}
	if runtime.GOOS != "linux" {
		opts.Format = artifact.FormatAny
	}
	return artifact.ValidateBinary(ctx, path, opts)
}

// findUsableCloudflared 只查找并验证，不触发下载。权限不足时仅尝试 chmod
// 一次；其他损坏候选被忽略，绝不把未通过 --version 的路径返回给节点。
func findUsableCloudflared(ctx context.Context) (string, error) {
	var fatal error
	var last error
	for _, path := range cloudflaredCandidates() {
		err := validateCloudflaredAt(ctx, path)
		if err == nil {
			return path, nil
		}
		last = err
		if artifact.IsPermissionError(err) {
			if chmodErr := os.Chmod(path, 0o755); chmodErr == nil {
				if retryErr := validateCloudflaredAt(ctx, path); retryErr == nil {
					LogWarn("已修复 cloudflared 可执行权限: %s", path)
					return path, nil
				} else {
					err = fmt.Errorf("%w；chmod 后仍校验失败: %v", err, retryErr)
				}
			} else {
				err = fmt.Errorf("%w；chmod 0755 失败: %v", err, chmodErr)
			}
		}
		if artifact.IsFatalBinaryError(err) && fatal == nil {
			fatal = err
		}
	}
	if fatal != nil {
		return "", fatal
	}
	if last != nil {
		return "", last
	}
	return "", os.ErrNotExist
}

func writableDir(dir string) bool {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".argoz-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func cloudflaredInstallPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(DefaultDataDir(), cloudflaredBinaryName())
	}
	dir := "/usr/local/bin"
	if writableDir(dir) {
		return filepath.Join(dir, cloudflaredBinaryName())
	}
	return filepath.Join(DefaultDataDir(), cloudflaredBinaryName())
}

// EnsureCloudflared 先验证 PATH 与 /usr/local/bin，再通过 artifact 管理器
// 单飞下载/缓存/安装。权限、ELF 损坏和 --version 段错误都不会被当成可用路径。
func EnsureCloudflared(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if path, err := findUsableCloudflared(ctx); err == nil {
		return path, nil
	}

	spec, err := cloudflaredSpec("latest")
	if err != nil {
		return "", err
	}
	mgr := artifactManagerFor(DefaultDataDir())
	target := cloudflaredInstallPath()
	st, err := mgr.Ensure(ctx, spec, artifact.Options{
		Force:           false,
		Offline:         false,
		Cooldown:        30 * time.Second,
		Timeout:         4 * time.Minute,
		ValidateVersion: true,
		VersionArgs:     []string{"--version"},
		VersionTimeout:  12 * time.Second,
		InstallPath:     target,
		BackupDir:       filepath.Join(DefaultDataDir(), "backups", "cloudflared"),
	})
	if err != nil {
		return "", fmt.Errorf("未找到可用 cloudflared 且自动安装失败: %w", err)
	}
	path := strings.TrimSpace(st.Path)
	if path == "" {
		path = strings.TrimSpace(st.CachePath)
	}
	if path == "" {
		return "", errors.New("cloudflared 安装后没有返回可用路径")
	}
	if err := validateCloudflaredAt(ctx, path); err != nil {
		return "", fmt.Errorf("cloudflared 安装后复验失败: %w", err)
	}
	LogOK("cloudflared 已就绪: %s", path)
	return path, nil
}

// downloadCloudflared 保留为包内兼容入口，实际下载/校验/安装全部交给
// artifact 管理器，避免与 install.sh、update 产生第二套逻辑。
func downloadCloudflared(ctx context.Context, dest string) error {
	spec, err := cloudflaredSpec("latest")
	if err != nil {
		return err
	}
	mgr := artifactManagerFor(filepath.Dir(dest))
	_, err = mgr.Ensure(ctx, spec, artifact.Options{
		Force:              true,
		Offline:            false,
		Timeout:            4 * time.Minute,
		ValidateVersion:    true,
		ValidateVersionSet: true,
		VersionArgs:        []string{"--version"},
		VersionTimeout:     12 * time.Second,
		InstallPath:        dest,
		BackupDir:          filepath.Join(filepath.Dir(dest), "backups", "cloudflared"),
	})
	return err
}
