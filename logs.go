package main

import (
	"bufio"
	"fmt"
	"os"
)

// tailLines 读取文件最后 n 行
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines, sc.Err()
}

// showLogs 日志查看页
func showLogs(e *Engine) {
	Clear()
	Title("运行日志")

	p := LogFilePath()
	fmt.Printf("  文件  %s\n", orDash(p))
	fmt.Printf("  等级  %s（可在 设置 → 6 调整）\n", e.Settings().LogLevel)
	fmt.Printf("  轮转  超过 5MB 自动切分为 argoz.log.1\n\n")

	lines, err := tailLines(p, 40)
	if err != nil {
		fmt.Printf("  %s读取失败: %v%s\n", ColorYellow, err, ColorReset)
		Pause("按回车返回...")
		return
	}
	if len(lines) == 0 {
		fmt.Println("  (暂无日志)")
		Pause("按回车返回...")
		return
	}

	for _, l := range lines {
		fmt.Println("  " + truncate(l, 130))
	}
	Pause("按回车返回...")
}
