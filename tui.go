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
	case "u":
		m.openUninstall()
	case "r":
		if n, ok := m.current(); ok {
			if err := m.eng.RestartTunnel(n.Node.ID); err != nil {
				m.toastf("重启失败: %v", err)
			} else {
				m.toastf("正在重启隧道: %s", n.Node.Name)
			}
			m.refresh()
		}
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

// ---------- 布局计算 ----------

// listCols 列表列宽；窗口越窄，可选列越少
type listCols struct {
	nameW      int
	domainW    int
	showProto  bool
	showPort   bool
	showDomain bool
}

func computeListCols(innerW int) listCols {
	c := listCols{nameW: 16}
	c.showProto = innerW >= 64
	c.showPort = innerW >= 52
	c.showDomain = innerW >= 76

	// sel(2) + num(3) + name + state(8) + conn(5) + 4 个分隔空格
	used := 22 + c.nameW
	if c.showProto {
		used += 8
	}
	if c.showPort {
		used += 7
	}
	if c.showDomain {
		c.domainW = innerW - used - 1
		if c.domainW < 12 {
			c.showDomain = false
		}
	}
	if !c.showDomain {
		if extra := innerW - used; extra > 0 {
			c.nameW += extra
		}
	}
	return c
}

func (c listCols) header() string {
	parts := []string{spaces(2), padFit("#", 3), padFit("名称", c.nameW)}
	if c.showProto {
		parts = append(parts, padFit("协议", 7))
	}
	if c.showPort {
		parts = append(parts, padLeftFit("端口", 6))
	}
	if c.showDomain {
		parts = append(parts, padFit("域名", c.domainW))
	}
	parts = append(parts, padFit("状态", 8), padLeftFit("连接", 5))
	return strings.Join(parts, " ")
}

// ---------- 视图 ----------

func (m *tuiModel) View() string {
	w := max(40, m.width)

	header := m.viewHeader(w)
	footer := m.viewFooter(w)

	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if bodyH < 6 {
		bodyH = 6
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
		out += "\n" + stErr.Render(fit("  "+m.confirm+"   [y/N]", w))
	} else if m.toast != "" && time.Since(m.toastAt) < 4*time.Second {
		out += "\n" + stWarn.Render(fit("  "+m.toast, w))
	}

	// 兜底：任何一行超宽都会被终端折行，这里强制裁剪
	return lipgloss.NewStyle().MaxWidth(w).Render(out)
}

func (m *tuiModel) viewHeader(w int) string {
	up := 0
	for _, n := range m.snap.Nodes {
		if n.State == "up" {
			up++
		}
	}

	inner := w - 4
	segs := []string{
		stTitle.Render("argoz " + m.snap.Version),
		stOK.Render("● running"),
		stMuted.Render(fmt.Sprintf("节点 %d · 运行 %d", len(m.snap.Nodes), up)),
		stMuted.Render(fmt.Sprintf("连接 %d", m.snap.Conns)),
		stMuted.Render(fmt.Sprintf("↑%s ↓%s", humanBytes(m.snap.Up), humanBytes(m.snap.Down))),
		stMuted.Render("up " + humanDuration(time.Since(m.snap.Started))),
	}

	line := segs[0]
	for _, s := range segs[1:] {
		next := line + "   " + s
		if lipgloss.Width(next) > inner {
			break
		}
		line = next
	}
	return stBox.Width(w - 2).Render(line)
}

func (m *tuiModel) viewFooter(w int) string {
	type kv [2]string
	var items []kv

	switch m.page {
	case pgDetail:
		items = []kv{{"esc", "返回"}, {"c", "复制链接"}, {"y", "复制 YAML"}, {"w", "写入文件"}, {"e", "编辑"}, {"r", "重启"}, {"d", "删除"}, {"q", "退出"}}
	case pgForm:
		items = []kv{{"esc", "取消"}, {"↑↓", "切换"}, {"←→", "选择"}, {"enter", "编辑/确认"}, {"ctrl+s", "保存"}}
	case pgLogs:
		items = []kv{{"esc", "返回"}, {"j/k", "滚动"}}
	default:
		items = []kv{{"q", "退出"}, {"j/k", "选择"}, {"enter", "详情"}, {"n", "新建"}, {"r", "重启"}, {"d", "删除"}, {"s", "设置"}, {"l", "日志"}, {"u", "卸载"}}
	}

	line := ""
	for _, it := range items {
		seg := stKey.Render(it[0]) + " " + stMuted.Render(it[1])
		cand := seg
		if line != "" {
			cand = line + "   " + seg
		}
		if lipgloss.Width(cand) > w-2 {
			break
		}
		line = cand
	}
	return " " + line
}

func (m *tuiModel) viewList(w, h int) string {
	innerW := w - 4
	cols := computeListCols(innerW)

	var b strings.Builder
	b.WriteString(stMuted.Render(cols.header()))
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
			b.WriteString(m.row(m.snap.Nodes[i], i, cols, innerW))
			b.WriteString("\n")
		}
	}

	if n, ok := m.current(); ok {
		extra := ""
		if n.Err != "" {
			extra = n.Err
		}
		if !n.NextRetry.IsZero() {
			if d := time.Until(n.NextRetry); d > 0 {
				extra += "  [" + humanDuration(d) + " 后重试]"
			}
		}
		if extra != "" {
			b.WriteString(stWarn.Render(fit("  "+extra, innerW)))
		}
	}

	return stPanel.Width(w - 2).Render(strings.TrimRight(b.String(), "\n"))
}

func (m *tuiModel) row(n NodeStatus, i int, c listCols, innerW int) string {
	selected := i == m.cursor
	sel := "  "
	if selected {
		sel = "▌ "
	}

	parts := []string{
		sel,
		padLeftFit(strconv.Itoa(i+1), 3),
		padFit(n.Node.Name, c.nameW),
	}
	if c.showProto {
		parts = append(parts, padFit(n.Node.Protocol, 7))
	}
	if c.showPort {
		parts = append(parts, padLeftFit(strconv.Itoa(n.Node.Port), 6))
	}
	if c.showDomain {
		d := n.Node.Tunnel.Domain
		if d == "" {
			d = "(未分配)"
		}
		parts = append(parts, padFit(d, c.domainW))
	}

	head := strings.Join(parts, " ")
	state := stateText(n.State)
	conns := padLeftFit(strconv.Itoa(int(n.Conns)), 5)

	if selected {
		plain := head + " " + padFit(state, 8) + " " + conns
		return stSel.Render(padFit(plain, innerW))
	}
	return head + " " + stateStyled(n.State) + spaces(8-lipgloss.Width(state)) + " " + conns
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
