package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func sampleSnapshot() Snapshot {
	return Snapshot{
		Version:  "v1.0.0",
		Settings: defaultSettings(),
		Nodes: []NodeStatus{
			{
				Node:  Node{ID: "n1", Name: "Argo-VLESS-1", Protocol: "vless", Port: 20001, Path: "/a1b2c3d4e5f6", UUID: "8f3a1c2b-1111-4222-8333-444455556666", Tunnel: TunnelCfg{Mode: "temp", Domain: "xxx-yyy.trycloudflare.com"}},
				State: "up", Conns: 12, Up: 1 << 30, Down: 8 << 30,
			},
			{
				Node:  Node{ID: "n2", Name: "Argo-Trojan-1", Protocol: "trojan", Port: 20002, Path: "/9f8e7d6c5b4a", Password: "aabbccddeeff00112233", Tunnel: TunnelCfg{Mode: "fixed", Domain: "argo.example.com"}},
				State: "up", Conns: 3,
			},
			{
				Node:  Node{ID: "n3", Name: "Argo-VLESS-2", Protocol: "vless", Port: 20003, Path: "/q1w2e3r4t5y6", UUID: "11111111-2222-4333-8444-555555555555", Tunnel: TunnelCfg{Mode: "temp"}},
				State: "degraded", Err: "隧道不存在 (HTTP 1033)", NextRetry: time.Now().Add(68 * time.Minute), Restarts: 3,
			},
		},
		Conns: 15, Up: 1 << 30, Down: 8 << 30, Started: time.Now().Add(-2 * time.Hour),
	}
}

func TestTUIListRender(t *testing.T) {
	m := &tuiModel{width: 100, height: 26, snap: sampleSnapshot(), cursor: 2}
	out := m.View()
	t.Log("\n" + out)

	if !strings.Contains(out, "argoz v1.0.0") {
		t.Fatal("缺少标题")
	}
	if !strings.Contains(out, "Argo-VLESS-1") || !strings.Contains(out, "Argo-Trojan-1") {
		t.Fatal("节点未渲染")
	}
	if !strings.Contains(out, "后重试") {
		t.Fatal("缺少重试提示")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("第 %d 行超宽: %d", i, w)
		}
	}
}

func TestTUIDetailRender(t *testing.T) {
	m := &tuiModel{width: 100, height: 30, snap: sampleSnapshot(), page: pgDetail}
	out := m.View()
	t.Log("\n" + out)

	if !strings.Contains(out, "分享链接") || !strings.Contains(out, "Clash / Mihomo") {
		t.Fatal("详情页缺少链接/YAML 区块")
	}
	if !strings.Contains(out, "vless://") {
		t.Fatal("详情页缺少分享链接内容")
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Fatalf("第 %d 行超宽: %d", i, w)
		}
	}
}

func TestTUIFormRender(t *testing.T) {
	m := &tuiModel{width: 80, height: 24, snap: sampleSnapshot()}
	m.openWizard()
	out := m.View()
	t.Log("\n" + out)

	if !strings.Contains(out, "新建节点") || !strings.Contains(out, "协议") {
		t.Fatal("向导渲染异常")
	}
	if m.page != pgForm {
		t.Fatal("应切换到表单页")
	}
}

func TestFormCycleAndEdit(t *testing.T) {
	f := newForm("t", []field{
		{label: "名称", kind: kText, value: "abc"},
		{label: "协议", kind: kChoice, opts: []string{"a", "b"}, idx: 0},
	}, func(*formModel) error { return nil })

	if f.str(1) != "a" {
		t.Fatal("初始选项应为 a")
	}
	f.focus = 1
	f.cycle(1)
	if f.str(1) != "b" {
		t.Fatal("切换后应为 b")
	}
	if _, cancelled := f.Handle(keyMsg("esc")); !cancelled {
		t.Fatal("esc 应取消")
	}
}

func TestEmptyStateRender(t *testing.T) {
	m := &tuiModel{width: 80, height: 20, snap: Snapshot{Version: "v1.0.0", Settings: defaultSettings(), Started: time.Now()}}
	out := m.View()
	if !strings.Contains(out, "还没有节点") {
		t.Fatalf("空状态提示缺失:\n%s", out)
	}
}

func TestUninstallFormGuard(t *testing.T) {
	m := &tuiModel{width: 90, height: 24}
	m.snap = sampleSnapshot()
	m.openUninstall()

	t.Log("\n" + m.View())
	if !contains(m.View(), "卸载") {
		t.Fatal("卸载表单未渲染")
	}

	// 错误确认词：应拒绝并保持表单
	m.form.fields[0].value = "delete"
	if done, _ := m.form.doSubmit(); done {
		t.Fatal("确认词错误时不应提交")
	}
	if m.pending != nil {
		t.Fatal("错误确认词不应产生卸载计划")
	}

	// 正确确认词：生成计划
	m.form.fields[0].value = "DELETE"
	m.form.fields[1].idx = 1 // 彻底删除
	if done, _ := m.form.doSubmit(); !done {
		t.Fatalf("正确确认词应提交，错误: %s", m.form.err)
	}
	if m.pending == nil || m.pending.KeepData {
		t.Fatal("卸载计划异常")
	}
}
