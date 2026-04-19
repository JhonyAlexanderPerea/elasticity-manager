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
	n := len(runningInstances)

	log.Printf("[autoscaler] avg=%.1f%% n=%d upper=%.0f lower=%.0f",
		avg, n, cfg.UpperThreshold, cfg.LowerThreshold)

	holdFor := time.Duration(cfg.EvaluationWindow) * time.Second
	if holdFor < 20*time.Second {
		holdFor = 20 * time.Second
	}
	now := time.Now()
	highCondition := avg > cfg.UpperThreshold && n < cfg.MaxInstances
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

func (a *AutoScaler) markScaleOutSuccess() {
	a.mu.Lock()
	a.lastScaleOut = time.Now()
	a.mu.Unlock()
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
			srv := haproxy.Server{Name: inst.Name, IP: inst.IP, Port: 8000, Weight: 1, Active: true}
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

	srv := haproxy.Server{Name: name, IP: inst.IP, Port: 8000, Weight: 1, Active: true}
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
