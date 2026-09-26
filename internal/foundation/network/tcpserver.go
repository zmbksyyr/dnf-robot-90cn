package network

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
)

const (
	defaultListenAddr         = "0.0.0.0:8188"
	maxReceiveBufferSize      = 204800
	initialReceiveBufferSize  = 4096
	defaultMaxClients         = 256
	defaultReadTimeout        = 90 * time.Second
	defaultWriteTimeout       = 15 * time.Second
	defaultFirstPacketTimeout = 5 * time.Second
	defaultMaxPendingPerIP    = 512
	defaultMaxPending         = 512
	// defaultCloseWaitTimeout bounds how long Close waits for in-flight
	// synchronous command handlers. A handler can run up to the managed
	// command budget (75s), but process shutdown must stay bounded.
	defaultCloseWaitTimeout = 15 * time.Second
)

var (
	ErrTCPServerRunning = errors.New("TCP server is already running")
	ErrTCPServerClosed  = errors.New("TCP server is closed")
)

type tcpClient struct {
	conn    net.Conn
	writeMu lockhub.Locker
}

type TCPServer struct {
	lifecycleMu      lockhub.Locker
	closed           bool
	closeDone        chan struct{}
	listener         net.Listener
	addr             string
	clients          map[string]*tcpClient
	connCount        int
	pendingCount     int
	pendingByIP      map[string]int
	clientsMu        lockhub.RWLocker
	onMessage        func(clientID string, data []byte)
	running          atomic.Bool
	wg               sync.WaitGroup
	maxClients       int
	readTimeout      time.Duration
	writeTimeout     time.Duration
	firstTimeout     time.Duration
	maxPending       int
	maxPendingIP     int
	closeWaitTimeout time.Duration
}

func NewTCPServer(addr string) *TCPServer {
	if addr == "" {
		addr = defaultListenAddr
	}
	return &TCPServer{
		addr:         addr,
		closeDone:    make(chan struct{}),
		clients:      make(map[string]*tcpClient),
		pendingByIP:  make(map[string]int),
		maxClients:   defaultMaxClients,
		readTimeout:  defaultReadTimeout,
		writeTimeout: defaultWriteTimeout,
		firstTimeout: defaultFirstPacketTimeout,
		maxPending:   defaultMaxPending,
		maxPendingIP: defaultMaxPendingPerIP,
	}
}

func (s *TCPServer) SetLimits(maxClients int, readTimeout, writeTimeout time.Duration) {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed || s.running.Load() {
		return
	}
	if maxClients > 0 {
		s.maxClients = maxClients
	}
	if readTimeout > 0 {
		s.readTimeout = readTimeout
	}
	if writeTimeout > 0 {
		s.writeTimeout = writeTimeout
	}
}

func (s *TCPServer) OnMessage(callback func(clientID string, data []byte)) {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closed || s.running.Load() {
		return
	}
	s.onMessage = callback
}

func (s *TCPServer) Start() error {
	if s == nil {
		return ErrTCPServerClosed
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closeDone == nil {
		s.closeDone = make(chan struct{})
	}
	if s.closed {
		return ErrTCPServerClosed
	}
	if s.running.Load() {
		return ErrTCPServerRunning
	}
	s.initializeLocked()
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("TCPServer listen %s: %w", s.addr, err)
	}
	s.listener = listener
	s.running.Store(true)
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *TCPServer) initializeLocked() {
	if s.addr == "" {
		s.addr = defaultListenAddr
	}
	if s.closeDone == nil {
		s.closeDone = make(chan struct{})
	}
	if s.maxClients <= 0 {
		s.maxClients = defaultMaxClients
	}
	if s.readTimeout <= 0 {
		s.readTimeout = defaultReadTimeout
	}
	if s.writeTimeout <= 0 {
		s.writeTimeout = defaultWriteTimeout
	}
	if s.firstTimeout <= 0 {
		s.firstTimeout = defaultFirstPacketTimeout
	}
	if s.maxPending <= 0 {
		s.maxPending = defaultMaxPending
	}
	if s.maxPendingIP <= 0 {
		s.maxPendingIP = defaultMaxPendingPerIP
	}
	s.clientsMu.Lock()
	if s.clients == nil {
		s.clients = make(map[string]*tcpClient)
	}
	if s.pendingByIP == nil {
		s.pendingByIP = make(map[string]int)
	}
	s.clientsMu.Unlock()
}

func (s *TCPServer) acceptLoop() {
	defer s.wg.Done()
	for s.running.Load() {
		conn, err := s.listener.Accept()
		if err != nil {
			if !s.running.Load() {
				return
			}
			continue
		}
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true)
			_ = tcpConn.SetKeepAlive(true)
			_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		}

		clientID := conn.RemoteAddr().String()
		clientIP := remoteIP(conn.RemoteAddr())
		s.clientsMu.Lock()
		if !s.running.Load() {
			s.clientsMu.Unlock()
			_ = conn.Close()
			return
		}
		if s.pendingLimitReachedLocked(clientIP) {
			s.clientsMu.Unlock()
			_ = conn.Close()
			continue
		}
		client := &tcpClient{conn: conn}
		s.clients[clientID] = client
		s.addPendingLocked(clientIP)
		s.clientsMu.Unlock()

		s.wg.Add(1)
		go s.handleClient(clientID, clientIP, client)
	}
}

func (s *TCPServer) handleClient(clientID, clientIP string, client *tcpClient) {
	defer s.wg.Done()
	firstPacketSeen := false
	registered := false
	defer func() {
		s.clientsMu.Lock()
		s.releaseClientLocked(clientID, clientIP, client, registered, firstPacketSeen)
		s.clientsMu.Unlock()
		_ = client.conn.Close()
	}()

	buf := make([]byte, 4096)
	recvBuf := make([]byte, 0, initialReceiveBufferSize)

	for {
		timeout := s.readTimeout
		if !firstPacketSeen && s.firstTimeout > 0 {
			timeout = s.firstTimeout
		}
		if timeout > 0 {
			_ = client.conn.SetReadDeadline(time.Now().Add(timeout))
		}
		n, err := client.conn.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}

		recvBuf = append(recvBuf, buf[:n]...)
		for {
			data, remaining, ok := tryExtractXMLPacket(recvBuf)
			if !ok {
				break
			}
			if len(data) > maxReceiveBufferSize {
				return
			}
			if len(bytes.TrimSpace(remaining)) != 0 {
				// The Robot API is one request/one response per connection. Reject
				// pipelined frames and trailing bytes before dispatching either.
				return
			}
			recvBuf = remaining
			if !firstPacketSeen {
				if !s.registerFirstPacket(clientIP) {
					return
				}
				registered = true
				firstPacketSeen = true
			}
			if s.onMessage != nil {
				s.dispatchMessage(clientID, data)
			}
			return
		}
		if len(recvBuf) > maxReceiveBufferSize {
			return
		}
	}
}

func (s *TCPServer) dispatchMessage(clientID string, data []byte) {
	defer func() {
		if rec := recover(); rec != nil {
			foundationlog.Robotf("TCP_MESSAGE_HANDLER_PANIC client=%s err=%v\n", clientID, rec)
		}
	}()
	s.onMessage(clientID, data)
}

func tryExtractXMLPacket(buf []byte) (packet []byte, remaining []byte, ok bool) {
	if len(buf) < len("<tw></tw>") {
		return nil, buf, false
	}
	if !bytes.HasPrefix(buf, []byte("<tw>")) {
		return nil, buf, false
	}
	end := bytes.Index(buf, []byte("</tw>"))
	if end < 0 {
		return nil, buf, false
	}
	packetLen := end + len("</tw>")
	packet = buf[:packetLen]
	remaining = buf[packetLen:]
	return packet, remaining, true
}

func (s *TCPServer) registerFirstPacket(clientIP string) bool {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if s.maxClients > 0 && s.connCount >= s.maxClients {
		return false
	}
	s.removePendingLocked(clientIP)
	s.connCount++
	return true
}

func (s *TCPServer) pendingLimitReachedLocked(clientIP string) bool {
	return s.maxPending > 0 && s.pendingCount >= s.maxPending ||
		s.maxPendingIP > 0 && s.pendingByIP[clientIP] >= s.maxPendingIP
}

func (s *TCPServer) addPendingLocked(clientIP string) {
	s.pendingCount++
	s.pendingByIP[clientIP]++
}

func (s *TCPServer) removePendingLocked(clientIP string) {
	if s.pendingCount > 0 {
		s.pendingCount--
	}
	if s.pendingByIP[clientIP] <= 1 {
		delete(s.pendingByIP, clientIP)
		return
	}
	s.pendingByIP[clientIP]--
}

func (s *TCPServer) releaseClientLocked(clientID, clientIP string, client *tcpClient, registered, firstPacketSeen bool) {
	// A remote address can be reused after a fast disconnect. Only remove this
	// handler's own map entry so an older defer cannot evict a newer connection
	// that happens to receive the same client ID.
	if s.clients[clientID] == client {
		delete(s.clients, clientID)
	}
	if registered && s.connCount > 0 {
		s.connCount--
	}
	if !firstPacketSeen {
		s.removePendingLocked(clientIP)
	}
}

func remoteIP(addr net.Addr) string {
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func (s *TCPServer) SendTo(clientID string, data []byte) error {
	s.clientsMu.RLock()
	client, ok := s.clients[clientID]
	s.clientsMu.RUnlock()
	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if s.writeTimeout > 0 {
		if err := client.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return fmt.Errorf("set client %s write deadline: %w", clientID, err)
		}
	}
	if err := WriteFull(client.conn, data); err != nil {
		return fmt.Errorf("write client %s: %w", clientID, err)
	}
	return nil
}

func (s *TCPServer) Close() error {
	if s == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	if s.closeDone == nil {
		s.closeDone = make(chan struct{})
	}
	if s.closed {
		done := s.closeDone
		s.lifecycleMu.Unlock()
		<-done
		return nil
	}
	s.closed = true
	s.running.Store(false)
	listener := s.listener
	s.lifecycleMu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	s.clientsMu.Lock()
	for _, client := range s.clients {
		_ = client.conn.Close()
	}
	s.clients = make(map[string]*tcpClient)
	s.clientsMu.Unlock()
	waitTimeout := defaultCloseWaitTimeout
	if s.closeWaitTimeout > 0 {
		waitTimeout = s.closeWaitTimeout
	}
	waited := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(waitTimeout):
		// In-flight command handlers are bounded by the managed command
		// budget; shutdown must not wait for them indefinitely.
		foundationlog.Robotf("TCP_SERVER_CLOSE_TIMEOUT waited=%s\n", waitTimeout)
	}
	close(s.closeDone)
	return nil
}
