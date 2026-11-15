package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestVerifyReleaseSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	old := SigningPublicKey
	defer func() { SigningPublicKey = old }()

	data := []byte("0123abcd  argoz-amd64\n4567ef01  argoz-arm64\n")
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
	SigningPublicKey = base64.StdEncoding.EncodeToString(pub)

	if err := VerifyReleaseSignature(data, []byte(good)); err != nil {
		t.Fatalf("正确签名应通过校验: %v", err)
	}
	if err := VerifyReleaseSignature([]byte("0123abcd  argoz-amd64\n"), []byte(good)); err == nil {
		t.Fatal("内容被篡改时必须校验失败")
	}

	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("生成第二把测试密钥失败: %v", err)
	}
	SigningPublicKey = base64.StdEncoding.EncodeToString(otherPub)
	if err := VerifyReleaseSignature(data, []byte(good)); err == nil {
		t.Fatal("公钥不匹配时必须校验失败")
	}

	SigningPublicKey = base64.StdEncoding.EncodeToString(pub)
	if err := VerifyReleaseSignature(data, []byte("不是 base64")); err == nil {
		t.Fatal("签名编码非法时必须报错")
	}
}

func TestReleaseSignatureRequired(t *testing.T) {
	old := SigningPublicKey
	defer func() { SigningPublicKey = old }()

	SigningPublicKey = ""
	if ReleaseSignatureRequired() {
		t.Fatal("未注入公钥时不应强制校验")
	}
	SigningPublicKey = "   "
	if ReleaseSignatureRequired() {
		t.Fatal("空白的公钥视同未注入")
	}
	SigningPublicKey = "c29tZS1rZXk="
	if !ReleaseSignatureRequired() {
		t.Fatal("注入公钥后必须强制校验")
	}
}
