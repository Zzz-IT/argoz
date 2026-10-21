package main

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	cPrimary = lipgloss.Color("81")
	cSuccess = lipgloss.Color("42")
	cWarn    = lipgloss.Color("214")
	cDanger  = lipgloss.Color("203")
	cMuted   = lipgloss.Color("244")
	cBorder  = lipgloss.Color("238")
	cSelBg   = lipgloss.Color("236")
)

var (
	stTitle = lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	stMuted = lipgloss.NewStyle().Foreground(cMuted)
	stOK    = lipgloss.NewStyle().Foreground(cSuccess)
	stWarn  = lipgloss.NewStyle().Foreground(cWarn)
	stErr   = lipgloss.NewStyle().Foreground(cDanger)
	stKey   = lipgloss.NewStyle().Foreground(cPrimary)
	stSel   = lipgloss.NewStyle().Background(cSelBg).Bold(true)
	stBox   = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
	stPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
)

// stateText 状态文本（不带颜色）
func stateText(s string) string {
	switch s {
	case "up":
		return "● up"
	case "err":
		return "✖ err"
	case "degraded":
		return "◐ 降级"
	case "starting":
		return "◌ init"
	default:
		return "○ down"
	}
}

// stateStyled 状态文本（带颜色）
func stateStyled(s string) string {
	switch s {
	case "up":
		return stOK.Render(stateText(s))
	case "err":
		return stErr.Render(stateText(s))
	case "degraded", "starting":
		return stWarn.Render(stateText(s))
	default:
		return stMuted.Render(stateText(s))
	}
}

// fit 按显示宽度截断（带省略号）
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// padFit 左对齐并补齐到宽度
func padFit(s string, w int) string {
	s = fit(s, w)
	d := lipgloss.Width(s)
	if d >= w {
		return s
	}
	return s + spaces(w-d)
}

// padLeftFit 右对齐并补齐到宽度
func padLeftFit(s string, w int) string {
	s = fit(s, w)
	d := lipgloss.Width(s)
	if d >= w {
		return s
	}
	return spaces(w-d) + s
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// keyBar 渲染底部快捷键条
func keyBar(pairs ...[2]string) string {
	out := ""
	for i, p := range pairs {
		if i > 0 {
			out += "   "
		}
		out += stKey.Render(p[0]) + " " + stMuted.Render(p[1])
	}
	return out
}

func titleBar(title string, width int) string {
	left := stTitle.Render(title)
	return left + spaces(max(0, width-lipgloss.Width(left)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
