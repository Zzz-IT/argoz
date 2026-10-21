package sys

import (
	"argoz/internal/logx"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// alpineMissingPackages 返回 Alpine 精简系统缺少的运行时依赖。
// root 仅用于测试；生产传空字符串。
func AlpineMissingPackages(root string) []string {
	exists := func(p string) bool {
		path := p
		if root != "" {
			path = filepath.Join(root, strings.TrimPrefix(p, "/"))
		}
		_, err := os.Stat(path)
		return err == nil
	}
	var pkgs []string
	if !exists("/etc/ssl/certs/ca-certificates.crt") {
		pkgs = append(pkgs, "ca-certificates")
	}
	if !exists("/usr/share/zoneinfo") {
		pkgs = append(pkgs, "tzdata")
	}
	loader := "/lib/ld-linux-x86-64.so.2"
	if runtime.GOARCH == "arm64" {
		loader = "/lib/ld-linux-aarch64.so.1"
	}
	if !exists(loader) {
		pkgs = append(pkgs, "gcompat")
	}
	return pkgs
}

// checkRuntimeEnv 在 Alpine 上做一次轻量自检；失败只告警，不阻塞 daemon。
func CheckRuntimeEnv() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := os.Stat("/etc/alpine-release"); err != nil {
		// 非 Alpine 不自愈，但缺少 CA 证书时给一条明确告警
		if _, err := os.Stat("/etc/ssl/certs/ca-certificates.crt"); err != nil {
			logx.LogWarn("系统缺少 CA 证书（/etc/ssl/certs/ca-certificates.crt），HTTPS 探测与 cloudflared 下载可能失败")
		}
		return
	}
	if _, err := exec.LookPath("apk"); err != nil {
		logx.LogWarn("Alpine 环境未找到 apk，跳过运行时依赖自检")
		return
	}

	missing := AlpineMissingPackages("")
	if len(missing) == 0 {
		return
	}
	if os.Geteuid() != 0 {
		logx.LogWarn("Alpine 缺少 %s；当前非 root，无法自动安装，请执行: apk add --no-cache %s",
			strings.Join(missing, ", "), strings.Join(missing, " "))
		return
	}

	logx.LogWarn("Alpine 缺少运行时依赖 %s，尝试 apk add ...", strings.Join(missing, ", "))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	base := make([]string, 0, len(missing))
	for _, pkg := range missing {
		if pkg != "gcompat" {
			base = append(base, pkg)
		}
	}
	if len(base) > 0 {
		args := append([]string{"add", "--no-cache"}, base...)
		out, err := exec.CommandContext(ctx, "apk", args...).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			logx.LogWarn("apk add 失败（不影响已有节点，但 HTTPS/cloudflared 可能异常）：%s", msg)
			return
		}
		if containsString(base, "ca-certificates") {
			_ = exec.Command("update-ca-certificates").Run()
		}
	}
	if containsString(missing, "gcompat") {
		out, err := exec.CommandContext(ctx, "apk", "add", "--no-cache", "gcompat").CombinedOutput()
		if err != nil {
			logx.LogWarn("gcompat 安装失败（cloudflared 若不是静态链接可能无法启动）：%s", strings.TrimSpace(string(out)))
		}
	}
	logx.LogOK("Alpine 运行时依赖已补齐: %s", strings.Join(missing, ", "))
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
