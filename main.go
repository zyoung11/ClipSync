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
)

func main() {
	csNet.Init()

	if len(os.Args) > 1 {
		command := os.Args[1]
		switch command {
		case "run":
			handleRun()
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
  delete    删除配置文件
  help      显示此帮助信息

选项:
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

	if len(config.IP) == 0 {
		fmt.Println("配置文件中没有IP地址，需要初始化配置")
		initConfig()
	}

	fmt.Printf("启动剪切板共享服务 (端口: %s)\n", config.Port)
	fmt.Printf("共享设备: %v\n", config.IP)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n正在停止服务...")
		cancel()
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	initializePeers()

	go func() {
		for _, peer := range peers {
			<-peer.Connected
		}
		fmt.Println("所有设备连接已建立！")
	}()

	recvCh := make(chan string, 100)
	go startNetworkReceiver(recvCh)

	fmt.Println("剪切板监控已启动，按 Ctrl+C 退出")

	err := startClipboardMonitor(ctx, func(content string) {
		if shouldBroadcast(content) {
			broadcastToPeers(content)
		}
	})
	if err != nil {
		fmt.Printf("警告: 启动剪切板监控失败: %v\n", err)
		fmt.Println("将以仅接收模式运行，按 Ctrl+C 退出")
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
		fmt.Printf("正在连接设备 %s:%s...\n", ip, config.Port)
	}
}

func broadcastToPeers(content string) {
	if len(content) > config.MaxSize {
		fmt.Printf("内容大小超过限制 (%d > %d)，跳过发送\n", len(content), config.MaxSize)
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
	fmt.Printf("收到剪切板内容 (%d bytes)\n", len(content))
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
