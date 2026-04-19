// Package autoscaler watches CPU averages and scales VMs in/out accordingly.
package autoscaler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"elasticity-manager/internal/config"
	"elasticity-manager/internal/haproxy"
	"elasticity-manager/internal/monitor"
	"elasticity-manager/internal/sshutil"
	"elasticity-manager/internal/vm"
)

// EventKind classifies a scaler event for the UI.
type EventKind string

const (
	EventScaleOut EventKind = "scale_out"
	EventScaleIn  EventKind = "scale_in"
	EventInfo     EventKind = "info"
	EventWarning  EventKind = "warning"
)

// Event is a single log entry from the auto-scaler.
type Event struct {
	Kind      EventKind `json:"kind"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// AutoScaler evaluates CPU thresholds every 10 s and scales accordingly.
type AutoScaler struct {
	mu           sync.Mutex
	store        *config.Store
	hap          *haproxy.Manager
	vmMgr        *vm.Manager
	mon          *monitor.CPUMonitor
	events       []Event
	instanceN    int
	backendName  string
	enabled      bool
	hapDegraded  bool
	lastScaleOut time.Time
	highSince    time.Time
	lowSince     time.Time
	sshUser      string
	sshKeyPath   string
}

const scaleInCooldownAfterScaleOut = 20 * time.Second
const scaleOutCooldown = 20 * time.Second

// New creates an AutoScaler.
func New(
	store *config.Store,
	hap *haproxy.Manager,
	vmMgr *vm.Manager,
	mon *monitor.CPUMonitor,
	initialEvents []Event,
	initialInstanceN int,
) *AutoScaler {
	events := make([]Event, len(initialEvents))
	copy(events, initialEvents)
	return &AutoScaler{
		store:       store,
		hap:         hap,
		vmMgr:       vmMgr,
		mon:         mon,
		events:      events,
		instanceN:   initialInstanceN,
		backendName: "app-backend",
		enabled:     true,
	}
}

// SetSSHConfig sets the SSH parameters for bootstrapping demo servers on new VMs.
func (a *AutoScaler) SetSSHConfig(sshUser, sshKeyPath string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sshUser = sshUser
	a.sshKeyPath = sshKeyPath
}

// Start runs the evaluation loop until ctx is cancelled.
func (a *AutoScaler) Start(ctx context.Context) {
	log.Println("[autoscaler] started")
	for {
		select {
		case <-ctx.Done():
			log.Println("[autoscaler] stopped")
			return
		case <-time.After(10 * time.Second):
			a.evaluate()
		}
	}
}

func (a *AutoScaler) SetEnabled(v bool) { a.mu.Lock(); a.enabled = v; a.mu.Unlock() }
func (a *AutoScaler) IsEnabled() bool   { a.mu.Lock(); defer a.mu.Unlock(); return a.enabled }

// EnsureMinInstances proactively brings capacity to at least minRequired running app VMs.
// This is used by API actions that must guarantee balancing readiness without waiting
// for the next periodic evaluation window.
func (a *AutoScaler) EnsureMinInstances(minRequired int) {
	if minRequired < 2 {
		minRequired = 2
	}
	cfg := a.store.GetConfig()
	if cfg.MaxInstances > 0 && minRequired > cfg.MaxInstances {
		minRequired = cfg.MaxInstances
	}
	if minRequired < 1 {
		minRequired = 1
	}

	maxAttempts := minRequired * 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		running := runningOnly(a.vmMgr.ListInstances())
		if len(running) >= minRequired {
			a.reconcileBackendServers(running)
			return
		}
		a.logEvent(EventInfo, fmt.Sprintf("Forzando capacidad mínima para balanceo: %d/%d instancias activas", len(running), minRequired))
		a.scaleOut(cfg)
	}

	running := runningOnly(a.vmMgr.ListInstances())
	a.reconcileBackendServers(running)
	if len(running) < minRequired {
		a.logEvent(EventWarning, fmt.Sprintf("No se alcanzó capacidad mínima de balanceo (%d/%d activas)", len(running), minRequired))
	}
}

// Events returns recent events newest-first (max 200).
func (a *AutoScaler) Events() []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Event, len(a.events))
	copy(out, a.events)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// AddEvent lets other packages inject informational events.
func (a *AutoScaler) AddEvent(kind EventKind, msg string) { a.logEvent(kind, msg) }

// SetState initializes mutable runtime state after loading persisted data.
func (a *AutoScaler) SetState(events []Event, instanceN int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append([]Event(nil), events...)
	a.instanceN = instanceN
}

// State returns the current events and instance counter for persistence.
func (a *AutoScaler) State() ([]Event, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := append([]Event(nil), a.events...)
	return events, a.instanceN
}

// ---------------------------------------------------------------------------
// Core evaluation
// ---------------------------------------------------------------------------

func (a *AutoScaler) evaluate() {
	a.mu.Lock()
	if !a.enabled {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	a.syncHAProxyIfNeeded()

	cfg := a.store.GetConfig()
	avg := a.mon.AverageCPU(cfg.EvaluationWindow)
	instances := a.vmMgr.ListInstances()
	runningInstances := runningOnly(instances)
	a.reconcileBackendServers(runningInstances)
	n := len(runningInstances)
	latest := a.mon.Latest()
	peak := maxCPUForRunning(runningInstances, latest)
	peakLimit := cfg.PeakThreshold
	if peakLimit <= 0 {
		peakLimit = 90
	}

	log.Printf("[autoscaler] avg=%.1f%% peak=%.1f%% n=%d upper=%.0f lower=%.0f peak_limit=%.0f",
		avg, peak, n, cfg.UpperThreshold, cfg.LowerThreshold, peakLimit)

	holdFor := time.Duration(cfg.EvaluationWindow) * time.Second
	if holdFor < 20*time.Second {
		holdFor = 20 * time.Second
	}
	now := time.Now()
	highCondition := (avg > cfg.UpperThreshold || peak > peakLimit) && n < cfg.MaxInstances
	lowCondition := avg < cfg.LowerThreshold && n > cfg.MinInstances

	if highCondition {
		if a.highSince.IsZero() {
			a.highSince = now
		}
	} else {
		a.highSince = time.Time{}
	}

	if lowCondition {
		if a.lowSince.IsZero() {
			a.lowSince = now
		}
	} else {
		a.lowSince = time.Time{}
	}

	switch {
	case n < cfg.MinInstances:
		a.logEvent(EventInfo, fmt.Sprintf("Instancias activas (%d) por debajo del mínimo configurado (%d); iniciando scale-out de cumplimiento", n, cfg.MinInstances))
		a.scaleOut(cfg)
	case n > cfg.MaxInstances:
		a.logEvent(EventWarning, fmt.Sprintf("VMs activas (%d) exceden máximo configurado (%d); ajustando hacia abajo", n, cfg.MaxInstances))
		a.scaleInToTarget(runningInstances, cfg.MaxInstances)
	case highCondition:
		if now.Sub(a.highSince) < holdFor {
			break
		}
		if !a.canScaleOutNow() {
			break
		}
		a.highSince = time.Time{}
		a.scaleOut(cfg)
	case lowCondition:
		if now.Sub(a.lowSince) < holdFor {
			break
		}
		if !a.canScaleInNow() {
			break
		}
		a.lowSince = time.Time{}
		a.scaleInToTarget(runningInstances, cfg.MinInstances)
	}
}

func (a *AutoScaler) reconcileBackendServers(running []vm.Instance) {
	if a.hap == nil {
		return
	}

	b, ok := a.hap.GetBackend(a.backendName)
	if !ok {
		if err := a.hap.CreateBackend(a.backendName, "roundrobin"); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				a.logEvent(EventWarning, fmt.Sprintf("No se pudo crear backend %q para reconciliación: %v", a.backendName, err))
				return
			}
		}
		b, _ = a.hap.GetBackend(a.backendName)
	}

	runningByName := make(map[string]vm.Instance, len(running))
	for _, inst := range running {
		runningByName[inst.Name] = inst
	}

	backendServers := make(map[string]haproxy.Server, len(b.Servers))
	for _, srv := range b.Servers {
		backendServers[srv.Name] = srv
	}

	for _, inst := range running {
		// Always derive the NAT host-forwarded port from the VM index.
		// inst.Port should already be 8000+idx (set by CloneAndStart / syncWithVirtualBox),
		// but after a state reload it can be 0 or stale. Re-computing from the index
		// guarantees we always give HAProxy the correct host port to reach this VM.
		idx, hasIdx := appVMIndex(inst.Name)
		if !hasIdx {
			// Non-standard VM name without a numeric index: we cannot determine the
			// correct NAT port, so skip it rather than registering a wrong address.
			a.logEvent(EventWarning, fmt.Sprintf("No se puede determinar el puerto NAT para VM '%s' (nombre sin índice numérico); omitiendo del backend", inst.Name))
			continue
		}
		port := 8000 + idx // host-forwarded port: 127.0.0.1:800X -> VM:8000

		desired := haproxy.Server{Name: inst.Name, IP: "10.0.2.2", Port: port, Weight: 1, Active: true}
		existing, ok := backendServers[inst.Name]
		if !ok {
			if err := a.registerServerInBackend(desired); err != nil {
				a.logEvent(EventWarning, fmt.Sprintf("No se pudo registrar %s en backend %s: %v", inst.Name, a.backendName, err))
			}
			continue
		}

		if existing.IP != desired.IP || existing.Port != desired.Port || existing.Weight != desired.Weight || !existing.Active {
			if err := a.hap.UpdateServer(a.backendName, desired.Name, desired.IP, desired.Port, desired.Weight); err != nil {
				a.logEvent(EventWarning, fmt.Sprintf("No se pudo sincronizar servidor %s en backend %s: %v", inst.Name, a.backendName, err))
			}
		}
	}

	for _, srv := range b.Servers {
		if !strings.HasPrefix(srv.Name, "app-vm-") {
			continue
		}
		if _, ok := runningByName[srv.Name]; ok {
			continue
		}
		if err := a.hap.RemoveServer(a.backendName, srv.Name); err != nil {
			a.logEvent(EventWarning, fmt.Sprintf("No se pudo remover servidor detenido %s de backend %s: %v", srv.Name, a.backendName, err))
		}
	}
}

func (a *AutoScaler) canScaleOutNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastScaleOut.IsZero() {
		return true
	}
	return time.Since(a.lastScaleOut) >= scaleOutCooldown
}

func (a *AutoScaler) canScaleInNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastScaleOut.IsZero() {
		return true
	}
	return time.Since(a.lastScaleOut) >= scaleInCooldownAfterScaleOut
}

func maxCPUForRunning(running []vm.Instance, latest map[string]float64) float64 {
	peak := 0.0
	for _, inst := range running {
		if v, ok := latest[inst.Name]; ok && v > peak {
			peak = v
		}
	}
	return peak
}

func (a *AutoScaler) markScaleOutSuccess() {
	a.mu.Lock()
	a.lastScaleOut = time.Now()
	a.mu.Unlock()
}

// bootstrapDemoServer launches the HTTP demo server on a new VM via SSH.
// This ensures that newly scaled VMs are ready to receive traffic immediately.
func (a *AutoScaler) bootstrapDemoServer(vmName string, sshPort int) {
	a.mu.Lock()
	sshUser := a.sshUser
	sshKeyPath := a.sshKeyPath
	a.mu.Unlock()

	if sshUser == "" || sshKeyPath == "" {
		a.logEvent(EventWarning, fmt.Sprintf("Saltando bootstrap de servidor demo en %s: SSH no configurado", vmName))
		return
	}

	// Create SSH client
	client, err := sshutil.New("127.0.0.1", sshPort, sshUser, sshKeyPath)
	if err != nil {
		a.logEvent(EventWarning, fmt.Sprintf("No se pudo conectar a %s via SSH para bootstrap: %v", vmName, err))
		return
	}

	// Bootstrap script: Python HTTP server with /health and /heavy endpoints.
	// Avoid pkill -f here because the SSH command itself can match the pattern
	// and terminate the remote shell with signal 15.
	bootstrapCmd := fmt.Sprintf(`cat > /tmp/em_demo_server.py <<'PYEOF'
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
import hashlib
import time

VMTAG = '%s'

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path
        if path == '/health':
            body = b'ok\n'
        elif path == '/heavy':
            params = parse_qs(parsed.query)
            budget_ms = 1200
            if 'ms' in params and params['ms']:
                try:
                    budget_ms = max(100, min(6000, int(params['ms'][0])))
                except ValueError:
                    budget_ms = 1200
            deadline = time.perf_counter() + (budget_ms / 1000.0)
            total = 0
            while time.perf_counter() < deadline:
                hashlib.pbkdf2_hmac('sha256', b'password', b'salt', 100000)
                total += 1
            body = f'served_by={VMTAG}\nload={total}\n'.encode()
        else:
            body = f'served_by={VMTAG}\n'.encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

ThreadingHTTPServer(('0.0.0.0', 8000), Handler).serve_forever()
PYEOF

nohup python3 -u /tmp/em_demo_server.py >/tmp/em_demo_server.log 2>&1 < /dev/null &
for i in 1 2 3 4 5; do
	if command -v curl >/dev/null 2>&1; then
		if curl -fsS --max-time 3 http://127.0.0.1:8000/health >/dev/null 2>&1; then
			echo "OK"
			exit 0
		fi
	elif command -v wget >/dev/null 2>&1; then
		if wget -qO- --timeout=3 http://127.0.0.1:8000/health >/dev/null 2>&1; then
			echo "OK"
			exit 0
		fi
	fi
	sleep 1
done
if command -v curl >/dev/null 2>&1; then
	curl -s --max-time 3 http://127.0.0.1:8000/health 2>/dev/null || true
elif command -v wget >/dev/null 2>&1; then
	wget -qO- --timeout=3 http://127.0.0.1:8000/health 2>/dev/null || true
fi
echo "FAILED"
exit 1
`, vmName)

	out, err := client.Run(bootstrapCmd)
	if err != nil || !strings.Contains(out, "OK") {
		a.logEvent(EventWarning, fmt.Sprintf("Error levantando servidor demo en %s: %v (output: %s)", vmName, err, out))
		return
	}

	a.logEvent(EventInfo, fmt.Sprintf("Servidor demo levantado exitosamente en %s", vmName))
}

func (a *AutoScaler) syncHAProxyIfNeeded() {
	if a.hap == nil {
		return
	}
	err := a.hap.Sync()
	a.mu.Lock()
	wasDegraded := a.hapDegraded
	if err != nil {
		a.hapDegraded = true
		a.mu.Unlock()
		if !wasDegraded {
			a.logEvent(EventWarning, fmt.Sprintf("HAProxy no disponible para aplicar configuración: %v", err))
		}
		return
	}
	a.hapDegraded = false
	a.mu.Unlock()
	if wasDegraded {
		a.logEvent(EventInfo, "HAProxy recuperado; configuración sincronizada")
	}
}

func (a *AutoScaler) scaleOut(cfg config.AutoScalerConfig) {
	allVMs := a.vmMgr.ListVirtualBoxVMs()
	if stopped, ok := pickReusableStoppedInstance(allVMs); ok {
		a.logEvent(EventScaleOut, fmt.Sprintf("Scale-OUT → reusando VM apagada %s (CPU avg > %.0f%%)", stopped.Name, cfg.UpperThreshold))
		inst, err := a.vmMgr.StartExisting(stopped.Name)
		if err == nil {
			if idx, ok := appVMIndex(stopped.Name); ok {
				a.mu.Lock()
				if idx > a.instanceN {
					a.instanceN = idx
				}
				a.mu.Unlock()
			}

			// Bootstrap FIRST, then register. If the Python server is not up
			// when HAProxy adds the backend, the health-checks fail immediately
			// and HAProxy marks the VM as DOWN — traffic never reaches it.
			a.bootstrapDemoServer(inst.Name, inst.SSHPort)

			port := inst.Port
			if idx, ok := appVMIndex(inst.Name); ok {
				port = 8000 + idx
			} else if port <= 0 {
				port = 8000
			}
			srv := haproxy.Server{Name: inst.Name, IP: "10.0.2.2", Port: port, Weight: 1, Active: true}
			if err := a.registerServerInBackend(srv); err != nil {
				a.logEvent(EventWarning, fmt.Sprintf("Error registrando en HAProxy: %v", err))
				return
			}
			a.markScaleOutSuccess()
			a.logEvent(EventScaleOut, fmt.Sprintf("VM %s reactivada y registrada en HAProxy backend '%s'", inst.Name, a.backendName))
			return
		}
		a.logEvent(EventWarning, fmt.Sprintf("No se pudo reactivar VM apagada %s: %v", stopped.Name, err))
	}

	_, startN := nextAvailableInstanceName(allVMs)

	var (
		inst *vm.Instance
		err  error
		name string
		n    int
	)

	const maxNameAttempts = 20
	for attempt := 0; attempt < maxNameAttempts; attempt++ {
		n = startN + attempt
		name = fmt.Sprintf("app-vm-%d", n)
		ip := fmt.Sprintf("10.0.2.%d", 10+n)
		sshPort := 2200 + n
		appForwardPort := 8000 + n

		a.logEvent(EventScaleOut, fmt.Sprintf("Scale-OUT → creando VM %s desde base %s (snapshot %s)", name, a.vmMgr.BaseVM(), a.vmMgr.BaseSnapshot()))
		inst, err = a.vmMgr.CloneAndStart(name, ip, appForwardPort, sshPort)
		if err == nil {
			break
		}

		if isVMNameConflict(err) {
			a.logEvent(EventWarning, fmt.Sprintf("Conflicto de nombre para VM %s, reintentando con siguiente índice", name))
			continue
		}

		a.logEvent(EventWarning, fmt.Sprintf("Error clonando VM: %v", err))
		return
	}

	if err != nil {
		a.logEvent(EventWarning, fmt.Sprintf("No se pudo crear VM tras %d intentos: %v", maxNameAttempts, err))
		return
	}

	a.mu.Lock()
	if n > a.instanceN {
		a.instanceN = n
	}
	a.mu.Unlock()

	// Bootstrap FIRST, then register — same reasoning as the reuse branch above.
	a.bootstrapDemoServer(name, inst.SSHPort)

	port := inst.Port
	if idx, ok := appVMIndex(name); ok {
		port = 8000 + idx
	} else if port <= 0 {
		port = 8000
	}
	srv := haproxy.Server{Name: name, IP: "10.0.2.2", Port: port, Weight: 1, Active: true}
	if err := a.registerServerInBackend(srv); err != nil {
		a.logEvent(EventWarning, fmt.Sprintf("Error registrando en HAProxy: %v", err))
		return
	}
	a.markScaleOutSuccess()
	a.logEvent(EventScaleOut, fmt.Sprintf("VM %s registrada en HAProxy backend '%s'", name, a.backendName))
}

func (a *AutoScaler) registerServerInBackend(srv haproxy.Server) error {
	err := a.hap.AddServer(a.backendName, srv)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already in backend") {
		return a.hap.UpdateServer(a.backendName, srv.Name, srv.IP, srv.Port, srv.Weight)
	}
	if strings.Contains(msg, "backend") && strings.Contains(msg, "not found") {
		if err := a.hap.CreateBackend(a.backendName, "roundrobin"); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return err
			}
		}
		err2 := a.hap.AddServer(a.backendName, srv)
		if err2 == nil {
			return nil
		}
		if strings.Contains(strings.ToLower(err2.Error()), "already in backend") {
			return a.hap.UpdateServer(a.backendName, srv.Name, srv.IP, srv.Port, srv.Weight)
		}
		return err2
	}
	return err
}

func isVMNameConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "vbox_e_file_error")
}

func nextAvailableInstanceName(instances []vm.Instance) (string, int) {
	used := make(map[int]bool, len(instances))
	for _, inst := range instances {
		if !strings.HasPrefix(inst.Name, "app-vm-") {
			continue
		}
		// Skip stuck/starting instances; they can be cleaned up later
		if inst.Status == vm.StatusStarting {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(inst.Name, "app-vm-"))
		if err != nil || n <= 0 {
			continue
		}
		used[n] = true
	}

	idx := 1
	for used[idx] {
		idx++
	}
	return fmt.Sprintf("app-vm-%d", idx), idx
}

func appVMIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "app-vm-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "app-vm-"))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func pickReusableStoppedInstance(instances []vm.Instance) (vm.Instance, bool) {
	var (
		best    vm.Instance
		bestIdx int
		found   bool
	)
	for _, inst := range instances {
		if inst.Status != vm.StatusStopped {
			continue
		}
		idx, ok := appVMIndex(inst.Name)
		if !ok {
			continue
		}
		if !found || idx < bestIdx {
			best = inst
			bestIdx = idx
			found = true
		}
	}
	return best, found
}

func (a *AutoScaler) scaleInToTarget(instances []vm.Instance, targetCount int) {
	if targetCount < 0 {
		targetCount = 0
	}
	for len(instances) > targetCount {
		if !a.scaleInOne(instances) {
			return
		}
		instances = instances[:len(instances)-1]
	}
}

func (a *AutoScaler) scaleInOne(instances []vm.Instance) bool {
	if len(instances) == 0 {
		return false
	}
	target := instances[len(instances)-1]
	a.logEvent(EventScaleIn, fmt.Sprintf("Scale-IN → apagando VM %s (CPU avg bajo umbral inferior)", target.Name))

	if err := a.hap.RemoveServer(a.backendName, target.Name); err != nil {
		a.logEvent(EventWarning, fmt.Sprintf("HAProxy remove: %v", err))
	}
	if err := a.vmMgr.StopAndKeep(target.Name); err != nil {
		a.logEvent(EventWarning, fmt.Sprintf("Stop VM: %v", err))
		return false
	}
	a.mon.RemoveInstanceData(target.Name)
	a.logEvent(EventScaleIn, fmt.Sprintf("VM %s apagada y conservada", target.Name))
	return true
}

func runningOnly(instances []vm.Instance) []vm.Instance {
	out := make([]vm.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst.Status == vm.StatusRunning {
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ii, iok := appVMIndex(out[i].Name)
		ji, jok := appVMIndex(out[j].Name)
		switch {
		case iok && jok:
			if ii != ji {
				return ii < ji
			}
			return out[i].Name < out[j].Name
		case iok && !jok:
			return false
		case !iok && jok:
			return true
		default:
			return out[i].Name < out[j].Name
		}
	})
	return out
}

func (a *AutoScaler) logEvent(kind EventKind, msg string) {
	e := Event{Kind: kind, Message: msg, Timestamp: time.Now()}
	log.Printf("[autoscaler][%s] %s", kind, msg)
	a.mu.Lock()
	a.events = append(a.events, e)
	if len(a.events) > 200 {
		a.events = a.events[len(a.events)-200:]
	}
	a.mu.Unlock()
}
