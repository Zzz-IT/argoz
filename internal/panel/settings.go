package panel

import (
	"fmt"
	"strings"
	"time"

	"argoz/internal/rpc"
)

// showSettings 设置页。
func showSettings(e rpc.API) {
	for {
		s := e.Settings()
		info := e.Info()

		Clear()
		Title("设置")
		fmt.Printf("  1 并发上限        %d 连接/节点\n", s.MaxConns)
		fmt.Printf("  2 保活间隔        %s\n", durOrRaw(s.Keepalive))
		fmt.Printf("  3 空闲超时        %s\n", durOrRaw(s.IdleTimeout))
		fmt.Printf("  4 监听与端口段    %s   %s\n", s.Listen, s.PortRange)
		fmt.Printf("  5 ECH 注入        %s  %s\n", boolText(s.ECH), stMutedHint("仅在 CF 为该域名启用 ECH 时有效"))
		fmt.Printf("  6 日志级别        %s\n", levelText(s.LogLevel))
		fmt.Printf("  7 UDP 转发        %s  %s\n", boolText(s.UDPEnabled), stMutedHint("仅 mihomo VLESS 标准 UDP"))
		fmt.Printf("  8 UDP 空闲        %s\n", durOrRaw(s.UDPIdle))
		fmt.Printf("  9 UDP 会话上限    %d\n", s.MaxUDPSessions)
		fmt.Printf(" 10 内存模式        %s  %s\n", memoryModeText(s.MemoryMode), stMutedHint(memoryModeHelp(s.MemoryMode)))
		fmt.Printf("  %s%s%s\n", ColorGrey, strings.Repeat("─", 56), ColorReset)
		fmt.Printf("  守护进程   PID %d · 已运行 %s · 内存 %s\n",
			info.PID, durationText(time.Duration(info.UptimeSec)*time.Second), humanBytes(info.MemBytes))
		fmt.Printf("  版本       %s\n", info.Version)
		fmt.Printf("  日志文件   %s\n", orDash(panelLogPath(e)))
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
				LogErr("时间格式非法（最少 5s，如 30s / 1m）")
				Pause("")
				continue
			}
			s.Keepalive = d.String()
			saveSettings(e, s)
		case "3":
			v := ReadLine(fmt.Sprintf("  新空闲超时 (当前 %s，如 5m): ", s.IdleTimeout))
			d, err := time.ParseDuration(v)
			if err != nil || d < 10*time.Second {
				LogErr("时间格式非法（最少 10s）")
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
			v := ReadLine("  日志级别 [1] 错误  [2] 警告  [3] 信息  [4] 调试 (或直接输入 error/warn/info/debug): ")
			switch strings.TrimSpace(v) {
			case "1":
				v = "error"
			case "2":
				v = "warn"
			case "3":
				v = "info"
			case "4":
				v = "debug"
			}
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "error" && v != "warn" && v != "info" && v != "debug" {
				LogErr("非法级别")
				Pause("")
				continue
			}
			s.LogLevel = v
			saveSettings(e, s)
		case "7":
			s.UDPEnabled = !s.UDPEnabled
			saveSettings(e, s)
		case "8":
			v := ReadLine(fmt.Sprintf("  新 UDP 空闲超时 (当前 %s，如 60s): ", s.UDPIdle))
			d, err := time.ParseDuration(v)
			if err != nil || d < 10*time.Second {
				LogErr("时间格式非法（最少 10s）")
				Pause("")
				continue
			}
			s.UDPIdle = d.String()
			saveSettings(e, s)
		case "9":
			v := ReadLine(fmt.Sprintf("  新 UDP 会话上限 (当前 %d，1-128): ", s.MaxUDPSessions))
			n, err := parseInt(v)
			if err != nil || n < 1 || n > 128 {
				LogErr("数值非法（1-128）")
				Pause("")
				continue
			}
			s.MaxUDPSessions = n
			saveSettings(e, s)
		case "10":
			v := strings.ToLower(strings.TrimSpace(ReadLine("  内存模式 [1] 自动  [2] 低内存  [3] 常规 (或 auto/low/normal): ")))
			switch v {
			case "1":
				v = "auto"
			case "2":
				v = "low"
			case "3":
				v = "normal"
			}
			if !validMemoryMode(v) {
				LogErr("非法内存模式，仅支持 auto / low / normal")
				Pause("")
				continue
			}
			s.MemoryMode = v
			LogInfo("内存模式已设为 %s：下一轮节点启动/重连时按新配额的连接与 UDP 上限执行", memoryModeText(v))
			saveSettings(e, s)
		case "0", "":
			return
		}
	}
}

// durOrRaw 把 "30s" 显示成 "30 秒"，解析失败时原样返回。
func durOrRaw(s string) string {
	if d, err := time.ParseDuration(strings.TrimSpace(s)); err == nil {
		return durationText(d)
	}
	return s
}

func saveSettings(e rpc.API, s Settings) {
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

// normalizeMemoryModeValue 兼容旧配置/空值：未知值按 auto 展示。
func normalizeMemoryModeValue(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "low":
		return "low"
	case "normal":
		return "normal"
	default:
		return "auto"
	}
}

func validMemoryMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto", "low", "normal":
		return true
	default:
		return false
	}
}

func memoryModeText(mode string) string {
	switch normalizeMemoryModeValue(mode) {
	case "low":
		return "低内存"
	case "normal":
		return "常规"
	default:
		return "自动"
	}
}

func memoryModeHelp(mode string) string {
	switch normalizeMemoryModeValue(mode) {
	case "low":
		return "限制连接与 UDP 配额，适合 64MB/小内存机器"
	case "normal":
		return "使用常规连接与 UDP 配额"
	default:
		return "按机器内存自动选择；小内存机器会降为低内存基线"
	}
}
