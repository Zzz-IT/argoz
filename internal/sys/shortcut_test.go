package sys

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestShortcutPath(t *testing.T) {
	got := ShortcutPath("/usr/local/bin/argoz")
	want := filepath.Join("/usr/local/bin", "az")
	if got != want {
		t.Fatalf("快捷命令路径应为 %s，实际 %s", want, got)
	}
}

func TestEnsureShortcutOnWindowsIsNoop(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅验证 Windows 分支")
	}
	p, err := EnsureShortcut(`C:argoz.exe`)
	if err != nil || p != "" {
		t.Fatalf("Windows 下应跳过: %q %v", p, err)
	}
}
