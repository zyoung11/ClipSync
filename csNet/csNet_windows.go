//go:build windows

package csNet

import (
	"os"
	"os/exec"
	"syscall"
)

func initPlatform() {
	k := syscall.NewLazyDLL("kernel32.dll")
	k.NewProc("SetConsoleOutputCP").Call(65001)
	k.NewProc("SetConsoleCP").Call(65001)
}

// SetupFirewall 尝试添加防火墙规则（需管理员权限）
func SetupFirewall(port string) {
	if !isAdmin() {
		return
	}
	exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name=csNet P2P", "dir=in", "action=allow",
		"protocol=TCP", "localport="+port,
	).Run()
}

// CleanupFirewall 清理防火墙规则
func CleanupFirewall(port string) {
	if !isAdmin() {
		return
	}
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name=csNet P2P").Run()
}

func isAdmin() bool {
	_, err := os.Open(`\\.\PHYSICALDRIVE0`)
	return err == nil
}
