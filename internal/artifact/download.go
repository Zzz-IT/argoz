package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (m *Manager) downloadAndVerify(ctx context.Context, spec Spec, cachePath string, opts Options) (Info, error) {
	if strings.TrimSpace(spec.URL) == "" {
		return Info{}, errors.New("下载 URL 为空")
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o750); err != nil {
		return Info{}, err
	}
	partPath := filepath.Join(filepath.Dir(cachePath), spec.Asset+".part")
	client := opts.HTTPClient
	if client == nil {
		client = m.httpClient
	}
	if client == nil {
		client = &http.Client{}
	}

	for attempt := 0; attempt < 2; attempt++ {
		info, retry, err := m.downloadAttempt(ctx, client, spec, partPath, opts)
		if retry {
			_ = removeIfExists(partPath)
			continue
		}
		if err != nil {
			return Info{}, err
		}
		if err := replacePath(partPath, cachePath); err != nil {
			return Info{}, fmt.Errorf("缓存文件原子落位失败: %w", err)
		}
		fsyncDir(filepath.Dir(cachePath))
		info.Path = cachePath
		return info, nil
	}
	return Info{}, errors.New("下载断点无效，重试后仍失败")
}

func (m *Manager) downloadAttempt(ctx context.Context, client *http.Client, spec Spec, partPath string, opts Options) (Info, bool, error) {
	var offset int64
	if st, err := os.Stat(partPath); err == nil && st.Mode().IsRegular() {
		offset = st.Size()
	}
	if spec.Size > 0 && offset > spec.Size {
		m.quarantine(partPath, spec.Name, spec.Asset)
		offset = 0
	}
	if spec.MaxSize > 0 && offset > spec.MaxSize {
		m.quarantine(partPath, spec.Name, spec.Asset)
		offset = 0
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return Info{}, false, err
	}
	req.Header.Set("User-Agent", "argoz-artifact/1.0")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, false, fmt.Errorf("下载 %s 失败: %w", spec.Asset, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		return Info{}, true, fmt.Errorf("服务器拒绝断点续传")
	}
	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0
	case http.StatusPartialContent:
		if offset == 0 {
			return Info{}, false, fmt.Errorf("服务器返回意外 206 响应")
		}
		start, ok := parseContentRangeStart(resp.Header.Get("Content-Range"))
		if !ok || start != offset {
			return Info{}, true, fmt.Errorf("服务器断点区间不匹配: %s", resp.Header.Get("Content-Range"))
		}
	default:
		return Info{}, false, fmt.Errorf("下载 %s 返回 HTTP %d", spec.Asset, resp.StatusCode)
	}

	if spec.MaxSize > 0 && resp.ContentLength > 0 && offset+resp.ContentLength > spec.MaxSize {
		return Info{}, false, fmt.Errorf("下载文件超过上限 %d 字节", spec.MaxSize)
	}

	h := sha256.New()
	if offset > 0 {
		f, err := os.Open(partPath)
		if err != nil {
			return Info{}, true, fmt.Errorf("读取断点文件失败: %w", err)
		}
		_, copyErr := io.Copy(h, f)
		_ = f.Close()
		if copyErr != nil {
			return Info{}, true, fmt.Errorf("读取断点文件失败: %w", copyErr)
		}
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(partPath, flags, 0o755)
	if err != nil {
		return Info{}, false, fmt.Errorf("打开断点文件失败: %w", err)
	}
	limit := spec.MaxSize - offset + 1
	if limit <= 0 {
		_ = f.Close()
		return Info{}, true, errors.New("断点文件超过最大大小")
	}
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit))
	syncErr := f.Sync()
	closeErr := f.Close()
	total := offset + n
	if copyErr != nil {
		return Info{}, false, fmt.Errorf("流式下载失败（已保留断点 %d 字节）: %w", total, copyErr)
	}
	if syncErr != nil {
		return Info{}, false, fmt.Errorf("断点刷盘失败: %w", syncErr)
	}
	if closeErr != nil {
		return Info{}, false, fmt.Errorf("关闭断点文件失败: %w", closeErr)
	}
	if total > spec.MaxSize {
		m.quarantine(partPath, spec.Name, spec.Asset)
		return Info{}, false, fmt.Errorf("下载文件超过上限 %d 字节", spec.MaxSize)
	}
	if spec.Size > 0 && total != spec.Size {
		m.quarantine(partPath, spec.Name, spec.Asset)
		return Info{}, false, fmt.Errorf("下载大小不符: got %d want %d", total, spec.Size)
	}
	if spec.MinSize > 0 && total < spec.MinSize {
		m.quarantine(partPath, spec.Name, spec.Asset)
		return Info{}, false, fmt.Errorf("下载文件过小: %d < %d 字节", total, spec.MinSize)
	}
	gotHash := hex.EncodeToString(h.Sum(nil))
	wantHash := strings.ToLower(strings.TrimSpace(spec.SHA256))
	if wantHash != "" && gotHash != wantHash {
		m.quarantine(partPath, spec.Name, spec.Asset)
		return Info{}, false, fmt.Errorf("SHA256 校验失败: got %s want %s", gotHash, wantHash)
	}
	if err := os.Chmod(partPath, 0o755); err != nil {
		return Info{}, false, fmt.Errorf("设置可执行权限失败: %w", err)
	}
	info, err := Inspect(ctx, partPath, ValidateOptions{
		Format:          spec.Format,
		MinSize:         spec.MinSize,
		MaxSize:         spec.MaxSize,
		SHA256:          wantHash,
		RunVersion:      opts.ValidateVersion,
		VersionArgs:     opts.VersionArgs,
		VersionContains: opts.VersionContains,
		VersionTimeout:  opts.VersionTimeout,
	})
	if err != nil {
		m.quarantine(partPath, spec.Name, spec.Asset)
		return Info{}, false, err
	}
	info.SHA256 = gotHash
	info.Path = partPath
	return info, false, nil
}

func parseContentRangeStart(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToLower(v), "bytes ") {
		return 0, false
	}
	v = strings.TrimSpace(v[len("bytes "):])
	first := strings.SplitN(v, "-", 2)
	if len(first) != 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(first[0]), 10, 64)
	return n, err == nil
}
