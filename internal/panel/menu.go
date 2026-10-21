package panel

import (
	"fmt"
	"sync/atomic"
	"time"

	"argoz/internal/config"
	"argoz/internal/rpc"
)

func Run(e rpc.API) {
	var hint atomic.Value
	hint.Store("")
	go func() {
		if info, err := e.UpdateCheck(); err == nil && info.HasUpdate {
			hint.Store("↑ 发现新版本 " + info.Latest + "，按 5 查看")
		}
	}()

	for {
		Clear()
		info := e.Info()
		_, conns, up, down := e.Totals()
		sts := e.Statuses()
		running := 0
		for _, s := range sts {
			if s.State == "up" {
				running++
			}
		}

		fmt.Printf("\n  %sargoz %s%s    %s● 运行中%s   节点 %d/%d   连接 %d   ↑%s ↓%s\n",
			ColorWhite, config.Version, ColorReset, ColorGreen, ColorReset,
			running, len(sts), conns, humanBytes(up), humanBytes(down))
		fmt.Printf("  %s守护进程   进程号 %d · 已运行 %s · 内存 %s%s\n",
			ColorGrey, info.PID,
			durationText(time.Duration(info.UptimeSec)*time.Second), humanBytes(info.MemBytes), ColorReset)

		for _, st := range sts {
			if st.State == "degraded" || st.State == "err" || st.State == "down" {
				txt, _ := stateText(st.State)
				fmt.Printf("  %s⚠ 节点 %s %s%s\n", ColorYellow, st.Node.Name, txt, ColorReset)
				break
			}
		}
		if s, _ := hint.Load().(string); s != "" {
			fmt.Printf("  %s%s%s\n", ColorYellow, s, ColorReset)
		}
		if proto := e.DaemonProto(); proto < 1 {
			fmt.Printf("  %s⚠ 守护进程版本过旧（不支持控制握手），请执行：argoz service install%s\n", ColorRed, ColorReset)
		} else if dv := e.DaemonVersion(); dv != "" && dv != config.Version {
			fmt.Printf("  %s⚠ 守护进程仍是 %s，与面板 %s 不一致，请执行：argoz service install%s\n",
				ColorYellow, dv, config.Version, ColorReset)
		}

		fmt.Printf("\n  1 节点管理    2 新建节点    3 设置    4 日志    5 检查更新    6 运行状态    9 卸载    0 退出\n\n")

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
			showUpdate(e)
		case "6":
			showStatus(e)
		case "9":
			if showUninstall(e) {
				return
			}
		case "0", "q", "":
			LogInfo("再见（daemon 仍在后台运行）")
			return
		}
	}
}
