package core

import (
	"argoz/internal/config"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	githubWebBase        = "https://github.com"
	githubAPIBase        = "https://api.github.com"
	updateHTTP           = &http.Client{Timeout: 90 * time.Second}
	minUpdateBytes int64 = 1 << 20
)

func updateRepo() string {
	if repo := strings.Trim(strings.TrimSpace(os.Getenv("ARGOZ_REPO")), "/"); repo != "" {
		return repo
	}
	return "Zzz-IT/argoz"
}

func updateAssetName() (string, error) {
	switch runtime.GOOS {
	case "linux":
	default:
		return "", fmt.Errorf("自更新仅支持 Linux（当前 %s）", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return "argoz-" + runtime.GOARCH, nil
	default:
		return "", fmt.Errorf("自更新不支持当前架构: %s", runtime.GOARCH)
	}
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// compareVersion 返回 a<b=-1, a==b=0, a>b=1。只比较数字段，忽略预发布后缀。
func compareVersion(a, b string) int {
	pa := strings.Split(normalizeVersion(a), ".")
	pb := strings.Split(normalizeVersion(b), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = numericPrefix(pa[i])
		}
		if i < len(pb) {
			y = numericPrefix(pb[i])
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func numericPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

func isNewerVersion(latest, current string) bool {
	if strings.TrimSpace(current) == "" {
		return true
	}
	return compareVersion(latest, current) > 0
}

type latestRelease struct {
	TagName string `json:"tag_name"`
}

func fetchLatestRelease(ctx context.Context, repo string) (string, error) {
	url := strings.TrimRight(githubAPIBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "argoz-updater")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询最新版本失败: HTTP %d", resp.StatusCode)
	}
	var r latestRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", fmt.Errorf("解析最新版本失败: %w", err)
	}
	if strings.TrimSpace(r.TagName) == "" {
		return "", errors.New("最新版本缺少 tag_name")
	}
	return r.TagName, nil
}

func fetchChecksums(ctx context.Context, repo string) (string, error) {
	url := strings.TrimRight(githubWebBase, "/") + "/" + repo + "/releases/latest/download/checksums.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "argoz-updater")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 checksums.txt 失败: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseChecksums(data string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var h [4]byte
	if _, err := io.ReadFull(f, h[:]); err != nil {
		return false
	}
	return h[0] == 0x7f && h[1] == 'E' && h[2] == 'L' && h[3] == 'F'
}

// downloadUpdateTo 下载 release 资源并原子替换 target。
func downloadUpdateTo(ctx context.Context, repo, target string) error {
	asset, err := updateAssetName()
	if err != nil {
		return err
	}
	url := strings.TrimRight(githubWebBase, "/") + "/" + repo + "/releases/latest/download/" + asset
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "argoz-updater")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败: HTTP %d", asset, resp.StatusCode)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".argoz-update-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败（目录是否可写）: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("下载更新失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if n < minUpdateBytes {
		return fmt.Errorf("更新文件只有 %d 字节，疑似失败页", n)
	}
	if !isELF(tmpName) {
		return errors.New("更新文件不是 ELF 可执行文件")
	}

	checksums, err := fetchChecksums(ctx, repo)
	if err != nil {
		return err
	}
	if checksums != "" {
		want := parseChecksums(checksums)[asset]
		if want == "" {
			return fmt.Errorf("checksums.txt 中没有 %s", asset)
		}
		got := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(want, got) {
			return fmt.Errorf("校验失败: got %s want %s", got, want)
		}
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("替换 %s 失败: %w", target, err)
	}
	// 目录项落盘，避免掉电后仍指向旧 inode
	if d, err := os.Open(filepath.Dir(target)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func restartInstalledService() (bool, error) {
	switch detectServiceKind() {
	case serviceSystemd:
		if _, err := os.Stat("/etc/systemd/system/argoz.service"); err == nil {
			return true, runServiceCommand("systemctl", "restart", "argoz")
		}
	case serviceOpenRC:
		if _, err := os.Stat("/etc/init.d/argoz"); err == nil {
			return true, runServiceCommand("rc-service", "argoz", "restart")
		}
	}
	return false, nil
}

func RunUpdateCommandLine(args []string, defaultDir string) error {
	_ = defaultDir
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "只检查最新版本，不下载")
	force := fs.Bool("force", false, "即使版本号相同也重新下载")
	repo := fs.String("repo", updateRepo(), "GitHub 仓库，如 Zzz-IT/argoz")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	latest, err := fetchLatestRelease(ctx, *repo)
	if err != nil {
		return err
	}
	fmt.Printf("当前版本: %s\n最新版本: %s\n", config.Version, latest)
	if *check {
		if isNewerVersion(latest, config.Version) {
			fmt.Println("有可用更新")
		} else {
			fmt.Println("已是最新")
		}
		return nil
	}
	if !*force && !isNewerVersion(latest, config.Version) {
		fmt.Println("已是最新，无需更新")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位当前二进制失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := downloadUpdateTo(ctx, *repo, exe); err != nil {
		return err
	}
	fmt.Printf("已更新: %s\n", exe)

	restarted, err := restartInstalledService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "二进制已更新，但自动重启服务失败: %v\n请手动重启 argoz。\n", err)
		return nil
	}
	if restarted {
		fmt.Println("服务已重启")
	} else if hasSupervisor() {
		fmt.Println("supervisor 模式下，退出 daemon 后会自动拉起新版本")
	} else {
		fmt.Println("未发现 systemd/OpenRC/supervisor，请手动重启 argoz 进程")
	}
	return nil
}

// ---------- 面板/daemon 侧接口 ----------

type releaseInfo struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func fetchReleaseInfo(ctx context.Context, repo string) (releaseInfo, error) {
	url := strings.TrimRight(githubAPIBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "argoz-updater")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, fmt.Errorf("查询最新版本失败: HTTP %d", resp.StatusCode)
	}
	var r releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return releaseInfo{}, fmt.Errorf("解析最新版本失败: %w", err)
	}
	if strings.TrimSpace(r.TagName) == "" {
		return releaseInfo{}, errors.New("最新版本缺少 tag_name")
	}
	return r, nil
}

// firstLines 取更新说明的前 n 行非空内容
func firstLines(s string, n int) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "##") || strings.HasPrefix(line, "###") {
			continue
		}
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out
}

// serviceInstalled 判断是否已由 systemd/OpenRC 托管
func serviceInstalled() bool {
	switch detectServiceKind() {
	case serviceSystemd:
		_, err := os.Stat("/etc/systemd/system/argoz.service")
		return err == nil
	case serviceOpenRC:
		_, err := os.Stat("/etc/init.d/argoz")
		return err == nil
	}
	return false
}

// Info 返回 daemon 运行信息
func (e *Engine) Info() DaemonInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return DaemonInfo{
		PID:        os.Getpid(),
		UptimeSec:  int64(time.Since(procStart).Seconds()),
		MemBytes:   int64(m.Alloc),
		Goroutines: runtime.NumGoroutine(),
		Version:    config.Version,
		Nodes:      len(e.Nodes()),
		DataDir:    e.dataDir,
	}
}

// UpdateCheck 查询最新版本
func (e *Engine) UpdateCheck() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	repo := updateRepo()
	ri, err := fetchReleaseInfo(ctx, repo)
	if err != nil {
		return UpdateInfo{}, err
	}
	info := UpdateInfo{
		Current:     config.Version,
		Latest:      ri.TagName,
		HasUpdate:   isNewerVersion(ri.TagName, config.Version),
		PublishedAt: ri.PublishedAt,
		Notes:       firstLines(ri.Body, 8),
	}
	asset := "argoz-" + runtime.GOARCH
	for _, a := range ri.Assets {
		if a.Name == asset {
			info.SizeBytes = a.Size
		}
	}
	return info, nil
}

// UpdateApply 下载并替换自身二进制
func (e *Engine) UpdateApply() (UpdateResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	exe, err := os.Executable()
	if err != nil {
		return UpdateResult{}, fmt.Errorf("定位当前二进制失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := downloadUpdateTo(ctx, updateRepo(), exe); err != nil {
		return UpdateResult{}, err
	}

	res := UpdateResult{Replaced: true, Path: exe}
	switch {
	case serviceInstalled():
		res.Restarted = true
		res.Hint = "服务将自动重启并加载新版本"
		// 先让响应发出去，再触发重启（重启会杀掉当前进程）
		go func() {
			time.Sleep(600 * time.Millisecond)
			_, _ = restartInstalledService()
		}()
	case hasSupervisor():
		res.Restarted = true
		res.Hint = "supervisor 将自动拉起新版本"
		go func() {
			time.Sleep(600 * time.Millisecond)
			os.Exit(0) // 退出后由 argoz --supervise 重新拉起
		}()
	default:
		res.Hint = "新版本已就位，但未检测到 systemd/OpenRC/supervisor，需要手动重启 daemon"
	}
	return res, nil
}
