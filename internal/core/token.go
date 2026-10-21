package core

import "strings"

// ExtractToken 从粘贴内容里提取隧道 Token：支持纯 token 或整条安装命令。
func ExtractToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Trim(s, "\"' \r\n")

	fields := strings.Fields(s)
	for i, f := range fields {
		if f == "--token" && i+1 < len(fields) {
			return strings.Trim(fields[i+1], "\"'")
		}
		if strings.HasPrefix(f, "--token=") {
			return strings.Trim(strings.TrimPrefix(f, "--token="), "\"'")
		}
	}
	last := strings.Trim(fields[len(fields)-1], "\"'")
	return last
}
