//go:build linux

package scNet

import (
	"os"
	"os/signal"
	"syscall"
)

func initPlatform() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGPIPE)
	go func() {
		for range sig {
		}
	}()
}

// SetupFirewall Linux 下留空，由系统权限或外部脚本处理
func SetupFirewall(port string) {}

// CleanupFirewall Linux 下留空
func CleanupFirewall(port string) {}
