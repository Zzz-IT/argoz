package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func (c *Config) Dir() string { return filepath.Dir(c.path) }

// isTerminal 判断是否为字符设备（TTY）
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func main() {
	dir := flag.String("dir", "", "数据目录（默认 /usr/local/etc/argoz）")
	daemon := flag.Bool("d", false, "无菜单守护模式")
	showVer := flag.Bool("version", false, "显示版本")
	classic := flag.Bool("classic", false, "使用经典菜单界面（不用全屏 TUI）")
	flag.Parse()

	if *showVer {
		fmt.Printf("%s %s\n", AppName, AppVersion)
		return
	}

	cfg, err := LoadConfig(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if err := InitLog(cfg.Dir(), cfg.Settings.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败: %v\n", err)
	}
	defer CloseLog()

	e := NewEngine(cfg, cfg.Dir())
	e.SetDaemon(*daemon)
	e.Start()
	e.StartHealth()

	// 后台预热 cloudflared：不阻塞节点启动，装好后后续节点直接可用
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if p, err := ensureCloudflared(ctx); err == nil {
			LogDebug("cloudflared 就绪: %s", p)
		} else {
			LogWarn("cloudflared 未就绪: %v", err)
		}
	}()
	defer e.Shutdown()

	if *daemon {
		SetConsoleLevel(LevelInfo)
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		LogInfo("%s %s 已启动，节点 %d 个，配置目录 %s", AppName, AppVersion, len(cfg.Nodes), cfg.Dir())
		<-ch
		LogInfo("收到退出信号，正在停止...")
		return
	}

	if *classic || !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		// 非交互环境（管道/重定向/无 TTY）自动降级为行式菜单，避免全屏界面卡死
		SetConsoleLevel(LevelWarn)
		showMainMenu(e)
		return
	}

	// 全屏 TUI：日志只写文件，避免破坏画面
	SetConsoleLevel(LevelOff)
	plan, err := runTUI(e)
	SetConsoleLevel(LevelWarn)
	if plan != nil {
		doUninstall(e, *plan)
		return
	}
	if err != nil {
		LogErr("TUI 启动失败: %v，回退到经典菜单", err)
		showMainMenu(e)
	}
}

// doUninstall 执行卸载并打印明细
func doUninstall(e *Engine, plan uninstallPlan) {
	fmt.Printf("\n  即将执行：%s\n\n", plan.Summary())
	lines, err := e.Uninstall(plan)
	for _, l := range lines {
		fmt.Println("  · " + l)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [错误] 卸载未完成: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n  卸载完成，感谢使用 argoz。")
}

func showMainMenu(e *Engine) {
	for {
		Clear()
		nodes, conns, up, down := e.Totals()
		all := e.Nodes()

		fmt.Printf("\n  %sargoz %s%s    %s● running%s   ↑%s ↓%s   节点 %d/%d   conns %d\n\n",
			ColorWhite, AppVersion, ColorReset,
			ColorGreen, ColorReset,
			humanBytes(up), humanBytes(down), nodes, len(all), conns)

		fmt.Printf("  1 节点管理      2 新建节点      3 设置      4 日志      9 卸载      0 退出\n\n")

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
		case "9":
			if showUninstall(e) {
				return
			}
		case "0", "q", "":
			LogInfo("再见")
			return
		}
	}
}
