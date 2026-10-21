package main

import (
	"strconv"
	"strings"
)

type logModel struct {
	lines  []string
	scroll int
}

func (l *logModel) reload(e *Engine) {
	lines, err := e.TailLog(300)
	if err != nil {
		l.lines = []string{"读取日志失败: " + err.Error()}
		return
	}
	l.lines = lines
}

func (m *tuiModel) viewLogs(w, h int) string {
	if m.logs == nil {
		return ""
	}
	innerW := w - 6
	maxLines := h - 4
	if maxLines < 3 {
		maxLines = 3
	}
	maxScroll := max(0, len(m.logs.lines)-maxLines)
	if m.logs.scroll > maxScroll {
		m.logs.scroll = maxScroll
	}
	end := min(len(m.logs.lines), m.logs.scroll+maxLines)

	var b strings.Builder
	for _, l := range m.logs.lines[m.logs.scroll:end] {
		b.WriteString(fit(l, innerW))
		b.WriteString("\n")
	}
	if len(m.logs.lines) == 0 {
		b.WriteString(stMuted.Render("（暂无日志）"))
	}

	head := stTitle.Render(" 运行日志") + "  " + stMuted.Render(LogFilePath())
	info := stMuted.Render("  " + strconv.Itoa(len(m.logs.lines)) + " 行 · j/k 滚动")
	return stPanel.Width(w - 2).Render(head + "\n" + info + "\n\n" + strings.TrimRight(b.String(), "\n"))
}
