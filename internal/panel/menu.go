package panel

import (
	"fmt"
	"strings"
	"time"

	"argoz/internal/config"
	"argoz/internal/rpc"
)

func Run(e rpc.API) {
	maybeAutoReload(e)

	for {
		Clear()
		info := e.Info()
		_, conns, up, down := e.Totals()
		sts := e.Statuses()
		s := e.Settings()
		proto := e.DaemonProto()

		running := 0
		for _, st := range sts {
			if st.State == "up" {
				running++
			}
		}

		fmt.Printf("\n  %sargoz %s%s\n", ColorWhite, config.Version, ColorReset)

		daemonLabel, daemonColor := daemonStateText(proto, info.PID)
		fmt.Printf("  %s守护进程%s    %s%s%s   PID %d   运行 %s   内存 %s\n",
			ColorGrey, ColorReset, daemonColor, daemonLabel, ColorReset,
			info.PID, durationText(time.Duration(info.UptimeSec)*time.Second), humanBytes(info.MemBytes))
		fmt.Printf("  %s节点%s        %d/%d 运行   连接 %d   流量 ↑%s  ↓%s\n",
			ColorGrey, ColorReset, running, len(sts), conns, humanBytes(up), humanBytes(down))

		mode := memoryModeText(s.MemoryMode)
		modeColor := ColorGrey
		if normalizeMemoryModeValue(s.MemoryMode) == "low" {
			modeColor = ColorYellow
		}
		fmt.Printf("  %s内存模式%s    %s%s%s\n", ColorGrey, ColorReset, modeColor, mode, ColorReset)

		if issue := recentNodeIssue(sts); issue != nil {
			state, _ := stateText(issue.State)
			detail := strings.TrimSpace(issue.Err)
			if detail == "" {
				detail = state
			}
			if !issue.NextRetry.IsZero() && issue.NextRetry.After(time.Now()) {
				detail += "（" + durationText(time.Until(issue.NextRetry)) + "后重试）"
			}
			fmt.Printf("  %s最近故障%s    节点 %s：%s\n", ColorYellow, ColorReset,
				truncate(issue.Node.Name, 20), truncate(detail, maxInt(20, termWidth()-32)))
			fmt.Printf("  %s              1 节点管理查看/重试，或 6 自检与修复%s\n", ColorGrey, ColorReset)
		}

		if proto < 1 {
			fmt.Printf("  %s⚠ 守护进程协议过旧，控制功能可能不完整；请重启 daemon 后重试（无需重跑安装脚本）%s\n",
				ColorRed, ColorReset)
		} else if dv := e.DaemonVersion(); dv != "" && dv != config.Version {
			fmt.Printf("  %s⚠ 守护进程版本 %s 与面板 %s 不一致；请重启 daemon，或进入 7 本体维护检查更新%s\n",
				ColorYellow, dv, config.Version, ColorReset)
		}

		fmt.Printf("\n  1 节点管理    2 新建节点    3 设置\n")
		fmt.Printf("  4 日志        5 运行状态    6 自检与修复\n")
		fmt.Printf("  7 本体维护    9 卸载        0 退出\n\n")

		switch ReadLine("  > ") {
		case "1":
			for {
				id := showNodeList(e)
				if id == "" {
					break
				}
				showNodeDetail(e, id)
			}
		case "2":
			wizardCreate(e)
		case "3":
			showSettings(e)
		case "4":
			showLogs(e)
		case "5":
			showStatus(e)
		case "6":
			showDoctor(e)
		case "7":
			showMaintenance(e)
		case "9":
			if showUninstall(e) {
				return
			}
		case "0", "q", "Q", "":
			LogInfo("再见（daemon 仍在后台运行）")
			return
		}
	}
}

func daemonStateText(proto, pid int) (string, string) {
	switch {
	case pid > 0 && proto < 1:
		return "运行中（旧协议）", ColorYellow
	case pid > 0:
		return "运行中", ColorGreen
	default:
		return "未响应", ColorRed
	}
}

// recentNodeIssue 返回最近一个需要用户注意的节点问题。优先展示带错误明细的节点。
func recentNodeIssue(sts []NodeStatus) *NodeStatus {
	var fallback *NodeStatus
	for i := range sts {
		st := &sts[i]
		if st.State == "up" {
			continue
		}
		if strings.TrimSpace(st.Err) != "" {
			return st
		}
		if fallback == nil {
			fallback = st
		}
	}
	return fallback
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
