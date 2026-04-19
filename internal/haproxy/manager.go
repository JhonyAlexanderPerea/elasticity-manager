// Package haproxy manages HAProxy running on a remote Linux VM via SSH.
// On Windows the host cannot run HAProxy natively, so all config writes
// and reloads are forwarded to the designated haproxy-vm through SSH.
package haproxy

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"elasticity-manager/internal/sshutil"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// Backend is a named HAProxy backend (load balancer group).
type Backend struct {
	Name      string    `json:"name"`
	Algorithm string    `json:"algorithm"` // roundrobin | leastconn | first
	Servers   []Server  `json:"servers"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Server is a single upstream inside a backend.
type Server struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Weight int    `json:"weight"`
	Active bool   `json:"active"`
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

// Manager performs CRUD on HAProxy configuration and reloads the daemon
// on the remote VM via SSH.
type Manager struct {
	mu         sync.RWMutex
	ssh        *sshutil.Client // connection to the VM running HAProxy
	remotePath string          // remote path to haproxy.cfg (e.g. /etc/haproxy/haproxy.cfg)
	backends   map[string]*Backend
	onChange   func()
}

// NewManager creates a Manager connected to a remote HAProxy VM.
//
//	host     – IP of the HAProxy VM as seen from Windows (usually 127.0.0.1 with NAT)
//	sshPort  – host-forwarded SSH port (e.g. 2200)
//	user     – SSH user on the VM (e.g. "debian")
//	keyPath  – Windows path to private key (e.g. C:\Users\you\.ssh\id_rsa)
func NewManager(host string, sshPort int, user, keyPath string, initial []Backend, onChange func()) (*Manager, error) {
	client, err := sshutil.New(host, sshPort, user, keyPath)
	if err != nil {
		return nil, fmt.Errorf("haproxy manager: %w", err)
	}
	if _, err := client.Run("echo ok"); err != nil {
		return nil, fmt.Errorf("haproxy manager ssh probe failed (%s:%d): %w", host, sshPort, err)
	}
	backendMap := make(map[string]*Backend, len(initial))
	for _, b := range initial {
		copyBackend := b
		if copyBackend.Servers == nil {
			copyBackend.Servers = []Server{}
		}
		backendMap[copyBackend.Name] = &copyBackend
	}
	return &Manager{
		ssh:        client,
		remotePath: "/etc/haproxy/haproxy.cfg",
		backends:   backendMap,
		onChange:   onChange,
	}, nil
}

// NewManagerNoSSH creates a Manager that only tracks state in memory
// (useful for development / running without a real HAProxy VM).
func NewManagerNoSSH(initial []Backend, onChange func()) *Manager {
	backendMap := make(map[string]*Backend, len(initial))
	for _, b := range initial {
		copyBackend := b
		if copyBackend.Servers == nil {
			copyBackend.Servers = []Server{}
		}
		backendMap[copyBackend.Name] = &copyBackend
	}
	return &Manager{backends: backendMap, onChange: onChange}
}

// SetOnChange updates the callback invoked after backend mutations.
func (m *Manager) SetOnChange(onChange func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = onChange
}

// ---------------------------------------------------------------------------
// Backend CRUD
// ---------------------------------------------------------------------------

func (m *Manager) ListBackends() []Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Backend, 0, len(m.backends))
	for _, b := range m.backends {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == "app-backend" && out[j].Name != "app-backend" {
			return true
		}
		if out[j].Name == "app-backend" && out[i].Name != "app-backend" {
			return false
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) GetBackend(name string) (Backend, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.backends[name]
	if !ok {
		return Backend{}, false
	}
	return *b, true
}

func (m *Manager) CreateBackend(name, algorithm string) error {
	m.mu.Lock()
	if _, exists := m.backends[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("backend %q already exists", name)
	}
	if algorithm == "" {
		algorithm = "roundrobin"
	}
	m.backends[name] = &Backend{
		Name:      name,
		Algorithm: algorithm,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return m.applyConfig()
}

func (m *Manager) UpdateBackend(name, algorithm string) error {
	m.mu.Lock()
	b, ok := m.backends[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("backend %q not found", name)
	}
	b.Algorithm = algorithm
	b.UpdatedAt = time.Now()
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return m.applyConfig()
}

func (m *Manager) DeleteBackend(name string) error {
	m.mu.Lock()
	if _, ok := m.backends[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("backend %q not found", name)
	}
	delete(m.backends, name)
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return m.applyConfig()
}

// ---------------------------------------------------------------------------
// Server CRUD
// ---------------------------------------------------------------------------

func (m *Manager) AddServer(backendName string, srv Server) error {
	m.mu.Lock()
	b, ok := m.backends[backendName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("backend %q not found", backendName)
	}
	for _, s := range b.Servers {
		if s.Name == srv.Name {
			m.mu.Unlock()
			return fmt.Errorf("server %q already in backend %q", srv.Name, backendName)
		}
	}
	if srv.Weight == 0 {
		srv.Weight = 1
	}
	srv.Active = true
	b.Servers = append(b.Servers, srv)
	b.UpdatedAt = time.Now()
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return m.applyConfig()
}

func (m *Manager) UpdateServer(backendName, serverName, ip string, port, weight int) error {
	m.mu.Lock()
	b, ok := m.backends[backendName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("backend %q not found", backendName)
	}
	for i, s := range b.Servers {
		if s.Name == serverName {
			b.Servers[i].IP = ip
			b.Servers[i].Port = port
			b.Servers[i].Weight = weight
			b.UpdatedAt = time.Now()
			changeFn := m.onChange
			m.mu.Unlock()
			if changeFn != nil {
				changeFn()
			}
			return m.applyConfig()
		}
	}
	m.mu.Unlock()
	return fmt.Errorf("server %q not found", serverName)
}

func (m *Manager) RemoveServer(backendName, serverName string) error {
	m.mu.Lock()
	b, ok := m.backends[backendName]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("backend %q not found", backendName)
	}
	for i, s := range b.Servers {
		if s.Name == serverName {
			b.Servers = append(b.Servers[:i], b.Servers[i+1:]...)
			b.UpdatedAt = time.Now()
			changeFn := m.onChange
			m.mu.Unlock()
			if changeFn != nil {
				changeFn()
			}
			return m.applyConfig()
		}
	}
	m.mu.Unlock()
	return fmt.Errorf("server %q not found", serverName)
}

// ---------------------------------------------------------------------------
// Config rendering & remote apply
// ---------------------------------------------------------------------------

const cfgTmpl = `# Generated by Elasticity Manager – {{ .Now }}
global
    log /dev/log local0
    daemon
    maxconn 4096

defaults
    log     global
    mode    http
    option  httplog
    option  dontlognull
    timeout connect 5s
    timeout client  60s
    timeout server  60s

frontend http_in
    bind *:80
    default_backend {{ .DefaultBackend }}
{{ range .Backends }}
backend {{ .Name }}
    balance {{ .Algorithm }}
    option httpchk GET /health
    http-check expect string ok
    {{ range .Servers }}server {{ .Name }} {{ .IP }}:{{ .Port }} weight {{ .Weight }} check inter 3s rise 2 fall 3
    {{ end }}
{{ end }}`

type tmplData struct {
	Now            string
	DefaultBackend string
	Backends       []Backend
}

func (m *Manager) render() (string, error) {
	m.mu.RLock()
	backends := make([]Backend, 0, len(m.backends))
	for _, b := range m.backends {
		backends = append(backends, *b)
	}
	m.mu.RUnlock()
	sort.Slice(backends, func(i, j int) bool {
		return backends[i].Name < backends[j].Name
	})
	def := "default_backend"
	for _, b := range backends {
		if b.Name == "app-backend" {
			def = b.Name
			break
		}
	}
	if def == "default_backend" && len(backends) > 0 {
		def = backends[0].Name
	}
	d := tmplData{
		Now:            time.Now().Format(time.RFC3339),
		DefaultBackend: def,
		Backends:       backends,
	}
	tmpl, err := template.New("cfg").Parse(cfgTmpl)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, d); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// applyConfig renders the config and pushes it to the remote VM via SSH.
// Must be called with m.mu held.
func (m *Manager) applyConfig() error {
	cfg, err := m.render()
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	// No SSH client → dev/no-VM mode, just log
	if m.ssh == nil {
		fmt.Println("[haproxy] (no-SSH mode) config rendered but not pushed")
		return nil
	}
	if err := m.ssh.WriteFile(m.remotePath, cfg); err != nil {
		return fmt.Errorf("write remote config: %w", err)
	}
	if err := m.ssh.ReloadService("haproxy"); err != nil {
		// Non-fatal: HAProxy might need restart instead of reload on first run
		fmt.Printf("[haproxy] reload warning: %v\n", err)
	}
	return nil
}

// GetConfig returns the current rendered configuration string.
func (m *Manager) GetConfig() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.render()
}

// Sync re-applies the current in-memory HAProxy config to the remote host.
// Useful when SSH was temporarily unavailable and previous apply attempts failed.
func (m *Manager) Sync() error {
	return m.applyConfig()
}

// RunCommand executes an arbitrary command on the HAProxy VM via SSH.
func (m *Manager) RunCommand(command string) (string, error) {
	if m.ssh == nil {
		return "", fmt.Errorf("haproxy ssh no disponible")
	}
	return m.ssh.Run(command)
}
