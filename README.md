# ClipSync

**ClipSync** 是一个轻量级的局域网剪切板共享工具。在同一局域网内的多台设备上运行 ClipSync，任何一台设备复制文本内容，其他设备会自动同步到剪切板，实现跨设备无缝粘贴。

## 功能特性

- **实时同步** — 复制即发送，自动写入对端剪切板
- **跨平台** — 支持 Windows 和 Linux（Wayland / X11）
- **P2P 直连** — 点对点 TCP 连接，无需中心服务器
- **智能连接** — 自动处理连接/重连，支持连接中断后恢复
- **心跳保活** — 内置心跳检测，及时感知连接状态
- **自动去重** — 避免重复发送相同内容，防止循环同步
- **大小限制** — 可配置最大同步内容大小（默认 3MB）
- **开机自启** — 一键配置开机自启动

## 快速开始

### 下载

从 [Releases](https://github.com/zyoung11/ClipSync/releases) 下载对应平台的二进制文件，或自行编译。

### 编译

```bash
# 克隆仓库
git clone https://github.com/Zyoung/clipsync.git
cd clipsync

# Windows
go build -ldflags="-s -w" -o clipsync.exe .

# Linux
GOOS=linux go build -ldflags="-s -w" -o clipsync .
```

> **注意：** Windows 下编译 Linux 版本时需要设置 `CGO_ENABLED=0`。

### 使用

ClipSync 提供两种使用方式：

#### 1. 交互式菜单

直接运行程序（不带参数）进入交互菜单：

```bash
clipsync
```

使用方向键选择操作，回车确认。

#### 2. 命令行模式

```bash
clipsync run        # 启动剪切板共享服务
clipsync autostart  # 切换开机自启（注册/取消）
clipsync log        # 查看服务运行日志
clipsync delete     # 删除配置文件
clipsync help       # 显示帮助信息
```

## 配置

### 首次运行

首次执行 `clipsync run` 时，程序会引导你输入要同步的局域网 IP 地址：

```
请输入要共享的局域网IP地址（多个IP用逗号分隔）:
IP地址: 192.168.1.101,192.168.1.102
```

配置文件保存在 `~/.config/clipsync/config.json`。

### 手动配置

```json
{
  "ip": ["192.168.1.101", "192.168.1.102"],
  "port": "8890",
  "maxLength": 100,
  "maxSize": 3145728
}
```

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `ip` | 对端设备 IP 地址列表 | `[]` |
| `port` | 通信端口 | `8890` |
| `maxLength` | 显示预览最大长度 | `100` |
| `maxSize` | 最大同步内容大小（字节） | `3145728` (3MB) |

> **提示：** 所有设备必须使用**相同端口号**。如果修改端口，请确保防火墙已放行。

## 命令详解

### `run` — 启动服务

启动剪切板监控和网络服务。程序会：
1. 读取配置文件
2. 连接到所有对端设备
3. 开始监听本地剪切板变化
4. 接收并写入来自对端的剪切板内容

按 `Ctrl + C` 停止服务。

### `autostart` — 切换开机自启

这是一个**开关式命令**：

- **没有自启任务时**：注册开机自启，并**立即在后台启动服务**
- **已有自启任务时**：删除自启任务，并**停止正在运行的服务**

#### Windows

使用 Windows 计划任务（schtasks），通过 VBS 脚本在后台静默启动。

#### Linux

使用 systemd user service，自动捕获当前 `WAYLAND_DISPLAY` / `DISPLAY` 环境变量。

> **注意：** Linux 下 systemd user service 在图形会话启动后延迟 15 秒启动，以确保剪切板工具就绪。

### `log` — 查看运行日志

查看服务运行日志，方便你排查问题或查看运行状态。

**静态查看**（显示最近 50 条）：
```bash
clipsync log
```

**实时跟踪**（类似 `tail -f`）：
```bash
clipsync log -f
```

按 `Ctrl + C` 退出实时跟踪。

日志文件位置：`~/.config/clipsync/clipsync.log`

示例输出：
```
[2026-05-22 15:30:01] 启动剪切板共享服务 (端口: 8890)
[2026-05-22 15:30:01] 共享设备: [192.168.1.101]
[2026-05-22 15:30:01] 正在连接设备 192.168.1.101:8890...
[2026-05-22 15:30:02] 所有设备连接已建立！
[2026-05-22 15:30:02] 剪切板监控已启动，按 Ctrl+C 退出
[2026-05-22 15:30:15] 收到剪切板内容 (42 bytes)
```

### `delete` — 删除配置

删除整个配置目录 `~/.config/clipsync/`。

## 平台支持

### Windows

- 使用 `golang.design/x/clipboard` 监听剪切板
- 支持 Wayland / X11 下的 `wl-paste`、`xclip`、`xsel` 自动检测
- 自动轮询模式（500ms 间隔）或监听模式

### Linux

- 自动检测可用剪切板工具（wl-paste > xclip > xsel）
- 支持 Wayland 和 X11
- 如果当前没有剪切板工具则自动重试

## 网络架构

ClipSync 采用 P2P 架构，每台设备同时作为**客户端**和**服务端**：

1. 每台设备在指定端口上监听 TCP 连接
2. 同时主动向对端发起连接
3. 通过 IP 比较解决"同时连接"冲突（较小 IP 的设备保持客户端角色）
4. 连接建立后发送心跳保活
5. 断线后自动重连，暂存未发送的最后一条消息

数据通过 TCP 直接传输，使用 `\n` 分隔的消息协议。

## 安全与防火墙

Windows 下提供了防火墙规则管理函数（需管理员权限），当前版本未自动调用。如需手动添加：

```cmd
netsh advfirewall firewall add rule name="ClipSync" dir=in action=allow protocol=TCP localport=8890
```

> **建议：** 仅在可信局域网内使用，ClipSync **不提供加密传输**。

## 常见问题

### 如何检查服务是否正常运行？

**查看日志：**
```bash
clipsync log
```

**查看进程：**
```bash
# Windows
tasklist /FI "IMAGENAME eq clipsync.exe"

# Linux
ps aux | grep clipsync
```

**查看网络连接：**
```bash
# Windows
netstat -an | findstr "8890"

# Linux
ss -tlnp | grep 8890
```

正常状态应看到：
- 端口 `8890` 处于 `LISTEN` 状态
- 对端 IP 的 TCP 连接处于 `ESTABLISHED` 状态

### 两台设备无法连接？

1. 确认两台设备在同一个局域网
2. 检查防火墙是否放行端口 `8890`
3. 确认配置文件中 IP 地址填写正确
4. 尝试 `clipsync delete` 后重新配置

### 如何手动停止程序？

- **前台运行**：按 `Ctrl + C`
- **后台运行**：使用系统进程管理器杀掉 `clipsync.exe` / `clipsync` 进程
- **一键停止**（Windows）：再次运行 `clipsync autostart` 即可删除自启并停止服务

## 许可证

[MIT](LICENSE)
