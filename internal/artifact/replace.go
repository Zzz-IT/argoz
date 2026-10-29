package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ErrNoBackup means no rollback file is available for the requested
// component.
var ErrNoBackup = errors.New("没有可用备份")

// ReplaceOptions controls a transactional binary replacement.
type ReplaceOptions struct {
	// BackupDir enables rollback. The previous target is copied here before
	// replacement. Leave empty only for ephemeral/test installs.
	BackupDir string
	// BackupKeep defaults to 3. Older backups are pruned after success.
	BackupKeep int
	// Validate is applied to both the staged new binary and the final target.
	// SHA256 is ignored here when empty; callers should set Format and
	// RunVersion as appropriate.
	Validate ValidateOptions
}

// ReplaceFile validates src, copies it to a temporary file beside target,
// validates that staged file, backs up the old target, and only then renames
// the new file into place. If post-install validation fails, the backup is
// restored automatically.
func ReplaceFile(ctx context.Context, src, target string, opts ReplaceOptions) (backup string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.BackupKeep <= 0 {
		opts.BackupKeep = 3
	}
	opts.Validate = normalizedValidateOptions(opts.Validate)
	newInfo, err := Inspect(ctx, src, opts.Validate)
	if err != nil {
		return "", fmt.Errorf("待安装文件校验失败: %w", err)
	}
	newSHA := newInfo.SHA256
	if opts.Validate.SHA256 == "" {
		opts.Validate.SHA256 = newSHA
	}
	absoluteSrc, _ := filepath.Abs(src)
	absoluteTarget, _ := filepath.Abs(target)
	if absoluteSrc != "" && absoluteSrc == absoluteTarget {
		return "", ValidateBinary(ctx, target, opts.Validate)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return "", err
	}
	if st, statErr := os.Stat(target); statErr == nil {
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("安装目标不是普通文件: %s", target)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("检查安装目标失败: %w", statErr)
	}
	staged, _, err := stageCopy(src, filepath.Dir(target), filepath.Base(target), 0o755)
	if err != nil {
		return "", fmt.Errorf("创建待替换文件失败: %w", err)
	}
	defer os.Remove(staged)
	if _, err := Inspect(ctx, staged, opts.Validate); err != nil {
		return "", fmt.Errorf("新文件安装前校验失败: %w", err)
	}

	oldSHA := ""
	if st, statErr := os.Stat(target); statErr == nil && st.Mode().IsRegular() && opts.BackupDir != "" {
		if err := os.MkdirAll(opts.BackupDir, 0o750); err != nil {
			return "", fmt.Errorf("创建备份目录失败: %w", err)
		}
		backup = uniqueBackupName(opts.BackupDir, filepath.Base(target))
		var copyErr error
		oldSHA, copyErr = copyFileAtomic(target, backup, 0o755)
		if copyErr != nil {
			return "", fmt.Errorf("备份旧文件失败: %w", copyErr)
		}
	}

	if err := replacePath(staged, target); err != nil {
		return backup, fmt.Errorf("原子替换 %s 失败: %w", target, err)
	}
	fsyncDir(filepath.Dir(target))

	if _, verifyErr := Inspect(ctx, target, opts.Validate); verifyErr != nil {
		if backup != "" {
			rollbackValidate := opts.Validate
			rollbackValidate.SHA256 = oldSHA
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
			restoreErr := copyFileAtomicValidated(rollbackCtx, backup, target, rollbackValidate)
			rollbackCancel()
			if restoreErr != nil {
				return backup, fmt.Errorf("新文件校验失败: %w；自动回滚也失败: %v", verifyErr, restoreErr)
			}
			return backup, fmt.Errorf("新文件校验失败，已回滚旧版本: %w", verifyErr)
		}
		_ = os.Remove(target)
		return "", fmt.Errorf("新文件校验失败，且没有可回滚备份: %w", verifyErr)
	}

	if opts.BackupDir != "" {
		pruneBackups(opts.BackupDir, opts.BackupKeep)
	}
	return backup, nil
}

// RestoreBackup validates a backup and atomically restores it to target.
func RestoreBackup(ctx context.Context, backup, target string, validate ValidateOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	validate = normalizedValidateOptions(validate)
	if _, err := Inspect(ctx, backup, validate); err != nil {
		return fmt.Errorf("备份校验失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	if err := copyFileAtomicValidated(ctx, backup, target, validate); err != nil {
		return fmt.Errorf("回滚失败: %w", err)
	}
	return nil
}

// LatestBackup returns the most recently modified regular file in dir.
func LatestBackup(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoBackup
		}
		return "", err
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		items = append(items, item{path: filepath.Join(dir, e.Name()), mod: st.ModTime()})
	}
	if len(items) == 0 {
		return "", ErrNoBackup
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].mod.Equal(items[j].mod) {
			return items[i].path > items[j].path
		}
		return items[i].mod.After(items[j].mod)
	})
	return items[0].path, nil
}

func copyFileAtomicValidated(ctx context.Context, src, target string, validate ValidateOptions) error {
	staged, _, err := stageCopy(src, filepath.Dir(target), filepath.Base(target), 0o755)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if _, err := Inspect(ctx, staged, validate); err != nil {
		return err
	}
	if err := replacePath(staged, target); err != nil {
		return err
	}
	fsyncDir(filepath.Dir(target))
	if _, err := Inspect(ctx, target, validate); err != nil {
		return err
	}
	return nil
}

func stageCopy(src, dir, base string, perm os.FileMode) (string, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, "."+sanitizeSegment(base, "artifact")+".staged-*")
	if err != nil {
		return "", "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, string, error) {
		_ = tmp.Close()
		_ = os.Remove(name)
		return "", "", err
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", "", err
	}
	return name, hex.EncodeToString(h.Sum(nil)), nil
}

// replacePath first attempts an atomic overwrite. Some platforms (notably
// Windows) refuse to rename over an existing form; in that case the old file
// is moved aside, the new file is moved in, and the old file is deleted. The
// backup copy remains the durable rollback source.
func replacePath(staged, target string) error {
	if err := os.Rename(staged, target); err == nil {
		return nil
	}
	if _, err := os.Stat(target); err != nil {
		return err
	}
	aside := fmt.Sprintf("%s.argoz-old-%d", target, time.Now().UnixNano())
	if err := os.Rename(target, aside); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(aside, target)
		return err
	}
	_ = os.Remove(aside)
	return nil
}

func pruneBackups(dir string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, err := e.Info()
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		items = append(items, item{path: filepath.Join(dir, e.Name()), mod: st.ModTime()})
	}
	if len(items) <= keep {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	for _, it := range items[keep:] {
		_ = os.Remove(it.path)
	}
}
