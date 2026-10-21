package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// copyClipboard 通过 OSC52 把文本送进本地终端剪贴板
func copyClipboard(content string) {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	fmt.Printf("\033]52;c;%s\a", enc)
}

// exportToFile 把内容追加到数据目录下的 nodes.txt
func exportToFile(dataDir, title, body string) (string, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return "", err
	}
	p := filepath.Join(dataDir, "nodes.txt")

	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()

	header := fmt.Sprintf("========== %s  %s ==========\n", title, time.Now().Format("2006-01-02 15:04:05"))
	if _, err := f.WriteString(header + body + "\n\n"); err != nil {
		return "", err
	}
	return p, nil
}
