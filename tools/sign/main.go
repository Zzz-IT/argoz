// Command sign 为发布产物生成 Ed25519 分离签名。
// 仅供发布流水线使用：私钥从环境变量 RELEASE_SIGNING_KEY 读取
// （base64 编码的 32 字节种子），对指定文件签名并写出 <file>.sig（base64）。
// 未配置私钥时写一个空占位文件——客户端只在注入了公钥时才校验签名。
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: sign <file>（私钥从 RELEASE_SIGNING_KEY 读取）")
		os.Exit(2)
	}
	path := os.Args[1]

	raw := strings.TrimSpace(os.Getenv("RELEASE_SIGNING_KEY"))
	if raw == "" {
		// GitHub 拒绝 0 字节的 release 资产，所以未配置私钥时不能留空占位文件；
		// 客户端在公钥为空时本来就不校验签名。
		_ = os.Remove(path + ".sig")
		fmt.Println("未配置 RELEASE_SIGNING_KEY，跳过签名（该发布不含 checksums.txt.sig）")
		return
	}

	seed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintf(os.Stderr, "RELEASE_SIGNING_KEY 必须是 base64 编码的 %d 字节种子\n", ed25519.SeedSize)
		os.Exit(1)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", path, err)
		os.Exit(1)
	}
	sig := ed25519.Sign(priv, data)
	out := base64.StdEncoding.EncodeToString(sig) + "\n"
	if err := os.WriteFile(path+".sig", []byte(out), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入签名失败: %v\n", err)
		os.Exit(1)
	}

	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("已签名 %s\n公钥（用于 -X argoz/internal/core.SigningPublicKey）: %s\n",
		path, base64.StdEncoding.EncodeToString(pub))
}
