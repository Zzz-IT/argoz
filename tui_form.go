package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	kText fieldKind = iota
	kChoice
	kBool
)

type field struct {
	label string
	kind  fieldKind
	value string
	opts  []string
	idx   int
	hint  string
}

type formModel struct {
	title   string
	fields  []field
	focus   int
	editing bool
	err     string
	submit  func(*formModel) error
}

func newForm(title string, fields []field, submit func(*formModel) error) *formModel {
	return &formModel{title: title, fields: fields, submit: submit}
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (f *formModel) str(i int) string {
	if i < 0 || i >= len(f.fields) {
		return ""
	}
	fd := f.fields[i]
	switch fd.kind {
	case kChoice:
		if fd.idx >= 0 && fd.idx < len(fd.opts) {
			return fd.opts[fd.idx]
		}
		return ""
	case kBool:
		if fd.value == "1" {
			return "true"
		}
		return "false"
	}
	return strings.TrimSpace(fd.value)
}

func (f *formModel) cycle(d int) {
	fd := &f.fields[f.focus]
	switch fd.kind {
	case kChoice:
		if len(fd.opts) == 0 {
			return
		}
		fd.idx = (fd.idx + d + len(fd.opts)) % len(fd.opts)
	case kBool:
		if fd.value == "1" {
			fd.value = "0"
		} else {
			fd.value = "1"
		}
	}
}

func (f *formModel) doSubmit() (bool, bool) {
	f.err = ""
	if f.submit != nil {
		if err := f.submit(f); err != nil {
			f.err = err.Error()
			return false, false
		}
	}
	return true, false
}

func (f *formModel) Handle(k tea.KeyMsg) (done, cancelled bool) {
	if f.editing {
		fd := &f.fields[f.focus]
		switch k.Type {
		case tea.KeyEsc:
			f.editing = false
		case tea.KeyEnter:
			f.editing = false
			if f.focus < len(f.fields)-1 {
				f.focus++
			} else {
				return f.doSubmit()
			}
		case tea.KeyBackspace:
			r := []rune(fd.value)
			if len(r) > 0 {
				fd.value = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			fd.value += " "
		case tea.KeyRunes:
			fd.value += string(k.Runes)
		}
		return false, false
	}

	switch k.String() {
	case "esc":
		return false, true
	case "up", "k":
		if f.focus > 0 {
			f.focus--
		}
	case "down", "j", "tab":
		if f.focus < len(f.fields)-1 {
			f.focus++
		}
	case "left", "h":
		f.cycle(-1)
	case "right", "l":
		f.cycle(1)
	case " ":
		f.cycle(1)
	case "enter":
		fd := &f.fields[f.focus]
		if fd.kind == kText {
			f.editing = true
		} else if f.focus == len(f.fields)-1 {
			return f.doSubmit()
		} else {
			f.focus++
		}
	case "ctrl+s":
		return f.doSubmit()
	}
	return false, false
}

func (f *formModel) View(w int) string {
	var b strings.Builder
	for i, fd := range f.fields {
		marker := "  "
		label := stMuted.Render(padFit(fd.label, 12))
		if i == f.focus {
			marker = stKey.Render("▌ ")
			label = stTitle.Render(padFit(fd.label, 12))
		}
		b.WriteString(marker + label + f.renderValue(fd, i) + "\n")
		if fd.hint != "" && i == f.focus {
			b.WriteString("    " + stMuted.Render(fd.hint) + "\n")
		}
	}
	if f.err != "" {
		b.WriteString("\n" + stErr.Render("  "+f.err))
	}
	body := strings.TrimRight(b.String(), "\n")
	return stPanel.Width(w - 2).Render(stTitle.Render(" "+f.title) + "\n\n" + body)
}

func (f *formModel) renderValue(fd field, i int) string {
	switch fd.kind {
	case kChoice:
		var parts []string
		for j, o := range fd.opts {
			if j == fd.idx {
				parts = append(parts, stKey.Render("◉ "+o))
			} else {
				parts = append(parts, stMuted.Render("○ "+o))
			}
		}
		return strings.Join(parts, "   ")
	case kBool:
		if fd.value == "1" {
			return stOK.Render("开")
		}
		return stMuted.Render("关")
	default:
		if i == f.focus && f.editing {
			return stTitle.Render(fd.value + "▏")
		}
		if fd.value == "" {
			return stMuted.Render("（空）")
		}
		return fd.value
	}
}

// ---------- 表单构造 ----------

func (m *tuiModel) openWizard() {
	fields := []field{
		{label: "名称", kind: kText, value: fmt.Sprintf("Argo-VLESS-%d", len(m.snap.Nodes)+1)},
		{label: "协议", kind: kChoice, opts: []string{"vless", "trojan"}, idx: 0},
		{label: "隧道", kind: kChoice, opts: []string{"临时", "固定"}, idx: 0, hint: "固定隧道需填 Token 与域名"},
		{label: "Token", kind: kText, hint: "固定隧道必填，可直接粘贴整条安装命令"},
		{label: "域名", kind: kText, hint: "固定隧道必填，如 argo.example.com"},
	}
	m.form = newForm("新建节点", fields, func(f *formModel) error {
		name := f.str(0)
		if name == "" {
			return fmt.Errorf("名称不能为空")
		}
		mode := "temp"
		if f.str(2) == "固定" {
			mode = "fixed"
		}
		token, domain := f.str(3), f.str(4)
		if mode == "fixed" {
			if token == "" || domain == "" {
				return fmt.Errorf("固定隧道必须填写 Token 与域名")
			}
			token = extractToken(token)
		}
		n, err := m.eng.Create(name, f.str(1), mode, token, domain)
		if err != nil {
			return err
		}
		m.toastf("已创建 %s（端口 %d），等待域名…", n.Name, n.Port)
		return nil
	})
	m.page = pgForm
}

func (m *tuiModel) openSettings() {
	s := m.snap.Settings
	levels := []string{"error", "warn", "info", "debug"}
	lvl := 2
	for i, o := range levels {
		if o == s.LogLevel {
			lvl = i
		}
	}
	fields := []field{
		{label: "并发上限", kind: kText, value: strconv.Itoa(s.MaxConns), hint: "1-4096，每会话约 41KB"},
		{label: "保活间隔", kind: kText, value: s.Keepalive, hint: "如 30s / 1m"},
		{label: "空闲超时", kind: kText, value: s.IdleTimeout, hint: "如 5m"},
		{label: "监听地址", kind: kText, value: s.Listen},
		{label: "端口段", kind: kText, value: s.PortRange, hint: "如 20000-20100"},
		{label: "ECH 注入", kind: kBool, value: b2s(s.ECH)},
		{label: "日志级别", kind: kChoice, opts: levels, idx: lvl},
	}
	m.form = newForm("设置", fields, func(f *formModel) error {
		maxc, err := strconv.Atoi(f.str(0))
		if err != nil || maxc < 1 || maxc > 4096 {
			return fmt.Errorf("并发上限需为 1-4096")
		}
		ka, err := time.ParseDuration(f.str(1))
		if err != nil || ka < 5*time.Second {
			return fmt.Errorf("保活间隔格式非法（最小 5s）")
		}
		idle, err := time.ParseDuration(f.str(2))
		if err != nil || idle < 10*time.Second {
			return fmt.Errorf("空闲超时格式非法（最小 10s）")
		}
		ns := Settings{
			Listen:      f.str(3),
			PortRange:   f.str(4),
			MaxConns:    maxc,
			Keepalive:   ka.String(),
			IdleTimeout: idle.String(),
			ECH:         f.str(5) == "true",
			LogLevel:    f.str(6),
		}
		if _, _, err := ns.PortBounds(); err != nil {
			return err
		}
		if err := m.eng.SaveSettings(ns); err != nil {
			return err
		}
		m.toastf("设置已保存")
		return nil
	})
	m.page = pgForm
}

func (m *tuiModel) openEdit(n NodeStatus) {
	credLabel := "UUID"
	if n.Node.Protocol == "trojan" {
		credLabel = "密码"
	}
	fields := []field{
		{label: "名称", kind: kText, value: n.Node.Name},
		{label: "端口", kind: kText, value: strconv.Itoa(n.Node.Port)},
		{label: "WS 路径", kind: kText, value: n.Node.Path},
		{label: credLabel, kind: kText, value: n.Node.Credential(), hint: "留空则自动重新生成"},
	}
	id := n.Node.ID
	proto := n.Node.Protocol
	m.form = newForm("编辑节点 · "+n.Node.Name, fields, func(f *formModel) error {
		name := f.str(0)
		if name == "" {
			return fmt.Errorf("名称不能为空")
		}
		port, err := strconv.Atoi(f.str(1))
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("端口需为 1-65535")
		}
		p := f.str(2)
		if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " \t") {
			return fmt.Errorf("路径需以 / 开头且不含空白")
		}
		cred := f.str(3)
		if cred == "" {
			if proto == "trojan" {
				cred = randHex(16)
			} else {
				cred = newUUID()
			}
		}
		if proto == "vless" {
			if _, err := parseUUID(cred); err != nil {
				return fmt.Errorf("UUID 非法: %v", err)
			}
		} else if len(cred) < 4 {
			return fmt.Errorf("密码太短")
		}

		if err := m.eng.Update(id, func(x *Node) error {
			x.Name = name
			x.Port = port
			x.Path = p
			if proto == "trojan" {
				x.Password = cred
			} else {
				x.UUID = cred
			}
			return nil
		}); err != nil {
			return err
		}
		m.toastf("节点已更新，请同步客户端")
		return nil
	})
	m.page = pgForm
}

func (m *tuiModel) openUninstall() {
	fields := []field{
		{label: "确认", kind: kText, hint: "输入 DELETE 确认卸载（不可恢复）"},
		{label: "数据", kind: kChoice, opts: []string{"保留配置", "彻底删除"}, idx: 0},
		{label: "cloudflared", kind: kBool, value: "0", hint: "是否同时删除 cloudflared 二进制"},
	}
	m.form = newForm("⚠ 卸载 argoz", fields, func(fm *formModel) error {
		if strings.TrimSpace(fm.str(0)) != "DELETE" {
			return fmt.Errorf("请输入 DELETE 以确认")
		}
		m.pending = &uninstallPlan{
			KeepData:          fm.str(1) == "保留配置",
			RemoveCloudflared: fm.str(2) == "true",
		}
		return nil
	})
	m.page = pgForm
}

func (m *tuiModel) viewForm(w, h int) string {
	if m.form == nil {
		return ""
	}
	return m.form.View(w)
}
