package main

import (
	"fmt"
	"strings"
	"time"
)

func boolText(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

// showSettings 设置页
func showSettings(e EngineAPI) {
	for {
		s := e.Settings()
		Clear()
		Title("设置")
		fmt.Printf("  1 并发上限        %d 连接/节点\n", s.MaxConns)
		fmt.Printf("  2 保活间隔        %s (WS ping)\n", s.Keepalive)
		fmt.Printf("  3 空闲超时        %s\n", s.IdleTimeout)
		fmt.Printf("  4 监听与端口段    %s  %s\n", s.Listen, s.PortRange)
		fmt.Printf("  5 ECH 注入        %s  %s\n", boolText(s.ECH), stMutedHint("仅在 CF 为该域名启用 ECH 时有效"))
		fmt.Printf("  6 日志级别        %s\n", s.LogLevel)
		fmt.Printf("    日志文件        %s\n", orDash(LogFilePath()))
		fmt.Printf("  0 返回\n\n")

		switch ReadLine("  请选择要修改的项 > ") {
		case "1":
			v := ReadLine(fmt.Sprintf("  新并发上限 (当前 %d): ", s.MaxConns))
			n, err := parseInt(v)
			if err != nil || n < 1 || n > 4096 {
				LogErr("数值非法（1-4096，每会话约 41KB 内存）")
				Pause("")
				continue
			}
			s.MaxConns = n
			saveSettings(e, s)
		case "2":
			v := ReadLine(fmt.Sprintf("  新保活间隔 (当前 %s，如 30s): ", s.Keepalive))
			d, err := time.ParseDuration(v)
			if err != nil || d < 5*time.Second {
				LogErr("时间格式非法（最小 5s，如 30s / 1m）")
				Pause("")
				continue
			}
			s.Keepalive = d.String()
			saveSettings(e, s)
		case "3":
			v := ReadLine(fmt.Sprintf("  新空闲超时 (当前 %s，如 5m): ", s.IdleTimeout))
			d, err := time.ParseDuration(v)
			if err != nil || d < 10*time.Second {
				LogErr("时间格式非法（最小 10s）")
				Pause("")
				continue
			}
			s.IdleTimeout = d.String()
			saveSettings(e, s)
		case "4":
			listen := ReadLine(fmt.Sprintf("  监听地址 (当前 %s): ", s.Listen))
			if listen != "" && !strings.ContainsAny(listen, " \t") {
				s.Listen = listen
			}
			pr := ReadLine(fmt.Sprintf("  端口段 (当前 %s): ", s.PortRange))
			if pr != "" {
				s.PortRange = pr
			}
			if _, _, err := s.PortBounds(); err != nil {
				LogErr("%v", err)
				Pause("")
				continue
			}
			LogWarn("监听地址/端口段的变更将在下次启动时对该项生效")
			saveSettings(e, s)
		case "5":
			s.ECH = !s.ECH
			saveSettings(e, s)
		case "6":
			v := ReadLine("  日志级别 (error/warn/info/debug): ")
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "error" && v != "warn" && v != "info" && v != "debug" {
				LogErr("非法级别")
				Pause("")
				continue
			}
			s.LogLevel = v
			saveSettings(e, s)
		case "0", "":
			return
		}
	}
}

func saveSettings(e EngineAPI, s Settings) {
	if err := e.SaveSettings(s); err != nil {
		LogErr("保存失败: %v", err)
	} else {
		LogOK("设置已保存")
	}
	Pause("")
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err
}
