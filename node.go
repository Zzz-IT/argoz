package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func stateCell(state string, w int) string {
	var text, color string
	switch state {
	case "up":
		text, color = "● up", ColorGreen
	case "err":
		text, color = "✖ err", ColorRed
	case "starting":
		text, color = "… init", ColorYellow
	case "degraded":
		text, color = "▲ 降级", ColorYellow
	default:
		text, color = "▲ down", ColorYellow
	}
	return color + padRight(text, w) + ColorReset
}

// showNodeList 节点列表；返回选中的节点 ID（空串表示返回）
func showNodeList(e *Engine) string {
	for {
		sts := e.Statuses()
		Clear()
		Title("节点列表")
		if len(sts) == 0 {
			fmt.Println("\n  当前没有任何节点。")
			Pause("按回车返回（可从主菜单选 2 新建节点）...")
			return ""
		}

		fmt.Printf("  %s %s %s %s %s %s %s\n",
			padLeft("#", 2), padRight("名称", 18), padRight("协议", 7),
			padLeft("端口", 5), padRight("域名", 34), padRight("状态", 8), padLeft("连接", 5))
		HR()
		for i, st := range sts {
			domain := st.Node.Tunnel.Domain
			if domain == "" {
				domain = "(未分配)"
			}
			fmt.Printf("  %s %s %s %s %s %s %s\n",
				padLeft(strconv.Itoa(i+1), 2),
				padRight(st.Node.Name, 18),
				padRight(st.Node.Protocol, 7),
				padLeft(strconv.Itoa(st.Node.Port), 5),
				padRight(domain, 34),
				stateCell(st.State, 8),
				padLeft(strconv.Itoa(int(st.Conns)), 5),
			)
			if st.Err != "" || !st.NextRetry.IsZero() {
				fmt.Printf("     %s%s%s%s\n", ColorGrey, truncate(st.Err, 60), retryHint(st), ColorReset)
			}
		}
		HR()

		choice := ReadLine("\n  输入编号进入详情 (0 返回): ")
		if choice == "0" || choice == "" {
			return ""
		}
		idx, err := strconv.Atoi(choice)
		if err != nil || idx < 1 || idx > len(sts) {
			LogErr("无效编号")
			Pause("")
			continue
		}
		return sts[idx-1].Node.ID
	}
}

// showNodeDetail 节点详情页
func showNodeDetail(e *Engine, id string) {
	for {
		node, ok := e.Node(id)
		if !ok {
			return
		}
		settings := e.Settings()

		var st NodeStatus
		for _, s := range e.Statuses() {
			if s.Node.ID == id {
				st = s
			}
		}

		Clear()
		Title(fmt.Sprintf("节点 %s  %s", node.ID, node.Name))
		fmt.Printf("  %s  %s:%d  %s  ↑%s ↓%s  conns %d\n",
			node.Protocol, settings.Listen, node.Port, node.Path,
			humanBytes(st.Up), humanBytes(st.Down), st.Conns)
		fmt.Printf("  隧道 %s  %s %s\n", node.Tunnel.Mode, stateCell(st.State, 8), orDash(node.Tunnel.Domain))
		if st.Restarts > 0 || !st.NextRetry.IsZero() {
			info := fmt.Sprintf("  重启 %d 次", st.Restarts)
			if !st.NextRetry.IsZero() {
				if d := time.Until(st.NextRetry); d > 0 {
					info += fmt.Sprintf("   下次重试 %s 后", humanDuration(d))
				}
			}
			fmt.Printf("  %s%s%s\n", ColorGrey, info, ColorReset)
		}
		if st.Err != "" {
			fmt.Printf("  错误 %s%s%s\n", ColorRed, truncate(st.Err, 90), ColorReset)
		}
		fmt.Println()

		uri := node.URI(settings)
		yaml := node.YAML(settings)

		fmt.Printf("  %s── 分享链接 ─────────────────────────────────────────────────────%s\n", ColorCyan, ColorReset)
		if uri == "" {
			fmt.Printf("  %s(域名尚未就绪，隧道成功后可生成)%s\n", ColorYellow, ColorReset)
		} else {
			fmt.Println("  " + uri)
		}
		fmt.Println()
		fmt.Printf("  %s── Clash / Mihomo ──────────────────────────────────────────────%s\n", ColorCyan, ColorReset)
		if yaml == "" {
			fmt.Printf("  %s(域名尚未就绪)%s\n", ColorYellow, ColorReset)
		} else {
			for _, line := range strings.Split(strings.TrimRight(yaml, "\n"), "\n") {
				fmt.Println("  " + line)
			}
		}
		fmt.Println()

		fmt.Printf("  1 复制链接   2 复制 YAML   3 写入文件   4 编辑   5 重启隧道   6 删除   0 返回\n\n")

		switch ReadLine("  请输入选项 > ") {
		case "1":
			if uri == "" {
				LogWarn("域名尚未就绪，暂无链接")
			} else {
				copyClipboard(uri)
				LogOK("链接已发送到系统剪贴板（OSC52，若终端不支持则请手动选中上面的文本）")
				fmt.Println("  " + uri)
			}
			Pause("")
		case "2":
			if yaml == "" {
				LogWarn("域名尚未就绪，暂无 YAML")
			} else {
				copyClipboard(yaml)
				LogOK("YAML 已发送到系统剪贴板")
			}
			Pause("")
		case "3":
			local, _ := e.Node(id)
			body := strings.Join([]string{local.URI(settings), "", strings.TrimRight(local.YAML(settings), "\n")}, "\n")
			if strings.TrimSpace(body) == "" {
				LogWarn("域名尚未就绪，暂无可写入内容")
				Pause("")
				continue
			}
			p, err := exportToFile(e.dataDir, node.Name, body)
			if err != nil {
				LogErr("写入失败: %v", err)
			} else {
				LogOK("已追加写入: %s", p)
			}
			Pause("")
		case "4":
			showEditMenu(e, id)
		case "5":
			if err := e.RestartTunnel(id); err != nil {
				LogErr("%v", err)
			} else {
				LogInfo("隧道重启中...")
				if node.Tunnel.Mode == "temp" {
					if d, ok := waitDomain(e, id, 30*time.Second); ok {
						LogOK("新域名: %s", d)
					} else {
						LogWarn("域名抓取超时，请稍后在列表查看或再次重启")
					}
				}
			}
			Pause("")
		case "6":
			if Confirm(fmt.Sprintf("  确认删除节点 [%s] ?", node.Name)) {
				if err := e.Delete(id); err != nil {
					LogErr("删除失败: %v", err)
				} else {
					LogOK("节点已删除")
					Pause("")
					return
				}
			}
		case "0", "":
			return
		}
	}
}

// retryHint 生成「多久后重试」的提示文本
func retryHint(st NodeStatus) string {
	if st.NextRetry.IsZero() {
		return ""
	}
	d := time.Until(st.NextRetry)
	if d <= 0 {
		return "  [即将重试]"
	}
	return fmt.Sprintf("  [%s 后重试]", humanDuration(d))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// showEditMenu 编辑节点
func showEditMenu(e *Engine, id string) {
	for {
		node, ok := e.Node(id)
		if !ok {
			return
		}
		Clear()
		Title("编辑节点 " + node.Name)
		credLabel := "UUID"
		if node.Protocol == "trojan" {
			credLabel = "密码"
		}
		fmt.Printf("  1 名称        %s\n", node.Name)
		fmt.Printf("  2 端口        %d\n", node.Port)
		fmt.Printf("  3 WS 路径     %s\n", node.Path)
		fmt.Printf("  4 %-9s %s\n", credLabel, node.Credential())
		fmt.Printf("  5 隧道        %s (%s)\n", node.Tunnel.Mode, orDash(node.Tunnel.Domain))
		fmt.Printf("  0 返回\n\n")

		choice := ReadLine("  请选择要修改的项 > ")
		if choice == "0" || choice == "" {
			return
		}

		switch choice {
		case "1":
			v := ReadLine("  新名称: ")
			if v == "" {
				continue
			}
			if err := e.Update(id, func(n *Node) error { n.Name = v; return nil }); err != nil {
				LogErr("%v", err)
			} else {
				LogOK("名称已更新")
			}
		case "2":
			v := ReadLine(fmt.Sprintf("  新端口 (当前 %d): ", node.Port))
			p, err := strconv.Atoi(v)
			if err != nil || p < 1 || p > 65535 {
				LogErr("端口非法")
				Pause("")
				continue
			}
			if err := e.Update(id, func(n *Node) error { n.Port = p; return nil }); err != nil {
				LogErr("%v", err)
			} else {
				LogOK("端口已更新（隧道已随之重启）")
			}
		case "3":
			v := ReadLine("  新 WS 路径 (需以 / 开头): ")
			if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \t") {
				LogErr("路径格式非法")
				Pause("")
				continue
			}
			if err := e.Update(id, func(n *Node) error { n.Path = v; return nil }); err != nil {
				LogErr("%v", err)
			} else {
				LogOK("路径已更新，请同步更新客户端")
			}
		case "4":
			if node.Protocol == "trojan" {
				v := ReadLine("  新密码 (回车=自动重新生成): ")
				if v == "" {
					v = randHex(16)
				}
				if len(v) < 4 {
					LogErr("密码太短")
					Pause("")
					continue
				}
				if err := e.Update(id, func(n *Node) error { n.Password = v; return nil }); err != nil {
					LogErr("%v", err)
				} else {
					LogOK("密码已更新，请同步更新客户端")
				}
			} else {
				v := ReadLine("  新 UUID (回车=自动重新生成): ")
				if v == "" {
					v = newUUID()
				}
				if _, err := parseUUID(v); err != nil {
					LogErr("UUID 格式非法: %v", err)
					Pause("")
					continue
				}
				if err := e.Update(id, func(n *Node) error { n.UUID = v; return nil }); err != nil {
					LogErr("%v", err)
				} else {
					LogOK("UUID 已更新，请同步更新客户端")
				}
			}
		case "5":
			if node.Tunnel.Mode == "fixed" {
				token := ReadLine("  新 Token (回车不变): ")
				domain := ReadLine("  新域名 (回车不变): ")
				if err := e.Update(id, func(n *Node) error {
					if token != "" {
						n.Tunnel.Token = extractToken(token)
					}
					if domain != "" {
						n.Tunnel.Domain = domain
					}
					return nil
				}); err != nil {
					LogErr("%v", err)
					continue
				}
				if err := e.RestartTunnel(id); err != nil {
					LogErr("%v", err)
				} else {
					LogOK("隧道配置已更新")
				}
			} else {
				LogInfo("临时隧道的域名由 Cloudflare 分配，请使用详情页的 5 重启隧道")
			}
		}
		Pause("")
	}
}

// waitDomain 轮询等待临时隧道拿到域名
func waitDomain(e *Engine, id string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n, ok := e.Node(id); ok && n.Tunnel.Domain != "" {
			return n.Tunnel.Domain, true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", false
}

// RestartTunnel 重启指定节点的隧道
func (e *Engine) RestartTunnel(id string) error {
	e.mu.Lock()
	rt := e.rt[id]
	e.mu.Unlock()
	if rt == nil {
		return errors.New("节点未运行")
	}
	if rt.tun != nil {
		rt.tun.Stop()
	}
	t := NewTunnel(e, id)
	rt.mu.Lock()
	rt.tun = t
	rt.mu.Unlock()
	t.Start()
	return nil
}

// wizardCreate 新建节点向导
func wizardCreate(e *Engine) {
	Clear()
	Title("新建节点")

	settings := e.Settings()
	nodes := e.Nodes()
	defaultName := fmt.Sprintf("Argo-VLESS-%d", len(nodes)+1)

	name := ReadLine(fmt.Sprintf("\n  名称 (默认 %s): ", defaultName))
	if name == "" {
		name = defaultName
	}

	fmt.Println("\n  协议   [1] vless    [2] trojan")
	proto := "vless"
	if ReadLine("  请选择 (默认 1): ") == "2" {
		proto = "trojan"
	}

	fmt.Println("\n  Argo 隧道   [1] 临时 (CF 自动分配域名)    [2] 固定 (Token + 自有域名)")
	mode := "temp"
	token, domain := "", ""
	if ReadLine("  请选择 (默认 1): ") == "2" {
		mode = "fixed"
		raw := ReadLine("  粘贴 Tunnel Token (可直接粘贴整条安装命令): ")
		token = extractToken(raw)
		domain = ReadLine("  绑定域名 (如 argo.example.com): ")
		if token == "" || domain == "" {
			LogErr("固定隧道必须提供 Token 与域名")
			Pause("")
			return
		}
		LogWarn("请确认 CF 面板中该隧道的路由指向 http://localhost:<自动分配的端口>")
		_ = settings
	}

	fmt.Println()
	node, err := e.Create(name, proto, mode, token, domain)
	if err != nil {
		LogErr("创建失败: %v", err)
		Pause("")
		return
	}
	LogOK("节点已创建: %s (端口 %d, 路径 %s)", node.Name, node.Port, node.Path)

	if mode == "temp" {
		fmt.Print("  正在等待 Cloudflare 分配域名 ...")
		if d, ok := waitDomain(e, node.ID, 30*time.Second); ok {
			fmt.Printf("\r  %s✓ 域名: %s%s\n", ColorGreen, d, ColorReset)
		} else {
			fmt.Printf("\r  %s! 域名抓取超时，可在详情页按 5 重启隧道重试%s\n", ColorYellow, ColorReset)
		}
	} else {
		fmt.Println("  固定隧道已启动，域名使用你填写的值")
	}

	Pause("按回车查看节点详情...")
	showNodeDetail(e, node.ID)
}

// extractToken 从整条 cloudflared 命令中提取 Token
func extractToken(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	for _, part := range strings.Fields(input) {
		if strings.HasPrefix(part, "eyJ") {
			return part
		}
	}
	fields := strings.Fields(input)
	if strings.Contains(input, "cloudflared") || strings.Contains(input, "tunnel") {
		return fields[len(fields)-1]
	}
	return input
}
