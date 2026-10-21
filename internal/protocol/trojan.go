package protocol

import (
	"argoz/internal/config"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// trojanHandshake 解析 Trojan 请求头，返回目标地址
func TrojanHandshake(r io.Reader, n config.Node) (string, error) {
	var hashBuf [56]byte
	if _, err := io.ReadFull(r, hashBuf[:]); err != nil {
		return "", err
	}

	sum := sha256.Sum224([]byte(n.Password))
	want := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare(hashBuf[:], []byte(want)) != 1 {
		return "", errAuthFailed
	}

	var crlf [2]byte
	if _, err := io.ReadFull(r, crlf[:]); err != nil {
		return "", err
	}
	if crlf[0] != 0x0d || crlf[1] != 0x0a {
		return "", errors.New("Trojan 请求头格式错误")
	}

	var cmd [1]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		return "", err
	}
	if cmd[0] != 0x01 {
		return "", fmt.Errorf("仅支持 CONNECT(1)，收到 cmd=%d", cmd[0])
	}

	host, err := ReadAddr(r)
	if err != nil {
		return "", err
	}

	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return "", err
	}
	port := int(binary.BigEndian.Uint16(pb[:]))

	var tail [2]byte
	if _, err := io.ReadFull(r, tail[:]); err != nil {
		return "", err
	}
	if tail[0] != 0x0d || tail[1] != 0x0a {
		return "", errors.New("Trojan 请求头缺少结尾 CRLF")
	}

	// 尾部 CRLF 之后可能还带着首批 payload，读到的部分已在调用方 reader 中，
	// 这里不做额外处理：调用方继续从同一个 reader 读取即可。
	if port == 0 {
		return "", errors.New("目标端口为 0")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
