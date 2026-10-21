package panel

import (
	"fmt"
	"strings"
	"time"
)

// stateText 把内部状态映射为中文显示（存储值不变）
func stateText(state string) (string, string) {
	switch state {
	case "up":
		return "● 运行中", ColorGreen
	case "starting":
		return "◌ 启动中", ColorYellow
	case "degraded":
		return "◐ 降级", ColorYellow
	case "err":
		return "✖ 错误", ColorRed
	default:
		return "○ 未运行", ColorYellow
	}
}

// modeText 隧道模式
func modeText(mode string) string {
	switch mode {
	case "fixed":
		return "固定隧道"
	case "temp":
		return "临时隧道"
	}
	return mode
}

// levelText 日志级别
func levelText(level string) string {
	switch strings.ToLower(level) {
	case "error":
		return "错误"
	case "warn", "warning":
		return "警告"
	case "info":
		return "信息"
	case "debug":
		return "调试"
	}
	return level
}

// boolText 开关
func boolText(b bool) string {
	if b {
		return "开启"
	}
	return "关闭"
}

// durationText 时长中文化
func durationText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return "刚刚"
	case d < time.Minute:
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	case d < time.Hour:
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		if s == 0 {
			return fmt.Sprintf("%d 分钟", m)
		}
		return fmt.Sprintf("%d 分 %d 秒", m, s)
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%d 小时 %02d 分", h, m)
	}
}

// wrapText 按显示宽度硬折行，用于超长链接
func wrapText(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	rs := []rune(s)
	if len(rs) <= width {
		return []string{s}
	}
	out := make([]string, 0, len(rs)/width+1)
	for len(rs) > width {
		out = append(out, string(rs[:width]))
		rs = rs[width:]
	}
	if len(rs) > 0 {
		out = append(out, string(rs))
	}
	return out
}

// nodeFilterMatch 列表搜索：名称/端口/域名/协议/状态
func nodeFilterMatch(st NodeStatus, kw string) bool {
	kw = strings.ToLower(strings.TrimSpace(kw))
	if kw == "" {
		return true
	}
	fields := []string{
		st.Node.Name,
		st.Node.Protocol,
		fmt.Sprintf("%d", st.Node.Port),
		st.Node.Tunnel.Domain,
		st.Node.Path,
		strings.ToLower(st.State),
		modeText(st.Node.Tunnel.Mode),
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), kw) {
			return true
		}
	}
	return false
}
