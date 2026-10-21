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

func main() {
	dir := flag.String("dir", "", "数据目录（默认 /usr/local/etc/argoz）")
	daemon := flag.Bool("d", false, "无菜单守护模式")
	showVer := flag.Bool("version", false, "显示版本")
	flag.Bool("classic", false, "兼容旧参数（现在默认就是行式菜单）")
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

	SetConsoleLevel(LevelWarn)
	showMainMenu(e)
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
