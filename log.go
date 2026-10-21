package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level 日志等级（数值越大越严重）
type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelOff // 完全静音（全屏 TUI 模式用）
)

const maxLogSize = 5 << 20 // 5MB 轮转

var (
	curLevel     atomic.Int32
	consoleLevel atomic.Int32
	logMu        sync.Mutex
	logFile      *os.File
	logPath      string
	logSize      int64
)

func init() {
	curLevel.Store(int32(LevelInfo))
	consoleLevel.Store(int32(LevelInfo))
}

// ParseLevel 解析日志级别
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// SetLevel 设置等级
func SetLevel(l Level) { curLevel.Store(int32(l)) }

// SetLevelByName 按名称设置等级
func SetLevelByName(s string) { SetLevel(ParseLevel(s)) }

// SetConsoleLevel 控制终端最低输出等级（TUI 模式设为 Warn，避免刷屏）
func SetConsoleLevel(l Level) { consoleLevel.Store(int32(l)) }

// LogFilePath 返回当前日志文件路径
func LogFilePath() string {
	logMu.Lock()
	defer logMu.Unlock()
	return logPath
}

// InitLog 打开（或创建）数据目录下的日志文件
func InitLog(dir, level string) error {
	SetLevelByName(level)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	p := filepath.Join(dir, "argoz.log")
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}

	logMu.Lock()
	logFile = f
	logPath = p
	logSize = 0
	if st, err := f.Stat(); err == nil {
		logSize = st.Size()
	}
	logMu.Unlock()
	return nil
}

// CloseLog 关闭日志文件
func CloseLog() {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

func rotateLocked() {
	if logFile == nil {
		return
	}
	_ = logFile.Close()
	_ = os.Remove(logPath + ".1")
	_ = os.Rename(logPath, logPath+".1")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logFile = nil
		return
	}
	logFile = f
	logSize = 0
}

func logAt(l Level, tag, color, format string, a ...any) {
	if int32(l) < curLevel.Load() {
		return
	}
	msg := fmt.Sprintf(format, a...)
	ts := time.Now().Format("2006-01-02 15:04:05")

	if int32(l) >= consoleLevel.Load() {
		fmt.Printf("%s[%s]%s %s\n", color, tag, ColorReset, msg)
	}

	logMu.Lock()
	if logFile != nil {
		n, err := logFile.WriteString(fmt.Sprintf("%s [%s] %s\n", ts, tag, msg))
		if err == nil {
			logSize += int64(n)
			if logSize > maxLogSize {
				rotateLocked()
			}
		}
	}
	logMu.Unlock()
}

// LogDebug 调试信息
func LogDebug(f string, a ...any) { logAt(LevelDebug, "调试", ColorGrey, f, a...) }

// LogInfo 普通信息
func LogInfo(f string, a ...any) { logAt(LevelInfo, "信息", ColorCyan, f, a...) }

// LogOK 成功信息
func LogOK(f string, a ...any) { logAt(LevelInfo, "成功", ColorGreen, f, a...) }

// LogWarn 警告
func LogWarn(f string, a ...any) { logAt(LevelWarn, "注意", ColorYellow, f, a...) }

// LogErr 错误
func LogErr(f string, a ...any) { logAt(LevelError, "错误", ColorRed, f, a...) }
