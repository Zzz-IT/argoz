package sys

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// shortcutName 面板快捷命令；与 argoz 同目录，直接敲 az 就能唤出面板
const shortcutName = "az"

// shortcutPath 返回快捷命令路径
func ShortcutPath(bin string) string {
	return filepath.Join(filepath.Dir(bin), shortcutName)
}

// ensureShortcut 创建 az -> argoz 的符号链接
func EnsureShortcut(bin string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	p := ShortcutPath(bin)

	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("%s 已存在且不是符号链接，已跳过", p)
		}
		if target, err := os.Readlink(p); err == nil && target == bin {
			return p, nil
		}
		_ = os.Remove(p)
	}

	if err := os.Symlink(bin, p); err != nil {
		return "", fmt.Errorf("创建快捷命令失败: %w", err)
	}
	return p, nil
}

// removeShortcut 删除指向 bin 的 az 链接
func RemoveShortcut(bin string) bool {
	p := ShortcutPath(bin)
	target, err := os.Readlink(p)
	if err != nil || target != bin {
		return false
	}
	return os.Remove(p) == nil
}

// removeShortcutAny 删除指向 argoz 的 az 链接（卸载时不知道原路径也能清）
func RemoveShortcutAny() bool {
	for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/bin"} {
		p := filepath.Join(dir, shortcutName)
		target, err := os.Readlink(p)
		if err != nil {
			continue
		}
		if filepath.Base(target) == "argoz" {
			if os.Remove(p) == nil {
				return true
			}
		}
	}
	return false
}
