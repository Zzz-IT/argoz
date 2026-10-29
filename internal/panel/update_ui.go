package panel

import (
	"fmt"
	"strings"

	"argoz/internal/config"
	"argoz/internal/rpc"
)

const (
	componentSelf        = "self"
	componentCloudflared = "cloudflared"
)

// showUpdate 保留旧入口；首页已不再调用，实际复用本体维护菜单。
func showUpdate(e rpc.API) {
	showMaintenance(e)
}

func componentLabel(component string) string {
	if component == componentCloudflared {
		return "cloudflared"
	}
	return "argoz"
}

func updateActionLabel(force bool) string {
	if force {
		return "重装"
	}
	return "升级"
}

// fetchUpdatePreview 先取 cloudflared 本地状态，再查询远端目标版本。
// 所有下载/替换仍只由 daemon 的 ApplyUpdate 完成，面板不重复实现下载。
func fetchUpdatePreview(e rpc.API, component string) (UpdateInfo, ArtifactStatus, error) {
	var artifact ArtifactStatus
	if component == componentCloudflared {
		artifact.Name = componentCloudflared
		if st, err := e.CloudflaredStatus(); err != nil {
			artifact.LastError = "状态读取失败: " + err.Error()
		} else {
			artifact = st
		}
	}
	info, err := e.CheckUpdate(component)
	return info, artifact, err
}

// showCheckUpdate 只检查并展示当前版本/目标版本/大小，不下载、不替换。
func showCheckUpdate(e rpc.API, component string) {
	label := componentLabel(component)
	Clear()
	Title("检查 " + label + " 更新")
	fmt.Print("  正在查询当前版本和目标版本 ...")

	info, artifact, err := fetchUpdatePreview(e, component)
	fmt.Println()
	if err != nil {
		printLocalUpdateContext(component, artifact)
		printUpdateFailure(label+"检查更新", err)
		Pause("按回车返回维护菜单...")
		return
	}

	renderUpdatePreview(component, info, artifact)
	if !info.HasUpdate {
		fmt.Println("\n  已是最新版本，无需升级。")
		Pause("按回车返回维护菜单...")
		return
	}
	fmt.Printf("\n  %s发现新版本 %s。返回维护菜单选择 %d 可升级。%s\n",
		ColorYellow, info.Latest, componentUpgradeMenuNo(component), ColorReset)
	Pause("按回车返回维护菜单...")
}

func componentUpgradeMenuNo(component string) int {
	if component == componentCloudflared {
		return 5
	}
	return 2
}

// applyComponentUpdate 检查版本 -> 展示明细 -> 用户确认 -> 调用 daemon 事务化升级。
func applyComponentUpdate(e rpc.API, component string, force bool) {
	label := componentLabel(component)
	action := updateActionLabel(force)

	Clear()
	Title(action + " " + label)
	fmt.Print("  正在检查当前版本、目标版本与包大小 ...")
	info, artifact, err := fetchUpdatePreview(e, component)
	fmt.Println()
	if err != nil {
		printLocalUpdateContext(component, artifact)
		printUpdateFailure(action+label, err)
		Pause("按回车返回维护菜单...")
		return
	}

	renderUpdatePreview(component, info, artifact)
	if !info.HasUpdate && !force {
		fmt.Println("\n  已是最新版本，无需升级。")
		Pause("按回车返回维护菜单...")
		return
	}
	if force {
		fmt.Printf("\n  将强制重新获取并校验 %s；失败时会保留当前可用版本。\n", orDash(info.Latest))
	}
	fmt.Println("  下载、SHA256/ELF 校验与原子替换均由 daemon 执行，面板不会重复下载。")
	if !Confirm("  确认" + action + "?") {
		LogWarn("已取消" + action)
		Pause("按回车返回维护菜单...")
		return
	}

	Clear()
	Title("正在" + action + " · " + label)
	fmt.Println("  请稍候。下载或校验失败不会覆盖当前可用版本。")
	res, err := e.ApplyUpdate(component, force)
	if err != nil {
		printUpdateFailure(action+label, err)
		if component == componentCloudflared {
			fmt.Println("\n  当前 cloudflared 状态：")
			printArtifactStatus(artifact)
		}
		Pause("按回车返回维护菜单...")
		return
	}

	fmt.Println()
	fmt.Printf("  ✓ %s已完成\n", action)
	if res.Path != "" {
		fmt.Printf("  路径       %s\n", res.Path)
	}
	if res.Replaced {
		fmt.Println("  替换       已校验并原子替换")
	}
	if res.Restarted {
		fmt.Println("  重启       已触发或已安排重启")
	} else if hint := strings.TrimSpace(res.Hint); hint != "" {
		fmt.Printf("  提示       %s\n", hint)
	} else if component == componentSelf {
		fmt.Println("  提示       新版本已就位；请通过 systemd/OpenRC 或 supervisor 重启 daemon 加载新版本")
	}
	if component == componentCloudflared {
		fmt.Println("\n  cloudflared 当前状态：")
		if st, statusErr := e.CloudflaredStatus(); statusErr != nil {
			fmt.Printf("  %s[注意] 状态读取失败: %v%s\n", ColorYellow, statusErr, ColorReset)
		} else {
			printArtifactStatus(st)
		}
	}
	Pause("按回车返回维护菜单...")
}

func printLocalUpdateContext(component string, artifact ArtifactStatus) {
	if component == componentCloudflared {
		fmt.Println("\n  cloudflared 本地状态：")
		printArtifactStatus(artifact)
		return
	}
	fmt.Printf("\n  当前本地版本   %s\n", config.Version)
}

func renderUpdatePreview(component string, info UpdateInfo, artifact ArtifactStatus) {
	current := strings.TrimSpace(info.Current)
	if current == "" {
		if artifact.Version != "" {
			current = artifact.Version
		} else if component == componentSelf {
			current = config.Version
		}
	}
	fmt.Printf("\n  %s当前版本%s   %s\n", ColorGrey, ColorReset, orDash(current))
	if component == componentCloudflared {
		printArtifactStatus(artifact)
	}
	target := orDash(strings.TrimSpace(info.Latest))
	fmt.Printf("  %s目标版本%s   %s\n", ColorGrey, ColorReset, target)
	if info.SizeBytes > 0 {
		fmt.Printf("  %s下载大小%s   %s\n", ColorGrey, ColorReset, humanBytes(info.SizeBytes))
	} else {
		fmt.Printf("  %s下载大小%s   未知（以发布页/缓存为准）\n", ColorGrey, ColorReset)
	}
	if info.PublishedAt != "" {
		fmt.Printf("  %s发布时间%s   %s\n", ColorGrey, ColorReset, info.PublishedAt)
	}
	if len(info.Notes) > 0 {
		fmt.Printf("\n  %s更新说明%s\n", ColorGrey, ColorReset)
		for _, n := range info.Notes {
			fmt.Println("    " + truncate(n, maxInt(20, termWidth()-6)))
		}
	}
	if component == componentSelf {
		fmt.Printf("  %s当前二进制%s %s\n", ColorGrey, ColorReset, currentSelfBinaryPath())
	}
}

func printArtifactStatus(st ArtifactStatus) {
	name := strings.TrimSpace(st.Name)
	if name == "" {
		name = "artifact"
	}
	installed := "未安装"
	if st.Installed {
		installed = "已安装"
	}
	healthy := "未验证"
	if st.Installed || st.Path != "" {
		if st.Healthy {
			healthy = "健康"
		} else {
			healthy = "异常"
		}
	}
	cached := "无"
	if st.Cached {
		cached = "有"
	}
	fmt.Printf("  %s资源%s       %s\n", ColorGrey, ColorReset, name)
	fmt.Printf("  %s版本%s       %s\n", ColorGrey, ColorReset, orDash(st.Version))
	fmt.Printf("  %s路径%s       %s\n", ColorGrey, ColorReset, orDash(st.Path))
	fmt.Printf("  %s状态%s       %s · %s · 缓存 %s", ColorGrey, ColorReset, installed, healthy, cached)
	if st.Size > 0 {
		fmt.Printf(" · %s", humanBytes(st.Size))
	}
	fmt.Println()
	if strings.TrimSpace(st.LastError) != "" {
		fmt.Printf("  %s最近错误%s   %s\n", ColorYellow, ColorReset, truncate(st.LastError, maxInt(20, termWidth()-16)))
		fmt.Println("    处理路径：重试刷新；已有离线缓存可重试；仍异常请去首页 6 自检与修复")
	}
}

func printUpdateFailure(action string, err error) {
	fmt.Printf("\n  %s[失败] %s未完成: %v%s\n", ColorRed, action, err, ColorReset)
	fmt.Println("  可执行路径：")
	fmt.Println("    · 重试：确认网络/代理或 GitHub 可达后，返回维护菜单重新选择本操作")
	fmt.Println("    · 离线：已有完整缓存时可重试；升级不会绕过校验，也不会覆盖可用的旧版本")
	fmt.Println("    · 去自检修复：首页 6 检查权限、缓存、依赖和 RPC 状态；仍失败再看 4 运行日志")
}
