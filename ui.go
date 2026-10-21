package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	ColorRed    = "\033[1;31m"
	ColorGreen  = "\033[0;32m"
	ColorYellow = "\033[0;33m"
	ColorCyan   = "\033[0;36m"
	ColorWhite  = "\033[1;37m"
	ColorGrey   = "\033[0;37m"
	ColorReset  = "\033[0m"
)

// 全局唯一 Reader：避免每次输入都新建 bufio 导致预输入丢失
var stdinReader = bufio.NewReader(os.Stdin)

// ReadLine 读取一行输入（去掉首尾空白）
func ReadLine(prompt string) string {
	fmt.Print(prompt)
	s, err := stdinReader.ReadString('\n')
	if err != nil && s == "" {
		return ""
	}
	return strings.TrimSpace(s)
}

// Pause 等待回车
func Pause(msg string) {
	if msg == "" {
		msg = "按回车键继续..."
	}
	fmt.Printf("\n  %s%s%s", ColorGrey, msg, ColorReset)
	_, _ = stdinReader.ReadString('\n')
}

// Confirm y/N 确认
func Confirm(prompt string) bool {
	return strings.EqualFold(ReadLine(prompt+" (y/N): "), "y")
}

// Clear 清屏
func Clear() {
	fmt.Print("\033[H\033[2J")
}

// Title 打印页头
func Title(s string) {
	fmt.Printf("\n  %s%s%s\n", ColorCyan, s, ColorReset)
	fmt.Printf("  %s──────────────────────────────────────────────────────────────%s\n", ColorGrey, ColorReset)
}

// HR 打印分隔线
func HR() {
	fmt.Printf("  %s──────────────────────────────────────────────────────────────%s\n", ColorGrey, ColorReset)
}
