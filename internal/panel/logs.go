package panel

import (
	"argoz/internal/rpc"
	"fmt"
	"path/filepath"
	"strings"
)

// panelLogPath 面板侧的日志路径（daemon 与面板同机，按数据目录拼接）
func panelLogPath(e rpc.API) string {
	if p := LogFilePath(); p != "" {
		return p
	}
	return filepath.Join(e.DataDir(), "argoz.log")
}

// logLevelTag 把级别映射成日志行里的标签
func logLevelTag(level string) string {
	switch level {
	case "error":
		return "[错误]"
	case "warn":
		return "[注意]"
	case "info":
		return "[信息]"
	case "debug":
		return "[调试]"
	}
	return ""
}

func logLevelName(level string) string {
	if level == "" {
		return "全部"
	}
	return "仅" + levelText(level)
}

// showLogs 日志查看页：分页 + 级别过滤
func showLogs(e rpc.API) {
	level := ""
	page := 1 << 20
	first := true

	for {
		all, err := tailLines(panelLogPath(e), 2000)
		if err != nil {
			Clear()
			Title("运行日志")
			fmt.Printf("  %s读取失败: %v%s\n", ColorYellow, err, ColorReset)
			Pause("按回车返回...")
			return
		}

		tag := logLevelTag(level)
		lines := make([]string, 0, len(all))
		for _, l := range all {
			if tag == "" || strings.Contains(l, tag) {
				lines = append(lines, l)
			}
		}

		per := termHeight() - 10
		if per < 6 {
			per = 6
		}
		pages := (len(lines) + per - 1) / per
		if pages == 0 {
			pages = 1
		}
		if page >= pages {
			page = pages - 1
		}
		if page < 0 {
			page = 0
		}
		if first {
			page = pages - 1
			first = false
		}

		Clear()
		Title("运行日志")
		fmt.Printf("  %s[%s]  第 %d/%d 页 · 共 %d 行%s\n\n", ColorGrey, logLevelName(level), page+1, pages, len(lines), ColorReset)

		start := page * per
		end := start + per
		if end > len(lines) {
			end = len(lines)
		}
		if len(lines) == 0 {
			fmt.Println("  （没有匹配的日志）")
		}
		for _, l := range lines[start:end] {
			fmt.Println("  " + truncate(l, termWidth()-4))
		}

		fmt.Printf("\n  n/p 翻页   e 错误  w 警告  i 信息  d 调试  a 全部   g 最新   0 返回\n")
		switch strings.ToLower(strings.TrimSpace(ReadLine("  > "))) {
		case "0", "":
			return
		case "n":
			page++
		case "p":
			page--
		case "g":
			page = 1 << 20
		case "e":
			level, page = "error", 1<<20
		case "w":
			level, page = "warn", 1<<20
		case "i":
			level, page = "info", 1<<20
		case "d":
			level, page = "debug", 1<<20
		case "a":
			level, page = "", 1<<20
		}
	}
}
