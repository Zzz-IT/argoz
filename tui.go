package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pageKind int

const (
	pgList pageKind = iota
	pgDetail
	pgForm
	pgLogs
)

type tickMsg time.Time

const (
	wSel   = 2
	wNum   = 3
	wName  = 17
	wProto = 7
	wPort  = 6
	wState = 9
	wConn  = 5
)

type tuiModel struct {
	eng  *Engine
	snap Snapshot
	page pageKind

	cursor int
	scroll int

	width  int
	height int

	form    *formModel
	logs    *logModel
	toast   string
	toastAt time.Time

	confirm    string
	confirmYes func()

	pending *uninstallPlan
}

func newTUIModel(e *Engine) *tuiModel {
	m := &tuiModel{eng: e, width: 100, height: 32}
	m.snap = e.Snapshot()
	return m
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *tuiModel) refresh() {
	m.snap = m.eng.Snapshot()
	if m.cursor >= len(m.snap.Nodes) {
		m.cursor = max(0, len(m.snap.Nodes)-1)
	}
	if m.page == pgLogs && m.logs != nil {
		m.logs.reload(m.eng)
	}
}

func (m *tuiModel) toastf(f string, a ...any) {
	m.toast = fmt.Sprintf(f, a...)
	m.toastAt = time.Now()
}

func (m *tuiModel) current() (NodeStatus, bool) {
	if m.cursor < 0 || m.cursor >= len(m.snap.Nodes) {
		return NodeStatus{}, false
	}
	return m.snap.Nodes[m.cursor], true
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		m.refresh()
		return m, tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })

	case tea.KeyMsg:
		if m.confirm != "" {
			switch strings.ToLower(msg.String()) {
			case "y":
				if m.confirmYes != nil {
					m.confirmYes()
				}
				m.confirm, m.confirmYes = "", nil
				m.refresh()
			case "n", "esc":
				m.confirm, m.confirmYes = "", nil
			}
			return m, nil
		}

		switch m.page {
		case pgList:
			return m.updateList(msg)
		case pgDetail:
			return m.updateDetail(msg)
		case pgForm:
			return m.updateForm(msg)
		case pgLogs:
			return m.updateLogs(msg)
		}
	}
	return m, nil
}

func (m *tuiModel) updateList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.snap.Nodes)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(0, len(m.snap.Nodes)-1)
	case "enter":
		if _, ok := m.current(); ok {
			m.page = pgDetail
			m.scroll = 0
		}
	case "n":
		m.openWizard()
	case "s":
		m.openSettings()
	case "l":
		m.logs = &logModel{}
		m.logs.reload(m.eng)
		m.page = pgLogs
	case "r":
		if n, ok := m.current(); ok {
			if err := m.eng.RestartTunnel(n.Node.ID); err != nil {
				m.toastf("重启失败: %v", err)
			} else {
				m.toastf("正在重启隧道: %s", n.Node.Name)
			}
			m.refresh()
		}
	case "u":
		m.openUninstall()
	case "d":
		if n, ok := m.current(); ok {
			id, name := n.Node.ID, n.Node.Name
			m.confirm = fmt.Sprintf("确认删除节点 [%s] ?", name)
			m.confirmYes = func() {
				if err := m.eng.Delete(id); err != nil {
					m.toastf("删除失败: %v", err)
				} else {
					m.toastf("已删除: %s", name)
				}
			}
		}
	}
	return m, nil
}

func (m *tuiModel) updateForm(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		m.page = pgList
		return m, nil
	}
	done, cancelled := m.form.Handle(k)
	if cancelled {
		m.form = nil
		m.page = pgList
		return m, nil
	}
	if done {
		m.form = nil
		m.page = pgList
		if m.pending != nil {
			return m, tea.Quit
		}
		m.refresh()
	}
	return m, nil
}

func (m *tuiModel) updateLogs(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.page = pgList
	case "j", "down":
		if m.logs != nil {
			m.logs.scroll++
		}
	case "k", "up":
		if m.logs != nil && m.logs.scroll > 0 {
			m.logs.scroll--
		}
	}
	return m, nil
}

// ---------- 视图 ----------

func (m *tuiModel) View() string {
	w := max(60, m.width)

	header := m.viewHeader(w)
	footer := m.viewFooter(w)

	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if bodyH < 8 {
		bodyH = 8
	}

	var body string
	switch m.page {
	case pgDetail:
		body = m.viewDetail(w, bodyH)
	case pgForm:
		body = m.viewForm(w, bodyH)
	case pgLogs:
		body = m.viewLogs(w, bodyH)
	default:
		body = m.viewList(w, bodyH)
	}

	out := header + "\n" + body + "\n" + footer

	if m.confirm != "" {
		out += "\n" + stErr.Render("  "+m.confirm+"   [y/N]")
	} else if m.toast != "" && time.Since(m.toastAt) < 4*time.Second {
		out += "\n" + stWarn.Render("  "+m.toast)
	}
	return out
}

func (m *tuiModel) viewHeader(w int) string {
	up := 0
	for _, n := range m.snap.Nodes {
		if n.State == "up" {
			up++
		}
	}
	left := stTitle.Render("argoz " + m.snap.Version)
	right := fmt.Sprintf("%s   节点 %d · 运行 %d   连接 %d   ↑%s ↓%s   %s",
		stOK.Render("● running"),
		len(m.snap.Nodes), up, m.snap.Conns,
		humanBytes(m.snap.Up), humanBytes(m.snap.Down),
		stMuted.Render("up "+humanDuration(time.Since(m.snap.Started))),
	)
	gap := w - 4 - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return stBox.Width(w - 2).Render(left + spaces(gap) + right)
}

func (m *tuiModel) viewFooter(w int) string {
	var bar string
	switch m.page {
	case pgDetail:
		bar = keyBar(
			[2]string{"c", "复制链接"}, [2]string{"y", "复制 YAML"}, [2]string{"w", "写入文件"},
			[2]string{"e", "编辑"}, [2]string{"r", "重启隧道"}, [2]string{"d", "删除"},
			[2]string{"esc", "返回"},
		)
	case pgForm:
		bar = keyBar(
			[2]string{"↑↓", "切换"}, [2]string{"←→", "选择"},
			[2]string{"enter", "编辑/确认"}, [2]string{"ctrl+s", "保存"}, [2]string{"esc", "取消"},
		)
	case pgLogs:
		bar = keyBar([2]string{"j/k", "滚动"}, [2]string{"esc", "返回"})
	default:
		bar = keyBar(
			[2]string{"j/k", "选择"}, [2]string{"enter", "详情"}, [2]string{"n", "新建"},
			[2]string{"r", "重启隧道"}, [2]string{"d", "删除"}, [2]string{"s", "设置"},
			[2]string{"l", "日志"}, [2]string{"u", "卸载"}, [2]string{"q", "退出"},
		)
	}
	return " " + bar
}

func domainWidth(innerW int) int {
	return max(10, innerW-(wSel+wNum+wName+wProto+wPort+wState+wConn)-7)
}

func (m *tuiModel) viewList(w, h int) string {
	innerW := w - 4
	dw := domainWidth(innerW)

	var b strings.Builder
	b.WriteString(stMuted.Render(
		spaces(wSel) + padFit("#", wNum) + " " +
			padFit("名称", wName) + " " + padFit("协议", wProto) + " " +
			padLeftFit("端口", wPort) + " " + padFit("域名", dw) + " " +
			padFit("状态", wState) + " " + padLeftFit("连接", wConn)))
	b.WriteString("\n")

	rows := h - 4
	if rows < 3 {
		rows = 3
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := min(len(m.snap.Nodes), start+rows)

	if len(m.snap.Nodes) == 0 {
		b.WriteString("\n" + stMuted.Render("  还没有节点，按 n 新建一个。") + "\n")
	} else {
		for i := start; i < end; i++ {
			b.WriteString(m.row(m.snap.Nodes[i], i, dw, innerW))
			b.WriteString("\n")
		}
	}

	if n, ok := m.current(); ok {
		extra := ""
		if n.Err != "" {
			extra = truncate(n.Err, innerW-30)
		}
		if !n.NextRetry.IsZero() {
			if d := time.Until(n.NextRetry); d > 0 {
				extra += "  [" + humanDuration(d) + " 后重试]"
			}
		}
		if extra != "" {
			b.WriteString(stWarn.Render("  " + extra))
		}
	}

	return stPanel.Width(w - 2).Render(strings.TrimRight(b.String(), "\n"))
}

func (m *tuiModel) row(n NodeStatus, i int, dw, innerW int) string {
	selected := i == m.cursor
	sel := "  "
	if selected {
		sel = "▌ "
	}
	num := padLeftFit(strconv.Itoa(i+1), wNum)
	name := padFit(n.Node.Name, wName)
	proto := padFit(n.Node.Protocol, wProto)
	port := padLeftFit(strconv.Itoa(n.Node.Port), wPort)
	domain := n.Node.Tunnel.Domain
	if domain == "" {
		domain = "(未分配)"
	}
	domain = padFit(domain, dw)
	state := padFit(stateText(n.State), wState)
	conns := padLeftFit(strconv.Itoa(int(n.Conns)), wConn)

	if selected {
		plain := sel + num + " " + name + " " + proto + " " + port + " " + domain + " " + state + " " + conns
		return stSel.Render(padFit(plain, innerW))
	}
	return sel + num + " " + name + " " + proto + " " + port + " " + domain + " " +
		stateStyled(n.State) + spaces(wState-lipgloss.Width(stateText(n.State))) + " " + conns
}

// runTUI 启动全屏界面（8 FPS，降低 CPU 抖动）
// 若用户在界面里确认卸载，返回卸载方案
func runTUI(e *Engine) (*uninstallPlan, error) {
	m := newTUIModel(e)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithFPS(8))
	if _, err := p.Run(); err != nil {
		return nil, err
	}
	return m.pending, nil
}
