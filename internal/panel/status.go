package panel

import (
	"argoz/internal/rpc"
	"fmt"
	"strconv"
	"time"
)

// showStatus 运行状态：采样 1 秒计算实时速率
func showStatus(e rpc.API) {
	prev := e.Statuses()
	prevAt := time.Now()
	time.Sleep(time.Second)
	cur := e.Statuses()
	now := time.Now()

	dt := now.Sub(prevAt).Seconds()
	if dt <= 0 {
		dt = 1
	}
	prevMap := make(map[string]NodeStatus, len(prev))
	for _, s := range prev {
		prevMap[s.Node.ID] = s
	}

	var totUp, totDown float64
	conns := 0
	for _, s := range cur {
		p := prevMap[s.Node.ID]
		totUp += float64(s.Up-p.Up) / dt
		totDown += float64(s.Down-p.Down) / dt
		conns += int(s.Conns)
	}

	Clear()
	Title("运行状态")
	fmt.Printf("  总吞吐    ↑ %s/s    ↓ %s/s        连接 %d\n\n",
		humanBytes(int64(totUp)), humanBytes(int64(totDown)), conns)

	fmt.Printf("  %s %s %s %s %s %s\n",
		padLeft("#", 2), padRight("名称", 18), padRight("状态", 10),
		padLeft("连接", 5), padLeft("上行", 10), padLeft("下行", 10))
	HR()
	for i, s := range cur {
		p := prevMap[s.Node.ID]
		ru := humanBytes(int64(float64(s.Up-p.Up)/dt)) + "/s"
		rd := humanBytes(int64(float64(s.Down-p.Down)/dt)) + "/s"
		fmt.Printf("  %s %s %s %s %s %s\n",
			padLeft(strconv.Itoa(i+1), 2), padRight(truncate(s.Node.Name, 18), 18),
			stateCell(s.State, 10), padLeft(strconv.Itoa(int(s.Conns)), 5),
			padLeft(ru, 10), padLeft(rd, 10))
	}
	HR()

	info := e.Info()
	fmt.Printf("\n  %s守护进程   已运行 %s · 内存 %s · 协程 %d%s\n",
		ColorGrey, durationText(time.Duration(info.UptimeSec)*time.Second),
		humanBytes(info.MemBytes), info.Goroutines, ColorReset)
	Pause("\n按回车返回...")
}
