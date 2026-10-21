package panel

import (
	"argoz/internal/config"
	"argoz/internal/rpc"
	"fmt"
	"strings"
)

// showUpdate 检查更新 / 一键升级
func showUpdate(e rpc.API) {
	Clear()
	Title("检查更新")
	fmt.Printf("  当前版本   %s\n", config.Version)
	fmt.Print("  正在检查 GitHub Releases ...")

	info, err := e.UpdateCheck()
	fmt.Println()
	if err != nil {
		fmt.Printf("\n  %s[错误] 无法访问 GitHub：%v%s\n\n", ColorRed, err, ColorReset)
		fmt.Println("  可稍后重试，或手动执行：")
		fmt.Println("    bash <(curl -fsSL https://raw.githubusercontent.com/Zzz-IT/argoz/main/install.sh)")
		Pause("\n按回车返回...")
		return
	}

	fmt.Printf("  最新版本   %s\n", info.Latest)
	if info.PublishedAt != "" {
		fmt.Printf("  更新时间   %s\n", info.PublishedAt)
	}
	if info.SizeBytes > 0 {
		fmt.Printf("  下载体积   %s\n", humanBytes(info.SizeBytes))
	}
	fmt.Println()

	if !info.HasUpdate {
		fmt.Println("  已是最新版本")
		Pause("\n按回车返回...")
		return
	}

	fmt.Println("  更新内容：")
	if len(info.Notes) == 0 {
		fmt.Println("    （发布说明为空）")
	}
	for _, n := range info.Notes {
		fmt.Println("    " + truncate(n, termWidth()-6))
	}

	fmt.Printf("\n  1 立即升级    0 返回\n")
	if strings.TrimSpace(ReadLine("  > ")) != "1" {
		return
	}

	Clear()
	Title("正在升级")
	fmt.Println("  · 下载并校验 ...")
	res, err := e.UpdateApply()
	if err != nil {
		fmt.Printf("\n  %s[错误] %v%s\n", ColorRed, err, ColorReset)
		fmt.Println("\n  可手动执行：")
		fmt.Println("    bash <(curl -fsSL https://raw.githubusercontent.com/Zzz-IT/argoz/main/install.sh)")
		Pause("\n按回车返回...")
		return
	}

	fmt.Printf("  ✓ 已替换 %s\n", res.Path)
	if res.Restarted {
		fmt.Println("  · 服务重启中，新版本将在几秒内生效")
		fmt.Println("  · 若面板随后断开，重新执行 argoz 即可")
	} else {
		fmt.Printf("\n  %s[注意] %s%s\n", ColorYellow, res.Hint, ColorReset)
		fmt.Printf("  手动重启：kill %d（或 rc-service argoz restart）\n", e.Info().PID)
	}
	Pause("\n按回车返回...")
}
