package csNet

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"
)

// Config 网络连接配置
type Config struct {
	PeerIP            string        // 对方 IP
	Port              string        // 监听/连接端口
	DialTimeout       time.Duration // 主动拨号超时（建议 1s）
	AcceptTimeout     time.Duration // 被动接受超时（建议 1s）
	HeartbeatInterval time.Duration // 心跳发送间隔（建议 3s）
	HeartbeatTimeout  time.Duration // 心跳写超时（建议 3s）
	ReconnectDelay    time.Duration // 断线后重连间隔（建议 1s）
}

// dropQueue 线程安全的丢弃式环形队列
type dropQueue struct {
	mu    sync.Mutex
	items []string
	head  int
	tail  int
	count int
	cap   int
}

func newDropQueue(cap int) *dropQueue {
	return &dropQueue{
		items: make([]string, cap),
		cap:   cap,
	}
}

// Push 压入数据。如果满了，自动丢弃最老的数据
func (q *dropQueue) Push(item string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == q.cap {
		// 队列满，头指针后移，等效于丢弃最老数据
		q.head = (q.head + 1) % q.cap
		q.count--
	}
	q.items[q.tail] = item
	q.tail = (q.tail + 1) % q.cap
	q.count++
}

// Pop 弹出最老的数据
func (q *dropQueue) Pop() (string, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.count == 0 {
		return "", false
	}
	item := q.items[q.head]
	// 防止内存泄漏，清理引用
	q.items[q.head] = ""
	q.head = (q.head + 1) % q.cap
	q.count--
	return item, true
}

// Clear 强制清空队列
func (q *dropQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.head = 0
	q.tail = 0
	q.count = 0
}

// Peer 对等连接实例
type Peer struct {
	cfg        Config
	Recv       chan string
	Connected  chan struct{}
	sendQ      *dropQueue    // 替换了原来的 channel
	notifySend chan struct{} // 用于通知底层有新数据可发
	closeCh    chan struct{}
}

// Init 初始化平台环境
func Init() {
	initPlatform()
}

// New 创建并启动一个智能对等连接（非阻塞）
func New(cfg Config) *Peer {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 1 * time.Second
	}
	if cfg.AcceptTimeout == 0 {
		cfg.AcceptTimeout = 1 * time.Second
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 3 * time.Second
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = 3 * time.Second
	}
	if cfg.ReconnectDelay == 0 {
		cfg.ReconnectDelay = 1 * time.Second
	}

	p := &Peer{
		cfg:        cfg,
		Recv:       make(chan string, 100),
		Connected:  make(chan struct{}),
		sendQ:      newDropQueue(100), // 容量 100
		notifySend: make(chan struct{}, 1),
		closeCh:    make(chan struct{}),
	}
	go p.run()
	return p
}

// Send 发送数据（绝对非阻塞，满时自动丢弃最旧数据）
func (p *Peer) Send(data string) {
	p.sendQ.Push(data)
	// 非阻塞通知底层有数据
	select {
	case p.notifySend <- struct{}{}:
	default:
	}
}

// Close 彻底关闭连接
func (p *Peer) Close() {
	close(p.closeCh)
}

type taggedConn struct {
	conn   net.Conn
	isDial bool
}

func (p *Peer) run() {
	defer close(p.Recv)
	peerAddr := p.cfg.PeerIP + ":" + p.cfg.Port
	var pendingMsg string
	isFirstConnect := true

	for {
		select {
		case <-p.closeCh:
			return
		default:
		}

		ln, err := net.Listen("tcp", ":"+p.cfg.Port)
		if err != nil {
			time.Sleep(p.cfg.ReconnectDelay)
			continue
		}
		tcpLn := ln.(*net.TCPListener)

		connCh := make(chan taggedConn, 2)
		stopConnCh := make(chan struct{})

		go func() {
			dialer := &net.Dialer{Timeout: p.cfg.DialTimeout}
			for {
				select {
				case <-stopConnCh:
					return
				default:
				}
				conn, err := dialer.Dial("tcp", peerAddr)
				if err == nil {
					connCh <- taggedConn{conn, true}
					return
				}
			}
		}()

		go func() {
			for {
				select {
				case <-stopConnCh:
					return
				default:
				}
				tcpLn.SetDeadline(time.Now().Add(p.cfg.AcceptTimeout))
				conn, err := tcpLn.Accept()
				if err == nil {
					connCh <- taggedConn{conn, false}
					return
				}
			}
		}()

		tc1 := <-connCh
		close(stopConnCh)

		localIP, _, _ := net.SplitHostPort(tc1.conn.LocalAddr().String())
		shouldDial := localIP < p.cfg.PeerIP

		var finalConn net.Conn
		if (shouldDial && tc1.isDial) || (!shouldDial && !tc1.isDial) {
			finalConn = tc1.conn
			go func() {
				for tc := range connCh {
					tc.conn.Close()
				}
			}()
			close(connCh)
		} else {
			tc2, ok := <-connCh
			if !ok {
				finalConn = tc1.conn
			} else {
				close(connCh)
				if (shouldDial && tc2.isDial) || (!shouldDial && !tc2.isDial) {
					finalConn = tc2.conn
					tc1.conn.Close()
				} else {
					tc1.conn.Close()
					tc2.conn.Close()
					ln.Close()
					time.Sleep(p.cfg.ReconnectDelay)
					continue
				}
			}
		}

		conn := finalConn
		netRecvCh := make(chan string, 100)
		done := make(chan struct{})

		go func() {
			sc := bufio.NewScanner(conn)
			for sc.Scan() {
				text := sc.Text()
				if text == "_CS_NET_PING_" {
					continue
				}
				netRecvCh <- text
			}
			close(done)
		}()

		go func() {
			ticker := time.NewTicker(p.cfg.HeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					conn.SetWriteDeadline(time.Now().Add(p.cfg.HeartbeatTimeout))
					if _, err := conn.Write([]byte("_CS_NET_PING_\n")); err != nil {
						conn.Close()
						return
					}
				case <-done:
					return
				}
			}
		}()

		// 补发暂存消息
		if pendingMsg != "" {
			conn.SetWriteDeadline(time.Now().Add(p.cfg.HeartbeatTimeout))
			_, err := fmt.Fprintf(conn, "%s\n", pendingMsg)
			if err != nil {
				conn.Close()
				ln.Close()
				time.Sleep(p.cfg.ReconnectDelay)
				continue
			}
			pendingMsg = ""
		}

		if isFirstConnect {
			close(p.Connected)
			isFirstConnect = false
		}

		// 核心事件循环
		connAlive := true
		for connAlive {
			// 1. 优先排空队列里积压的消息（处理重连期间用户疯狂 Send 的数据）
			if msg, ok := p.sendQ.Pop(); ok {
				conn.SetWriteDeadline(time.Now().Add(p.cfg.HeartbeatTimeout))
				_, err := fmt.Fprintf(conn, "%s\n", msg)
				if err != nil {
					pendingMsg = msg
					connAlive = false
				}
				continue // 队列里还有就继续发，不卡在 select 里
			}

			// 2. 正常事件监听
			select {
			case <-p.notifySend:
				// 收到新数据信号，批量排空队列
			draining:
				for {
					msg, ok := p.sendQ.Pop()
					if !ok {
						break draining
					}
					conn.SetWriteDeadline(time.Now().Add(p.cfg.HeartbeatTimeout))
					_, err := fmt.Fprintf(conn, "%s\n", msg)
					if err != nil {
						pendingMsg = msg
						connAlive = false
						break draining
					}
				}
			case msg := <-netRecvCh:
				select {
				case p.Recv <- msg:
				case <-p.closeCh:
					connAlive = false
				}
			case <-done:
				// 网络断开，只抢救最后一条未发出的消息，清空其余历史积压防混乱
				if lastMsg, ok := p.sendQ.Pop(); ok {
					pendingMsg = lastMsg
				}
				p.sendQ.Clear()
				connAlive = false
			case <-p.closeCh:
				conn.Close()
				ln.Close()
				return
			}
		}

		conn.Close()
		ln.Close()
		time.Sleep(p.cfg.ReconnectDelay)
	}
}
