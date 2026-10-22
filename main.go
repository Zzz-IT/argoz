package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"argoz/internal/config"
	"argoz/internal/core"
	"argoz/internal/logx"
	"argoz/internal/panel"
	"argoz/internal/rpc"
	"argoz/internal/sys"
)

func main() {
	dir := flag.String("dir", "", "数据目录（默认 /usr/local/etc/argoz）")
	daemonMode := flag.Bool("d", false, "守护进程模式（无菜单，供 systemd/OpenRC 托管）")
	supervise := flag.Bool("supervise", false, "自守护模式：拉起并看护 daemon（容器无 init 时用）")
	showVer := flag.Bool("version", false, "显示版本")
	flag.Bool("classic", false, "兼容旧参数（现在默认就是行式菜单）")
	flag.Parse()

	if *showVer {
		fmt.Printf("%s %s\n", config.AppName, config.Version)
		return
	}

	dataDir := *dir
	if dataDir == "" {
		dataDir = config.DefaultDataDir()
	}

	if args := flag.Args(); len(args) > 0 {
		switch args[0] {
		case "service":
			if err := sys.RunServiceCommandLine(args[1:], dataDir); err != nil {
				fmt.Fprintln(os.Stderr, "错误:", err)
				os.Exit(1)
			}
			return
		case "update":
			if err := core.RunUpdateCommandLine(args[1:], dataDir); err != nil {
				fmt.Fprintln(os.Stderr, "错误:", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "未知命令: %s（可用: service, update）\n", args[0])
			os.Exit(2)
		}
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
	lock, err := core.AcquireLock(dir, "argoz.lock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer lock.Release()

	cfg, err := config.LoadConfig(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if err := logx.InitLog(cfg.Dir(), cfg.Settings.LogLevel); err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败: %v\n", err)
	}
	defer logx.CloseLog()

	sys.CheckRuntimeEnv()

	for _, msg := range core.CleanupOrphanCloudflared(cfg) {
		logx.LogWarn("%s", msg)
	}

	e := core.NewEngine(cfg, cfg.Dir())
	e.SetDaemon(true)
	e.Start()
	e.StartHealth()
	defer e.Shutdown()

	// 心跳文件 + systemd watchdog（假死检测）
	stopBeat := core.StartHeartbeat(cfg.Dir())
	defer stopBeat()

	srv, err := rpc.Start(e, cfg.Dir())
	if err != nil {
		logx.LogErr("启动控制通道失败: %v", err)
		os.Exit(1)
	}
	defer srv.Shutdown()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if p, err := core.EnsureCloudflared(ctx); err == nil {
			logx.LogDebug("cloudflared 就绪: %s", p)
		} else {
			logx.LogWarn("cloudflared 未就绪: %v", err)
		}
	}()

	logx.LogInfo("%s %s daemon 已启动，节点 %d 个，配置目录 %s",
		config.AppName, config.Version, len(cfg.Nodes), cfg.Dir())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ch:
		logx.LogInfo("收到退出信号")
	case <-srv.Done():
		logx.LogInfo("控制通道关闭")
	}
	logx.LogInfo("daemon 正在退出...")
}

// runPanel 面板：只做客户端，退出不影响 daemon
func runPanel(dir string) {
	cfg, err := config.LoadConfig(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	client, err := rpc.EnsureDaemon(cfg.Dir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	logx.SetConsoleLevel(logx.LevelWarn)
	panel.Run(client)
}
