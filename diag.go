package main

import (
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
func showDiag(e *Engine, id string) {
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
		fmt.Printf("  %s✗ 本地监听%s  %-24s 连接失败：%v\n", ColorRed, ColorReset, addr, err)
	} else {
		c.Close()
		fmt.Printf("  %s✓ 本地监听%s  %-24s 可连接\n", ColorGreen, ColorReset, addr)
	}

	domain := node.Tunnel.Domain
	if domain == "" {
		fmt.Printf("  %s✗ 隧道域名%s  尚未分配\n", ColorRed, ColorReset)
		Pause("按回车返回...")
		return
	}

	// 2. DNS
	if ips, err := net.LookupHost(domain); err != nil || len(ips) == 0 {
		fmt.Printf("  %s✗ DNS%s        %-24s 解析失败：%v\n", ColorRed, ColorReset, domain, err)
	} else {
		fmt.Printf("  %s✓ DNS%s        %-24s → %s\n", ColorGreen, ColorReset, domain, strings.Join(ips, ", "))
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
		fmt.Printf("  %s✗ 端到端%s      %-24s 请求失败：%v\n", ColorRed, ColorReset, url, err)
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		ms := time.Since(start).Milliseconds()

		tag, mark := ColorGreen, "✓"
		if resp.StatusCode >= 500 {
			tag, mark = ColorRed, "✗"
		}
		fmt.Printf("  %s%s 端到端%s      HTTP %d  %dms  server=%s\n", tag, mark, ColorReset, resp.StatusCode, ms, resp.Header.Get("Server"))
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
			fmt.Printf("      %s提示：1033/530 多为 CF 面板路由未指向 http://localhost:%d%s\n", ColorGrey, node.Port, ColorReset)
		}
	}

	if s.ECH {
		fmt.Printf("\n  %s提示：ECH 注入已开启；若客户端连不上，先到 设置 → 5 关闭，再重新复制链接%s\n", ColorYellow, ColorReset)
	}
	Pause("按回车返回...")
}
