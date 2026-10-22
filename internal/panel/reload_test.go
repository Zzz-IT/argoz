package panel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReloadRateLimit(t *testing.T) {
	dir := t.TempDir()
	if !reloadAllowed(dir) {
		t.Fatal("首次应允许")
	}
	markReload(dir)
	if !reloadAllowed(dir) {
		t.Fatal("1 次后仍应允许")
	}
	markReload(dir)
	if reloadAllowed(dir) {
		t.Fatal("10 分钟内 2 次后应拒绝")
	}

	// 回拨到 11 分钟前，旧记录应失效
	old := time.Now().Add(-11 * time.Minute).Unix()
	if err := os.WriteFile(filepath.Join(dir, "argoz.reload"),
		[]byte(itoa(old)+"\n"+itoa(old)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !reloadAllowed(dir) {
		t.Fatal("过期记录不应计入")
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
