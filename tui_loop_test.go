package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// 真正跑一遍事件循环：发一个 q 应当退出
func TestTUIProgramQuitsOnQ(t *testing.T) {
	m := &tuiModel{width: 100, height: 30, snap: sampleSnapshot()}
	var out bytes.Buffer

	p := tea.NewProgram(m,
		tea.WithInput(strings.NewReader("q")),
		tea.WithOutput(&out),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TUI 运行失败: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("按 q 后 TUI 未退出")
	}
}

// 方向键移动 + 回车进入详情
func TestTUIKeysNavigate(t *testing.T) {
	m := &tuiModel{width: 100, height: 30}
	m.snap = sampleSnapshot()

	// 直接调 Update，避免依赖 TTY
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("下移后 cursor 应为 1，实际 %d", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.page != pgDetail {
		t.Fatalf("回车后应进入详情页，实际 %v", m.page)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.page != pgList {
		t.Fatalf("esc 后应返回列表，实际 %v", m.page)
	}
}
