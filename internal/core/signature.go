package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SigningPublicKey 发布签名公钥（Ed25519, base64 标准编码），由发布流水线通过
// -ldflags "-X argoz/internal/core.SigningPublicKey=..." 注入。
// 为空表示该构建不校验发布签名（兼容未配置签名的仓库与开发构建）。
var SigningPublicKey = ""

// ReleaseSignatureRequired 表示当前构建是否强制校验发布签名。
func ReleaseSignatureRequired() bool { return strings.TrimSpace(SigningPublicKey) != "" }

// VerifyReleaseSignature 用内置公钥校验分离签名（base64 编码）。
// data 是 checksums.txt 的原始字节，sigB64 是 checksums.txt.sig 的内容。
func VerifyReleaseSignature(data, sigB64 []byte) error {
	pubRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(SigningPublicKey))
	if err != nil {
		return fmt.Errorf("内置公钥编码非法: %w", err)
	}
	if len(pubRaw) != ed25519.PublicKeySize {
		return fmt.Errorf("内置公钥长度非法: %d", len(pubRaw))
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil {
		return fmt.Errorf("签名编码非法: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("签名长度非法: %d", len(sig))
	}
	if !ed25519.Verify(ed25519.PublicKey(pubRaw), data, sig) {
		return errors.New("发布签名校验失败：checksums.txt 与内置公钥不匹配")
	}
	return nil
}
