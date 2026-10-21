package main

import (
	"os"
	"path/filepath"
	"testing"
)

func newUninstallEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: &Config{}, rt: map[string]*nodeRuntime{}, dataDir: dir, health: newHealthState()}
	return e, dir
}

func TestUninstallKeepsData(t *testing.T) {
	e, dir := newUninstallEngine(t)
	bin := filepath.Join(t.TempDir(), "argoz")
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	lines, err := e.Uninstall(uninstallPlan{KeepData: true, BinaryPath: bin, SkipService: true})
	if err != nil {
		t.Fatalf("卸载失败: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("应返回执行明细")
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatal("二进制应被删除")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal("配置应保留")
	}
}

func TestUninstallPurgesData(t *testing.T) {
	e, dir := newUninstallEngine(t)
	bin := filepath.Join(t.TempDir(), "argoz")
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Uninstall(uninstallPlan{KeepData: false, BinaryPath: bin, SkipService: true}); err != nil {
		t.Fatalf("卸载失败: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("数据目录应被删除")
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatal("二进制应被删除")
	}
}

func TestUninstallSummary(t *testing.T) {
	p := uninstallPlan{KeepData: true}
	if s := p.Summary(); !contains(s, "保留数据目录") || !contains(s, "保留 cloudflared") {
		t.Fatalf("摘要异常: %s", s)
	}
	p2 := uninstallPlan{KeepData: false, RemoveCloudflared: true}
	if s := p2.Summary(); !contains(s, "删除数据目录") || !contains(s, "删除 cloudflared") {
		t.Fatalf("摘要异常: %s", s)
	}
}
