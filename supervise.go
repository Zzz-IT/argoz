package main

import (
	"argoz/internal/core"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// runSupervisor 父进程守护：拉起 argoz -d，退出后自动重启（容器没有 init 时用）
func runSupervisor(dir string) error {
	lock, err := core.AcquireLock(dir, "argoz-supervisor.lock")
	if err != nil {
		return err
	}
	defer lock.Release()

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位自身程序失败: %w", err)
	}

	var mu sync.Mutex
	var child *exec.Cmd
	stopping := false

	doneCh := make(chan struct{})
	var doneOnce sync.Once
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
		doneOnce.Do(func() { close(doneCh) })
	}()

	// 崩溃退避：稳定运行越久，下次重启越快；连续秒退说明是启动即崩
	// （配置损坏、OOM、二进制错误），必须拉长间隔，避免刷屏与反复抢锁。
	backoff := time.Second
	crashes := 0
	for {
		mu.Lock()
		st := stopping
		mu.Unlock()
		if st {
			fmt.Println("[supervisor] 已停止")
			return nil
		}

		cmd := exec.Command(exe, "-d", "-dir", dir)
		cmd.SysProcAttr = core.ChildDeathAttr()
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		mu.Lock()
		child = cmd
		mu.Unlock()

		started := time.Now()
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "[supervisor] 启动 daemon 失败: %v\n", err)
		} else {
			fmt.Printf("[supervisor] daemon 已启动 pid=%d\n", cmd.Process.Pid)
			_ = cmd.Wait()
			ranFor := time.Since(started)

			mu.Lock()
			st := stopping
			mu.Unlock()
			if st {
				fmt.Println("[supervisor] 已停止")
				return nil
			}
			if ranFor >= time.Minute {
				// 跑满 1 分钟视为稳定，清空崩溃计数。
				backoff = time.Second
				crashes = 0
			} else {
				crashes++
				switch {
				case crashes == 1:
					backoff = 2 * time.Second
				case crashes == 2:
					backoff = 5 * time.Second
				case crashes == 3:
					backoff = 15 * time.Second
				default:
					backoff = 30 * time.Second
				}
				fmt.Printf("[supervisor] daemon 仅运行 %s 即退出（疑似崩溃/启动失败，连续 %d 次），%s 后重启；请查看日志\n",
					ranFor.Round(time.Millisecond), crashes, backoff)
			}
			if backoff > time.Second {
				fmt.Printf("[supervisor] daemon 退出，%s 后重启\n", backoff)
			}
		}

		select {
		case <-doneCh:
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second && crashes == 0 {
			backoff *= 2
		}
	}
}
