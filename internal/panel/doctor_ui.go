package panel

import (
	"fmt"
	"strconv"
	"strings"

	"argoz/internal/rpc"
)

// showDoctor 自检与修复页。诊断只读；修复必须由用户选中项目并二次确认后执行。
func showDoctor(e rpc.API) {
	var (
		report  DoctorReport
		loadErr error
		loaded  bool
	)

	for {
		Clear()
		Title("自检与修复")
		if !loaded {
			fmt.Print("  正在运行只读自检 ...")
			report, loadErr = e.Doctor()
			fmt.Println()
			loaded = true
		}

		if loadErr != nil {
			fmt.Printf("\n  %s[错误] 无法获取自检报告: %v%s\n", ColorRed, loadErr, ColorReset)
			fmt.Println("  请先确认 daemon 仍在运行；若面板刚升级，请重启 daemon 后重试。")
			fmt.Println("\n  1 重试    2 查看运行日志    3 本体维护    0 返回")
			switch strings.TrimSpace(ReadLine("  > ")) {
			case "1":
				loaded = false
			case "2":
				showLogs(e)
				loaded = false
			case "3":
				showMaintenance(e)
				loaded = false
			case "0", "":
				return
			}
			continue
		}

		renderDoctorReport(report)
		if len(report.Checks) == 0 {
			fmt.Printf("\n  r 重新检查    0 返回\n")
		} else {
			fmt.Printf("\n  r 重新检查    1-%d 查看说明/按动作修复    0 返回\n", len(report.Checks))
		}

		choice := strings.ToLower(strings.TrimSpace(ReadLine("  > ")))
		switch {
		case choice == "0" || choice == "":
			return
		case choice == "r":
			loaded = false
		default:
			n, err := strconv.Atoi(choice)
			if err != nil || n < 1 || n > len(report.Checks) {
				continue
			}
			showDoctorCheck(e, report.Checks[n-1])
			// 修复后自动刷新，让用户立即看到结果。
			loaded = false
		}
	}
}

func renderDoctorReport(r DoctorReport) {
	summary := strings.TrimSpace(r.Summary)
	if summary == "" {
		if r.Healthy {
			summary = "未发现需要处理的问题"
		} else {
			summary = "发现需要处理的问题"
		}
	}
	stateText, stateColor := "健康", ColorGreen
	if !r.Healthy {
		stateText, stateColor = "需要处理", ColorYellow
	}

	fmt.Printf("\n  总览    %s%s%s    %s\n", stateColor, stateText, ColorReset, summary)
	mem := "内存信息不可用"
	if r.MemTotalMB > 0 {
		mem = fmt.Sprintf("总计 %d MB / 可用 %d MB", r.MemTotalMB, r.MemAvailableMB)
	}
	if r.LowMemory {
		mem += "  " + ColorYellow + "低内存" + ColorReset
	}
	fmt.Printf("  内存    %s\n", mem)
	fmt.Println()

	width := termWidth()
	nameW, statusW := 16, 8
	if width < 78 {
		nameW, statusW = 12, 8
	}
	// 2+1+nameW+1+statusW+1+detailW+1+repairW，留出两边缩进和余量。
	avail := width - (nameW + statusW + 8)
	if avail < 24 {
		avail = 24
	}
	detailW := avail / 2
	repairW := avail - detailW
	if detailW < 10 {
		detailW = 10
	}
	if repairW < 10 {
		repairW = 10
	}

	fmt.Printf("  %s %s %s %s %s\n",
		padLeft("#", 2), padRight("名称", nameW), padRight("状态", statusW),
		padRight("说明", detailW), padRight("修复建议", repairW))
	doctorHR(width)

	if len(r.Checks) == 0 {
		fmt.Println("  （没有检查项）")
		return
	}
	for i, c := range r.Checks {
		name := orDash(strings.TrimSpace(c.Name))
		detail := orDash(strings.TrimSpace(c.Detail))
		repair := "无需修复"
		if strings.TrimSpace(c.Repair) != "" {
			repair = doctorRepairLabel(c.Repair)
		}
		fmt.Printf("  %s %s %s %s %s\n",
			padLeft(strconv.Itoa(i+1), 2),
			padRight(truncate(name, nameW), nameW),
			doctorStatusCell(c.Status, statusW),
			padRight(truncate(detail, detailW), detailW),
			padRight(truncate(repair, repairW), repairW))
	}
	doctorHR(width)
}

func doctorHR(width int) {
	fmt.Printf("  %s%s%s\n", ColorGrey, strings.Repeat("─", maxInt(20, width-4)), ColorReset)
}

func doctorStatusCell(status string, width int) string {
	text, color := doctorStatusLabel(status)
	return color + padRight(text, width) + ColorReset
}

func doctorStatusLabel(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "pass", "healthy":
		return "正常", ColorGreen
	case "warn", "warning", "degraded":
		return "注意", ColorYellow
	case "error", "err", "fail", "failed":
		return "错误", ColorRed
	default:
		return "未知", ColorGrey
	}
}

// showDoctorCheck 展示单项的完整说明并处理修复确认。
func showDoctorCheck(e rpc.API, c DoctorCheck) {
	Clear()
	Title("自检项目 · " + orDash(c.Name))

	status, color := doctorStatusLabel(c.Status)
	fmt.Printf("  状态    %s%s%s\n", color, status, ColorReset)
	printDoctorField("名称", c.Name)
	printDoctorField("说明", c.Detail)

	action := strings.TrimSpace(c.Repair)
	if action == "" {
		fmt.Println("  修复    该检查不提供自动修复。请按上方说明手动处理；")
		fmt.Println("          处理后返回本页重新检查。")
		Pause("按回车返回自检页...")
		return
	}
	if !doctorRepairSupported(action) {
		fmt.Printf("  %s修复    UI 未识别修复动作 %q；为避免误操作，本页不会执行它。%s\n", ColorRed, action, ColorReset)
		fmt.Println("          请更新面板/daemon，或按说明手动处理。")
		Pause("按回车返回自检页...")
		return
	}

	fmt.Printf("  修复    %s（动作 %s）\n", doctorRepairLabel(action), action)
	fmt.Println("          仅在确认后执行；cloudflared 修复可能需要联网下载，其他动作只做本地安全修复。")
	if strings.EqualFold(strings.TrimSpace(c.Status), "ok") {
		fmt.Println("          当前检查正常，通常无需执行该修复。")
	}
	if !Confirm("  确认执行该修复?") {
		LogWarn("已取消修复")
		Pause("按回车返回自检页...")
		return
	}

	Clear()
	Title("正在修复 · " + orDash(c.Name))
	fmt.Print("  正在执行本地修复 ...")
	lines, err := e.Repair(action)
	fmt.Println()
	for _, line := range lines {
		fmt.Println("  · " + line)
	}
	if err != nil {
		fmt.Printf("\n  %s[失败] 修复未完成: %v%s\n", ColorRed, err, ColorReset)
		fmt.Println("  可执行路径：")
		fmt.Println("    · 重试：返回自检页 r 重新检查，再选择同一项重试")
		fmt.Println("    · 离线：若涉及 cloudflared，使用已有缓存或稍后重试；不会绕过校验")
		fmt.Println("    · 诊断：4 查看运行日志，或 7 本体维护查看二进制与缓存状态")
	} else {
		fmt.Println("  ✓ 修复动作已完成。返回自检页后会自动重新检查。")
	}
	Pause("按回车返回自检页...")
}

func printDoctorField(label, value string) {
	width := termWidth() - 14
	if width < 20 {
		width = 20
	}
	lines := wrapText(orDash(value), width)
	for i, line := range lines {
		if i == 0 {
			fmt.Printf("  %s%s%s%s\n", ColorGrey, padRight(label, 8), ColorReset, line)
		} else {
			fmt.Printf("  %s%s\n", strings.Repeat(" ", 8), line)
		}
	}
}

// doctorRepairLabel 把稳定的 action id 映射成中文，避免把内部标识直接当建议展示。
func doctorRepairLabel(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "chmod":
		return "修复执行权限"
	case "cloudflared":
		return "恢复/重装 cloudflared"
	case "locks":
		return "清理陈旧 lock/socket"
	case "env":
		return "修复运行时依赖"
	case "config":
		return "修复配置目录/文件"
	case "all":
		return "执行安全本地修复"
	default:
		return action
	}
}

func doctorRepairSupported(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "chmod", "cloudflared", "locks", "env", "config", "all":
		return true
	default:
		return false
	}
}
