package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug": LevelDebug, "DEBUG": LevelDebug,
		"info": LevelInfo, "": LevelInfo, "whatever": LevelInfo,
		"warn": LevelWarn, "warning": LevelWarn,
		"error": LevelError,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Fatalf("ParseLevel(%q)=%v，期望 %v", in, got, want)
		}
	}
}

func TestLogLevelFiltering(t *testing.T) {
	dir := t.TempDir()
	if err := InitLog(dir, "error"); err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	t.Cleanup(func() {
		CloseLog()
		SetLevel(LevelInfo)
		SetConsoleLevel(LevelInfo)
	})
	SetConsoleLevel(LevelError) // 静音终端输出

	LogInfo("hidden-info")
	LogWarn("hidden-warn")
	LogErr("visible-error")

	p := filepath.Join(dir, "argoz.log")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "hidden-info") || strings.Contains(s, "hidden-warn") {
		t.Fatalf("error 等级下不应写入 info/warn:\n%s", s)
	}
	if !strings.Contains(s, "visible-error") {
		t.Fatalf("error 等级下应写入 error:\n%s", s)
	}

	// 调到 debug 后应全部写入
	SetLevelBundle("debug")
	LogDebug("now-debug")
	LogInfo("now-info")
	data, _ = os.ReadFile(p)
	s = string(data)
	if !strings.Contains(s, "now-debug") || !strings.Contains(s, "now-info") {
		t.Fatalf("debug 等级下应写入全部日志:\n%s", s)
	}
	if !strings.Contains(s, "["+"调试"+"]") {
		t.Fatal("日志格式缺少标签")
	}
}

// SetLevelBundle 模拟设置项保存时的联动
func SetLevelBundle(name string) {
	SetLevelByName(name)
}

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line-")
		b.WriteString(strings.Repeat("x", 1))
		b.WriteString(strings.TrimSpace(strings.Repeat(" ", 0)))
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	lines, err := tailLines(p, 10)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if len(lines) != 10 {
		t.Fatalf("应返回 10 行，实际 %d", len(lines))
	}
	if lines[9] != "line-x99" {
		t.Fatalf("最后一行错误: %q", lines[9])
	}

	if _, err := tailLines(filepath.Join(dir, "missing.log"), 5); err == nil {
		t.Fatal("不存在的文件应报错")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
