package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func renderAt(t *testing.T, w, h int, page pageKind) string {
	t.Helper()
	m := &tuiModel{width: w, height: h, snap: sampleSnapshot(), page: page}
	m.cursor = 0
	return m.View()
}

func TestLayoutNoOverflow(t *testing.T) {
	for _, w := range []int{50, 60, 70, 80, 90, 100, 120, 160} {
		for _, page := range []pageKind{pgList, pgDetail, pgLogs} {
			out := renderAt(t, w, 30, page)
			for i, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Fatalf("宽度 %d 页面 %d 第 %d 行超宽: %d\n%s", w, page, i, got, out)
				}
			}
		}
	}
}

func TestHeaderNotWrapped(t *testing.T) {
	for _, w := range []int{60, 80, 100} {
		m := &tuiModel{width: w, height: 30, snap: sampleSnapshot()}
		header := m.viewHeader(w)
		if n := len(strings.Split(header, "\n")); n != 3 {
			t.Fatalf("宽度 %d 时页头应为 3 行（上下边框+内容），实际 %d 行:\n%s", w, n, header)
		}
	}
}

func TestFooterFits(t *testing.T) {
	for _, w := range []int{50, 60, 80, 100} {
		m := &tuiModel{width: w, height: 30, snap: sampleSnapshot()}
		f := m.viewFooter(w)
		if got := lipgloss.Width(f); got > w {
			t.Fatalf("宽度 %d 时快捷键条超宽: %d (%q)", w, got, f)
		}
	}
}

func TestColumnsAdaptive(t *testing.T) {
	wide := computeListCols(90)
	if !wide.showDomain || !wide.showProto || !wide.showPort {
		t.Fatal("宽屏应显示全部列")
	}
	narrow := computeListCols(46)
	if narrow.showDomain || narrow.showProto || narrow.showPort {
		t.Fatal("窄屏应隐藏可选列")
	}
	if narrow.nameW <= 16 {
		t.Fatalf("窄屏应把富余宽度给名称列，实际 %d", narrow.nameW)
	}
}

func TestListRendersRetryHint(t *testing.T) {
	m := &tuiModel{width: 90, height: 30, snap: sampleSnapshot(), cursor: 2}
	out := m.View()
	if !strings.Contains(out, "后重试") {
		t.Fatalf("列表应显示重试提示:\n%s", out)
	}
	if strings.Count(out, "Argo-VLESS-1") != 1 {
		t.Fatal("节点行重复渲染")
	}
}

var _ = time.Second
