//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.design/x/clipboard"
)

func startClipboardMonitor(ctx context.Context, callback func(content string)) error {
	if err := clipboard.Init(); err != nil {
		return fmt.Errorf("初始化剪切板失败: %w", err)
	}

	last := string(clipboard.Read(clipboard.FmtText))
	callback(last)

	ch := clipboard.Watch(ctx, clipboard.FmtText)
	for content := range ch {
		s := string(content)
		if s == last {
			continue
		}
		last = s
		callback(s)
	}

	return nil
}

func writeToClipboard(content string) error {
	if err := clipboard.Init(); err != nil {
		return fmt.Errorf("初始化剪切板失败: %w", err)
	}

	clipboard.Write(clipboard.FmtText, []byte(content))
	return nil
}

func handleAutostart() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("获取程序路径失败: %v\n", err)
		return
	}

	taskName := "ClipSync"
	vbsDir := filepath.Dir(exePath)
	vbsPath := filepath.Join(vbsDir, "clipsync-autostart.vbs")

	check := exec.Command("schtasks", "/Query", "/TN", taskName, "/FO", "CSV", "/V")
	output, err := check.CombinedOutput()

	if err == nil && strings.Contains(string(output), taskName) {
		del := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
		del.Stdout = os.Stdout
		del.Stderr = os.Stderr
		if err := del.Run(); err != nil {
			fmt.Printf("删除自启动任务失败: %v\n", err)
			return
		}
		os.Remove(vbsPath)
		fmt.Println("已删除开机自启动任务")
	} else {
		vbsContent := fmt.Sprintf("CreateObject(\"WScript.Shell\").Run \"%s run\", 0, False", exePath)
		if err := os.WriteFile(vbsPath, []byte(vbsContent), 0644); err != nil {
			fmt.Printf("创建启动脚本失败: %v\n", err)
			return
		}

		taskCmd := fmt.Sprintf(`wscript.exe //Nologo "%s"`, vbsPath)
		create := exec.Command("schtasks", "/Create", "/SC", "ONLOGON",
			"/TN", taskName,
			"/TR", taskCmd,
			"/RL", "LIMITED",
			"/F")
		create.Stdout = os.Stdout
		create.Stderr = os.Stderr
		if err := create.Run(); err != nil {
			fmt.Printf("创建自启动任务失败: %v\n", err)
			return
		}
		fmt.Println("已创建开机自启动任务（后台静默运行，无窗口）")
	}
}
