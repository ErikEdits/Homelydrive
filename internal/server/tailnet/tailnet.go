// Package tailnet finds the Tailscale addresses of this machine (Tailscale is
// installed by the user, Homelydrive only uses it) and manages the HTTP
// listeners so the server is reachable only through the tailnet and localhost.
package tailnet

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	cgnat   = netip.MustParsePrefix("100.64.0.0/10")
	tsULA   = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	errNoTS = errors.New("tailscale CLI not found")
)

// IsTailscaleAddr reports whether ip belongs to the Tailscale address ranges.
func IsTailscaleAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return cgnat.Contains(ip) || tsULA.Contains(ip)
}

// LocalIPs returns this machine's Tailscale IP addresses found on its network
// interfaces (works without the Tailscale CLI).
func LocalIPs() []netip.Addr {
	var out []netip.Addr
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			pfx, err := netip.ParsePrefix(a.String())
			if err != nil {
				continue
			}
			if IsTailscaleAddr(pfx.Addr()) {
				out = append(out, pfx.Addr())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

// Status is what we show in the server window.
type Status struct {
	Installed bool     `json:"installed"`
	Running   bool     `json:"running"`
	DNSName   string   `json:"dns_name"` // e.g. "meinpc.tail1234.ts.net"
	HostName  string   `json:"host_name"`
	IPs       []string `json:"ips"`
	Error     string   `json:"error,omitempty"`
}

// CLIPath returns the path of the tailscale CLI, or "".
func CLIPath() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		for _, base := range []string{os.Getenv("ProgramFiles"), `C:\Program Files`} {
			p := filepath.Join(base, "Tailscale", "tailscale.exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// GetStatus queries `tailscale status --json`, falling back to the network
// interfaces if the CLI is not available.
func GetStatus(ctx context.Context) Status {
	var st Status
	for _, ip := range LocalIPs() {
		st.IPs = append(st.IPs, ip.String())
	}
	st.Running = len(st.IPs) > 0
	cli := CLIPath()
	if cli == "" {
		st.Installed = st.Running
		if !st.Running {
			st.Error = errNoTS.Error()
		}
		return st
	}
	st.Installed = true
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "status", "--json").Output()
	if err != nil && len(out) == 0 {
		st.Error = err.Error()
		return st
	}
	var js struct {
		BackendState string
		Self         struct {
			DNSName      string
			HostName     string
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal(out, &js); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Running = js.BackendState == "Running"
	st.DNSName = strings.TrimSuffix(js.Self.DNSName, ".")
	st.HostName = js.Self.HostName
	if len(js.Self.TailscaleIPs) > 0 {
		st.IPs = js.Self.TailscaleIPs
	}
	if !st.Running && js.BackendState != "" {
		st.Error = "Tailscale: " + js.BackendState
	}
	return st
}

// Manager keeps one HTTP server per listen address and re-checks the
// Tailscale addresses periodically (Tailscale may start after us or change).
type Manager struct {
	Port     int
	Handler  http.Handler
	Fixed    []string // explicit listen addresses (overrides detection)
	TLS      func() *tls.Config
	Log      *slog.Logger
	mu       sync.Mutex
	servers  map[string]*http.Server
	stopping bool
}

func (m *Manager) desired() map[string]bool {
	want := map[string]bool{}
	if len(m.Fixed) > 0 {
		for _, a := range m.Fixed {
			want[a] = true
		}
		return want
	}
	want[net.JoinHostPort("127.0.0.1", fmt.Sprint(m.Port))] = true
	for _, ip := range LocalIPs() {
		want[net.JoinHostPort(ip.String(), fmt.Sprint(m.Port))] = true
	}
	return want
}

// Refresh starts listeners for new addresses and stops stale ones.
func (m *Manager) Refresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return
	}
	if m.servers == nil {
		m.servers = map[string]*http.Server{}
	}
	want := m.desired()
	for addr, srv := range m.servers {
		if !want[addr] {
			m.Log.Info("stopping listener", "addr", addr)
			srv.Close()
			delete(m.servers, addr)
		}
	}
	for addr := range want {
		if _, ok := m.servers[addr]; ok {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			m.Log.Warn("cannot listen", "addr", addr, "err", err)
			continue
		}
		srv := &http.Server{
			Handler:           m.Handler,
			ReadHeaderTimeout: 30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		host, _, _ := net.SplitHostPort(addr)
		useTLS := false
		if m.TLS != nil && host != "127.0.0.1" && host != "::1" && host != "localhost" {
			if cfg := m.TLS(); cfg != nil {
				srv.TLSConfig = cfg
				ln = tls.NewListener(ln, cfg)
				useTLS = true
			}
		}
		m.servers[addr] = srv
		m.Log.Info("listening", "addr", addr, "https", useTLS)
		go func(addr string) {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				m.Log.Warn("listener stopped", "addr", addr, "err", err)
			}
		}(addr)
	}
}

// Addrs returns the active listen addresses.
func (m *Manager) Addrs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for a := range m.servers {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Restart closes all listeners and starts them again (after a port or HTTPS change).
func (m *Manager) Restart(port int, fixed []string) {
	m.mu.Lock()
	for addr, srv := range m.servers {
		srv.Close()
		delete(m.servers, addr)
	}
	m.Port = port
	m.Fixed = fixed
	m.mu.Unlock()
	m.Refresh()
}

// Run refreshes the listeners every 15 seconds until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.Refresh()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.stopping = true
			for _, srv := range m.servers {
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				srv.Shutdown(sctx)
				cancel()
			}
			m.mu.Unlock()
			return
		case <-t.C:
			m.Refresh()
		}
	}
}

// AllowRemote reports whether a request from remoteAddr may use the server:
// loopback and Tailscale addresses only (unless fixed listen addresses were
// configured, e.g. in Docker, where the admin decides).
func AllowRemote(remoteAddr string, fixed bool) bool {
	if fixed {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || IsTailscaleAddr(ip)
}

// IsLoopback reports whether the request comes from this machine.
func IsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// CertManager fetches and renews the HTTPS certificate via `tailscale cert`.
type CertManager struct {
	Dir  string
	Log  *slog.Logger
	mu   sync.Mutex
	cert *tls.Certificate
	at   time.Time
	err  error
}

// Config returns a TLS config if a certificate can be obtained.
func (c *CertManager) Config() *tls.Config {
	if _, err := c.get(); err != nil {
		return nil
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return c.get()
		},
	}
}

// LastError returns the last error obtaining a certificate.
func (c *CertManager) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *CertManager) get() (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert != nil && time.Since(c.at) < 24*time.Hour {
		return c.cert, nil
	}
	cert, err := c.fetch()
	if err != nil {
		c.err = err
		if c.cert != nil {
			return c.cert, nil // keep serving the old one
		}
		return nil, err
	}
	c.cert, c.at, c.err = cert, time.Now(), nil
	return cert, nil
}

func (c *CertManager) fetch() (*tls.Certificate, error) {
	cli := CLIPath()
	if cli == "" {
		return nil, errNoTS
	}
	st := GetStatus(context.Background())
	if st.DNSName == "" {
		return nil, errors.New("tailscale: no MagicDNS name (is MagicDNS enabled?)")
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return nil, err
	}
	certFile := filepath.Join(c.Dir, "tls.crt")
	keyFile := filepath.Join(c.Dir, "tls.key")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "cert", "--cert-file", certFile, "--key-file", keyFile, st.DNSName).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tailscale cert: %v: %s", err, strings.TrimSpace(string(out)))
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	c.Log.Info("HTTPS certificate loaded", "name", st.DNSName)
	return &cert, nil
}
