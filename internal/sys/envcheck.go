package sys

import (
	"argoz/internal/logx"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	RuntimeEnvOK    = "ok"
	RuntimeEnvWarn  = "warn"
	RuntimeEnvError = "error"
)

// RuntimeEnvCheck 是运行时依赖的单向只读检查结果，字段与 doctor.Check 对齐。
type RuntimeEnvCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | error
	Detail string `json:"detail"`
	Repair string `json:"repair,omitempty"`
}

// RuntimeEnvReport 是 Alpine 运行时依赖的纯只读报告。
// 该报告不会安装包；自动修复由 CheckRuntimeEnv 在满足 root/apk 条件时执行一次。
type RuntimeEnvReport struct {
	Alpine        bool              `json:"alpine"`
	Root          bool              `json:"root"`
	CanAutoRepair bool              `json:"can_auto_repair"`
	Checks        []RuntimeEnvCheck `json:"checks"`
	Missing       []string          `json:"missing,omitempty"`
	Healthy       bool              `json:"healthy"`
	Summary       string            `json:"summary"`
}

// AlpineMissingPackages 返回 Alpine 精简系统缺少的运行时依赖。
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
	if !exists(caCertificatePath()) {
		pkgs = append(pkgs, "ca-certificates")
	}
	if !exists("/usr/share/zoneinfo") {
		pkgs = append(pkgs, "tzdata")
	}
	if loader := runtimeLoaderPath(); loader == "" || !exists(loader) {
		pkgs = append(pkgs, "gcompat")
	}
	return pkgs
}

func caCertificatePath() string {
	return "/etc/ssl/certs/ca-certificates.crt"
}

func runtimeLoaderPath() string {
	if runtime.GOARCH == "arm64" {
		return "/lib/ld-linux-aarch64.so.1"
	}
	// 保持历史检测路径；项目 release 支持 amd64/arm64。
	return "/lib/ld-linux-x86-64.so.2"
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func runtimeDependencyPresent(pkg string) bool {
	switch pkg {
	case "ca-certificates":
		return fileExists(caCertificatePath())
	case "tzdata":
		return fileExists("/usr/share/zoneinfo")
	case "gcompat":
		return fileExists(runtimeLoaderPath())
	default:
		return false
	}
}

// CheckRuntimeEnvReport 只检查并汇报，不安装任何包、不修改系统。
func CheckRuntimeEnvReport() RuntimeEnvReport {
	r := RuntimeEnvReport{Checks: []RuntimeEnvCheck{}}
	if runtime.GOOS != "linux" {
		r.Healthy = true
		r.Checks = append(r.Checks, RuntimeEnvCheck{
			Name:   "runtime-dependencies",
			Status: RuntimeEnvWarn,
			Detail: fmt.Sprintf("当前平台 %s 不支持 Alpine 运行时依赖检查", runtime.GOOS),
		})
		r.Summary = fmt.Sprintf("当前平台 %s 已跳过 Alpine 运行时依赖检查", runtime.GOOS)
		return r
	}

	if !fileExists("/etc/alpine-release") {
		caOK := fileExists(caCertificatePath())
		check := RuntimeEnvCheck{
			Name:   "ca-certificates",
			Status: RuntimeEnvOK,
			Detail: "CA 证书存在: " + caCertificatePath(),
		}
		if !caOK {
			check.Status = RuntimeEnvWarn
			check.Detail = "系统缺少 CA 证书（HTTPS 探测和 cloudflared 下载可能失败）: " + caCertificatePath()
			check.Repair = "apk add --no-cache ca-certificates"
			r.Missing = append(r.Missing, "ca-certificates")
			r.Healthy = false
			r.Summary = "未检测到 Alpine；系统缺少 CA 证书"
		} else {
			r.Healthy = true
			r.Summary = "未检测到 Alpine；CA 证书可用"
		}
		r.Checks = append(r.Checks, check)
		return r
	}

	r.Alpine = true
	r.Root = os.Geteuid() == 0
	apkPath, apkErr := exec.LookPath("apk")
	if apkErr == nil {
		r.Checks = append(r.Checks, RuntimeEnvCheck{
			Name:   "apk",
			Status: RuntimeEnvOK,
			Detail: "apk 可用: " + apkPath,
		})
	} else {
		r.Checks = append(r.Checks, RuntimeEnvCheck{
			Name:   "apk",
			Status: RuntimeEnvWarn,
			Detail: "Alpine 未找到 apk，无法自动安装运行时依赖",
		})
	}

	r.Missing = AlpineMissingPackages("")
	for _, pkg := range r.Missing {
		check := RuntimeEnvCheck{
			Name:   pkg,
			Status: RuntimeEnvWarn,
			Detail: fmt.Sprintf("缺少运行时依赖包 %s", pkg),
			Repair: "apk add --no-cache " + pkg,
		}
		switch pkg {
		case "ca-certificates":
			check.Detail = "缺少 CA 证书: " + caCertificatePath()
		case "tzdata":
			check.Detail = "缺少时区数据目录: /usr/share/zoneinfo"
		case "gcompat":
			check.Detail = "缺少 gcompat 动态加载器: " + runtimeLoaderPath()
		}
		r.Checks = append(r.Checks, check)
	}
	for _, pkg := range []string{"ca-certificates", "tzdata", "gcompat"} {
		if containsString(r.Missing, pkg) {
			continue
		}
		detail := "依赖文件存在"
		switch pkg {
		case "ca-certificates":
			detail = "CA 证书存在: " + caCertificatePath()
		case "tzdata":
			detail = "时区数据目录存在: /usr/share/zoneinfo"
		case "gcompat":
			detail = "动态加载器存在: " + runtimeLoaderPath()
		}
		r.Checks = append(r.Checks, RuntimeEnvCheck{Name: pkg, Status: RuntimeEnvOK, Detail: detail})
	}

	r.Healthy = len(r.Missing) == 0
	r.CanAutoRepair = len(r.Missing) > 0 && r.Root && apkErr == nil
	switch {
	case len(r.Missing) == 0:
		r.Summary = "Alpine 运行时依赖完整"
	case apkErr != nil:
		r.Summary = "Alpine 缺少运行时依赖: " + strings.Join(r.Missing, ", ") + "（apk 不可用）"
	case !r.Root:
		r.Summary = "Alpine 缺少运行时依赖: " + strings.Join(r.Missing, ", ") + "（当前非 root，需手动安装）"
	default:
		r.Summary = "Alpine 缺少运行时依赖: " + strings.Join(r.Missing, ", ")
	}
	return r
}

// CheckRuntimeEnv 在 Alpine 上做一次轻量自检；仅尝试一次 apk 安装，
// 每类依赖安装后都会重新检查实际文件，验证失败只告警，绝不虚报已补齐。
func CheckRuntimeEnv() {
	report := CheckRuntimeEnvReport()
	if runtime.GOOS != "linux" {
		return
	}
	if !report.Alpine {
		for _, check := range report.Checks {
			if check.Status == RuntimeEnvWarn {
				logx.LogWarn("%s", check.Detail)
			}
		}
		return
	}
	if len(report.Missing) == 0 {
		return
	}
	if _, err := exec.LookPath("apk"); err != nil {
		logx.LogWarn("Alpine 缺少 %s，且未找到 apk；请手动安装: apk add --no-cache %s",
			strings.Join(report.Missing, ", "), strings.Join(report.Missing, " "))
		return
	}
	if os.Geteuid() != 0 {
		logx.LogWarn("Alpine 缺少 %s；当前非 root，无法自动安装，请执行: apk add --no-cache %s",
			strings.Join(report.Missing, ", "), strings.Join(report.Missing, " "))
		return
	}

	logx.LogWarn("Alpine 缺少运行时依赖 %s，尝试安装（每项仅尝试一次）", strings.Join(report.Missing, ", "))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, pkg := range report.Missing {
		if err := ctx.Err(); err != nil {
			logx.LogWarn("Alpine 运行时依赖 %s 安装超时，未标记为已补齐", pkg)
			continue
		}
		out, err := exec.CommandContext(ctx, "apk", "add", "--no-cache", pkg).CombinedOutput()
		if err != nil {
			logx.LogWarn("apk add %s 失败（不重试）: %s", pkg, commandErrorText(out, err))
			continue
		}
		if pkg == "ca-certificates" {
			if err := exec.CommandContext(ctx, "update-ca-certificates").Run(); err != nil {
				logx.LogWarn("CA 证书包已安装，但 update-ca-certificates 失败: %v", err)
			}
		}
		if !runtimeDependencyPresent(pkg) {
			logx.LogWarn("apk add %s 返回成功，但依赖文件验证失败；未标记为已补齐", pkg)
			continue
		}
		logx.LogOK("Alpine 运行时依赖 %s 已安装并验证", pkg)
	}
}

func commandErrorText(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return msg
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
