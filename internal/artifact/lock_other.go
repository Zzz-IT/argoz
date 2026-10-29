//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package artifact

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// On platforms without flock(2), an atomically-created directory is a
// portable cross-process lock. A crashed owner is recovered after 20 minutes,
// comfortably longer than the update command's 10 minute hard timeout.
func acquireFileLock(ctx context.Context, path string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lockDir := path + ".d"
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o750); err != nil {
		return nil, err
	}
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			owner := filepath.Join(lockDir, "owner")
			_ = os.WriteFile(owner, []byte(fmt.Sprintf("pid=%d\ntime=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))), 0o600)
			return func() { _ = os.RemoveAll(lockDir) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("创建下载锁失败: %w", err)
		}
		if st, statErr := os.Stat(lockDir); statErr == nil && time.Since(st.ModTime()) > 20*time.Minute {
			_ = os.RemoveAll(lockDir)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("等待下载锁: %w", ctx.Err())
		case <-time.After(150 * time.Millisecond):
		}
	}
}
