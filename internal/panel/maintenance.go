package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"argoz/internal/config"
	"argoz/internal/rpc"
)

// showMaintenance 本体维护：argoz 与 cloudflared 的检查、升级、重装严格分开。
func showMaintenance(e rpc.API) {
	for {
		Clear()
		Title("本体维护")

		info := e.Info()
		fmt.Printf("  argoz      面板 %s · daemon %s\n", config.Version, orDash(info.Version))
		fmt.Printf("  数据目录   %s\n", orDash(info.DataDir))
		if st, err := e.CloudflaredStatus(); err != nil {
			fmt.Printf("  cloudflared %s状态读取失败: %v%s\n", ColorYellow, err, ColorReset)
		} else {
			printCloudflaredSummary(st)
		}

		fmt.Printf("\n  1 检查 argoz 更新\n")
		fmt.Printf("  2 升级 argoz\n")
		fmt.Printf("  3 重装 argoz\n")
		fmt.Printf("  4 检查 cloudflared 更新\n")
		fmt.Printf("  5 升级 cloudflared\n")
		fmt.Printf("  6 重装 cloudflared\n")
		fmt.Printf("  7 查看版本 / 路径 / 缓存状态\n")
		fmt.Printf("  8 返回\n\n")
		fmt.Printf("  %s重装会强制重新获取并校验同一版本；失败时保留旧版本。%s\n\n", ColorGrey, ColorReset)

		switch strings.TrimSpace(ReadLine("  > ")) {
		case "1":
			showCheckUpdate(e, componentSelf)
		case "2":
			applyComponentUpdate(e, componentSelf, false)
		case "3":
			applyComponentUpdate(e, componentSelf, true)
		case "4":
			showCheckUpdate(e, componentCloudflared)
		case "5":
			applyComponentUpdate(e, componentCloudflared, false)
		case "6":
			applyComponentUpdate(e, componentCloudflared, true)
		case "7":
			showArtifactStatusPage(e)
		case "8", "0", "":
			return
		}
	}
}

func printCloudflaredSummary(st ArtifactStatus) {
	installed := "未安装"
	if st.Installed {
		installed = "已安装"
	}
	health := "未验证"
	if st.Installed || st.Path != "" {
		if st.Healthy {
			health = "健康"
		} else {
			health = "异常"
		}
	}
	cache := "无缓存"
	if st.Cached {
		cache = "有缓存"
	}
	version := orDash(st.Version)
	fmt.Printf("  cloudflared %s · %s · %s · %s\n", version, installed, health, cache)
	if strings.TrimSpace(st.LastError) != "" {
		fmt.Printf("  %s最近错误    %s%s\n", ColorYellow,
			truncate(st.LastError, maxInt(20, termWidth()-16)), ColorReset)
	}
}

func showArtifactStatusPage(e rpc.API) {
	Clear()
	Title("版本 / 路径 / 缓存状态")

	info := e.Info()
	fmt.Println("  argoz")
	fmt.Printf("    面板版本   %s\n", config.Version)
	fmt.Printf("    守护版本   %s\n", orDash(info.Version))
	fmt.Printf("    进程 PID   %d\n", info.PID)
	fmt.Printf("    二进制     %s\n", orDash(currentSelfBinaryPath()))
	fmt.Printf("    数据目录   %s\n", orDash(info.DataDir))
	printCacheFolder(filepath.Join(e.DataDir(), "cache", "argoz"))

	fmt.Println("\n  cloudflared")
	st, err := e.CloudflaredStatus()
	if err != nil {
		fmt.Printf("    %s[错误] 状态读取失败: %v%s\n", ColorRed, err, ColorReset)
		fmt.Println("    可返回维护菜单选择 6 强制重装，或去首页 6 自检与修复查看权限/缓存。")
	} else {
		printArtifactStatus(st)
		printCacheFolder(filepath.Join(e.DataDir(), "cache", componentCloudflared))
	}

	fmt.Println("\n  说明：升级/重装的下载、校验、备份和原子替换由 daemon 事务处理；")
	fmt.Println("        本页只查看状态，不会下载或修改任何文件。")
	Pause("按回车返回维护菜单...")
}

func currentSelfBinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "-"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe
}

// printCacheFolder 只读汇总 artifact 缓存目录；用于展示本地离线缓存是否已有内容。
func printCacheFolder(dir string) {
	fmt.Printf("    缓存目录   %s\n", orDash(dir))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fmt.Println("    缓存摘要   无（尚未下载或缓存已清理）")
		return
	}
	var files int
	var bytes int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil || fi == nil || fi.IsDir() {
			return nil
		}
		files++
		bytes += fi.Size()
		return nil
	})
	if files == 0 {
		fmt.Println("    缓存摘要   空目录")
		return
	}
	fmt.Printf("    缓存摘要   %d 个文件 · %s\n", files, humanBytes(bytes))
}
