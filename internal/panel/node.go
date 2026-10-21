package panel

import (
	"argoz/internal/rpc"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func stateCell(state string, w int) string {
	text, color := stateText(state)
	return color + padRight(text, w) + ColorReset
}

func stateColor(state string) string {
	_, c := stateText(state)
	return c
}

// showNodeList 节点列表：宽度自适应 + 分页 + 搜索；返回选中的节点 ID
func showNodeList(e rpc.API) string {
	const perPage = 15
	filter := ""
	page := 0

	for {
		all := e.Statuses()
		view := make([]NodeStatus, 0, len(all))
		for _, st := range all {
			if nodeFilterMatch(st, filter) {
				view = append(view, st)
			}
		}

		pages := (len(view) + perPage - 1) / perPage
		if pages == 0 {
			pages = 1
		}
		if page >= pages {
			page = pages - 1
		}
		if page < 0 {
			page = 0
		}

		Clear()
		title := "节点列表"
		if filter != "" {
			title += " · 筛选「" + filter + "」"
		}
		Title(title)
		fmt.Printf("  %s第 %d/%d 页 · 共 %d 个%s\n\n", ColorGrey, page+1, pages, len(view), ColorReset)

		if len(all) == 0 {
			fmt.Println("  当前没有任何节点。")
			Pause("按回车返回（可从主菜单选 2 新建节点）...")
			return ""
		}
		if len(view) == 0 {
			fmt.Printf("  %s没有匹配「%s」的节点%s\n\n", ColorYellow, filter, ColorReset)
			fmt.Println("  x 清空筛选     0 返回")
			switch s := strings.TrimSpace(ReadLine("\n  > ")); s {
			case "x", "X":
				filter = ""
			case "0", "":
				return ""
			}
			continue
		}

		w := termWidth()
		showDomain := w >= 96
		showProto := w >= 76

		switch {
		case showDomain:
			fmt.Printf("  %s %s %s %s %s %s %s\n",
				padLeft("#", 2), padRight("名称", 18), padRight("协议", 7),
				padLeft("端口", 5), padRight("域名", 30), padRight("状态", 10), padLeft("连接", 5))
		case showProto:
			fmt.Printf("  %s %s %s %s %s %s\n",
				padLeft("#", 2), padRight("名称", 18), padRight("协议", 7),
				padLeft("端口", 5), padRight("状态", 10), padLeft("连接", 5))
		default:
			fmt.Printf("  %s %s %s %s %s\n",
				padLeft("#", 2), padRight("名称", 14),
				padLeft("端口", 5), padRight("状态", 10), padLeft("连接", 5))
		}
		HR()

		start := page * perPage
		end := start + perPage
		if end > len(view) {
			end = len(view)
		}
		var issue *NodeStatus
		for i := start; i < end; i++ {
			st := view[i]
			domain := st.Node.Tunnel.Domain
			if domain == "" {
				domain = "(未分配)"
			}
			switch {
			case showDomain:
				fmt.Printf("  %s %s %s %s %s %s %s\n",
					padLeft(strconv.Itoa(i+1), 2), padRight(truncate(st.Node.Name, 18), 18),
					padRight(st.Node.Protocol, 7), padLeft(strconv.Itoa(st.Node.Port), 5),
					padRight(truncate(domain, 30), 30), stateCell(st.State, 10),
					padLeft(strconv.Itoa(int(st.Conns)), 5))
			case showProto:
				fmt.Printf("  %s %s %s %s %s %s\n",
					padLeft(strconv.Itoa(i+1), 2), padRight(truncate(st.Node.Name, 18), 18),
					padRight(st.Node.Protocol, 7), padLeft(strconv.Itoa(st.Node.Port), 5),
					stateCell(st.State, 10), padLeft(strconv.Itoa(int(st.Conns)), 5))
			default:
				fmt.Printf("  %s %s %s %s %s\n",
					padLeft(strconv.Itoa(i+1), 2), padRight(truncate(st.Node.Name, 14), 14),
					padLeft(strconv.Itoa(st.Node.Port), 5), stateCell(st.State, 10),
					padLeft(strconv.Itoa(int(st.Conns)), 5))
			}
			if issue == nil && (st.Err != "" || st.State == "degraded" || st.State == "err") {
				s := st
				issue = &s
			}
		}
		HR()
		if issue != nil {
			fmt.Printf("  %s节点 [%s]：%s%s%s\n", ColorGrey, issue.Node.Name,
				truncate(issue.Err, 60), retryHint(*issue), ColorReset)
		}

		fmt.Printf("\n  1-%d 选择    n/p 翻页    / 搜索    r 刷新    0 返回\n", len(view))
		choice := strings.TrimSpace(ReadLine("\n  > "))
		switch {
		case choice == "" || choice == "0":
			return ""
		case strings.EqualFold(choice, "r"):
			continue
		case strings.EqualFold(choice, "n"):
			page++
		case strings.EqualFold(choice, "p"):
			page--
		case choice == "/":
			filter = strings.TrimSpace(ReadLine("  搜索关键字: "))
			page = 0
		case strings.EqualFold(choice, "x"):
			filter = ""
			page = 0
		default:
			idx, err := strconv.Atoi(choice)
			if err != nil || idx < 1 || idx > len(view) {
				LogErr("无效编号")
				Pause("")
				continue
			}
			return view[idx-1].Node.ID
		}
	}
}

// detailLines 生成详情页内容（按屏切分显示）
func detailLines(node Node, st NodeStatus, settings Settings) []string {
	w := termWidth()
	var lines []string

	if st.Restarts > 0 || !st.NextRetry.IsZero() || st.Err != "" {
		info := "重启 " + strconv.Itoa(st.Restarts) + " 次"
		if !st.NextRetry.IsZero() {
			if d := time.Until(st.NextRetry); d > 0 {
				if d < time.Second {
					info += "   即将重试"
				} else {
					info += "   " + durationText(d) + "后重试"
				}
			}
		}
		lines = append(lines, "  "+ColorGrey+info+ColorReset)
		if st.Err != "" {
			lines = append(lines, "  "+ColorRed+"错误 "+truncate(st.Err, w-8)+ColorReset)
		}
		lines = append(lines, "")
	}

	traffic := "↑" + humanBytes(st.Up) + " ↓" + humanBytes(st.Down)
	lines = append(lines,
		fmt.Sprintf("  协议    %-14s 监听    %s:%d", node.Protocol, settings.Listen, node.Port),
		fmt.Sprintf("  路径    %-14s 隧道    %s · %s", orDash(node.Path), modeText(node.Tunnel.Mode), orDash(node.Tunnel.Domain)),
		fmt.Sprintf("  流量    %-14s 连接    %d", traffic, st.Conns),
		"",
	)

	uri := node.URI(settings)
	lines = append(lines, "  "+ColorCyan+"── 分享链接 ────────────────────────────────────"+ColorReset)
	if uri == "" {
		lines = append(lines, "  "+ColorYellow+"（域名尚未就绪，隧道成功后可生成）"+ColorReset)
	} else {
		for _, seg := range wrapText(uri, w-4) {
			lines = append(lines, "  "+seg)
		}
	}

	nl := string(byte(10))
	yaml := strings.TrimRight(node.YAML(settings), nl)
	lines = append(lines, "", "  "+ColorCyan+"── Clash / Mihomo ──────────────────────────────"+ColorReset)
	if yaml == "" {
		lines = append(lines, "  "+ColorYellow+"（域名尚未就绪）"+ColorReset)
	} else {
		for _, l := range strings.Split(yaml, nl) {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}

// showNodeDetail 节点详情页：内容分屏显示
func showNodeDetail(e rpc.API, id string) {
	page := 0
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

		lines := detailLines(node, st, settings)
		h := termHeight() - 9
		if h < 8 {
			h = 8
		}
		pages := (len(lines) + h - 1) / h
		if pages == 0 {
			pages = 1
		}
		if page >= pages {
			page = 0
		}

		Clear()
		Title(fmt.Sprintf("节点 %s   %s", node.ID, node.Name))
		stText, stCol := stateText(st.State)
		fmt.Printf("  %s%s%s\n\n", stCol, stText, ColorReset)

		start := page * h
		end := start + h
		if end > len(lines) {
			end = len(lines)
		}
		for _, l := range lines[start:end] {
			fmt.Println(l)
		}

		fmt.Printf("\n  %s第 %d/%d 屏%s    n 下一屏   p 上一屏\n", ColorGrey, page+1, pages, ColorReset)
		fmt.Printf("  1 复制链接   2 复制 YAML   3 写入文件   4 编辑\n")
		fmt.Printf("  5 重启隧道   6 删除        7 自检       0 返回\n\n")

		uri := node.URI(settings)
		switch ReadLine("  请输入选项 > ") {
		case "n", "N":
			page++
		case "p", "P":
			page--
		case "1":
			if uri == "" {
				LogWarn("域名尚未就绪，暂无链接")
			} else {
				copyClipboard(uri)
				LogOK("链接已发送到系统剪贴板（OSC52，终端不支持时请手动选中）")
				fmt.Println("  " + uri)
			}
			Pause("")
		case "2":
			if yaml := node.YAML(settings); yaml == "" {
				LogWarn("域名尚未就绪，暂无 YAML")
			} else {
				copyClipboard(yaml)
				LogOK("YAML 已发送到系统剪贴板")
			}
			Pause("")
		case "3":
			body := strings.Join([]string{uri, "", strings.TrimRight(node.YAML(settings), string(byte(10)))}, string(byte(10)))
			if strings.TrimSpace(body) == "" {
				LogWarn("域名尚未就绪，暂无可写入内容")
				Pause("")
				continue
			}
			p, err := exportToFile(e.DataDir(), node.Name, body)
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
		case "7":
			showDiag(e, id)
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

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// showEditMenu 编辑节点
func showEditMenu(e rpc.API, id string) {
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
		fmt.Printf("  5 隧道        %s · %s\n", modeText(node.Tunnel.Mode), orDash(node.Tunnel.Domain))
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
func waitDomain(e rpc.API, id string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n, ok := e.Node(id); ok && n.Tunnel.Domain != "" {
			return n.Tunnel.Domain, true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", false
}

// wizardCreate 新建节点向导
func wizardCreate(e rpc.API) {
	Clear()
	Title("新建节点")

	defaultName := fmt.Sprintf("Argo-VLESS-%d", len(e.Nodes())+1)
	name := ReadLine(fmt.Sprintf("\n  名称 (默认 %s): ", defaultName))
	if name == "" {
		name = defaultName
	}

	fmt.Println("\n  协议   [1] vless    [2] trojan")
	proto := "vless"
	if ReadLine("  请选择 (默认 1): ") == "2" {
		proto = "trojan"
	}

	port := promptPort(e)
	path := promptPath()
	cred := promptCredential(e, proto)

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
	}

	fmt.Printf("\n  %s── 即将创建 ──────────────────────────────────────%s\n", ColorCyan, ColorReset)
	fmt.Printf("  名称    %s\n", name)
	fmt.Printf("  协议    %s\n", proto)
	if port == 0 {
		fmt.Printf("  端口    自动分配\n")
	} else {
		fmt.Printf("  端口    %d\n", port)
	}
	if path == "" {
		fmt.Printf("  路径    随机生成\n")
	} else {
		fmt.Printf("  路径    %s\n", path)
	}
	switch {
	case cred == "":
		fmt.Printf("  凭据    自动生成\n")
	case proto == "vless":
		fmt.Printf("  UUID    %s\n", cred)
	default:
		fmt.Printf("  密码    %s\n", cred)
	}
	if mode == "temp" {
		fmt.Printf("  隧道    临时\n")
	} else {
		fmt.Printf("  隧道    固定 · %s\n", domain)
	}

	if !ConfirmYes("\n  确认创建?") {
		LogWarn("已取消创建")
		Pause("")
		return
	}

	spec := NodeSpec{
		Name: name, Protocol: proto, Mode: mode, Token: token, Domain: domain,
		Port: port, Path: path,
	}
	if proto == "vless" {
		spec.UUID = cred
	} else {
		spec.Password = cred
	}

	fmt.Println()
	node, err := e.Create(spec)
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

// promptPort 询问端口；回车 = 自动分配
func promptPort(e rpc.API) int {
	for {
		raw := ReadLine("  端口 (回车自动分配): ")
		if raw == "" {
			return 0
		}
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			LogErr("端口需为 1-65535，请重新输入")
			continue
		}
		if err := e.CheckPortFree(p, ""); err != nil {
			LogErr("%v", err)
			continue
		}
		return p
	}
}

// promptPath 询问 WS 路径；回车 = 随机生成
func promptPath() string {
	for {
		raw := ReadLine("  WS 路径 (回车随机生成): ")
		if raw == "" {
			return ""
		}
		if !strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, " \t") {
			LogErr("路径需以 / 开头且不含空白")
			continue
		}
		return raw
	}
}

// promptCredential 询问 UUID / 密码；回车 = 自动生成
func promptCredential(e rpc.API, proto string) string {
	if proto == "trojan" {
		for {
			raw := ReadLine("  密码 (回车随机生成，8-64 位): ")
			if raw == "" {
				return ""
			}
			if len(raw) < 8 || len(raw) > 64 || strings.ContainsAny(raw, " \t") {
				LogErr("密码需为 8-64 位且不含空白")
				continue
			}
			if dup := e.FindByCredential(raw); dup != "" {
				LogWarn("该密码与节点 [%s] 重复", dup)
				if !Confirm("  确认继续?") {
					continue
				}
			}
			return raw
		}
	}

	for {
		raw := ReadLine("  UUID (回车自动生成): ")
		if raw == "" {
			return ""
		}
		if _, err := parseUUID(raw); err != nil {
			LogErr("UUID 格式非法，请重新输入")
			continue
		}
		norm := normalizeUUID(raw)
		if dup := e.FindByCredential(norm); dup != "" {
			LogWarn("该 UUID 与节点 [%s] 重复", dup)
			if !Confirm("  确认继续?") {
				continue
			}
		}
		return norm
	}
}
