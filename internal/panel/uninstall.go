package panel

import (
	"fmt"

	"argoz/internal/rpc"
)

// showUninstall 卸载确认页
func showUninstall(e rpc.API) bool {
	Clear()
	Title("卸载 argoz")
	fmt.Println("  将停止所有节点，并删除 systemd / OpenRC 服务与 argoz 二进制。")
	fmt.Println()
	fmt.Println("  1) 保留配置与日志（推荐）")
	fmt.Println("  2) 彻底删除（配置、日志、导出的链接文件）")
	choice := ReadLine("\n  请选择 (默认 1): ")
	keep := choice != "2"

	rmcf := ReadLine("  同时删除 cloudflared? (y/N): ") == "y"

	plan := UninstallPlan{KeepData: keep, RemoveCloudflared: rmcf}
	fmt.Printf("\n  即将执行：%s\n", plan.Summary())
	if ReadLine("  输入 DELETE 确认: ") != "DELETE" {
		LogWarn("已取消卸载")
		Pause("")
		return false
	}

	lines, err := e.Uninstall(plan)
	for _, l := range lines {
		fmt.Println("  · " + l)
	}
	if err != nil {
		LogErr("卸载未完成: %v", err)
		Pause("")
		return false
	}
	fmt.Println("\n  卸载完成，感谢使用 argoz。")
	return true
}
