package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"DojinGo/internal/config"
)

type Server struct {
	cfg         config.ProxyConfig
	logger      *log.Logger
	rateLimiter *RateLimiter
}

func New(cfg config.ProxyConfig, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:         cfg,
		logger:      logger,
		rateLimiter: NewRateLimiter(cfg.RateLimitPerMinute, time.Minute),
	}
}

func (s *Server) Start(ctx context.Context) error {
	var listeners []net.Listener

	if s.cfg.Listen.HTTP != "" {
		ln, err := net.Listen("tcp", s.cfg.Listen.HTTP)
		if err != nil {
			return fmt.Errorf("listen http proxy on %s: %w", s.cfg.Listen.HTTP, err)
		}
		listeners = append(listeners, ln)
		go s.serveHTTPProxy(ctx, ln)
		s.logger.Printf("http proxy listening on %s", s.cfg.Listen.HTTP)
	}

	if s.cfg.Listen.SOCKS5 != "" {
		ln, err := net.Listen("tcp", s.cfg.Listen.SOCKS5)
		if err != nil {
			for _, listener := range listeners {
				_ = listener.Close()
			}
			return fmt.Errorf("listen socks5 proxy on %s: %w", s.cfg.Listen.SOCKS5, err)
		}
		listeners = append(listeners, ln)
		go s.serveSOCKS5(ctx, ln)
		s.logger.Printf("socks5 proxy listening on %s", s.cfg.Listen.SOCKS5)
	}

	go func() {
		<-ctx.Done()
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	return nil
}

func (s *Server) serveHTTPProxy(ctx context.Context, ln net.Listener) {
	server := &http.Server{
		Handler: http.HandlerFunc(s.handleHTTPProxy),
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		s.logger.Printf("http proxy stopped with error: %v", err)
	}
}

func (s *Server) handleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	clientIP := clientIP(r.RemoteAddr)
	if !s.allow(clientIP) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if !s.checkHTTPAuth(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="eh2telegraph"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}

	if r.Method == http.MethodConnect {
		s.handleHTTPConnect(w, r, clientIP)
		return
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	resp, err := transport.RoundTrip(cloneProxyRequest(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.Printf("http proxy copy response failed: %v", err)
	}
	s.logger.Printf("http proxy %s %s from %s -> %s", r.Method, r.URL.String(), clientIP, resp.Status)
}

func (s *Server) handleHTTPConnect(w http.ResponseWriter, r *http.Request, clientIP string) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	dstConn, err := net.DialTimeout("tcp", r.Host, 30*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		_ = dstConn.Close()
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	s.logger.Printf("http connect from %s to %s", clientIP, r.Host)
	relay(clientConn, dstConn)
}

func (s *Server) serveSOCKS5(ctx context.Context, ln net.Listener) {
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Printf("socks5 accept failed: %v", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			if err := s.handleSOCKS5Conn(conn); err != nil {
				s.logger.Printf("socks5 connection failed from %s: %v", conn.RemoteAddr(), err)
			}
		}()
	}
}

func (s *Server) handleSOCKS5Conn(conn net.Conn) error {
	clientIP := clientIP(conn.RemoteAddr().String())
	if !s.allow(clientIP) {
		return fmt.Errorf("rate limit exceeded")
	}

	reader := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 5 {
		return fmt.Errorf("unsupported socks version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}

	selectedMethod := byte(0x00)
	if s.cfg.Auth.Enabled {
		selectedMethod = 0x02
	}
	if !containsMethod(methods, selectedMethod) {
		_, _ = conn.Write([]byte{0x05, 0xFF})
		return fmt.Errorf("client does not support required socks5 auth method")
	}
	if _, err := conn.Write([]byte{0x05, selectedMethod}); err != nil {
		return err
	}
	if s.cfg.Auth.Enabled {
		if err := s.handleSOCKS5Auth(reader, conn); err != nil {
			return err
		}
	}

	request := make([]byte, 4)
	if _, err := io.ReadFull(reader, request); err != nil {
		return err
	}
	if request[0] != 5 || request[1] != 1 {
		return fmt.Errorf("unsupported socks command %d", request[1])
	}

	target, err := readSOCKS5Address(reader, request[3])
	if err != nil {
		return err
	}
	dstConn, err := net.DialTimeout("tcp", target, 30*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return err
	}

	boundHost, boundPort := splitHostPort(dstConn.LocalAddr().String())
	boundIP := net.ParseIP(boundHost).To4()
	if boundIP == nil {
		boundIP = net.IPv4zero
	}
	reply := []byte{0x05, 0x00, 0x00, 0x01, boundIP[0], boundIP[1], boundIP[2], boundIP[3], 0, 0}
	binary.BigEndian.PutUint16(reply[8:], uint16(boundPort))
	if _, err := conn.Write(reply); err != nil {
		_ = dstConn.Close()
		return err
	}

	s.logger.Printf("socks5 connect from %s to %s", clientIP, target)
	relay(conn, dstConn)
	return nil
}

func (s *Server) handleSOCKS5Auth(reader *bufio.Reader, conn net.Conn) error {
	version, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("unsupported auth version %d", version)
	}
	usernameLen, err := reader.ReadByte()
	if err != nil {
		return err
	}
	username := make([]byte, int(usernameLen))
	if _, err := io.ReadFull(reader, username); err != nil {
		return err
	}
	passwordLen, err := reader.ReadByte()
	if err != nil {
		return err
	}
	password := make([]byte, int(passwordLen))
	if _, err := io.ReadFull(reader, password); err != nil {
		return err
	}

	status := byte(0x00)
	if string(username) != s.cfg.Auth.Username || string(password) != s.cfg.Auth.Password {
		status = 0x01
	}
	if _, err := conn.Write([]byte{0x01, status}); err != nil {
		return err
	}
	if status != 0x00 {
		return fmt.Errorf("invalid socks5 credentials")
	}
	return nil
}

func (s *Server) checkHTTPAuth(r *http.Request) bool {
	if !s.cfg.Auth.Enabled {
		return true
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		username, password, ok = proxyBasicAuth(r.Header.Get("Proxy-Authorization"))
	}
	return ok && username == s.cfg.Auth.Username && password == s.cfg.Auth.Password
}

func (s *Server) allow(clientIP string) bool {
	return s.rateLimiter.Allow(clientIP)
}

func cloneProxyRequest(r *http.Request) *http.Request {
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = r.URL.Scheme
	out.URL.Host = r.URL.Host
	out.Host = r.Host
	return out
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func relay(left, right net.Conn) {
	var wg sync.WaitGroup
	copyConn := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		_ = dst.SetDeadline(time.Now())
		_ = src.SetDeadline(time.Now())
	}

	wg.Add(2)
	go copyConn(left, right)
	go copyConn(right, left)
	wg.Wait()
	_ = left.Close()
	_ = right.Close()
}

func readSOCKS5Address(reader *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(reader)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(net.IP(addr).String(), strconv.Itoa(port)), nil
	case 0x03:
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		addr := make([]byte, int(length))
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(reader)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(string(addr), strconv.Itoa(port)), nil
	case 0x04:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(reader, addr); err != nil {
			return "", err
		}
		port, err := readSOCKS5Port(reader)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(net.IP(addr).String(), strconv.Itoa(port)), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}

func readSOCKS5Port(reader *bufio.Reader) (int, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint16(buf)), nil
}

func splitHostPort(value string) (string, int) {
	host, portRaw, err := net.SplitHostPort(value)
	if err != nil {
		return "", 0
	}
	port, _ := strconv.Atoi(strings.TrimSpace(portRaw))
	return host, port
}

func containsMethod(methods []byte, method byte) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func proxyBasicAuth(header string) (string, string, bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return "", "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	return username, password, ok
}
