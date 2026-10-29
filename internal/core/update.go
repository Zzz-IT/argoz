package core

import (
	"argoz/internal/artifact"
	"argoz/internal/config"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	githubWebBase        = "https://github.com"
	githubAPIBase        = "https://api.github.com"
	updateHTTP           = &http.Client{}
	minUpdateBytes int64 = 1 << 20
)

const cloudflaredRepo = "cloudflare/cloudflared"

var (
	artifactManagersMu sync.Mutex
	artifactManagers   = map[string]*artifact.Manager{}
)

// artifactManagerFor keeps one Manager per data directory so in-process
// single-flight locks are shared by all nodes and RPC calls.
func artifactManagerFor(dataDir string) *artifact.Manager {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = DefaultDataDir()
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	dataDir = filepath.Clean(dataDir)
	artifactManagersMu.Lock()
	defer artifactManagersMu.Unlock()
	if mgr := artifactManagers[dataDir]; mgr != nil {
		return mgr
	}
	mgr := artifact.New(dataDir)
	artifactManagers[dataDir] = mgr
	return mgr
}

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

type releaseInfo struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func fetchLatestRelease(ctx context.Context, repo string) (string, error) {
	ri, err := fetchReleaseInfo(ctx, repo)
	if err != nil {
		return "", err
	}
	return ri.TagName, nil
}

func fetchReleaseInfo(ctx context.Context, repo string) (releaseInfo, error) {
	u := strings.TrimRight(githubAPIBase, "/") + "/repos/" + strings.Trim(repo, "/") + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

func releaseDownloadBase(repo, version string) string {
	base := strings.TrimRight(githubWebBase, "/") + "/" + strings.Trim(repo, "/") + "/releases"
	version = strings.TrimSpace(version)
	if version == "" || strings.EqualFold(version, "latest") {
		return base + "/latest/download"
	}
	return base + "/download/" + url.PathEscape(version)
}

func fetchChecksums(ctx context.Context, repo string) (string, error) {
	return fetchChecksumsAt(ctx, repo, "")
}

func fetchChecksumsAt(ctx context.Context, repo, version string) (string, error) {
	u := releaseDownloadBase(repo, version) + "/checksums.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

// downloadUpdateTo is retained for source compatibility with older callers and
// tests. Production update paths use the artifact manager directly; this
// wrapper still goes through it and keeps optional checksums.txt behavior.
func downloadUpdateTo(ctx context.Context, repo, target string) error {
	asset, err := updateAssetName()
	if err != nil {
		return err
	}
	checksums, err := fetchChecksums(ctx, repo)
	if err != nil {
		return err
	}
	want := ""
	if checksums != "" {
		want = parseChecksums(checksums)[asset]
		if want == "" {
			return fmt.Errorf("checksums.txt 中没有 %s", asset)
		}
	}
	spec := artifact.Spec{
		Name:    "argoz",
		Version: "latest",
		Asset:   asset,
		URL:     releaseDownloadBase(repo, "latest") + "/" + asset,
		SHA256:  want,
		MinSize: minUpdateBytes,
		MaxSize: 512 << 20,
		Format:  artifact.FormatELF,
	}
	mgr := artifactManagerFor(filepath.Dir(target))
	_, err = mgr.Ensure(ctx, spec, artifact.Options{
		Force:              true,
		HTTPClient:         updateHTTP,
		ValidateVersion:    false,
		ValidateVersionSet: true,
		InstallPath:        target,
		BackupDir:          "",
	})
	return err
}

func selfSpec(repo, version, sha string) (artifact.Spec, error) {
	asset, err := updateAssetName()
	if err != nil {
		return artifact.Spec{}, err
	}
	if strings.TrimSpace(version) == "" {
		version = "latest"
	}
	return artifact.Spec{
		Name:    "argoz",
		Version: version,
		Asset:   asset,
		URL:     releaseDownloadBase(repo, version) + "/" + asset,
		SHA256:  sha,
		MinSize: minUpdateBytes,
		MaxSize: 512 << 20,
		Format:  artifact.FormatELF,
	}, nil
}

func selfExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前二进制失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func selfBackupDir(dataDir string) string {
	return filepath.Join(dataDir, "backups", "self")
}

func cloudflaredBackupDir(dataDir string) string {
	return filepath.Join(dataDir, "backups", "cloudflared")
}

func checkSelfUpdate(ctx context.Context, mgr *artifact.Manager, offline bool, repo string) (UpdateInfo, error) {
	if offline {
		st, err := mgr.Status("argoz")
		if err != nil {
			return UpdateInfo{}, err
		}
		latest := strings.TrimSpace(st.Version)
		if latest == "" || latest == "latest" {
			latest = config.Version
		}
		return UpdateInfo{
			Current:   config.Version,
			Latest:    latest,
			HasUpdate: isNewerVersion(latest, config.Version),
			Notes:     []string{"离线模式：仅使用已缓存资源和状态"},
		}, nil
	}
	if strings.TrimSpace(repo) == "" {
		repo = updateRepo()
	}
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
	if asset, err := updateAssetName(); err == nil {
		for _, a := range ri.Assets {
			if a.Name == asset {
				info.SizeBytes = a.Size
				break
			}
		}
	}
	return info, nil
}

func applySelfUpdate(ctx context.Context, mgr *artifact.Manager, dataDir string, force, offline bool, repo string) (UpdateInfo, UpdateResult, error) {
	if strings.TrimSpace(repo) == "" {
		repo = updateRepo()
	}
	info, checkErr := checkSelfUpdate(ctx, mgr, offline, repo)
	if checkErr != nil {
		// apply 模式在 GitHub API 限流时降级到 latest 直链；--check 仍会
		// 明确返回 API 错误，避免把未知版本伪装成已检查。
		if offline || ctx.Err() != nil {
			return UpdateInfo{}, UpdateResult{}, checkErr
		}
		info = UpdateInfo{
			Current:   config.Version,
			Latest:    "latest",
			HasUpdate: true,
			Notes:     []string{"无法查询 GitHub API，已降级使用 latest 直链: " + checkErr.Error()},
		}
	}
	exe, err := selfExecutable()
	if err != nil {
		return info, UpdateResult{}, err
	}
	if !force && !info.HasUpdate {
		return info, UpdateResult{Replaced: false, Path: exe, Hint: "已是最新版本，无需更新"}, nil
	}

	version := info.Latest
	want := ""
	if version == "" || strings.EqualFold(version, "latest") {
		if offline {
			st, _ := mgr.Status("argoz")
			version = st.Version
			if version == "" || strings.EqualFold(version, "latest") {
				return info, UpdateResult{}, errors.New("离线模式无法确定已缓存的 argoz 版本")
			}
		} else {
			// releases/latest/download 仍可提供 checksums.txt，且下载和
			// 安装仍会经过完整 SHA256、ELF、--version 与回滚事务。
			checksums, err := fetchChecksums(ctx, repo)
			if err != nil {
				return info, UpdateResult{}, err
			}
			want = parseChecksums(checksums)[mustSelfAsset()]
			if want == "" {
				return info, UpdateResult{}, fmt.Errorf("release latest 缺少 %s 的 SHA256，拒绝未校验升级", mustSelfAsset())
			}
			version = "latest"
		}
	} else if !offline {
		checksums, err := fetchChecksumsAt(ctx, repo, version)
		if err != nil {
			return info, UpdateResult{}, err
		}
		want = parseChecksums(checksums)[mustSelfAsset()]
		if want == "" {
			return info, UpdateResult{}, fmt.Errorf("release %s 缺少 %s 的 SHA256，拒绝未校验升级", version, mustSelfAsset())
		}
	}
	spec, err := selfSpec(repo, version, want)
	if err != nil {
		return info, UpdateResult{}, err
	}
	if info.SizeBytes > 0 {
		spec.Size = info.SizeBytes
	}
	versionContains := ""
	if !strings.EqualFold(version, "latest") {
		versionContains = strings.TrimPrefix(version, "v")
	}
	st, err := mgr.Ensure(ctx, spec, artifact.Options{
		Force:              force,
		Offline:            offline,
		Cooldown:           30 * time.Second,
		Timeout:            10 * time.Minute,
		HTTPClient:         updateHTTP,
		ValidateVersion:    true,
		ValidateVersionSet: true,
		VersionArgs:        []string{"--version"},
		VersionContains:    versionContains,
		VersionTimeout:     15 * time.Second,
		InstallPath:        exe,
		BackupDir:          selfBackupDir(dataDir),
		BackupKeep:         3,
	})
	if err != nil {
		return info, UpdateResult{}, err
	}
	res := UpdateResult{Replaced: true, Path: exe, Hint: fmt.Sprintf("已安装 %s；缓存 %s", version, st.CachePath)}
	return info, res, nil
}

func mustSelfAsset() string {
	asset, _ := updateAssetName()
	return asset
}

func runCloudflaredVersion(ctx context.Context, path string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if probeCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("cloudflared --version 超时")
	}
	if err != nil {
		return "", fmt.Errorf("cloudflared --version 失败: %v: %s", err, text)
	}
	return parseVersionToken(text), nil
}

func parseVersionToken(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if strings.EqualFold(strings.TrimSpace(f), "version") && i+1 < len(fields) {
			return strings.Trim(fields[i+1], "()[],;")
		}
	}
	for _, f := range fields {
		f = strings.Trim(f, "()[],;")
		low := strings.ToLower(f)
		if strings.ContainsAny(f, "0123456789") && strings.Contains(f, ".") && !strings.HasPrefix(low, "http") {
			return strings.TrimPrefix(f, "v")
		}
	}
	return ""
}

func cloudflaredCurrent(ctx context.Context) (path, version string) {
	if p, err := findUsableCloudflared(ctx); err == nil {
		path = p
		if v, err := runCloudflaredVersion(ctx, p); err == nil {
			version = v
		}
	}
	return path, version
}

func checkCloudflaredUpdate(ctx context.Context, mgr *artifact.Manager, offline bool) (UpdateInfo, error) {
	st, _ := mgr.Status("cloudflared")
	path, current := cloudflaredCurrent(ctx)
	if path == "" {
		path = st.Path
	}
	if current == "" && st.Version != "" && !strings.EqualFold(st.Version, "latest") {
		current = st.Version
	}
	info := UpdateInfo{Current: current}
	if offline {
		latest := st.Version
		if latest == "" || strings.EqualFold(latest, "latest") {
			latest = current
		}
		info.Latest = latest
		info.HasUpdate = latest != "" && current != "" && isNewerVersion(latest, current)
		info.Notes = []string{"离线模式：仅使用 cloudflared 缓存与本地版本"}
		return info, nil
	}
	ri, err := fetchReleaseInfo(ctx, cloudflaredRepo)
	if err != nil {
		return UpdateInfo{}, err
	}
	info.Latest = ri.TagName
	info.PublishedAt = ri.PublishedAt
	info.Notes = firstLines(ri.Body, 8)
	info.HasUpdate = current == "" || isNewerVersion(ri.TagName, current)
	asset := "cloudflared-linux-" + runtime.GOARCH
	for _, a := range ri.Assets {
		if a.Name == asset {
			info.SizeBytes = a.Size
			break
		}
	}
	return info, nil
}

func cloudflaredUpdateTarget(ctx context.Context, preferred string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" && writableDir(filepath.Dir(preferred)) {
		return preferred
	}
	return cloudflaredInstallPath()
}

func applyCloudflaredUpdate(ctx context.Context, mgr *artifact.Manager, dataDir string, force, offline bool) (UpdateInfo, UpdateResult, error) {
	info, err := checkCloudflaredUpdate(ctx, mgr, offline)
	if err != nil {
		// GitHub API 可能被限流；安装/修复仍可降级到 releases/latest/download。
		// 显式 --check 仍会返回错误，apply 模式才使用直链兜底。
		if offline || ctx.Err() != nil {
			return UpdateInfo{}, UpdateResult{}, err
		}
		_, currentVersion := cloudflaredCurrent(ctx)
		info = UpdateInfo{
			Current:   currentVersion,
			Latest:    "latest",
			HasUpdate: true,
			Notes:     []string{"无法查询 GitHub API，已降级使用 latest 直链: " + err.Error()},
		}
	}
	currentPath, _ := cloudflaredCurrent(ctx)
	if !force && currentPath != "" && !info.HasUpdate {
		return info, UpdateResult{Replaced: false, Path: currentPath, Hint: "cloudflared 已是最新版本"}, nil
	}
	version := info.Latest
	if version == "" {
		st, _ := mgr.Status("cloudflared")
		version = st.Version
	}
	if version == "" {
		version = "latest"
	}
	spec, err := cloudflaredSpec(version)
	if err != nil {
		return info, UpdateResult{}, err
	}
	if info.SizeBytes > 0 {
		spec.Size = info.SizeBytes
	}
	target := cloudflaredUpdateTarget(ctx, currentPath)
	st, err := mgr.Ensure(ctx, spec, artifact.Options{
		Force:              force,
		Offline:            offline,
		Cooldown:           30 * time.Second,
		Timeout:            10 * time.Minute,
		HTTPClient:         updateHTTP,
		ValidateVersion:    true,
		ValidateVersionSet: true,
		VersionArgs:        []string{"--version"},
		VersionTimeout:     15 * time.Second,
		InstallPath:        target,
		BackupDir:          cloudflaredBackupDir(dataDir),
		BackupKeep:         3,
	})
	if err != nil {
		return info, UpdateResult{}, err
	}
	path := st.Path
	if path == "" {
		path = st.CachePath
	}
	res := UpdateResult{Replaced: true, Path: path, Hint: "cloudflared 已更新；运行中的隧道会在下次启动时使用新二进制"}
	return info, res, nil
}

func rollbackComponent(ctx context.Context, dataDir, component string) (UpdateResult, error) {
	switch strings.ToLower(strings.TrimSpace(component)) {
	case "", "self", "argoz":
		exe, err := selfExecutable()
		if err != nil {
			return UpdateResult{}, err
		}
		backup, err := artifact.LatestBackup(selfBackupDir(dataDir))
		if err != nil {
			return UpdateResult{}, err
		}
		err = artifact.RestoreBackup(ctx, backup, exe, artifact.ValidateOptions{
			Format:         artifact.FormatELF,
			RunVersion:     true,
			VersionArgs:    []string{"--version"},
			VersionTimeout: 15 * time.Second,
		})
		if err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{Replaced: true, Path: exe, Hint: "已回滚 argoz；请重启服务使旧版本生效"}, nil
	case "cloudflared", "cf":
		currentPath, _ := cloudflaredCurrent(ctx)
		target := cloudflaredUpdateTarget(ctx, currentPath)
		backup, err := artifact.LatestBackup(cloudflaredBackupDir(dataDir))
		if err != nil {
			return UpdateResult{}, err
		}
		err = artifact.RestoreBackup(ctx, backup, target, artifact.ValidateOptions{
			Format:         artifact.FormatELF,
			MinSize:        1 << 20,
			MaxSize:        256 << 20,
			RunVersion:     true,
			VersionArgs:    []string{"--version"},
			VersionTimeout: 15 * time.Second,
		})
		if err != nil {
			return UpdateResult{}, err
		}
		return UpdateResult{Replaced: true, Path: target, Hint: "cloudflared 已回滚；运行中的隧道会在下次启动时使用回滚版本"}, nil
	default:
		return UpdateResult{}, fmt.Errorf("未知组件 %q（支持 self/cloudflared）", component)
	}
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

// RunUpdateCommandLine is the stable `argoz update` entry point. Existing
// --check/--force/--repo flags remain valid; --component, --offline and
// --rollback are additive.
func RunUpdateCommandLine(args []string, defaultDir string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "只检查版本，不下载")
	force := fs.Bool("force", false, "即使版本相同也重新下载/重装")
	offline := fs.Bool("offline", false, "只使用本地缓存，不访问网络")
	rollback := fs.Bool("rollback", false, "回滚到最近一次备份")
	component := fs.String("component", "self", "组件：self 或 cloudflared")
	repo := fs.String("repo", updateRepo(), "GitHub 仓库，例如 Zzz-IT/argoz")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("update 不接受位置参数: %v", fs.Args())
	}
	dataDir := strings.TrimSpace(defaultDir)
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	mgr := artifactManagerFor(dataDir)

	switch strings.ToLower(strings.TrimSpace(*component)) {
	case "", "self", "argoz":
		if *rollback {
			res, err := rollbackComponent(ctx, dataDir, "self")
			if err != nil {
				return err
			}
			fmt.Println(res.Hint)
			return restartAfterCLIUpdate()
		}
		if *check {
			info, err := checkSelfUpdate(ctx, mgr, *offline, *repo)
			if err != nil {
				return err
			}
			fmt.Printf("当前 argoz 版本: %s\n目标版本: %s\n", info.Current, info.Latest)
			if info.HasUpdate {
				fmt.Println("有可用更新")
			} else {
				fmt.Println("已是最新版本")
			}
			return nil
		}
		info, res, err := applySelfUpdate(ctx, mgr, dataDir, *force, *offline, *repo)
		if err != nil {
			return err
		}
		fmt.Printf("当前 argoz 版本: %s\n目标版本: %s\n", info.Current, info.Latest)
		if !res.Replaced {
			fmt.Println(res.Hint)
			return nil
		}
		fmt.Printf("已更新: %s\n", res.Path)
		return restartAfterCLIUpdate()

	case "cloudflared", "cf":
		if *rollback {
			res, err := rollbackComponent(ctx, dataDir, "cloudflared")
			if err != nil {
				return err
			}
			fmt.Println(res.Hint)
			return nil
		}
		if *check {
			info, err := checkCloudflaredUpdate(ctx, mgr, *offline)
			if err != nil {
				return err
			}
			fmt.Printf("当前 cloudflared: %s\n目标版本: %s\n", valueOr(info.Current, "未安装"), valueOr(info.Latest, "未知"))
			if info.HasUpdate {
				fmt.Println("有可用更新")
			} else {
				fmt.Println("已是最新版本")
			}
			return nil
		}
		info, res, err := applyCloudflaredUpdate(ctx, mgr, dataDir, *force, *offline)
		if err != nil {
			return err
		}
		fmt.Printf("当前 cloudflared: %s\n目标版本: %s\n", valueOr(info.Current, "未安装"), valueOr(info.Latest, "未知"))
		if !res.Replaced {
			fmt.Println(res.Hint)
			return nil
		}
		fmt.Printf("已更新 cloudflared: %s\n", res.Path)
		return nil
	default:
		return fmt.Errorf("未知组件 %q（支持 self/cloudflared）", *component)
	}
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func restartAfterCLIUpdate() error {
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

// RunArtifactCommandLine is an optional maintenance command for future main
// wiring: `argoz artifact status [name]` or
// `argoz artifact ensure cloudflared [--force] [--offline]`.
func RunArtifactCommandLine(args []string, defaultDir string) error {
	if len(args) == 0 {
		return errors.New("用法: artifact status [name] | artifact ensure cloudflared [--force] [--offline] | artifact verify <path>")
	}
	dataDir := strings.TrimSpace(defaultDir)
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	mgr := artifactManagerFor(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch strings.ToLower(args[0]) {
	case "status":
		name := "cloudflared"
		if len(args) > 1 {
			name = args[1]
		}
		st, err := mgr.Status(name)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(st)
	case "ensure":
		if len(args) < 2 || strings.ToLower(args[1]) != "cloudflared" {
			return errors.New("目前 artifact ensure 仅支持 cloudflared")
		}
		force, offline := false, false
		for _, arg := range args[2:] {
			switch arg {
			case "--force":
				force = true
			case "--offline":
				offline = true
			default:
				return fmt.Errorf("未知参数: %s", arg)
			}
		}
		_, res, err := applyCloudflaredUpdate(ctx, mgr, dataDir, force, offline)
		if err != nil {
			return err
		}
		fmt.Println(valueOr(res.Path, res.Hint))
		return nil
	case "verify":
		if len(args) != 2 {
			return errors.New("用法: artifact verify <path>")
		}
		format := artifact.FormatELF
		if runtime.GOOS != "linux" {
			format = artifact.FormatAny
		}
		return artifact.ValidateBinary(ctx, args[1], artifact.ValidateOptions{
			Format:         format,
			RunVersion:     true,
			VersionArgs:    []string{"--version"},
			VersionTimeout: 15 * time.Second,
		})
	default:
		return fmt.Errorf("未知 artifact 子命令: %s", args[0])
	}
}

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

// UpdateCheck 查询最新版本（self 兼容入口）
func (e *Engine) UpdateCheck() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return checkSelfUpdate(ctx, artifactManagerFor(e.dataDir), false, updateRepo())
}

// UpdateApply 下载并替换自身二进制（self 兼容入口）
func (e *Engine) UpdateApply() (UpdateResult, error) {
	return e.ApplyUpdate("self", false)
}

// CheckUpdate 是 RPC 的组件化检查入口。
func (e *Engine) CheckUpdate(component string) (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return e.checkUpdateContext(ctx, component)
}

func (e *Engine) checkUpdateContext(ctx context.Context, component string) (UpdateInfo, error) {
	mgr := artifactManagerFor(e.dataDir)
	switch strings.ToLower(strings.TrimSpace(component)) {
	case "", "self", "argoz":
		return checkSelfUpdate(ctx, mgr, false, updateRepo())
	case "cloudflared", "cf":
		return checkCloudflaredUpdate(ctx, mgr, false)
	default:
		return UpdateInfo{}, fmt.Errorf("未知组件 %q（支持 self/cloudflared）", component)
	}
}

// ApplyUpdate 是 RPC 的组件化安装入口。force=true 会重装当前版本。
func (e *Engine) ApplyUpdate(component string, force bool) (UpdateResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	mgr := artifactManagerFor(e.dataDir)
	switch strings.ToLower(strings.TrimSpace(component)) {
	case "", "self", "argoz":
		_, res, err := applySelfUpdate(ctx, mgr, e.dataDir, force, false, updateRepo())
		if err != nil {
			return UpdateResult{}, err
		}
		if !res.Replaced {
			return res, nil
		}
		return e.restartAfterSelfUpdate(res)
	case "cloudflared", "cf":
		_, res, err := applyCloudflaredUpdate(ctx, mgr, e.dataDir, force, false)
		return res, err
	default:
		return UpdateResult{}, fmt.Errorf("未知组件 %q（支持 self/cloudflared）", component)
	}
}

func (e *Engine) restartAfterSelfUpdate(res UpdateResult) (UpdateResult, error) {
	res.Restarted = false
	_, conns, _, _ := e.Totals()
	switch {
	case conns == 0:
		res.Restarted = true
		res.Hint = "正在原地重启到新版本"
		go func() {
			time.Sleep(600 * time.Millisecond)
			if err := e.ReloadSelf(); err != nil {
				_, _ = restartInstalledService()
			}
		}()
	case serviceInstalled():
		res.Restarted = true
		res.Hint = "服务将自动重启并加载新版本"
		go func() {
			time.Sleep(600 * time.Millisecond)
			_, _ = restartInstalledService()
		}()
	case hasSupervisor():
		res.Restarted = true
		res.Hint = "supervisor 将自动拉起新版本"
		go func() {
			time.Sleep(600 * time.Millisecond)
			os.Exit(0)
		}()
	default:
		res.Hint = "新版本已就位，但未检测到 systemd/OpenRC/supervisor，需要手动重启 daemon"
	}
	return res, nil
}

// CloudflaredStatus 把 artifact.Status 映射为 RPC 使用的 core.ArtifactStatus。
func (e *Engine) CloudflaredStatus() (ArtifactStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	mgr := artifactManagerFor(e.dataDir)
	st, err := mgr.Status("cloudflared")
	if err != nil {
		return ArtifactStatus{Name: "cloudflared", LastError: err.Error()}, err
	}
	usablePath, version := cloudflaredCurrent(ctx)
	path := usablePath
	if path == "" {
		path = st.Path
	}
	if version == "" && st.Version != "" && !strings.EqualFold(st.Version, "latest") {
		version = st.Version
	}
	out := ArtifactStatus{
		Name:      "cloudflared",
		Path:      path,
		Version:   version,
		Installed: coreArtifactPathExists(path),
		Healthy:   usablePath != "",
		Cached:    coreArtifactPathExists(st.CachePath),
		Size:      st.SizeBytes,
		LastError: st.LastError,
	}
	if out.Size == 0 {
		if fi, statErr := os.Stat(path); statErr == nil {
			out.Size = fi.Size()
		}
	}
	if out.Installed && !out.Healthy && out.LastError == "" {
		out.LastError = "cloudflared 存在但 ELF/执行权限/--version 校验未通过"
	}
	return out, nil
}

// coreArtifactPathExists is a tiny local helper shared by status mapping.
func coreArtifactPathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
