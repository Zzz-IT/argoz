//go:build windows

package panel

import (
	"syscall"
	"unsafe"
)

var (
	kernel32dll                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferIn = kernel32dll.NewProc("GetConsoleScreenBufferInfo")
)

type consoleScreenBufferInfo struct {
	sizeX, sizeY             int16
	cursorX, cursorY         int16
	attributes               uint16
	left, top, right, bottom int16
	maxX, maxY               int16
}

// termSize 通过 GetConsoleScreenBufferInfo 获取窗口尺寸
func termSize() (int, int) {
	var info consoleScreenBufferInfo
	ret, _, _ := procGetConsoleScreenBufferIn.Call(
		uintptr(syscall.Stdout),
		uintptr(unsafe.Pointer(&info)),
	)
	if ret == 0 {
		return 0, 0
	}
	return int(info.right-info.left) + 1, int(info.bottom-info.top) + 1
}
