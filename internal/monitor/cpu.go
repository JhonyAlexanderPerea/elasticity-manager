// Package monitor measures CPU usage of remote VMs via SSH.
package monitor

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"elasticity-manager/internal/sshutil"
	"elasticity-manager/internal/vm"
)

// Sample is one CPU reading for one instance at a point in time.
type Sample struct {
	InstanceName string    `json:"instance_name"`
	CPU          float64   `json:"cpu"`
	Timestamp    time.Time `json:"timestamp"`
}

// Config holds the monitor's runtime parameters (mirrors api.AutoScalerConfig).
type Config interface {
	SampleIntervalSec() int
	EvaluationWindowSec() int
}

// CPUMonitor polls each VM's CPU via SSH and keeps a rolling history.
type CPUMonitor struct {
	mu         sync.RWMutex
	vmMgr      *vm.Manager
	getConfig  func() (sampleInterval, evalWindow int)
	sshUser    string
	sshKeyPath string
	history    map[string][]Sample // keyed by instance name
	latest     map[string]float64
	simulated  map[string]simulatedLoad
}

type simulatedLoad struct {
	CPU       float64
	ExpiresAt time.Time
}

// NewCPUMonitor creates a CPUMonitor.
// getConfig is a closure that returns (sampleIntervalSec, evalWindowSec) at call time.
func NewCPUMonitor(vmMgr *vm.Manager, sshUser, sshKeyPath string, getConfig func() (int, int)) *CPUMonitor {
	return &CPUMonitor{
		vmMgr:      vmMgr,
		getConfig:  getConfig,
		sshUser:    sshUser,
		sshKeyPath: sshKeyPath,
		history:    make(map[string][]Sample),
		latest:     make(map[string]float64),
		simulated:  make(map[string]simulatedLoad),
	}
}

// Start runs the polling loop until ctx is cancelled.
func (m *CPUMonitor) Start(ctx context.Context) {
	log.Println("[monitor] CPU monitor started")
	for {
		interval, _ := m.getConfig()
		select {
		case <-ctx.Done():
			log.Println("[monitor] stopped")
			return
		case <-time.After(time.Duration(interval) * time.Second):
			m.poll()
		}
	}
}

func (m *CPUMonitor) poll() {
	for _, inst := range m.vmMgr.ListInstances() {
		if inst.Status != vm.StatusRunning {
			continue
		}
		cpu, err := m.measureCPU(inst.SSHPort)
		if err != nil {
			log.Printf("[monitor] %s: %v", inst.Name, err)
			m.clearLatest(inst.Name)
			continue
		}
		m.record(inst.Name, cpu)
	}
}

func (m *CPUMonitor) clearLatest(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.latest, name)
}

// measureCPU connects via SSH to the VM on the given host-forwarded port
// and reads CPU usage from /proc/stat over a 1-second window.
func (m *CPUMonitor) measureCPU(sshPort int) (float64, error) {
	client, err := sshutil.New("127.0.0.1", sshPort, m.sshUser, m.sshKeyPath)
	if err != nil {
		return 0, fmt.Errorf("ssh client: %w", err)
	}

	// Python one-liner: read /proc/stat twice 1 second apart, compute CPU %
	cmd := `python3 -c "
import time
with open('/proc/stat') as f: a=f.readline().split()
time.sleep(1)
with open('/proc/stat') as f: b=f.readline().split()
idle=int(b[4])-int(a[4])
total=sum(int(x) for x in b[1:])-sum(int(x) for x in a[1:])
print(round(100*(1-idle/total),2) if total else 0)
"`
	out, err := client.Run(cmd)
	if err != nil {
		return 0, fmt.Errorf("measure cmd: %w", err)
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(out), 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", out, err)
	}
	return val, nil
}

func (m *CPUMonitor) record(name string, cpu float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Sample{InstanceName: name, CPU: cpu, Timestamp: time.Now()}
	m.history[name] = append(m.history[name], s)
	m.latest[name] = cpu
	// Trim history older than 10 minutes
	cutoff := time.Now().Add(-10 * time.Minute)
	kept := m.history[name][:0]
	for _, h := range m.history[name] {
		if h.Timestamp.After(cutoff) {
			kept = append(kept, h)
		}
	}
	m.history[name] = kept
}

// Latest returns the most recent CPU % per instance name.
func (m *CPUMonitor) Latest() map[string]float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneExpiredSimulatedLocked(time.Now())
	out := make(map[string]float64, len(m.latest))
	for k, v := range m.latest {
		out[k] = v
	}
	for name, s := range m.simulated {
		out[name] = s.CPU
	}
	return out
}

// AverageCPU returns the mean CPU over the last `seconds` seconds across all instances.
func (m *CPUMonitor) AverageCPU(seconds int) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.pruneExpiredSimulatedLocked(now)
	cutoff := time.Now().Add(-time.Duration(seconds) * time.Second)
	var sum float64
	var n int
	for _, samples := range m.history {
		for _, s := range samples {
			if s.Timestamp.After(cutoff) {
				sum += s.CPU
				n++
			}
		}
	}
	for _, s := range m.simulated {
		sum += s.CPU
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// AllHistory returns all samples keyed by instance name.
func (m *CPUMonitor) AllHistory() map[string][]Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string][]Sample, len(m.history))
	for k, v := range m.history {
		cp := make([]Sample, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// AddSimulatedSample injects a fake CPU reading (for demo/testing without real VMs).
func (m *CPUMonitor) AddSimulatedSample(name string, cpu float64) {
	m.record(name, cpu)
}

// StartSimulatedLoad keeps a synthetic CPU load active for a duration.
// While active, it contributes to AverageCPU and Latest.
func (m *CPUMonitor) StartSimulatedLoad(name string, cpu float64, duration time.Duration) {
	if duration <= 0 {
		duration = 60 * time.Second
	}
	m.mu.Lock()
	m.simulated[name] = simulatedLoad{CPU: cpu, ExpiresAt: time.Now().Add(duration)}
	m.latest[name] = cpu
	m.mu.Unlock()

	// Keep chart/history moving during the simulation window.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			m.mu.RLock()
			s, ok := m.simulated[name]
			m.mu.RUnlock()
			if !ok || time.Now().After(s.ExpiresAt) {
				m.RemoveInstanceData(name)
				return
			}
			m.record(name, cpu)
			<-ticker.C
		}
	}()
}

// RemoveInstanceData clears all tracked CPU samples for an instance.
// Useful after simulated runs to avoid stale values in the dashboard.
func (m *CPUMonitor) RemoveInstanceData(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.history, name)
	delete(m.latest, name)
	delete(m.simulated, name)
}

func (m *CPUMonitor) pruneExpiredSimulatedLocked(now time.Time) {
	for name, s := range m.simulated {
		if now.After(s.ExpiresAt) {
			delete(m.simulated, name)
			delete(m.latest, name)
		}
	}
}
