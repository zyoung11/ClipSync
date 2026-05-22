package main

import (
	"clipsync/csNet"
	"clipsync/result"
	"clipsync/text"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultPort      = "8890"
	defaultMaxLength = 100
	defaultMaxSize   = 3 * 1024 * 1024 // 3MB
	configDirName    = ".config/clipsync"
	configFileName   = "config.json"
	logFileName      = "clipsync.log"
)

type Config struct {
	IP        []string `json:"ip"`
	Port      string   `json:"port"`
	MaxLength int      `json:"maxLength"`
	MaxSize   int      `json:"maxSize"`
}

var (
	config     Config
	configPath string
	peers      []*csNet.Peer
	peerLock   sync.RWMutex
	lastSent   string
	lastSentMu sync.RWMutex
	lastRecv   string
	lastRecvMu sync.RWMutex
	logFile    *os.File
	logMu      sync.Mutex
)

func main() {
	csNet.Init()

	if len(os.Args) > 1 {
		command := os.Args[1]
		switch command {
		case "run":
			handleRun()
		case "log":
			follow := len(os.Args) > 2 && (os.Args[2] == "-f" || os.Args[2] == "--follow")
			handleLog(follow)
		case "delete":
			handleDelete()
		case "autostart":
			handleAutostart()
		case "help", "-h", "--help", "-help":
			printHelp()
		default:
			fmt.Printf("未知命令：%s\n", command)
			printHelp()
			os.Exit(1)
		}
	} else {
		showInteractiveMenu()
	}
}

func printHelp() {
	helpText := `
clipsync - 局域网剪切板共享工具

用法：clipsync [命令]

命令:
  run       启动剪切板共享服务
  autostart 创建/删除开机自启动任务（仅 Windows）
  log       查看服务运行日志（加 -f 实时跟踪）
  delete    删除配置文件
  help      显示此帮助信息

选项:
  -f        与 log 搭配使用，实时跟踪日志输出
  -h, --help    显示帮助信息

交互模式:
  直接运行 clipsync 进入交互式菜单
`
	fmt.Print(helpText)
}

func showInteractiveMenu() {
	config := result.RadioConfig{
		Question: "请选择要执行的操作:",
		Options: []string{
			"run       - 启动剪切板共享服务",
			"autostart - 创建/删除开机自启动任务",
			"log       - 查看服务运行日志",
			"delete    - 删除配置文件",
			"help      - 显示帮助信息",
			"exit      - 退出程序",
		},
	}

	choice := result.RadioList(config)

	switch {
	case strings.Contains(choice, "run"):
		handleRun()
	case strings.Contains(choice, "autostart"):
		handleAutostart()
	case strings.Contains(choice, "log"):
		handleLog(false)
	case strings.Contains(choice, "delete"):
		handleDelete()
	case strings.Contains(choice, "help"):
		printHelp()
	case strings.Contains(choice, "exit"):
		return
	}
}

func handleRun() {
	loadConfig()
	initLogging()

	if len(config.IP) == 0 {
		logPrintln("配置文件中没有IP地址，需要初始化配置")
		initConfig()
	}

	logPrintf("启动剪切板共享服务 (端口: %s)\n", config.Port)
	logPrintf("共享设备: %v\n", config.IP)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n正在停止服务...")
		logPrintln("正在停止服务...")
		cancel()
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	initializePeers()

	go func() {
		for _, peer := range peers {
			<-peer.Connected
		}
		logPrintln("所有设备连接已建立！")
	}()

	recvCh := make(chan string, 100)
	go startNetworkReceiver(recvCh)

	logPrintln("剪切板监控已启动，按 Ctrl+C 退出")

	err := startClipboardMonitor(ctx, func(content string) {
		if shouldBroadcast(content) {
			broadcastToPeers(content)
		}
	})
	if err != nil {
		logPrintf("警告: 启动剪切板监控失败: %v\n", err)
		logPrintln("将以仅接收模式运行，按 Ctrl+C 退出")
	}

	// Block until interrupted
	<-ctx.Done()
}

func handleDelete() {
	fmt.Println("删除配置文件...")
	cleanupConfig()
}

func initConfig() {
	fmt.Println("请输入要共享的局域网IP地址（多个IP用逗号分隔）:")
	input := text.TextInput("IP地址: ")

	ips := strings.Split(strings.TrimSpace(input), ",")
	var validIPs []string
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			validIPs = append(validIPs, ip)
		}
	}

	if len(validIPs) == 0 {
		fmt.Println("未输入有效的IP地址")
		os.Exit(1)
	}

	config = Config{
		IP:        validIPs,
		Port:      defaultPort,
		MaxLength: defaultMaxLength,
		MaxSize:   defaultMaxSize,
	}

	saveConfig()
	fmt.Printf("配置已保存: %v\n", validIPs)
}

func getConfigPath() string {
	if configPath != "" {
		return configPath
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("无法获取用户主目录:", err)
	}

	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			if u, err := user.Lookup(sudoUser); err == nil {
				home = u.HomeDir
			}
		}
	}

	configDir := filepath.Join(home, configDirName)
	configPath = filepath.Join(configDir, configFileName)
	return configPath
}

func loadConfig() {
	path := getConfigPath()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		config = Config{
			Port:      defaultPort,
			MaxLength: defaultMaxLength,
			MaxSize:   defaultMaxSize,
		}
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("读取配置文件失败:", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		log.Fatal("解析配置文件失败:", err)
	}

	if config.Port == "" {
		config.Port = defaultPort
	}
	if config.MaxLength == 0 {
		config.MaxLength = defaultMaxLength
	}
	if config.MaxSize == 0 {
		config.MaxSize = defaultMaxSize
	}
}

func saveConfig() {
	path := getConfigPath()
	configDir := filepath.Dir(path)

	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Fatal("创建配置目录失败:", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Fatal("序列化配置失败:", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Fatal("保存配置文件失败:", err)
	}
}

func initializePeers() {
	peerLock.Lock()
	defer peerLock.Unlock()

	for _, ip := range config.IP {
		cfg := csNet.Config{
			PeerIP:            ip,
			Port:              config.Port,
			DialTimeout:       1 * time.Second,
			AcceptTimeout:     1 * time.Second,
			HeartbeatInterval: 3 * time.Second,
			HeartbeatTimeout:  3 * time.Second,
			ReconnectDelay:    1 * time.Second,
		}

		peer := csNet.New(cfg)
		peers = append(peers, peer)
		logPrintf("正在连接设备 %s:%s...\n", ip, config.Port)
	}
}

func broadcastToPeers(content string) {
	if len(content) > config.MaxSize {
		logPrintf("内容大小超过限制 (%d > %d)，跳过发送\n", len(content), config.MaxSize)
		return
	}

	peerLock.RLock()
	defer peerLock.RUnlock()

	lastSentMu.Lock()
	lastSent = content
	lastSentMu.Unlock()

	for _, peer := range peers {
		peer.Send(content)
	}
}

func shouldBroadcast(content string) bool {
	if len(content) > config.MaxSize {
		return false
	}

	lastSentMu.RLock()
	defer lastSentMu.RUnlock()

	return content != lastSent
}

func startNetworkReceiver(recvCh chan string) {
	for {
		peerLock.RLock()
		peerList := make([]*csNet.Peer, len(peers))
		copy(peerList, peers)
		peerLock.RUnlock()

		for _, peer := range peerList {
			select {
			case msg, ok := <-peer.Recv:
				if !ok {
					continue
				}
				recvCh <- msg
			default:
			}
		}

		select {
		case msg := <-recvCh:
			handleReceivedMessage(msg)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func handleReceivedMessage(content string) {
	lastRecvMu.RLock()
	sameAsLast := content == lastRecv
	lastRecvMu.RUnlock()

	if sameAsLast {
		return
	}

	lastRecvMu.Lock()
	lastRecv = content
	lastRecvMu.Unlock()

	writeToClipboard(content)
	logPrintf("收到剪切板内容 (%d bytes)\n", len(content))
}

func cleanupConfig() {
	path := getConfigPath()
	configDir := filepath.Dir(path)

	if err := os.RemoveAll(configDir); err != nil {
		fmt.Printf("删除配置目录失败: %v\n", err)
	} else {
		fmt.Println("配置目录已删除")
	}
}

func initLogging() {
	logDir := filepath.Dir(getConfigPath())
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(logDir, logFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	logFile = f
}

func logPrintf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Print(msg)
	writeLog(msg)
}

func logPrintln(a ...interface{}) {
	msg := fmt.Sprintln(a...)
	fmt.Print(msg)
	writeLog(msg)
}

func writeLog(msg string) {
	clean := strings.TrimSpace(msg)
	if clean == "" {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		ts := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(logFile, "[%s] %s\n", ts, clean)
	}
}

func isServiceRunning() bool {
	exe, _ := os.Executable()
	name := filepath.Base(exe)
	myPid := os.Getpid()

	// Linux: pgrep -x <name> 返回 PID 列表
	output, err := exec.Command("pgrep", "-x", name).Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			if line == "" {
				continue
			}
			pid := 0
			fmt.Sscanf(line, "%d", &pid)
			if pid != 0 && pid != myPid {
				return true
			}
		}
	}

	// Windows: tasklist /NH /FI /FO CSV
	output, err = exec.Command("tasklist", "/NH", "/FI",
		fmt.Sprintf("IMAGENAME eq %s", name),
		"/FO", "CSV").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			parts := strings.Split(line, ",")
			if len(parts) < 2 {
				continue
			}
			pidStr := strings.Trim(parts[1], "\"")
			pid := 0
			fmt.Sscanf(pidStr, "%d", &pid)
			if pid != 0 && pid != myPid {
				return true
			}
		}
	}

	return false
}

func handleLog(follow bool) {
	// 先显示服务运行状态
	if isServiceRunning() {
		fmt.Println("服务状态: 🟢 运行中\n")
	} else {
		fmt.Println("服务状态: 🔴 未运行\n")
	}

	logPath := filepath.Join(filepath.Dir(getConfigPath()), logFileName)

	f, err := os.Open(logPath)
	if err != nil {
		fmt.Printf("暂无日志，服务可能尚未启动 (%v)\n", err)
		return
	}
	defer f.Close()

	// 读末尾最多 50 行
	const maxLines = 50
	stat, _ := f.Stat()
	if stat.Size() == 0 {
		if !follow {
			return
		}
	}

	var start int64 = 0
	if stat.Size() > 4096 {
		start = stat.Size() - 4096
	}
	buf := make([]byte, stat.Size()-start)
	f.ReadAt(buf, start)
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for _, line := range lines {
		fmt.Println(line)
	}

	if !follow {
		return
	}

	// Follow 模式：轮询文件追加内容
	fmt.Println("\n等待新日志... (Ctrl+C 退出)")
	f.Seek(0, 2) // 移到末尾
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return
		case <-ticker.C:
			cur, _ := f.Stat()
			if cur.Size() > stat.Size() {
				data := make([]byte, cur.Size()-stat.Size())
				f.Read(data)
				fmt.Print(string(data))
				stat = cur
			}
		}
	}
}
