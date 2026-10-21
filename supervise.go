package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// runSupervisor 父进程守护：拉起 argoz -d，退出后自动重启（容器没有 init 时用）
func runSupervisor(dir string) {
	lock, err := acquireLock(dir, "argoz-supervisor.lock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer lock.Release()

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "定位自身程序失败: %v\n", err)
		os.Exit(1)
	}

	var mu sync.Mutex
	var child *exec.Cmd
	stopping := false

	doneCh := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		mu.Lock()
		stopping = true
		c := child
		mu.Unlock()
		if c != nil && c.Process != nil {
			_ = c.Process.Signal(syscall.SIGTERM)
		}
		close(doneCh)
	}()

	backoff := time.Second
	for {
		mu.Lock()
		st := stopping
		mu.Unlock()
		if st {
			fmt.Println("[supervisor] 已停止")
			return
		}

		cmd := exec.Command(exe, "-d", "-dir", dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		mu.Lock()
		child = cmd
		mu.Unlock()

		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "[supervisor] 启动 daemon 失败: %v\n", err)
		} else {
			fmt.Printf("[supervisor] daemon 已启动 pid=%d\n", cmd.Process.Pid)
			_ = cmd.Wait()

			mu.Lock()
			st := stopping
			mu.Unlock()
			if st {
				fmt.Println("[supervisor] 已停止")
				return
			}
			fmt.Printf("[supervisor] daemon 退出，%s 后重启\n", backoff)
		}

		select {
		case <-doneCh:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
