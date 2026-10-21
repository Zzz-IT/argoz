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
	daemonMode := flag.Bool("d", false, "守护进程模式（无菜单，供 systemd/OpenRC 托管）")
	supervise := flag.Bool("supervise", false, "自守护模式：拉起并看护 daemon（容器无 init 时用）")
	showVer := flag.Bool("version", false, "显示版本")
	flag.Bool("classic", false, "兼容旧参数（现在默认就是行式菜单）")
	flag.Parse()

	if *showVer {
		fmt.Printf("%s %s\n", AppName, AppVersion)
		return
	}

	dataDir := *dir
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}

	switch {
	case *supervise:
		runSupervisor(dataDir)
	case *daemonMode:
		runDaemon(dataDir)
	default:
		runPanel(dataDir)
	}
}

// runDaemon 常驻引擎：跑所有节点 + 保活 + 本地控制通道
func runDaemon(dir string) {
	lock, err := acquireLock(dir, "argoz.lock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer lock.Release()

	cfg, err := LoadConfig(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if err := InitLog(cfg.Dir(), cfg.Settings.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败: %v\n", err)
	}
	defer CloseLog()

	e := NewEngine(cfg, cfg.Dir())
	e.SetDaemon(true)
	e.Start()
	e.StartHealth()
	defer e.Shutdown()

	srv, err := startRPC(e, cfg.Dir())
	if err != nil {
		LogErr("启动控制通道失败: %v", err)
		os.Exit(1)
	}
	defer srv.Shutdown()

	// 后台预热 cloudflared，不阻塞节点启动
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if p, err := ensureCloudflared(ctx); err == nil {
			LogDebug("cloudflared 就绪: %s", p)
		} else {
			LogWarn("cloudflared 未就绪: %v", err)
		}
	}()

	LogInfo("%s %s daemon 已启动，节点 %d 个，配置目录 %s", AppName, AppVersion, len(cfg.Nodes), cfg.Dir())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ch:
		LogInfo("收到退出信号")
	case <-srv.done:
		LogInfo("控制通道关闭")
	}
	LogInfo("daemon 正在退出...")
}

// runPanel 面板：只做客户端，退出不影响 daemon
func runPanel(dir string) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	client, err := ensureDaemon(cfg.Dir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	SetConsoleLevel(LevelWarn)
	showMainMenu(client)
}

func showMainMenu(e EngineAPI) {
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
			LogInfo("再见（daemon 仍在后台运行）")
			return
		}
	}
}
