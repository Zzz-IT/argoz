package panel

import (
	"argoz/internal/rpc"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// showDiag 节点自检：本地监听 / DNS / 端到端 HTTPS，用来定位「连不上」卡在哪一层
func showDiag(e rpc.API, id string) {
	node, ok := e.Node(id)
	if !ok {
		return
	}
	s := e.Settings()

	Clear()
	Title("节点自检 · " + node.Name)

	// 1. 本地监听
	addr := net.JoinHostPort(s.Listen, strconv.Itoa(node.Port))
	if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err != nil {
		fmt.Printf("  %s✗ %s%s %-26s 连接失败：%v\n", ColorRed, padRight("本地监听", 10), ColorReset, addr, err)
	} else {
		c.Close()
		fmt.Printf("  %s✓ %s%s %-26s 可连接\n", ColorGreen, padRight("本地监听", 10), ColorReset, addr)
	}

	domain := node.Tunnel.Domain
	if domain == "" {
		fmt.Printf("  %s✗ %s%s 尚未分配\n", ColorRed, padRight("隧道域名", 10), ColorReset)
		Pause("按回车返回...")
		return
	}

	// 2. DNS
	if ips, err := net.LookupHost(domain); err != nil || len(ips) == 0 {
		fmt.Printf("  %s✗ %s%s %-26s 解析失败：%v\n", ColorRed, padRight("域名解析", 10), ColorReset, domain, err)
	} else {
		fmt.Printf("  %s✓ %s%s %-26s → %s\n", ColorGreen, padRight("域名解析", 10), ColorReset, domain, strings.Join(ips, ", "))
	}

	// 3. 端到端 HTTPS
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	}
	url := "https://" + domain + node.Path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		LogErr("构造请求失败: %v", err)
		Pause("按回车返回...")
		return
	}
	req.Header.Set("User-Agent", "argoz-doctor")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  %s✗ %s%s %-26s 请求失败：%v\n", ColorRed, padRight("端到端访问", 10), ColorReset, url, err)
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		ms := time.Since(start).Milliseconds()

		tag, mark := ColorGreen, "✓"
		if resp.StatusCode >= 500 {
			tag, mark = ColorRed, "✗"
		}
		fmt.Printf("  %s%s %s%s HTTP %d  %dms  server=%s\n", tag, mark, padRight("端到端访问", 10), ColorReset, resp.StatusCode, ms, resp.Header.Get("Server"))
		if ray := resp.Header.Get("CF-Ray"); ray != "" {
			fmt.Printf("      CF-Ray: %s\n", ray)
		}
		if snippet := strings.TrimSpace(string(body)); snippet != "" {
			if len(snippet) > 160 {
				snippet = snippet[:160]
			}
			fmt.Printf("      响应: %s\n", strings.ReplaceAll(snippet, "\n", " "))
		}
		if resp.StatusCode >= 500 {
			fmt.Printf("      %s提示：返回 1033/530 多为面板路由未指向 http://localhost:%d%s\n", ColorGrey, node.Port, ColorReset)
		}
	}

	if s.ECH {
		fmt.Printf("\n  %s提示：ECH 注入已开启；若客户端连不上，先到 设置 → 5 关闭，再重新复制链接%s\n", ColorYellow, ColorReset)
	}
	Pause("按回车返回...")
}
