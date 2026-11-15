package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// isCloudflaredProcess 判断 argv[0] 是否是 cloudflared。
func isCloudflaredProcess(arg0 string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(arg0)))
	return base == "cloudflared" || base == "cloudflared.exe"
}

// hasArg 判断参数列表里是否有指定 flag。
func hasArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

// isOurCloudflared 判断进程是否是由 argoz 启动的 cloudflared 隧道进程。
// 旧版本固定带 --no-autoupdate，可直接据此判定；开启 cloudflared 自带自更新后不再传该
// flag，改按「tunnel 子命令 + --url/--token」识别，避免孤儿进程漏清理。
func isOurCloudflared(args []string) bool {
	if len(args) == 0 || !isCloudflaredProcess(args[0]) {
		return false
	}
	if hasArg(args, "--no-autoupdate") {
		return true
	}
	return hasArg(args, "tunnel") && (hasArg(args, "--url") || hasArg(args, "--token"))
}

// hasArgValue 匹配 "--name value" 或 "--name=value"。
func hasArgValue(args []string, name, value string) bool {
	for i, arg := range args {
		if arg == name && i+1 < len(args) && args[i+1] == value {
			return true
		}
		if strings.HasPrefix(arg, name+"=") && strings.TrimPrefix(arg, name+"=") == value {
			return true
		}
	}
	return false
}

// orphanMatchesNode 只匹配当前配置中的节点，避免误杀其他 cloudflared 实例。
func orphanMatchesNode(args []string, n Node) bool {
	if len(args) == 0 || !isCloudflaredProcess(args[0]) {
		return false
	}
	if n.Tunnel.Mode == "fixed" {
		token := strings.TrimSpace(n.Tunnel.Token)
		if token == "" {
			return false
		}
		return hasArgValue(args, "--token", token)
	}
	if n.Port <= 0 {
		return false
	}
	return hasArgValue(args, "--url", fmt.Sprintf("http://127.0.0.1:%d", n.Port))
}
