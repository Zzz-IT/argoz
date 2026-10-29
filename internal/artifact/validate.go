package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const defaultMaxArtifactSize int64 = 1 << 30

func normalizedValidateOptions(o ValidateOptions) ValidateOptions {
	if o.Format == "" {
		o.Format = FormatELF
	}
	if o.MaxSize <= 0 {
		o.MaxSize = defaultMaxArtifactSize
	}
	if o.VersionTimeout <= 0 {
		o.VersionTimeout = 10 * time.Second
	}
	if o.RunVersion && len(o.VersionArgs) == 0 {
		o.VersionArgs = []string{"--version"}
	}
	return o
}

// Inspect validates a local file set. When RunVersion is set it also executes
// the binary with a hard timeout and captures a bounded amount of output.
func Inspect(ctx context.Context, path string, opts ValidateOptions) (Info, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts = normalizedValidateOptions(opts)
	info := Info{Path: path}

	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return info, binaryErr(path, BinaryPermission, "无法读取文件", err)
		}
		return info, binaryErr(path, BinaryIO, "无法读取文件", err)
	}
	if !st.Mode().IsRegular() {
		return info, binaryErr(path, BinaryFormat, "不是普通文件", nil)
	}
	info.Size = st.Size()
	if opts.MinSize > 0 && st.Size() < opts.MinSize {
		return info, binaryErr(path, BinaryCorrupt, fmt.Sprintf("文件过小: %d < %d 字节", st.Size(), opts.MinSize), nil)
	}
	if opts.MaxSize > 0 && st.Size() > opts.MaxSize {
		return info, binaryErr(path, BinaryCorrupt, fmt.Sprintf("文件过大: %d > %d 字节", st.Size(), opts.MaxSize), nil)
	}
	info.Executable = runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0
	if !info.Executable {
		return info, binaryErr(path, BinaryPermission, "文件没有可执行权限", nil)
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return info, binaryErr(path, BinaryPermission, "打开文件被拒绝", err)
		}
		return info, binaryErr(path, BinaryIO, "打开文件失败", err)
	}
	defer f.Close()

	if opts.Format == FormatELF {
		var magic [4]byte
		if _, err := io.ReadFull(f, magic[:]); err != nil {
			return info, binaryErr(path, BinaryCorrupt, "无法读取 ELF 头", err)
		}
		if magic != [4]byte{0x7f, 'E', 'L', 'F'} {
			return info, binaryErr(path, BinaryFormat, "不是 ELF 可执行文件", nil)
		}
		info.ELF = true
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return info, binaryErr(path, BinaryIO, "读取 ELF 后无法回卷", err)
		}
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return info, binaryErr(path, BinaryIO, "计算 SHA256 失败", err)
	}
	info.SHA256 = hex.EncodeToString(h.Sum(nil))
	if want := strings.ToLower(strings.TrimSpace(opts.SHA256)); want != "" && want != info.SHA256 {
		return info, binaryErr(path, BinaryCorrupt, fmt.Sprintf("SHA256 不匹配: got %s want %s", info.SHA256, want), nil)
	}

	if opts.RunVersion {
		if err := runVersionProbe(ctx, path, opts); err != nil {
			return info, err
		}
	}
	return info, nil
}

// ValidateBinary is a convenience wrapper around Inspect.
func ValidateBinary(ctx context.Context, path string, opts ValidateOptions) error {
	_, err := Inspect(ctx, path, opts)
	return err
}

func runVersionProbe(ctx context.Context, path string, opts ValidateOptions) error {
	probeCtx, cancel := context.WithTimeout(ctx, opts.VersionTimeout)
	defer cancel()

	var out limitedBuffer
	out.max = 64 << 10
	cmd := exec.CommandContext(probeCtx, path, opts.VersionArgs...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	text := strings.TrimSpace(out.String())

	if probeCtx.Err() == context.DeadlineExceeded {
		return binaryErr(path, BinaryTimeout, "执行 --version 超时", probeCtx.Err())
	}
	if probeCtx.Err() != nil {
		return binaryErr(path, BinaryIO, "执行 --version 被取消", probeCtx.Err())
	}
	if runErr != nil {
		low := strings.ToLower(text + " " + runErr.Error())
		switch {
		case strings.Contains(low, "permission denied"), strings.Contains(low, "operation not permitted"), strings.Contains(low, "access is denied"):
			return binaryErr(path, BinaryPermission, probeDetail(text, runErr), runErr)
		case strings.Contains(low, "exec format error"), strings.Contains(low, "invalid elf"),
			strings.Contains(low, "cannot execute binary file"), strings.Contains(low, "not a valid win32 application"), strings.Contains(low, "no such file or directory"):
			return binaryErr(path, BinaryFormat, probeDetail(text, runErr), runErr)
		case strings.Contains(low, "segmentation fault"), strings.Contains(low, "sigsegv"), strings.Contains(low, "signal: segmentation"):
			return binaryErr(path, BinarySegfault, probeDetail(text, runErr), runErr)
		default:
			return binaryErr(path, BinaryVersionFail, probeDetail(text, runErr), runErr)
		}
	}
	if opts.VersionContains != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(opts.VersionContains)) {
		return binaryErr(path, BinaryVersionFail, fmt.Sprintf("--version 输出不包含 %q: %s", opts.VersionContains, probeDetail(text, nil)), nil)
	}
	if text == "" {
		return binaryErr(path, BinaryVersionFail, "--version 没有输出", nil)
	}
	return nil
}

func probeDetail(text string, err error) string {
	text = strings.TrimSpace(text)
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	if text == "" && err != nil {
		return err.Error()
	}
	if err != nil && !strings.Contains(strings.ToLower(text), strings.ToLower(err.Error())) {
		if text != "" {
			return text + ": " + err.Error()
		}
		return err.Error()
	}
	return text
}

func binaryErr(path string, kind BinaryErrorKind, detail string, err error) error {
	return &BinaryError{Path: path, Kind: kind, Detail: detail, Err: err}
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return len(p), nil
	}
	remain := b.max - b.buf.Len()
	if remain > 0 {
		if len(p) < remain {
			remain = len(p)
		}
		_, _ = b.buf.Write(p[:remain])
	}
	if len(p) > remain {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
