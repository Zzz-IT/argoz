//go:build !windows

package panel

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// termSize 通过 TIOCGWINSZ 获取终端尺寸
func termSize() (int, int) {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}
