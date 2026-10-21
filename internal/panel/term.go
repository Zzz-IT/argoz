package panel

import (
	"os"
	"strconv"
)

func envInt(name string) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// termWidth 返回终端宽度（字符列），失败时回退 80
func termWidth() int {
	if n := envInt("ARGOZ_COLS"); n > 0 {
		return n
	}
	w, _ := termSize()
	if w <= 0 {
		w = 80
	}
	return w
}

// termHeight 返回终端高度（行），失败时回退 24
func termHeight() int {
	if n := envInt("ARGOZ_LINES"); n > 0 {
		return n
	}
	_, h := termSize()
	if h <= 0 {
		h = 24
	}
	return h
}
