package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 全局变量
var (
	currentProxy *ProxyServer
	currentMOTD  string
	proxyList    = make(map[string]string) // 保存代理服务器列表 [地址]描述
)

// 添加版本定义
type MinecraftVersion struct {
	Protocol int
	Name     string
}

var (
	// 支持的版本列表
	supportedVersions = map[int]MinecraftVersion{
		47:  {47, "1.8.x"},
		107: {107, "1.9"},
		210: {210, "1.10.x"},
		315: {315, "1.11.x"},
		335: {335, "1.12"},
		340: {340, "1.12.1"},
		338: {338, "1.12.2"},
		393: {393, "1.13"},
		401: {401, "1.13.1"},
		404: {404, "1.13.2"},
		477: {477, "1.14.x"},
		480: {480, "1.14.1"},
		485: {485, "1.14.2"},
		490: {490, "1.14.3"},
		498: {498, "1.14.4"},
		573: {573, "1.15"},
		575: {575, "1.15.1"},
		578: {578, "1.15.2"},
		735: {735, "1.16"},
		736: {736, "1.16.1"},
		751: {751, "1.16.2"},
		753: {753, "1.16.3"},
		754: {754, "1.16.4/5"},
		755: {755, "1.17"},
		756: {756, "1.17.1"},
		757: {757, "1.18/1.18.1"},
		758: {758, "1.18.2"},
		759: {759, "1.19"},
		760: {760, "1.19.1/2"},
		761: {761, "1.19.3"},
		762: {762, "1.19.4"},
		763: {763, "1.20/1.20.1"},
		764: {764, "1.20.2"},
		765: {765, "1.20.3/4"},
		766: {766, "1.20.5"},
		767: {767, "1.21"},
		768: {768, "1.21.1"},
		769: {769, "1.21.2"},
		770: {770, "1.21.3"},
		771: {771, "1.21.4"},
	}
)

// 添加新的结构来跟踪连接状态
type clientConnection struct {
	conn                 net.Conn
	compressionThreshold int  // -1 表示未压缩
	loggedIn             bool // 是否已登录到服务器
}

// ProxyServer 代理服务器
type ProxyServer struct {
	targetServer           string
	localPort              string
	listener               net.Listener
	connections            map[string]*clientConnection
	mutex                  sync.RWMutex
	motdMessage            string
	messageQueue           []string
	done                   chan struct{}
	defaultProtocolVersion int        // 仅用于聊天消息
	pendingMessages        []string   // 待发送的消息队列
	messageMutex           sync.Mutex // 消息队列的互斥锁
}

// 添加日志结构体
type Logger struct {
	file    *os.File
	mutex   sync.Mutex
	logChan chan string
}

var globalLogger *Logger

// 初始化日志系统
func initLogger() error {
	logFile, err := os.OpenFile("proxy.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("创建日志文件失败: %v", err)
	}

	globalLogger = &Logger{
		file:    logFile,
		logChan: make(chan string, 1000),
	}

	// 启动日志写入协程
	go globalLogger.logWriter()

	// 启动日志查看器
	go startLogViewer()

	return nil
}

// 日志写入
func (l *Logger) logWriter() {
	for msg := range l.logChan {
		l.mutex.Lock()
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(l.file, "[%s] %s\n", timestamp, msg)
		l.mutex.Unlock()
	}
}

// 写入日志
func logMessage(format string, args ...interface{}) {
	if globalLogger != nil {
		msg := fmt.Sprintf(format, args...)
		globalLogger.logChan <- msg
	}
}

// 启动日志查看器
func startLogViewer() {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "cmd", "/c", "type", "proxy.log", "&", "timeout", "/t", "1", "&", "goto", "0")
	case "linux", "darwin":
		cmd = exec.Command("xterm", "-e", "tail -f proxy.log")
	default:
		fmt.Println("不支持的操作系统")
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("启动日志查看器失败: %v\n", err)
	}
}

func main() {
	// 初始化日志系统
	if err := initLogger(); err != nil {
		fmt.Printf("初始化日志系统失败: %v\n", err)
		return
	}
	defer globalLogger.file.Close()

	logMessage("代理服务器启动")

	reader := bufio.NewReader(os.Stdin)
	loadProxyList() // 从文件加载代理服务器列表

	for {
		showMenu()
		choice := readInput(reader)

		switch choice {
		case "1":
			handleMOTDEditor()
		case "2":
			handleProxyControl(reader)
		case "3":
			fmt.Println("再见!")
			return
		default:
			fmt.Println("无效的选择，请重试")
		}
	}
}

func showMenu() {
	fmt.Println("\n=== 在线聊天栏插入工具 ===")
	fmt.Println("1. MOTD编辑器")
	fmt.Println("2. 代理控制")
	fmt.Println("3. 退出")
	fmt.Print("请选择: ")
}

func handleMOTDEditor() {
	fmt.Println("\n=== 聊天栏编辑器 ===")
	fmt.Println("颜色代码:")
	// 使用ANSI颜色代码展示
	fmt.Printf("&0 %s黑色%s    ", "\033[30m", "\033[97m") // 黑色用白色底色显示
	fmt.Printf("&1 %s深蓝色%s  ", "\033[34m", "\033[0m")
	fmt.Printf("&2 %s深绿色%s  ", "\033[32m", "\033[0m")
	fmt.Printf("&3 %s湖蓝色%s\n", "\033[36m", "\033[0m")
	fmt.Printf("&4 %s深红色%s  ", "\033[31m", "\033[0m")
	fmt.Printf("&5 %s紫色%s    ", "\033[35m", "\033[0m")
	fmt.Printf("&6 %s金色%s    ", "\033[33m", "\033[0m")
	fmt.Printf("&7 %s灰色%s\n", "\033[37m", "\033[0m")
	fmt.Printf("&8 %s深灰色%s  ", "\033[90m", "\033[0m")
	fmt.Printf("&9 %s蓝色%s    ", "\033[94m", "\033[0m")
	fmt.Printf("&a %s绿色%s    ", "\033[92m", "\033[0m")
	fmt.Printf("&b %s天蓝色%s\n", "\033[96m", "\033[0m")
	fmt.Printf("&c %s红色%s    ", "\033[91m", "\033[0m")
	fmt.Printf("&d %s粉红色%s  ", "\033[95m", "\033[0m")
	fmt.Printf("&e %s黄色%s    ", "\033[93m", "\033[0m")
	fmt.Printf("&f %s白色%s\n", "\033[97m", "\033[0m")

	fmt.Println("\n格式代码:")
	fmt.Printf("&k %s随机字符%s  ", "\033[5m", "\033[0m")
	fmt.Printf("&l %s粗体%s    ", "\033[1m", "\033[0m")
	fmt.Printf("&m %s删除线%s  ", "\033[9m", "\033[0m")
	fmt.Printf("&n %s下划线%s  ", "\033[4m", "\033[0m")
	fmt.Printf("&o %s斜体%s    ", "\033[3m", "\033[0m")
	fmt.Printf("&r %s重置%s\n", "\033[0m", "\033[0m")

	fmt.Println("\n命令:")
	fmt.Println("//wq - 保存并退出")
	fmt.Println("//up - 插入到聊天栏")
	fmt.Println("//pv - 预览效果")
	fmt.Println("\n请输入MOTD内容:")

	var lines []string
	reader := bufio.NewReader(os.Stdin)

	for {
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		switch line {
		case "//wq":
			currentMOTD = strings.Join(lines, "\n")
			if currentProxy != nil {
				currentProxy.SetMOTD(currentMOTD)
			}
			return
		case "//up":
			if currentProxy != nil {
				motd := strings.Join(lines, "\n")
				if err := currentProxy.InsertChatMessage(motd); err != nil {
					fmt.Printf("插入失败: %v\n", err)
				} else {
					fmt.Println("成功插入到聊天栏")
				}
			} else {
				fmt.Println("错误: 代理服务器未运行")
			}
		case "//pv":
			fmt.Println("\n预览效果:")
			previewMOTD(strings.Join(lines, "\n"))
			fmt.Println()
		default:
			lines = append(lines, line)
			// 实时预览
			fmt.Print("\033[2K\r") // 清除当前行
			previewMOTD(line)
		}
	}
}

func handleProxyControl(reader *bufio.Reader) {
	for {
		status := "已停止"
		if currentProxy != nil {
			status = "运行中"
		}

		fmt.Printf("\n=== 代理控制 (状态: %s) ===\n", status)
		fmt.Println("1. 启动代理")
		fmt.Println("2. 停止代理")
		fmt.Println("3. 添加代理服务器")
		fmt.Println("4. 删除代理服务器")
		fmt.Println("5. 返回主菜单")
		fmt.Print("请选择: ")

		choice := readInput(reader)
		switch choice {
		case "1":
			startProxy(reader)
		case "2":
			stopProxy()
		case "3":
			addProxyServer(reader)
		case "4":
			deleteProxyServer(reader)
		case "5":
			return
		}
	}
}

func addProxyServer(reader *bufio.Reader) {
	listProxyServers()
	fmt.Print("\n请输入服务器地址 (例如: mc.hypixel.net): ")
	address := readInput(reader)
	fmt.Print("请输入服务器描述: ")
	desc := readInput(reader)

	proxyList[address] = desc
	saveProxyList()
	fmt.Println("代理服务器已添加")
}

func deleteProxyServer(reader *bufio.Reader) {
	listProxyServers()
	if len(proxyList) == 0 {
		return
	}

	fmt.Print("\n请输入要删除的服务器序号或地址: ")
	input := readInput(reader)

	// 尝试解析序号
	if index, err := strconv.Atoi(input); err == nil && index > 0 && index <= len(proxyList) {
		// 找到对应序号的服务器
		i := 1
		for addr := range proxyList {
			if i == index {
				delete(proxyList, addr)
				saveProxyList()
				fmt.Println("代理服务器已删除")
				return
			}
			i++
		}
	}

	// 如果不是序号，当作地址处理
	if _, exists := proxyList[input]; exists {
		delete(proxyList, input)
		saveProxyList()
		fmt.Println("代理服务器已删除")
	} else {
		fmt.Println("服务器不存在")
	}
}

func listProxyServers() {
	fmt.Println("\n当前代理服务器列表:")
	if len(proxyList) == 0 {
		fmt.Println("(空)")
		return
	}

	// 将服务器列表转换为切片以保持顺序
	var servers []struct {
		index   int
		address string
		desc    string
	}

	i := 1
	for addr, desc := range proxyList {
		servers = append(servers, struct {
			index   int
			address string
			desc    string
		}{i, addr, desc})
		i++
	}

	// 按序号排序并显示
	for _, server := range servers {
		fmt.Printf("%d. %s - %s\n", server.index, server.address, server.desc)
	}
}

func loadProxyList() {
	data, err := os.ReadFile("proxy_list.txt")
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 {
			proxyList[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
}

func saveProxyList() {
	var lines []string
	for addr, desc := range proxyList {
		lines = append(lines, fmt.Sprintf("%s|%s", addr, desc))
	}
	os.WriteFile("proxy_list.txt", []byte(strings.Join(lines, "\n")), 0644)
}

// ProxyServer方法
func (p *ProxyServer) Start() error {
	p.done = make(chan struct{})
	listener, err := net.Listen("tcp", ":"+p.localPort)
	if err != nil {
		return fmt.Errorf("启动代理服务器失败: %v", err)
	}

	p.listener = listener
	go p.acceptConnections()
	return nil
}

func (p *ProxyServer) Stop() error {
	// 关闭done通道，通知所有goroutine退出
	close(p.done)

	// 关闭监听器
	if p.listener != nil {
		p.listener.Close()
	}

	// 关闭所有连接
	p.mutex.Lock()
	for _, conn := range p.connections {
		conn.conn.Close()
	}
	p.connections = make(map[string]*clientConnection)
	p.mutex.Unlock()

	return nil
}

func (p *ProxyServer) SetMOTD(motd string) {
	p.mutex.Lock()
	p.motdMessage = motd
	p.mutex.Unlock()
}

func (p *ProxyServer) acceptConnections() {
	for {
		clientConn, err := p.listener.Accept()
		if err != nil {
			if !isTemporaryError(err) {
				return
			}
			continue
		}

		go p.handleConnection(clientConn)
	}
}

func (p *ProxyServer) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// 连接到目标服务器
	serverConn, err := net.Dial("tcp", p.targetServer)
	if err != nil {
		fmt.Printf("连接目标服务器失败: %v\n", err)
		return
	}
	defer serverConn.Close()

	// 创建客户端连接对象
	connID := fmt.Sprintf("%s", clientConn.RemoteAddr())
	conn := &clientConnection{
		conn:                 clientConn,
		compressionThreshold: -1,
		loggedIn:             false, // 初始状态为未登录
	}

	// 添加到连接池
	p.mutex.Lock()
	p.connections[connID] = conn
	p.mutex.Unlock()

	defer func() {
		p.mutex.Lock()
		delete(p.connections, connID)
		p.mutex.Unlock()
	}()

	// 创建连接专用的done通道
	connDone := make(chan struct{})
	defer close(connDone)

	// 启动数据转发
	errChan := make(chan error, 2)
	go p.proxyData(clientConn, serverConn, errChan, connDone, "客户端->服务器", connID)
	go p.proxyData(serverConn, clientConn, errChan, connDone, "服务器->客户端", connID)

	// 等待错误或关闭信号
	select {
	case <-errChan:
		return
	case <-p.done:
		return
	}
}

func (p *ProxyServer) proxyData(src, dst net.Conn, errChan chan error, done chan struct{}, direction string, connID string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("数据转发恢复自panic: %v\n", r)
			errChan <- fmt.Errorf("panic: %v", r)
		}
	}()

	for {
		select {
		case <-done:
			return
		case <-p.done:
			return
		default:
			// 读取数据包长度
			packetLength, err := readVarInt(src)
			if err != nil {
				if err != io.EOF {
					errChan <- fmt.Errorf("%s读取数据包长度错误: %v", direction, err)
				}
				return
			}

			// 读取完整的数据包
			packetData := make([]byte, packetLength)
			_, err = io.ReadFull(src, packetData)
			if err != nil {
				errChan <- fmt.Errorf("%s读取数据包内容错误: %v", direction, err)
				return
			}

			// 如果是服务器到客户端的方向
			if direction == "服务器->客户端" {
				// 读取数据包ID
				packetReader := bytes.NewReader(packetData)
				packetID, err := readVarInt(packetReader)
				if err != nil {
					errChan <- fmt.Errorf("读取数据包ID错误: %v", err)
					return
				}

				// 检测压缩阈值设置包
				if packetID == 0x03 { // Set Compression packet ID
					threshold, err := readVarInt(bytes.NewReader(packetData[1:])) // 跳过包ID
					if err == nil {
						p.mutex.Lock()
						if client, exists := p.connections[connID]; exists {
							client.compressionThreshold = threshold
							fmt.Printf("客户端 %s 的压缩阈值设置为: %d\n", connID, threshold)
						}
						p.mutex.Unlock()
					}
				}

				// 检测玩家是否已进入游戏
				if packetID == 0x26 { // Join Game packet ID (可能需要根据版本调整)
					p.mutex.Lock()
					if client, exists := p.connections[connID]; exists {
						if !client.loggedIn {
							client.loggedIn = true
							// 发送连接成功提示
							go func() {
								time.Sleep(500 * time.Millisecond) // 稍微延迟一下，确保客户端准备好
								p.InsertChatMessage("§e已连接自定义聊天栏信息系统")
							}()
						}
					}
					p.mutex.Unlock()
				}

				// 转发原始数据包
				var fullPacket bytes.Buffer
				writeVarInt(&fullPacket, len(packetData))
				fullPacket.Write(packetData)
				if _, err := dst.Write(fullPacket.Bytes()); err != nil {
					errChan <- fmt.Errorf("发送原始数据包失败: %v", err)
					return
				}
			} else {
				// 客户端到服务器的数据包直接转发
				var fullPacket bytes.Buffer
				writeVarInt(&fullPacket, len(packetData))
				fullPacket.Write(packetData)
				if _, err := dst.Write(fullPacket.Bytes()); err != nil {
					errChan <- fmt.Errorf("%s写入错误: %v", direction, err)
					return
				}
			}
		}
	}
}

func (p *ProxyServer) InsertChatMessage(message string) error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	if len(p.connections) == 0 {
		return fmt.Errorf("没有客户端连接到代理服务器")
	}

	message = strings.ReplaceAll(message, "&", "§")

	// 构建聊天数据包
	packet := buildSystemMessagePacket(message, p.defaultProtocolVersion)
	if packet == nil {
		return fmt.Errorf("构建数据包失败")
	}

	// 发送给所有连接的客户端
	var lastErr error
	sentCount := 0
	for connID, client := range p.connections {
		if !client.loggedIn {
			logMessage("跳过未登录的客户端: %s", connID)
			continue
		}

		var fullPacket bytes.Buffer
		if client.compressionThreshold > 0 {
			// 需要压缩
			if len(packet) >= client.compressionThreshold {
				// 压缩数据
				compressed, err := compressPacket(packet)
				if err != nil {
					lastErr = err
					continue
				}
				// 写入数据包总长度
				writeVarInt(&fullPacket, len(compressed)+varIntLength(len(packet)))
				// 写入原始数据长度
				writeVarInt(&fullPacket, len(packet))
				// 写入压缩后的数据
				fullPacket.Write(compressed)
			} else {
				// 数据长度小于阈值，不压缩
				// 写入数据包总长度
				writeVarInt(&fullPacket, len(packet)+1)
				// 写入未压缩标记(0)
				writeVarInt(&fullPacket, 0)
				// 写入原始数据
				fullPacket.Write(packet)
			}
		} else {
			// 未启用压缩，直接发送
			writeVarInt(&fullPacket, len(packet))
			fullPacket.Write(packet)
		}

		// 修改这里：正确处理 Write 的返回值
		_, err := client.conn.Write(fullPacket.Bytes())
		if err != nil {
			lastErr = err
			logMessage("发送消息到客户端 %s 失败: %v", connID, err)
			continue
		}
		logMessage("成功发送消息到客户端: %s", connID)
		sentCount++
	}

	if sentCount == 0 {
		if lastErr != nil {
			return fmt.Errorf("发送失败: %v", lastErr)
		}
		return fmt.Errorf("没有成功发送到任何客户端")
	}

	fmt.Printf("成功发送消息到 %d 个客户端\n", sentCount)
	return nil
}

// 添加计算 VarInt 长度的函数
func varIntLength(value int) int {
	length := 0
	for {
		length++
		value >>= 7
		if value == 0 {
			break
		}
	}
	return length
}

// 压缩数据包
func compressPacket(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// 添加新的辅助函数用于分割消息
func splitMessage(message string, maxLength int) []string {
	var messages []string
	runes := []rune(message)

	for len(runes) > 0 {
		if len(runes) <= maxLength {
			messages = append(messages, string(runes))
			break
		}

		// 查找合适的分割点（颜色代码处）
		splitIndex := maxLength
		for i := maxLength; i >= 0; i-- {
			if i < len(runes)-1 && runes[i] == '§' {
				splitIndex = i
				break
			}
			if runes[i] == ' ' || runes[i] == '\n' {
				splitIndex = i
				break
			}
		}

		// 保持颜色代码连续性
		currentMsg := string(runes[:splitIndex])
		messages = append(messages, currentMsg)

		// 获取当前消息的最后一个颜色代码
		lastColor := getLastColorCode(currentMsg)
		runes = runes[splitIndex:]

		// 如果有颜色代码，添加到下一段消息开头
		if lastColor != "" && len(runes) > 0 {
			runes = append([]rune(lastColor), runes...)
		}
	}

	return messages
}

// 添加新的函数用于获取最后的颜色代码
func getLastColorCode(message string) string {
	runes := []rune(message)
	lastCode := ""

	for i := len(runes) - 2; i >= 0; i-- {
		if runes[i] == '§' {
			code := string(runes[i : i+2])
			if strings.ContainsAny(string(runes[i+1]), "0123456789abcdefklmnor") {
				lastCode = code
				break
			}
		}
	}

	return lastCode
}

// 辅助函数
func readInput(reader *bufio.Reader) string {
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func isTemporaryError(err error) bool {
	if tempErr, ok := err.(interface{ Temporary() bool }); ok {
		return tempErr.Temporary()
	}
	return false
}

func buildChatPacketForVersion(message string, version int) []byte {
	var packet bytes.Buffer

	// 转义消息中的特殊字符
	message = strings.ReplaceAll(message, `"`, `\"`)

	if version >= 760 { // 1.19.1+
		// 数据包ID (0x62 for system message in 1.19+)
		writeVarInt(&packet, 0x62)

		// 消息JSON
		chatJson := fmt.Sprintf(`{"text":"%s"}`, message)
		writeString(&packet, chatJson)

		// 是否在动作栏显示 (false)
		packet.WriteByte(0)

	} else if version >= 736 { // 1.16+
		// 数据包ID (0x0E for system message in 1.16+)
		writeVarInt(&packet, 0x0E)

		// 消息JSON
		chatJson := fmt.Sprintf(`{"text":"%s"}`, message)
		writeString(&packet, chatJson)

		// 消息类型 (1 = system message)
		packet.WriteByte(1)

		// Sender UUID
		packet.Write(make([]byte, 16))

	} else { // 1.8 - 1.15
		// 数据包ID (0x02)
		writeVarInt(&packet, 0x02)

		// 消息JSON
		chatJson := fmt.Sprintf(`{"text":"%s"}`, message)
		writeString(&packet, chatJson)

		// 消息类型 (1 = system message)
		packet.WriteByte(1)
	}

	return packet.Bytes()
}

// 修改 writeVarInt 函数，确保正确处理大数字
func writeVarInt(w io.Writer, value int) error {
	if value < 0 {
		return fmt.Errorf("不能写入负数: %d", value)
	}

	for {
		temp := byte(value & 0x7F)
		value >>= 7
		if value != 0 {
			temp |= 0x80
		}
		if err := binary.Write(w, binary.BigEndian, temp); err != nil {
			return err
		}
		if value == 0 {
			break
		}
	}
	return nil
}

func (p *ProxyServer) sendPacket(connID string, data []byte) error {
	p.mutex.RLock()
	client, exists := p.connections[connID]
	p.mutex.RUnlock()

	if !exists {
		return fmt.Errorf("客户端不存在")
	}

	// 构建完整的数据包
	var packet bytes.Buffer

	// 写入数据包长度
	writeVarInt(&packet, len(data))

	// 写入数据包内容
	packet.Write(data)

	// 一次性发送完整的数据包
	_, err := client.conn.Write(packet.Bytes())
	if err != nil {
		return fmt.Errorf("发送数据包失败: %v", err)
	}

	return nil
}

func previewMOTD(motd string) {
	// 使用ANSI转义序列显示颜色
	colorCodes := map[byte]string{
		'0': "\033[30m", // 黑色
		'1': "\033[34m", // 深蓝色
		'2': "\033[32m", // 深绿色
		'3': "\033[36m", // 湖蓝色
		'4': "\033[31m", // 深红色
		'5': "\033[35m", // 紫色
		'6': "\033[33m", // 金色
		'7': "\033[37m", // 灰色
		'8': "\033[90m", // 深灰色
		'9': "\033[94m", // 蓝色
		'a': "\033[92m", // 绿色
		'b': "\033[96m", // 天蓝色
		'c': "\033[91m", // 红色
		'd': "\033[95m", // 粉红色
		'e': "\033[93m", // 黄色
		'f': "\033[97m", // 白色
	}

	reset := "\033[0m"
	bold := "\033[1m"
	underline := "\033[4m"
	strikethrough := "\033[9m"
	italic := "\033[3m"

	formatted := motd
	inFormat := false

	for i := 0; i < len(formatted); i++ {
		if formatted[i] == '&' && i+1 < len(formatted) {
			code := formatted[i+1]
			if color, ok := colorCodes[code]; ok {
				fmt.Print(color)
				i++
				continue
			}

			switch code {
			case 'l':
				fmt.Print(bold)
			case 'n':
				fmt.Print(underline)
			case 'm':
				fmt.Print(strikethrough)
			case 'o':
				fmt.Print(italic)
			case 'r':
				fmt.Print(reset)
			case 'k':
				if !inFormat {
					inFormat = true
					fmt.Print("█")
				}
			}
			i++
			continue
		}

		if !inFormat {
			fmt.Print(string(formatted[i]))
		}
	}

	fmt.Println(reset)
}

// 添加新的函数用于解析服务器地址
func resolveServerAddress(address string) (string, error) {
	// 如果地址中已包含端口号，直接返回
	if strings.Contains(address, ":") {
		return address, nil
	}

	// 尝试 SRV 解析
	_, addrs, err := net.LookupSRV("minecraft", "tcp", address)
	if err == nil && len(addrs) > 0 {
		srv := addrs[0]
		return fmt.Sprintf("%s:%d", srv.Target[:len(srv.Target)-1], srv.Port), nil
	}

	// 如果 SRV 解析失败，尝试 A 记录解析
	ips, err := net.LookupIP(address)
	if err != nil {
		return "", fmt.Errorf("无法解析服务器地址: %v", err)
	}

	// 使用找到的第一个 IPv4 地址
	for _, ip := range ips {
		if ip.To4() != nil {
			return fmt.Sprintf("%s:25565", ip.String()), nil
		}
	}

	return "", fmt.Errorf("未找到有效的服务器地址")
}

// 添加版本选择函数
func selectMinecraftVersion(reader *bufio.Reader) int {
	fmt.Println("\n请选择 Minecraft 版本:")

	// 将版本按照协议号排序
	var versions []struct {
		protocol int
		name     string
	}
	for protocol, version := range supportedVersions {
		versions = append(versions, struct {
			protocol int
			name     string
		}{protocol, version.Name})
	}

	// 按协议号排序
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].protocol > versions[j].protocol
	})

	// 显示版本列表
	for i, v := range versions {
		fmt.Printf("%d. %s (协议版本: %d)\n", i+1, v.name, v.protocol)
	}

	fmt.Print("\n请选择版本编号: ")
	for {
		input := readInput(reader)
		if index, err := strconv.Atoi(input); err == nil && index > 0 && index <= len(versions) {
			return versions[index-1].protocol
		}
		fmt.Print("无效的选择，请重试: ")
	}
}

// 修改 startProxyWithAddress 函数
func startProxyWithAddress(inputServer string) {
	// 解析服务器地址
	targetServer, err := resolveServerAddress(inputServer)
	if err != nil {
		fmt.Printf("解析服务器地址失败: %v\n", err)
		return
	}

	// 选择版本
	reader := bufio.NewReader(os.Stdin)
	protocolVersion := selectMinecraftVersion(reader)

	proxy := &ProxyServer{
		targetServer:           targetServer,
		localPort:              "25566",
		connections:            make(map[string]*clientConnection),
		motdMessage:            currentMOTD,
		done:                   make(chan struct{}),
		defaultProtocolVersion: protocolVersion,
	}

	if err := proxy.Start(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		return
	}

	currentProxy = proxy
	fmt.Printf("代理服务器已启动，本地端口: %s\n", proxy.localPort)
	fmt.Printf("请在MC客户端中输入地址: localhost:%s\n", proxy.localPort)
	fmt.Printf("目标服务器: %s (解析自: %s)\n", targetServer, inputServer)
	fmt.Printf("选择的版本: %s (协议版本: %d)\n",
		supportedVersions[protocolVersion].Name, protocolVersion)
}

func stopProxy() {
	if currentProxy == nil {
		fmt.Println("代理服务器未运行")
		return
	}

	if err := currentProxy.Stop(); err != nil {
		fmt.Printf("停止失败: %v\n", err)
		return
	}

	currentProxy = nil
	fmt.Println("代理服务器已停止")
}

// 添加 readVarInt 函数
func readVarInt(r io.Reader) (int, error) {
	var value int
	var position int

	for {
		var currentByte byte
		if err := binary.Read(r, binary.BigEndian, &currentByte); err != nil {
			return 0, err
		}

		value |= int(currentByte&0x7F) << position
		position += 7

		if currentByte&0x80 == 0 {
			break
		}

		if position >= 32 {
			return 0, fmt.Errorf("VarInt 太长")
		}
	}

	return value, nil
}

// 添加 writeString 函数
func writeString(w io.Writer, s string) error {
	bytes := []byte(s)
	if err := writeVarInt(w, len(bytes)); err != nil {
		return err
	}
	_, err := w.Write(bytes)
	return err
}

// 添加 startProxy 函数
func startProxy(reader *bufio.Reader) {
	if currentProxy != nil {
		fmt.Println("代理服务器已在运行")
		return
	}

	// 显示服务器列表
	listProxyServers()
	if len(proxyList) == 0 {
		fmt.Print("\n请输入目标服务器地址: ")
		inputServer := readInput(reader)
		startProxyWithAddress(inputServer)
		return
	}

	fmt.Print("\n请选择服务器序号或直接输入地址: ")
	input := readInput(reader)

	// 尝试解析序号
	if index, err := strconv.Atoi(input); err == nil && index > 0 && index <= len(proxyList) {
		// 找到对应序号的服务器
		i := 1
		for addr := range proxyList {
			if i == index {
				startProxyWithAddress(addr)
				return
			}
			i++
		}
	}

	// 如果不是有效序号，当作地址处理
	startProxyWithAddress(input)
}

// 添加设置压缩阈值的方法
func (p *ProxyServer) setCompressionThreshold(connID string, threshold int) {
	p.mutex.Lock()
	if client, exists := p.connections[connID]; exists {
		client.compressionThreshold = threshold
	}
	p.mutex.Unlock()
}

// 修改 buildChatPacketForVersion 函数，构建系统消息
func buildSystemMessagePacket(message string, version int) []byte {
	var packet bytes.Buffer

	// 转义消息中的特殊字符
	message = strings.ReplaceAll(message, `"`, `\"`)

	if version >= 760 { // 1.19.1+
		// 聊天消息数据包 ID (0x62)
		writeVarInt(&packet, 0x62)

		// 构建消息 JSON
		chatJson := fmt.Sprintf(`{"text":"%s","bold":false,"italic":false,"underlined":false,"strikethrough":false,"obfuscated":false}`, message)
		writeString(&packet, chatJson)

		// 消息位置 (0 = chat, 1 = system, 2 = game info)
		packet.WriteByte(0)

		// 发送者 UUID (全0表示系统消息)
		packet.Write(make([]byte, 16))

		// 是否为系统消息
		packet.WriteByte(0)

	} else if version >= 736 { // 1.16+
		// 聊天消息数据包 ID (0x0E)
		writeVarInt(&packet, 0x0E)

		// 构建消息 JSON
		chatJson := fmt.Sprintf(`{"text":"%s","bold":false,"italic":false,"underlined":false,"strikethrough":false,"obfuscated":false}`, message)
		writeString(&packet, chatJson)

		// 消息位置 (0 = chat, 1 = system, 2 = game info)
		packet.WriteByte(0)

		// 发送者 UUID (全0表示系统消息)
		packet.Write(make([]byte, 16))

	} else { // 1.8 - 1.15
		// 聊天消息数据包 ID (0x02)
		writeVarInt(&packet, 0x02)

		// 构建消息 JSON
		chatJson := fmt.Sprintf(`{"text":"%s","bold":false,"italic":false,"underlined":false,"strikethrough":false,"obfuscated":false}`, message)
		writeString(&packet, chatJson)

		// 消息位置 (0 = chat)
		packet.WriteByte(0)
	}

	return packet.Bytes()
}
