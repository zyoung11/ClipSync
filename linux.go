//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type clipboardTool struct {
	name           string
	cmd            string
	readArgs       []string
	writeArgs      []string
	watchSupported bool
}

var (
	availableTools []clipboardTool
	defaultTool    *clipboardTool
)

func detectClipboardTools() {
	tools := []clipboardTool{
		{
			name:           "wl-paste",
			cmd:            "wl-paste",
			readArgs:       []string{"--type", "text"},
			writeArgs:      []string{"wl-copy"},
			watchSupported: false,
		},
		{
			name:           "xclip",
			cmd:            "xclip",
			readArgs:       []string{"-selection", "clipboard", "-o"},
			writeArgs:      []string{"xclip", "-selection", "clipboard"},
			watchSupported: false,
		},
		{
			name:           "xsel",
			cmd:            "xsel",
			readArgs:       []string{"--clipboard", "--output"},
			writeArgs:      []string{"xsel", "--clipboard", "--input"},
			watchSupported: false,
		},
	}

	for _, tool := range tools {
		if _, err := exec.LookPath(tool.cmd); err == nil {
			availableTools = append(availableTools, tool)
			if defaultTool == nil {
				defaultTool = &tool
			}
		}
	}

	if defaultTool == nil {
		if checkClipboardManager() {
			fmt.Println("检测到剪贴板管理器，但需要手动配置工具")
		} else {
			fmt.Println("未找到可用的剪切板工具")
		}
	}
}

func checkClipboardManager() bool {
	cmd := exec.Command("pgrep", "-x", "copyq")
	if err := cmd.Run(); err == nil {
		return true
	}

	cmd = exec.Command("pgrep", "-x", "gpaste-daemon")
	return cmd.Run() == nil
}

func startClipboardMonitor(ctx context.Context, callback func(content string)) error {
	detectClipboardTools()

	if defaultTool == nil {
		return fmt.Errorf("未找到可用的剪切板工具")
	}

	fmt.Printf("使用剪切板工具: %s\n", defaultTool.name)

	if defaultTool.watchSupported {
		return startWatchMode(ctx, callback)
	}

	return startPollingMode(ctx, callback)
}

func startWatchMode(ctx context.Context, callback func(content string)) error {
	cmd := exec.Command(defaultTool.cmd, "--watch")
	if defaultTool.name == "wl-paste" {
		cmd = exec.Command(defaultTool.cmd, "--watch", "--type", "text")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动监视进程失败: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	go func() {
		defer cmd.Process.Kill()

		buf := make([]byte, 4096)
		var currentContent strings.Builder
		lastContent := ""

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := stdout.Read(buf)
			if err != nil {
				break
			}

			data := string(buf[:n])
			currentContent.WriteString(data)

			if strings.Contains(data, "\n") || n < len(buf) {
				content := strings.TrimSpace(currentContent.String())
				currentContent.Reset()

				if content != "" && content != lastContent {
					lastContent = content
					callback(content)
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		cmd.Process.Kill()
		<-waitCh
		return ctx.Err()
	case err := <-waitCh:
		if err != nil {
			return fmt.Errorf("监视进程退出: %w", err)
		}
		return nil
	}
}

func startPollingMode(ctx context.Context, callback func(content string)) error {
	lastContent := readClipboard()
	if lastContent != "" {
		callback(lastContent)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			content := readClipboard()
			if content != "" && content != lastContent {
				lastContent = content
				callback(content)
			}
		}
	}
}

func readClipboard() string {
	if defaultTool == nil {
		return ""
	}

	args := make([]string, len(defaultTool.readArgs))
	copy(args, defaultTool.readArgs)

	cmd := exec.Command(defaultTool.cmd, args...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func writeToClipboard(content string) error {
	if defaultTool == nil {
		detectClipboardTools()
		if defaultTool == nil {
			return fmt.Errorf("未找到可用的剪切板工具")
		}
	}

	var cmd *exec.Cmd
	if defaultTool.name == "wl-paste" {
		cmd = exec.Command("wl-copy")
	} else {
		args := make([]string, len(defaultTool.writeArgs))
		copy(args, defaultTool.writeArgs)
		cmd = exec.Command(args[0], args[1:]...)
	}

	cmd.Stdin = strings.NewReader(content)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("写入剪切板失败: %w", err)
	}

	return nil
}

func handleAutostart() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("获取程序路径失败: %v\n", err)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("获取用户主目录失败: %v\n", err)
		return
	}

	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	servicePath := filepath.Join(serviceDir, "clipsync.service")
	serviceName := "clipsync"

	// 通过 systemctl 检查服务是否存在且已启用
	check := exec.Command("systemctl", "--user", "is-enabled", serviceName)
	output, err := check.CombinedOutput()
	enabled := err == nil && strings.TrimSpace(string(output)) == "enabled"

	if enabled {
		// 已启用 → 停用 + 删除
		exec.Command("systemctl", "--user", "stop", serviceName).Run()
		exec.Command("systemctl", "--user", "disable", serviceName).Run()
		os.Remove(servicePath)
		exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Println("已删除开机自启动服务")
	} else {
		// 创建 systemd user service 文件
		serviceContent := `[Unit]
Description=ClipSync - LAN Clipboard Sync
After=graphical-session.target network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStartPre=/bin/sleep 5
ExecStart=` + exePath + ` run
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

		if err := os.MkdirAll(serviceDir, 0755); err != nil {
			fmt.Printf("创建服务目录失败: %v\n", err)
			return
		}
		if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
			fmt.Printf("创建服务文件失败: %v\n", err)
			return
		}

		exec.Command("systemctl", "--user", "daemon-reload").Run()
		exec.Command("systemctl", "--user", "enable", serviceName).Run()
		exec.Command("systemctl", "--user", "start", serviceName).Run()
		fmt.Println("已创建开机自启动服务（后台静默运行）")
	}
}
