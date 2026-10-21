package protocol

import (
	"argoz/internal/config"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

var errAuthFailed = errors.New("认证失败")

// parseUUID 接受带横线或 32 位纯十六进制
func ParseUUID(s string) ([16]byte, error) {
	var out [16]byte
	clean := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(clean) != 32 {
		return out, fmt.Errorf("UUID 长度非法: %s", s)
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return out, fmt.Errorf("UUID 含非法字符: %w", err)
	}
	copy(out[:], b)
	return out, nil
}

// readAddr 读取 SOCKS5 风格地址（ATYP 在前）
func ReadAddr(r io.Reader) (string, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return "", err
	}
	switch atyp[0] {
	case 0x01:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	case 0x03:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", err
		}
		if l[0] == 0 {
			return "", errors.New("域名长度非法")
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	case 0x04:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	}
	return "", fmt.Errorf("不支持的地址类型: %d", atyp[0])
}

// vlessHandshake 解析 VLESS 请求头，返回目标地址
func VlessHandshake(r io.Reader, n config.Node) (string, error) {
	var ver [1]byte
	if _, err := io.ReadFull(r, ver[:]); err != nil {
		return "", err
	}
	if ver[0] != 0 {
		return "", fmt.Errorf("不支持的 VLESS 版本: %d", ver[0])
	}

	var uid [16]byte
	if _, err := io.ReadFull(r, uid[:]); err != nil {
		return "", err
	}
	want, err := ParseUUID(n.UUID)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare(uid[:], want[:]) != 1 {
		return "", errAuthFailed
	}

	var addonLen [1]byte
	if _, err := io.ReadFull(r, addonLen[:]); err != nil {
		return "", err
	}
	if addonLen[0] > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(addonLen[0])); err != nil {
			return "", err
		}
	}

	var cmd [1]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		return "", err
	}
	if cmd[0] != 0x01 {
		return "", fmt.Errorf("仅支持 TCP(1)，收到 cmd=%d（Argo 隧道不支持 UDP）", cmd[0])
	}

	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return "", err
	}
	port := int(binary.BigEndian.Uint16(pb[:]))

	host, err := ReadVlessAddr(r)
	if err != nil {
		return "", err
	}
	if port == 0 {
		return "", errors.New("目标端口为 0")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// vlessResponse 写回 VLESS 应答头
func VlessResponse(w io.Writer) error {
	_, err := w.Write([]byte{0x00, 0x00})
	return err
}

// normalizeUUID 统一成 8-4-4-4-12 的小写形式
func NormalizeUUID(s string) string {
	b, err := ParseUUID(s)
	if err != nil {
		return s
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// readVlessAddr 解析 VLESS 目标地址。
// 注意：VLESS 的地址类型与 SOCKS5 不同 —— 1=IPv4，2=域名，3=IPv6。
// （SOCKS5/Trojan 是 1=IPv4，3=域名，4=IPv6，不要混用）
func ReadVlessAddr(r io.Reader) (string, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(r, atyp[:]); err != nil {
		return "", err
	}
	switch atyp[0] {
	case 0x01: // IPv4
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	case 0x02: // 域名
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", err
		}
		if l[0] == 0 {
			return "", errors.New("域名长度非法")
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	case 0x03: // IPv6
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	}
	return "", fmt.Errorf("不支持的地址类型: %d", atyp[0])
}
