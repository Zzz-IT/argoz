// Package artifact provides a small, standard-library-only cache and
// transaction layer for executable downloads (argoz itself and cloudflared).
package artifact

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Format describes the file format that must be validated before an artifact
// is allowed to become visible at its cache path.
type Format string

const (
	FormatAny Format = "any"
	FormatELF Format = "elf"
)

// Spec describes one immutable artifact version.
type Spec struct {
	// Name is a stable component name, for example "argoz" or "cloudflared".
	Name string
	// Version is a cache namespace. Prefer an exact release tag; "latest" is
	// accepted for callers that intentionally follow a moving release.
	Version string
	// Asset is the remote file name and the final cache file name.
	Asset string
	// URL is the download address.
	URL string
	// SHA256, when set, is mandatory and checked while streaming to disk.
	SHA256 string
	// Size, when non-zero, must match the completed file exactly.
	Size int64
	// MinSize and MaxSize bound the accepted file size. MaxSize defaults to
	// 1 GiB when left at zero.
	MinSize int64
	MaxSize int64
	// Format defaults to FormatELF for executable artifacts.
	Format Format
}

// Options controls one Ensure operation.
type Options struct {
	Force    bool
	Offline  bool
	Cooldown time.Duration
	// Timeout bounds the whole Ensure call, including waiting for a lock.
	Timeout time.Duration
	// HTTPClient defaults to a client without an overall timeout; callers
	// should still pass a context deadline (or Timeout).
	HTTPClient *http.Client
	// ValidateVersion runs the downloaded binary with VersionArgs (default
	// --version). It defaults to true for FormatELF.
	ValidateVersion bool
	// ValidateVersionSet distinguishes an explicit false from the default.
	ValidateVersionSet bool
	VersionArgs        []string
	VersionContains    string
	VersionTimeout     time.Duration
	// InstallPath, when set, atomically installs the verified cache file to
	// this path after download. BackupDir enables rollback of an existing file.
	InstallPath string
	BackupDir   string
	BackupKeep  int
}

// Status is both the persisted state and the stable public view of an
// artifact. Ready means a verified file exists; State may still be "failed"
// when a newer download failed but the previous file remains usable.
type Status struct {
	Name          string    `json:"name"`
	Version       string    `json:"version,omitempty"`
	Asset         string    `json:"asset,omitempty"`
	State         string    `json:"state"`
	Ready         bool      `json:"ready"`
	Valid         bool      `json:"valid"`
	Path          string    `json:"path,omitempty"`
	CachePath     string    `json:"cache_path,omitempty"`
	Source        string    `json:"source,omitempty"`
	SizeBytes     int64     `json:"size_bytes,omitempty"`
	SHA256        string    `json:"sha256,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	FailureCount  int       `json:"failure_count,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	RetryAfter    time.Time `json:"retry_after,omitempty"`
	Quarantine    string    `json:"quarantine_path,omitempty"`
}

const (
	StateMissing     = "missing"
	StateDownloading = "downloading"
	StateReady       = "ready"
	StateFailed      = "failed"
	StateOffline     = "offline"
)

// BinaryErrorKind classifies failures from structural validation or the
// --version probe. Permission, format, segfault, corrupt and version failures
// are permanent for the current file and must not be retried indefinitely.
type BinaryErrorKind string

const (
	BinaryPermission  BinaryErrorKind = "permission"
	BinaryFormat      BinaryErrorKind = "exec_format"
	BinarySegfault    BinaryErrorKind = "segfault"
	BinaryCorrupt     BinaryErrorKind = "corrupt"
	BinaryVersionFail BinaryErrorKind = "version_failed"
	BinaryTimeout     BinaryErrorKind = "timeout"
	BinaryIO          BinaryErrorKind = "io"
)

// BinaryError carries actionable validation details without losing the
// original error.
type BinaryError struct {
	Path   string
	Kind   BinaryErrorKind
	Detail string
	Err    error
}

func (e *BinaryError) Error() string {
	msg := fmt.Sprintf("二进制校验失败(%s): %s", e.Kind, e.Path)
	if strings.TrimSpace(e.Detail) != "" {
		msg += ": " + strings.TrimSpace(e.Detail)
	}
	if e.Err != nil && !strings.Contains(msg, e.Err.Error()) {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *BinaryError) Unwrap() error { return e.Err }

// IsFatalBinaryError reports whether retrying the same file is pointless.
func IsFatalBinaryError(err error) bool {
	var be *BinaryError
	if !errors.As(err, &be) {
		return false
	}
	switch be.Kind {
	case BinaryPermission, BinaryFormat, BinarySegfault, BinaryCorrupt, BinaryVersionFail:
		return true
	default:
		return false
	}
}

// IsPermissionError reports whether a validation or install operation failed
// because of file permissions.
func IsPermissionError(err error) bool {
	var be *BinaryError
	if errors.As(err, &be) && be.Kind == BinaryPermission {
		return true
	}
	return errors.Is(err, os.ErrPermission)
}

// CooldownError is returned while a previous failed attempt is still cooling
// down. Err is the previous failure.
type CooldownError struct {
	Name  string
	Until time.Time
	Err   error
}

func (e *CooldownError) Error() string {
	retry := time.Until(e.Until).Round(time.Second)
	if retry < 0 {
		retry = 0
	}
	if e.Err != nil {
		return fmt.Sprintf("%s 下载失败冷却中，约 %s 后重试: %v", e.Name, retry, e.Err)
	}
	return fmt.Sprintf("%s 下载失败冷却中，约 %s 后重试", e.Name, retry)
}

func (e *CooldownError) Unwrap() error { return e.Err }

// OfflineError indicates that offline mode was requested and no verified
// cache entry was available.
type OfflineError struct {
	Name string
}

func (e *OfflineError) Error() string {
	return fmt.Sprintf("%s 离线模式：没有可用的已校验缓存", e.Name)
}

// ValidateOptions configures ValidateBinary and transaction helpers.
type ValidateOptions struct {
	Format          Format
	MinSize         int64
	MaxSize         int64
	SHA256          string
	RunVersion      bool
	VersionArgs     []string
	VersionContains string
	VersionTimeout  time.Duration
}

// Info is the result of a successful inspection.
type Info struct {
	Path       string
	Size       int64
	SHA256     string
	Executable bool
	ELF        bool
}

// Ctx returns ctx or context.Background for convenience in helper code.
func (s Spec) ctx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
