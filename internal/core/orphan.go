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
