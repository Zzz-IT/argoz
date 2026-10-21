package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *tuiModel) updateDetail(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n, ok := m.current()
	if !ok {
		m.page = pgList
		return m, nil
	}
	s := m.snap.Settings

	switch k.String() {
	case "esc", "q":
		m.page = pgList
	case "j", "down":
		m.scroll++
	case "k", "up":
		if m.scroll > 0 {
			m.scroll--
		}
	case "c":
		if uri := n.Node.URI(s); uri != "" {
			copyClipboard(uri)
			m.toastf("链接已复制到剪贴板（OSC52）")
		} else {
			m.toastf("域名尚未就绪")
		}
	case "y":
		if y := n.Node.YAML(s); y != "" {
			copyClipboard(y)
			m.toastf("YAML 已复制到剪贴板")
		} else {
			m.toastf("域名尚未就绪")
		}
	case "w":
		body := strings.Join([]string{n.Node.URI(s), "", strings.TrimRight(n.Node.YAML(s), "\n")}, "\n")
		if strings.TrimSpace(body) == "" {
			m.toastf("域名尚未就绪，暂无可写入内容")
			break
		}
		if p, err := exportToFile(m.eng.dataDir, n.Node.Name, body); err != nil {
			m.toastf("写入失败: %v", err)
		} else {
			m.toastf("已写入 %s", p)
		}
	case "e":
		m.openEdit(n)
	case "r":
		if err := m.eng.RestartTunnel(n.Node.ID); err != nil {
			m.toastf("重启失败: %v", err)
		} else {
			m.toastf("隧道重启中…")
		}
	case "d":
		id, name := n.Node.ID, n.Node.Name
		m.confirm = fmt.Sprintf("确认删除节点 [%s] ?", name)
		m.confirmYes = func() {
			if err := m.eng.Delete(id); err != nil {
				m.toastf("删除失败: %v", err)
			} else {
				m.toastf("已删除: %s", name)
				m.page = pgList
			}
		}
	}
	return m, nil
}

func wrapText(s string, w int) []string {
	if w <= 0 || len(s) == 0 {
		return []string{s}
	}
	var out []string
	r := []rune(s)
	for len(r) > w {
		out = append(out, string(r[:w]))
		r = r[w:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

func (m *tuiModel) viewDetail(w, h int) string {
	n, ok := m.current()
	if !ok {
		m.page = pgList
		return ""
	}
	s := m.snap.Settings
	innerW := w - 8
	var lines []string

	lines = append(lines, stMuted.Render(fmt.Sprintf("%s · %s:%d", n.Node.Protocol, s.Listen, n.Node.Port))+"   "+stateStyled(n.State))
	lines = append(lines, stMuted.Render("路径 "+n.Node.Path+"    隧道 "+n.Node.Tunnel.Mode+" "+orDash(n.Node.Tunnel.Domain)))
	lines = append(lines, stMuted.Render(fmt.Sprintf("流量 ↑%s ↓%s    连接 %d    重启 %d 次",
		humanBytes(n.Up), humanBytes(n.Down), n.Conns, n.Restarts)))
	if n.Err != "" {
		for _, l := range wrapText("错误: "+n.Err, innerW) {
			lines = append(lines, stErr.Render(l))
		}
	}
	if !n.NextRetry.IsZero() {
		if d := time.Until(n.NextRetry); d > 0 {
			lines = append(lines, stWarn.Render(humanDuration(d)+" 后重试"))
		}
	}

	lines = append(lines, "")
	lines = append(lines, stTitle.Render("分享链接"))
	if uri := n.Node.URI(s); uri == "" {
		lines = append(lines, stWarn.Render("域名尚未就绪"))
	} else {
		for _, l := range wrapText(uri, innerW) {
			lines = append(lines, l)
		}
	}

	lines = append(lines, "")
	lines = append(lines, stTitle.Render("Clash / Mihomo"))
	if y := n.Node.YAML(s); y == "" {
		lines = append(lines, stWarn.Render("域名尚未就绪"))
	} else {
		for _, l := range strings.Split(strings.TrimRight(y, "\n"), "\n") {
			lines = append(lines, l)
		}
	}

	maxLines := h - 4
	if maxLines < 4 {
		maxLines = 4
	}
	maxScroll := max(0, len(lines)-maxLines)
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	end := min(len(lines), m.scroll+maxLines)

	body := strings.Join(lines[m.scroll:end], "\n")
	head := stTitle.Render(" "+n.Node.Name) + spaces(max(0, w-8-len([]rune(n.Node.Name))))
	if m.scroll > 0 {
		head += stMuted.Render(" ↑")
	}
	return stPanel.Width(w - 2).Render(head + "\n" + body)
}
