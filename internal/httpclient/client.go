package httpclient

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"DojinGo/internal/config"
)

const DefaultTimeout = 30 * time.Second

var userAgents = []string{
	"Mozilla/5.0 (X11; Linux x86_64; rv:123.0) Gecko/20100101 Firefox/123.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_4) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3.1 Safari/605.1.15",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3.1 Mobile/15E148 Safari/604.1",
}

type Client struct {
	httpClient     *http.Client
	defaultHeaders http.Header
}

type Options struct {
	ForceIPv4 bool
}

func New(cfg *config.Config, defaultHeaders http.Header) (*Client, error) {
	return NewWithOptions(cfg, defaultHeaders, Options{})
}

func NewWithOptions(cfg *config.Config, defaultHeaders http.Header, options Options) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	transport := defaultTransport()

	if options.ForceIPv4 {
		transport.DialContext = dialContextIPv4()
	} else if strings.TrimSpace(cfg.IPv6.Prefix) != "" {
		prefix, err := parseIPv6Prefix(cfg.IPv6.Prefix)
		if err != nil {
			return nil, err
		}
		transport.DialContext = dialContextWithPrefix(prefix)
	}

	return &Client{
		httpClient: &http.Client{
			Timeout:   DefaultTimeout,
			Transport: transport,
			Jar:       jar,
		},
		defaultHeaders: cloneHeader(defaultHeaders),
	}, nil
}

func defaultTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 10
	transport.Proxy = http.ProxyFromEnvironment
	return transport
}

func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

func (c *Client) NewRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for key, values := range c.defaultHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", randomUserAgent())
	}
	return req, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", randomUserAgent())
	}
	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("http %s %s error=%v", req.Method, req.URL.Redacted(), err)
		return nil, err
	}
	log.Printf("http %s %s -> %s (%s)", req.Method, req.URL.Redacted(), resp.Status, time.Since(start))
	return resp, nil
}

func (c *Client) GetString(ctx context.Context, rawURL string) (string, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("unexpected status %s for %s", resp.Status, rawURL)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *Client) GetBytes(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %s for %s", resp.Status, rawURL)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) SetCookies(rawURL string, cookies []*http.Cookie) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	c.httpClient.Jar.SetCookies(parsed, cookies)
	return nil
}

func cloneHeader(header http.Header) http.Header {
	if header == nil {
		return make(http.Header)
	}
	out := make(http.Header, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func randomUserAgent() string {
	return userAgents[mrand.Intn(len(userAgents))]
}

func parseIPv6Prefix(raw string) (*net.IPNet, error) {
	ip, network, err := net.ParseCIDR(raw)
	if err != nil {
		return nil, fmt.Errorf("parse ipv6.prefix %q: %w", raw, err)
	}
	if ip.To16() == nil || ip.To4() != nil {
		return nil, fmt.Errorf("ipv6.prefix %q is not an IPv6 CIDR", raw)
	}
	network.IP = ip
	return network, nil
}

func dialContextWithPrefix(prefix *net.IPNet) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{
			Timeout:   DefaultTimeout,
			KeepAlive: 30 * time.Second,
		}
		ip := randomIPFromPrefix(prefix)
		if ip != nil {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
		}
		return dialer.DialContext(ctx, network, address)
	}
}

func dialContextIPv4() func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer := &net.Dialer{
			Timeout:   DefaultTimeout,
			KeepAlive: 30 * time.Second,
		}
		return dialer.DialContext(ctx, "tcp4", address)
	}
}

func randomIPFromPrefix(prefix *net.IPNet) net.IP {
	ones, bits := prefix.Mask.Size()
	if bits != 128 || ones >= 128 {
		return prefix.IP
	}

	base := new(big.Int).SetBytes(prefix.IP.To16())
	hostBits := uint(128 - ones)
	limit := new(big.Int).Lsh(big.NewInt(1), hostBits)
	offset, err := crand.Int(crand.Reader, limit)
	if err != nil {
		return prefix.IP
	}

	base.Add(base, offset)
	result := base.Bytes()
	if len(result) < net.IPv6len {
		padded := make([]byte, net.IPv6len)
		copy(padded[net.IPv6len-len(result):], result)
		result = padded
	}
	return net.IP(result)
}
