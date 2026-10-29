package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns one data directory. It is safe for concurrent use and also
// serializes with other argoz processes through a lock file per component.
type Manager struct {
	dataDir string
	root    string

	httpClient *http.Client

	mu        sync.Mutex
	keyLocks  map[string]chan struct{}
	validated map[string]validationMemo
}

type validationMemo struct {
	size            int64
	mod             time.Time
	sha             string
	format          Format
	runVersion      bool
	versionContains string
}

// New creates a manager rooted at <dataDir>/cache. The root is created on a
// best-effort basis; Ensure reports filesystem errors.
func New(dataDir string) *Manager {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "."
	}
	m := &Manager{
		dataDir:    dataDir,
		root:       filepath.Join(dataDir, "cache"),
		httpClient: &http.Client{},
		keyLocks:   map[string]chan struct{}{},
		validated:  map[string]validationMemo{},
	}
	_ = os.MkdirAll(m.root, 0o750)
	return m
}

// NewManager is an explicit alias retained for callers that prefer the longer
// constructor name.
func NewManager(dataDir string) *Manager { return New(dataDir) }

// DataDir returns the manager's data directory.
func (m *Manager) DataDir() string { return m.dataDir }

// Ensure makes a verified artifact available. It returns the existing cache or
// target when healthy, resumes a .part file when possible, and never replaces a
// working file until the new download has passed size, SHA-256, ELF and
// optional --version validation.
func (m *Manager) Ensure(ctx context.Context, spec Spec, opts Options) (Status, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return Status{State: StateFailed, LastError: err.Error()}, err
	}
	opts = normalizeOptions(spec, opts)
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	if err := os.MkdirAll(m.root, 0o750); err != nil {
		return Status{Name: spec.Name, State: StateFailed, LastError: err.Error()}, err
	}

	unlockKey, err := m.acquireKeyLock(ctx, spec.Name)
	if err != nil {
		return Status{Name: spec.Name, State: StateFailed, LastError: err.Error()}, err
	}
	defer unlockKey()

	lockPath := filepath.Join(m.root, spec.Name, "state.lock")
	unlockFile, err := acquireFileLock(ctx, lockPath)
	if err != nil {
		return Status{Name: spec.Name, State: StateFailed, LastError: err.Error()}, err
	}
	defer unlockFile()

	prev, err := m.readStatus(spec.Name)
	if err != nil {
		return Status{Name: spec.Name, State: StateFailed, LastError: err.Error()}, err
	}
	cachePath := m.cachePath(spec)
	st := prev
	st.Name = spec.Name
	st.Version = spec.Version
	st.Asset = spec.Asset
	st.State = StateDownloading
	st.LastAttemptAt = time.Now().UTC()
	st.RetryAfter = time.Time{}
	if err := m.writeStatus(st); err != nil {
		return st, fmt.Errorf("写入资源状态失败: %w", err)
	}

	var cacheInfo Info
	if !opts.Force || opts.Offline {
		info, exists, inspectErr := m.inspectCached(ctx, cachePath, spec, opts, prev.SHA256)
		if exists {
			if inspectErr == nil {
				cacheInfo = info
			} else {
				st.Quarantine = m.quarantine(cachePath, spec.Name, spec.Asset)
				st.Ready = regularFile(st.Path)
				st.Valid = st.Ready
				st.CachePath = ""
				st.LastError = inspectErr.Error()
				st.State = StateFailed
				_ = m.writeStatus(st)
				prev = st
			}
		}
	}

	if cacheInfo.Path != "" {
		materialized, materializeErr := m.materialize(ctx, spec, cachePath, cacheInfo, opts, "cache")
		if materializeErr != nil {
			failed := m.recordFailure(prev, spec, opts, materializeErr, nil)
			if cacheInfo.Path != "" {
				failed.CachePath = cachePath
				failed.SizeBytes = cacheInfo.Size
				failed.SHA256 = cacheInfo.SHA256
				failed.Ready = true
				failed.Valid = true
				_ = m.writeStatus(failed)
			}
			return failed, materializeErr
		}
		if err := m.writeStatus(materialized); err != nil {
			return materialized, err
		}
		return materialized, nil
	}

	if opts.Offline {
		offlineErr := &OfflineError{Name: spec.Name}
		st.State = StateOffline
		st.LastError = offlineErr.Error()
		st.Ready = false
		st.Valid = false
		err = m.writeStatus(st)
		if err != nil {
			return st, fmt.Errorf("%v；写入状态失败: %w", offlineErr, err)
		}
		return st, offlineErr
	}
	if !opts.Force && opts.Cooldown > 0 && !prev.RetryAfter.IsZero() && time.Now().Before(prev.RetryAfter) {
		err := &CooldownError{Name: spec.Name, Until: prev.RetryAfter, Err: errors.New(prev.LastError)}
		st = prev
		st.Name = spec.Name
		st.Version = spec.Version
		st.Asset = spec.Asset
		st.LastError = err.Error()
		_ = m.writeStatus(st)
		return st, err
	}

	info, err := m.downloadAndVerify(ctx, spec, cachePath, opts)
	if err != nil {
		failed := m.recordFailure(prev, spec, opts, err, nil)
		return failed, fmt.Errorf("获取 %s 失败: %w", spec.Name, err)
	}
	cacheInfo = info
	source := "download"
	materialized, materializeErr := m.materialize(ctx, spec, cachePath, cacheInfo, opts, source)
	if materializeErr != nil {
		failed := m.recordFailure(prev, spec, opts, materializeErr, &cacheInfo)
		return failed, materializeErr
	}
	if err := m.writeStatus(materialized); err != nil {
		return materialized, err
	}
	return materialized, nil
}

func (m *Manager) materialize(ctx context.Context, spec Spec, cachePath string, cacheInfo Info, opts Options, source string) (Status, error) {
	usedPath := cachePath
	target := strings.TrimSpace(opts.InstallPath)
	if target != "" && !samePath(target, cachePath) {
		already := false
		if !opts.Force {
			if info, exists, err := m.inspectCached(ctx, target, spec, opts, cacheInfo.SHA256); exists && err == nil && info.SHA256 == cacheInfo.SHA256 {
				usedPath = target
				already = true
			}
		}
		if !already {
			validate := validateOptionsFor(spec, opts, cacheInfo.SHA256)
			if _, err := ReplaceFile(ctx, cachePath, target, ReplaceOptions{
				BackupDir:  opts.BackupDir,
				BackupKeep: opts.BackupKeep,
				Validate:   validate,
			}); err != nil {
				return Status{Name: spec.Name, State: StateFailed, LastError: err.Error()}, fmt.Errorf("安装 %s 到 %s 失败: %w", spec.Name, target, err)
			}
			usedPath = target
		}
	}
	now := time.Now().UTC()
	st := Status{
		Name:          spec.Name,
		Version:       spec.Version,
		Asset:         spec.Asset,
		State:         StateReady,
		Ready:         true,
		Valid:         true,
		Path:          usedPath,
		CachePath:     cachePath,
		Source:        source,
		SizeBytes:     cacheInfo.Size,
		SHA256:        cacheInfo.SHA256,
		LastAttemptAt: now,
		UpdatedAt:     now,
	}
	return st, nil
}

// Status returns the last persisted state. A missing state is not an error.
func (m *Manager) Status(name string) (Status, error) {
	safeName := sanitizeSegment(name, "")
	if safeName == "" {
		return Status{State: StateMissing}, errors.New("资源名称不能为空")
	}
	st, err := m.readStatus(safeName)
	if err != nil {
		return Status{Name: safeName, State: StateFailed, LastError: err.Error()}, err
	}
	if st.State == "" {
		st.State = StateMissing
	}
	if st.Name == "" {
		st.Name = safeName
	}
	st.Valid = st.Ready
	if st.Ready {
		pathExists := regularFile(st.Path)
		cacheExists := regularFile(st.CachePath)
		if !pathExists && !cacheExists {
			st.Ready = false
			st.Valid = false
		}
	}
	return st, nil
}

func (m *Manager) inspectCached(ctx context.Context, path string, spec Spec, opts Options, previousSHA string) (Info, bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Info{}, false, nil
		}
		return Info{}, true, binaryErr(path, BinaryIO, "无法读取缓存文件", err)
	}
	if !st.Mode().IsRegular() {
		return Info{}, true, binaryErr(path, BinaryFormat, "缓存路径不是普通文件", nil)
	}
	expected := strings.ToLower(strings.TrimSpace(spec.SHA256))
	if expected == "" {
		expected = strings.ToLower(strings.TrimSpace(previousSHA))
	}
	key := cleanAbs(path)
	m.mu.Lock()
	memo, ok := m.validated[key]
	m.mu.Unlock()
	if ok && memo.size == st.Size() && memo.mod.Equal(st.ModTime()) && memo.format == spec.Format && memo.runVersion == opts.ValidateVersion && memo.versionContains == opts.VersionContains && (expected == "" || strings.EqualFold(expected, memo.sha)) {
		return Info{Path: path, Size: memo.size, SHA256: memo.sha, ELF: spec.Format == FormatELF, Executable: true}, true, nil
	}
	info, err := Inspect(ctx, path, validateOptionsFor(spec, opts, expected))
	if err != nil {
		return Info{}, true, err
	}
	m.mu.Lock()
	m.validated[key] = validationMemo{size: info.Size, mod: st.ModTime(), sha: info.SHA256, format: spec.Format, runVersion: opts.ValidateVersion, versionContains: opts.VersionContains}
	m.mu.Unlock()
	return info, true, nil
}

func (m *Manager) acquireKeyLock(ctx context.Context, name string) (func(), error) {
	m.mu.Lock()
	ch := m.keyLocks[name]
	if ch == nil {
		ch = make(chan struct{}, 1)
		m.keyLocks[name] = ch
	}
	m.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("等待 %s 单飞锁: %w", name, ctx.Err())
	}
}

func (m *Manager) recordFailure(base Status, spec Spec, opts Options, err error, cacheInfo *Info) Status {
	now := time.Now().UTC()
	st := base
	st.Name = spec.Name
	st.Version = spec.Version
	st.Asset = spec.Asset
	st.State = StateFailed
	st.LastError = err.Error()
	st.FailureCount++
	st.LastAttemptAt = now
	if opts.Cooldown > 0 {
		st.RetryAfter = now.Add(opts.Cooldown)
	} else {
		st.RetryAfter = time.Time{}
	}
	if st.Ready && !regularFile(st.Path) && !regularFile(st.CachePath) {
		st.Ready = false
		st.Valid = false
	}
	if cacheInfo != nil {
		st.Ready = true
		st.Valid = true
		st.CachePath = m.cachePath(spec)
		st.SizeBytes = cacheInfo.Size
		st.SHA256 = cacheInfo.SHA256
		st.Source = "cache"
	}
	_ = m.writeStatus(st)
	return st
}

func (m *Manager) cachePath(spec Spec) string {
	return filepath.Join(m.root, spec.Name, spec.Version, spec.Asset)
}

func (m *Manager) statePath(name string) string {
	return filepath.Join(m.root, name, "state.json")
}

func (m *Manager) readStatus(name string) (Status, error) {
	path := m.statePath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{Name: name, State: StateMissing}, nil
		}
		return Status{Name: name, State: StateFailed}, err
	}
	var st Status
	if err := json.Unmarshal(data, &st); err != nil {
		return Status{Name: name, State: StateFailed}, fmt.Errorf("解析资源状态失败: %w", err)
	}
	if st.Name == "" {
		st.Name = name
	}
	return st, nil
}

func (m *Manager) writeStatus(st Status) error {
	st.UpdatedAt = time.Now().UTC()
	if st.State == "" {
		st.State = StateMissing
	}
	st.Valid = st.Ready
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(m.statePath(st.Name), data, 0o600)
}

func (m *Manager) quarantine(path, name, asset string) string {
	if !regularFile(path) {
		return ""
	}
	dir := filepath.Join(m.root, sanitizeSegment(name, "artifact"), "quarantine")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ""
	}
	base := sanitizeSegment(asset, "artifact")
	dst := filepath.Join(dir, fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405.000000000Z"), base))
	if err := os.Rename(path, dst); err != nil {
		return ""
	}
	fsyncDir(dir)
	return dst
}

func normalizeSpec(spec Spec) (Spec, error) {
	name := sanitizeSegment(spec.Name, "")
	if name == "" {
		return spec, errors.New("资源名称不能为空")
	}
	spec.Name = name
	if strings.TrimSpace(spec.Version) == "" {
		spec.Version = "latest"
	}
	spec.Version = sanitizeSegment(spec.Version, "latest")
	if strings.TrimSpace(spec.Asset) == "" && spec.URL != "" {
		spec.Asset = filepath.Base(strings.SplitN(spec.URL, "?", 2)[0])
	}
	spec.Asset = sanitizeSegment(spec.Asset, name)
	if spec.Format == "" {
		spec.Format = FormatELF
	}
	if spec.Format != FormatELF && spec.Format != FormatAny {
		return spec, fmt.Errorf("不支持的资源格式: %s", spec.Format)
	}
	if spec.MaxSize <= 0 {
		spec.MaxSize = defaultMaxArtifactSize
	}
	if spec.MinSize < 0 {
		spec.MinSize = 0
	}
	if spec.MinSize > spec.MaxSize {
		return spec, errors.New("MinSize 不能大于 MaxSize")
	}
	spec.SHA256 = strings.ToLower(strings.TrimSpace(spec.SHA256))
	return spec, nil
}

func normalizeOptions(spec Spec, opts Options) Options {
	if opts.Cooldown == 0 {
		opts.Cooldown = 30 * time.Second
	}
	if opts.VersionTimeout <= 0 {
		opts.VersionTimeout = 10 * time.Second
	}
	if opts.BackupKeep <= 0 {
		opts.BackupKeep = 3
	}
	if !opts.ValidateVersionSet && spec.Format == FormatELF {
		opts.ValidateVersion = true
	}
	if opts.ValidateVersion && len(opts.VersionArgs) == 0 {
		opts.VersionArgs = []string{"--version"}
	}
	return opts
}

func validateOptionsFor(spec Spec, opts Options, sha string) ValidateOptions {
	return ValidateOptions{
		Format:          spec.Format,
		MinSize:         spec.MinSize,
		MaxSize:         spec.MaxSize,
		SHA256:          strings.ToLower(strings.TrimSpace(sha)),
		RunVersion:      opts.ValidateVersion,
		VersionArgs:     opts.VersionArgs,
		VersionContains: opts.VersionContains,
		VersionTimeout:  opts.VersionTimeout,
	}
}

func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	if aa == "" || bb == "" {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func cleanAbs(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func regularFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
