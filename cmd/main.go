package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"elasticity-manager/internal/api"
	"elasticity-manager/internal/autoscaler"
	"elasticity-manager/internal/config"
	"elasticity-manager/internal/haproxy"
	"elasticity-manager/internal/monitor"
	vmmanager "elasticity-manager/internal/vm"
)

type persistedState struct {
	Config        config.AutoScalerConfig `json:"config"`
	Backends      []haproxy.Backend       `json:"backends"`
	Instances     []vmmanager.Instance    `json:"instances"`
	Events        []autoscaler.Event      `json:"events"`
	InstanceN     int                     `json:"instance_n"`
	ScalerEnabled bool                    `json:"scaler_enabled"`
}

func main() {
	appDir := appRootDir()
	loadDotEnvFile(filepath.Join(appDir, ".env"))
	loadDotEnvFile(filepath.Join(appDir, ".env.example"))
	loadDotEnvFile(".env")
	loadDotEnvFile(".env.example")

	fmt.Println(`
╔══════════════════════════════════════════════╗
║     Elastic Load Balancer Manager  v1.0      ║
║     Universidad del Quindío – 2026-1         ║
║     Ejecutando en: ` + runtime.GOOS + `      ║
╚══════════════════════════════════════════════╝
`)

	statePath := filepath.Join(appDir, "data", "state.json")
	savedState, hasSavedState := loadPersistedState(statePath)

	defaultConfig := config.AutoScalerConfig{
		UpperThreshold:   80.0,
		LowerThreshold:   20.0,
		SampleInterval:   10,
		EvaluationWindow: 60,
		MaxInstances:     5,
		MinInstances:     1,
	}
	initialConfig := defaultConfig
	if hasSavedState {
		initialConfig = mergeConfig(defaultConfig, savedState.Config)
	}
	store := config.NewStore(initialConfig, nil)

	// ── SSH key path (Windows: %USERPROFILE%\.ssh\id_rsa) ───────────────────
	sshKeyPath := sshKey()
	sshUser := envOr("SSH_USER", "debian")

	if sshKeyPath == "" {
		log.Printf("[warn] SSH key not found in ~/.ssh (tried: id_ed25519, id_ecdsa, id_rsa). Set SSH_KEY or create one with: ssh-keygen -t ed25519 -f $env:USERPROFILE\\.ssh\\id_ed25519")
	} else {
		log.Printf("SSH key: %s", sshKeyPath)
	}
	log.Printf("SSH user: %s", sshUser)

	// ── HAProxy VM connection ────────────────────────────────────────────────
	// HAProxy runs on a designated Linux VM, reachable via NAT port-forwarding.
	// Default: localhost:2200 → haproxy-vm:22
	haproxyHost := envOr("HAPROXY_HOST", "127.0.0.1")
	haproxySSHPort := envOrInt("HAPROXY_SSH_PORT", 2200)

	initialBackends := []haproxy.Backend{}
	if hasSavedState {
		initialBackends = normalizeAppVMBackends(savedState.Backends)
	}
	var hapMgr *haproxy.Manager
	if sshKeyPath == "" {
		log.Printf("[warn] HAProxy SSH disabled: no private key available – running in no-SSH mode (UI/state only)")
		hapMgr = haproxy.NewManagerNoSSH(initialBackends, nil)
	} else {
		hap, err := haproxy.NewManager(haproxyHost, haproxySSHPort, sshUser, sshKeyPath, initialBackends, nil)
		if err != nil {
			log.Printf("[warn] HAProxy SSH unavailable (%v) – running in no-SSH mode (UI/state only)", err)
			hapMgr = haproxy.NewManagerNoSSH(initialBackends, nil)
		} else {
			hapMgr = hap
			log.Printf("HAProxy VM: %s:%d", haproxyHost, haproxySSHPort)
		}
	}

	// ── VM manager ───────────────────────────────────────────────────────────
	initialInstances := []vmmanager.Instance{}
	if hasSavedState {
		initialInstances = savedState.Instances
	}
	vmMgr := vmmanager.NewManager(sshKeyPath, initialInstances, nil)
	vmMgr.SetSSHUser(sshUser)
	baseVM := envOr("BASE_VM", "debian-base")
	baseSnap := envOr("BASE_SNAPSHOT", "base-snapshot")
	vmMgr.SetBaseVM(baseVM, baseSnap)
	log.Printf("Base VM: %s (snapshot: %s)", baseVM, baseSnap)

	// ── CPU monitor ──────────────────────────────────────────────────────────
	cpuMon := monitor.NewCPUMonitor(vmMgr, sshUser, sshKeyPath, func() (int, int) {
		cfg := store.GetConfig()
		return cfg.SampleInterval, cfg.EvaluationWindow
	})

	// ── Auto-scaler ──────────────────────────────────────────────────────────
	initialEvents := []autoscaler.Event{}
	initialInstanceN := 0
	if hasSavedState {
		initialEvents = savedState.Events
		initialInstanceN = savedState.InstanceN
		if derived := maxAppVMIndex(savedState.Instances); derived > initialInstanceN {
			initialInstanceN = derived
		}
	}
	scaler := autoscaler.New(store, hapMgr, vmMgr, cpuMon, initialEvents, initialInstanceN)
	if hasSavedState {
		scaler.SetEnabled(savedState.ScalerEnabled)
	}
	if len(initialEvents) > 0 || initialInstanceN > 0 {
		scaler.SetState(initialEvents, initialInstanceN)
	}

	var persistMu sync.Mutex
	persistState := func() {
		persistMu.Lock()
		defer persistMu.Unlock()
		events, instanceN := scaler.State()
		state := persistedState{
			Config:        store.GetConfig(),
			Backends:      hapMgr.ListBackends(),
			Instances:     vmMgr.ListInstances(),
			Events:        events,
			InstanceN:     instanceN,
			ScalerEnabled: scaler.IsEnabled(),
		}
		if err := savePersistedState(statePath, state); err != nil {
			log.Printf("[warn] could not persist app state: %v", err)
		}
	}
	store.SetOnChange(persistState)
	hapMgr.SetOnChange(persistState)
	vmMgr.SetOnChange(persistState)
	persistState()

	// ── HTTP server ──────────────────────────────────────────────────────────
	router := api.NewRouter(store, hapMgr, vmMgr, cpuMon, scaler)
	srv := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── Background services ──────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vmMgr.StartBackgroundSync(ctx, 3*time.Second)
	go cpuMon.Start(ctx)
	go scaler.Start(ctx)

	// ── Graceful shutdown ────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("✓ Panel web disponible en → http://localhost:8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-quit
	log.Println("Cerrando servidor…")
	cancel()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx) //nolint
	log.Println("Servidor detenido.")
}

// sshKey returns the path to the SSH private key.
// Priority: SSH_KEY env var → common keys in ~/.ssh (id_ed25519, id_ecdsa, id_rsa).
func sshKey() string {
	if v := os.Getenv("SSH_KEY"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
		log.Printf("[warn] SSH_KEY is set but file does not exist: %s", v)
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates := []string{"id_ed25519_haproxy", "id_ed25519", "id_ecdsa", "id_rsa"}
		for _, keyName := range candidates {
			p := filepath.Join(home, ".ssh", keyName)
			if _, statErr := os.Stat(p); statErr == nil {
				return p
			}
		}
	}
	return ""
}

func appRootDir() string {
	if cwd, err := os.Getwd(); err == nil {
		if root, ok := findProjectRoot(cwd); ok {
			return root
		}
	}
	if exe, err := os.Executable(); err == nil {
		if root, ok := findProjectRoot(filepath.Dir(exe)); ok {
			return root
		}
	}
	return "."
}

func findProjectRoot(start string) (string, bool) {
	current := start
	for {
		if hasProjectMarker(current) {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

func hasProjectMarker(dir string) bool {
	for _, name := range []string{"go.mod", ".env", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		fmt.Sscan(v, &n)
		if n > 0 {
			return n
		}
	}
	return def
}

func mergeConfig(base, override config.AutoScalerConfig) config.AutoScalerConfig {
	if override.UpperThreshold != 0 {
		base.UpperThreshold = override.UpperThreshold
	}
	if override.LowerThreshold != 0 {
		base.LowerThreshold = override.LowerThreshold
	}
	if override.SampleInterval != 0 {
		base.SampleInterval = override.SampleInterval
	}
	if override.EvaluationWindow != 0 {
		base.EvaluationWindow = override.EvaluationWindow
	}
	if override.MaxInstances != 0 {
		base.MaxInstances = override.MaxInstances
	}
	if override.MinInstances != 0 {
		base.MinInstances = override.MinInstances
	}
	return base
}

func loadPersistedState(path string) (persistedState, bool) {
	var state persistedState
	f, err := os.Open(path)
	if err != nil {
		return state, false
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&state); err != nil {
		log.Printf("[warn] could not load persisted state: %v", err)
		return persistedState{}, false
	}
	if state.Instances == nil {
		state.Instances = []vmmanager.Instance{}
	}
	if state.Backends == nil {
		state.Backends = []haproxy.Backend{}
	}
	if state.Events == nil {
		state.Events = []autoscaler.Event{}
	}
	return state, true
}

func maxAppVMIndex(instances []vmmanager.Instance) int {
	max := 0
	for _, inst := range instances {
		name := strings.TrimSpace(inst.Name)
		if !strings.HasPrefix(name, "app-vm-") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(name, "app-vm-"), "%d", &n); err == nil && n > max {
			max = n
		}
	}
	return max
}

func normalizeAppVMBackends(backends []haproxy.Backend) []haproxy.Backend {
	out := make([]haproxy.Backend, len(backends))
	copy(out, backends)
	for bi := range out {
		for si := range out[bi].Servers {
			server := &out[bi].Servers[si]
			if !strings.HasPrefix(server.Name, "app-vm-") {
				continue
			}
			var n int
			if _, err := fmt.Sscanf(strings.TrimPrefix(server.Name, "app-vm-"), "%d", &n); err != nil || n <= 0 {
				continue
			}
			server.IP = "10.0.2.2"
			server.Port = 8000 + n
			server.Active = true
		}
	}
	return out
}

func savePersistedState(path string, state persistedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(state); err != nil {
		f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// loadDotEnvFile loads KEY=VALUE pairs from a .env file without overriding
// variables that are already set in the process environment.
func loadDotEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		value := strings.TrimSpace(line[eq+1:])
		value = strings.Trim(value, `"'`)
		_ = os.Setenv(key, value)
	}

	if scanErr := s.Err(); scanErr != nil {
		log.Printf("[warn] could not parse .env: %v", scanErr)
	}
}
