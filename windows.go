//go:build windows

package main

import (
	"context"
	"fmt"

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
