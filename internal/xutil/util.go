package xutil

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// randHex 生成 n 字节随机数的十六进制串（2n 个字符）
func RandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// newUUID 生成标准 UUID v4
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// randomPath 生成 / + 12 位十六进制 的随机 WS 路径
func RandomPath() string { return "/" + RandHex(6) }

// atomicWrite 原子写：临时文件 -> fsync -> rename -> 同步目录
func AtomicWrite(p string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// runeWidth 估算单字符终端宽度（CJK 记 2 列）
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32:
		return 0
	case r < 127:
		return 1
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6:
		return 2
	}
	return 1
}

func DispWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// truncate 按显示宽度截断，超出补省略号
func Truncate(s string, w int) string {
	if DispWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return ""
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := runeWidth(r)
		if cur+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String() + "…"
}

func PadRight(s string, w int) string {
	s = Truncate(s, w)
	d := DispWidth(s)
	if d >= w {
		return s
	}
	return s + strings.Repeat(" ", w-d)
}

func PadLeft(s string, w int) string {
	s = Truncate(s, w)
	d := DispWidth(s)
	if d >= w {
		return s
	}
	return strings.Repeat(" ", w-d) + s
}

func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func HumanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// tailLines 读取文件最后 n 行
func TailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines, sc.Err()
}
