// Package vm handles VirtualBox VM lifecycle from a Windows host.
// VBoxManage is called directly (it is a native Windows executable).
// SSH connections to the VMs go through NAT port-forwarding on 127.0.0.1.
package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"elasticity-manager/internal/sshutil"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type Status string

const (
	StatusRunning  Status = "running"
	StatusStarting Status = "starting"
	StatusStopped  Status = "stopped"
	StatusSaved    Status = "saved"
	StatusUnknown  Status = "unknown"
)

// Instance is a VirtualBox VM tracked by this system.
type Instance struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IP        string    `json:"ip"`       // internal VM IP
	Port      int       `json:"port"`     // host-forwarded app port (Windows host → VM:8000)
	SSHPort   int       `json:"ssh_port"` // host-forwarded SSH port (127.0.0.1:SSHPort → VM:22)
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	CPU       float64   `json:"cpu"`
}

// ---------------------------------------------------------------------------
// Manager
// ---------------------------------------------------------------------------

type Manager struct {
	mu         sync.RWMutex
	cacheMu    sync.RWMutex
	sshKeyPath string // Windows path, e.g. C:\Users\you\.ssh\id_rsa
	sshUser    string
	baseVM     string // name of the base VM to clone from
	baseSnap   string // snapshot name inside baseVM
	instances  map[string]*Instance
	cachedVMs  []Instance
	vboxPath   string // full path to VBoxManage.exe
	onChange   func()
}

// NewManager creates a VM Manager.
// keyPath should be an absolute Windows path, e.g. C:\Users\you\.ssh\id_rsa
func NewManager(sshKeyPath string, initial []Instance, onChange func()) *Manager {
	instances := make(map[string]*Instance, len(initial))
	cachedVMs := make([]Instance, 0, len(initial))
	for i := range initial {
		inst := initial[i]
		copyInst := inst
		instances[copyInst.Name] = &copyInst
		cachedVMs = append(cachedVMs, copyInst)
	}
	return &Manager{
		sshKeyPath: sshKeyPath,
		sshUser:    "debian",
		baseVM:     "debian-base",
		baseSnap:   "base-snapshot",
		instances:  instances,
		cachedVMs:  cachedVMs,
		vboxPath:   detectVBoxManage(),
		onChange:   onChange,
	}
}

// SetOnChange updates the callback invoked after instance mutations.
func (m *Manager) SetOnChange(onChange func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = onChange
}

// detectVBoxManage finds VBoxManage on the current OS.
func detectVBoxManage() string {
	if runtime.GOOS == "windows" {
		// Try common installation paths
		candidates := []string{
			`C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`,
			`C:\Program Files (x86)\Oracle\VirtualBox\VBoxManage.exe`,
		}
		for _, p := range candidates {
			if _, err := exec.LookPath(p); err == nil {
				return p
			}
		}
		// Fallback: assume it's in PATH (installer adds it)
		return "VBoxManage.exe"
	}
	// Linux/macOS
	return "VBoxManage"
}

func (m *Manager) SetBaseVM(name, snapshot string) { m.baseVM = name; m.baseSnap = snapshot }
func (m *Manager) SetSSHUser(u string)             { m.sshUser = u }
func (m *Manager) BaseVM() string                  { return m.baseVM }
func (m *Manager) BaseSnapshot() string            { return m.baseSnap }

// ---------------------------------------------------------------------------
// Instance management
// ---------------------------------------------------------------------------

func (m *Manager) ListInstances() []Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Instance, 0, len(m.instances))
	for _, v := range m.instances {
		out = append(out, *v)
	}
	return out
}

// StartBackgroundSync keeps the cached VirtualBox view fresh without blocking
// hot API paths. It also adopts/prunes managed instances from the current VBox state.
func (m *Manager) StartBackgroundSync(ctx context.Context, interval time.Duration) {
	if interval < time.Second {
		interval = 2 * time.Second
	}
	go func() {
		m.syncWithVirtualBox()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.syncWithVirtualBox()
			}
		}
	}()
}

// PruneMissingInstances removes managed instances that no longer exist in VirtualBox.
// This is used at startup to avoid restoring stale state from a previous run.
func (m *Manager) PruneMissingInstances() int {
	return len(m.PruneMissingInstanceNames())
}

// PruneMissingInstanceNames removes managed instances that no longer exist in
// VirtualBox and returns the removed instance names.
func (m *Manager) PruneMissingInstanceNames() []string {
	return m.syncWithVirtualBox()
}

// EnsureManagedAppInstances adopts existing app-vm-* VirtualBox VMs that are
// not currently tracked in memory (e.g. after app restart with incomplete state).
func (m *Manager) EnsureManagedAppInstances() []string {
	added, _, _ := m.syncWithVBoxSnapshot()
	return added
}

// ListVirtualBoxVMs returns all VMs registered in VirtualBox.
// For VMs not managed by this app, only name/status are guaranteed.
func (m *Manager) ListVirtualBoxVMs() []Instance {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	out := make([]Instance, len(m.cachedVMs))
	copy(out, m.cachedVMs)
	return out
}

func (m *Manager) GetInstance(name string) (Instance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.instances[name]
	return *v, ok
}

// CloneAndStart creates a linked clone of baseVM from baseSnap, configures
// NAT port-forwarding for SSH and the app, starts the VM headlessly, and
// waits for SSH to become reachable (up to 60 s).
func (m *Manager) CloneAndStart(newName, ip string, appForwardPort, sshPort int) (*Instance, error) {
	cloneArgs := []string{
		"clonevm", m.baseVM,
		"--name", newName,
		"--snapshot", m.baseSnap,
		"--options", "link",
		"--register",
	}

	// 1. Linked clone
	out, err := m.vbox(cloneArgs...)
	if err != nil {
		if isVBoxFileConflict(out) {
			cleaned, cleanErr := m.cleanupOrphanVMDir(newName)
			if cleanErr != nil {
				return nil, fmt.Errorf("clonevm %q: %w\n%s\norphan cleanup failed: %v", newName, err, out, cleanErr)
			}
			if cleaned {
				out, err = m.vbox(cloneArgs...)
				if err == nil {
					goto cloned
				}
			}
		}
		return nil, fmt.Errorf("clonevm %q: %w\n%s", newName, err, out)
	}

cloned:

	// 2. Clean inherited NAT rules from snapshot (best-effort)
	m.vbox("modifyvm", newName, "--natpf1", "delete", "ssh-haproxy") //nolint
	m.vbox("modifyvm", newName, "--natpf1", "delete", "SSH-servidor1") //nolint
	m.vbox("modifyvm", newName, "--natpf1", "delete", "SSH-"+m.baseVM) //nolint
	m.vbox("modifyvm", newName, "--natpf1", "delete", "APP-servidor1") //nolint
	m.vbox("modifyvm", newName, "--natpf1", "delete", "APP-"+m.baseVM) //nolint

	// 3. NAT rule: host 127.0.0.1:<sshPort> → VM :22
	natRule := fmt.Sprintf("SSH-%s,tcp,127.0.0.1,%d,,22", newName, sshPort)
	if out, err := m.vbox("modifyvm", newName, "--natpf1", natRule); err != nil {
		return nil, fmt.Errorf("natpf %q: %w\n%s", newName, err, out)
	}

	// 4. NAT rule for the app: host 127.0.0.1:<appForwardPort> → VM :8000
	appRule := fmt.Sprintf("APP-%s,tcp,127.0.0.1,%d,,8000", newName, appForwardPort)
	if out, err := m.vbox("modifyvm", newName, "--natpf1", appRule); err != nil {
		return nil, fmt.Errorf("natpf app %q: %w\n%s", newName, err, out)
	}

	// 5. Start headless
	if out, err := m.vbox("startvm", newName, "--type", "headless"); err != nil {
		return nil, fmt.Errorf("startvm %q: %w\n%s", newName, err, out)
	}

	inst := &Instance{
		Name:      newName,
		IP:        ip,
		Port:      appForwardPort,
		SSHPort:   sshPort,
		Status:    StatusStarting,
		CreatedAt: time.Now(),
	}
	m.mu.Lock()
	m.instances[newName] = inst
	m.cacheMu.Lock()
	m.cachedVMs = appendCachedInstance(m.cachedVMs, *inst)
	m.cacheMu.Unlock()
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}

	// 6. Wait for SSH (VM boot can take longer on cold boots)
	fmt.Printf("[vm] waiting for SSH on %s (port %d)…\n", newName, sshPort)
	if err := m.waitForSSH(sshPort, 180*time.Second); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if current, ok := m.instances[newName]; ok {
		current.Status = StatusRunning
	}
	changeFn = m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}

	return inst, nil
}

// StartExisting powers on an already-registered VM and waits for SSH.
func (m *Manager) StartExisting(name string) (*Instance, error) {
	m.mu.Lock()
	inst, ok := m.instances[name]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("instance %q not found", name)
	}
	inst.Status = StatusStarting
	if idx, ok := parseAppVMIndex(name); ok {
		if inst.IP == "" || inst.IP == "-" {
			inst.IP = fmt.Sprintf("10.0.2.%d", 10+idx)
		}
		if inst.Port == 0 {
			inst.Port = 8000 + idx
		}
		if inst.SSHPort == 0 {
			inst.SSHPort = 2200 + idx
		}
	}
	sshPort := inst.SSHPort
	changeFn := m.onChange
	m.mu.Unlock()

	if out, err := m.vbox("startvm", name, "--type", "headless"); err != nil {
		// Mark as stopped if startup fails, so it can be retried or skipped for new allocations
		m.mu.Lock()
		if inst, ok := m.instances[name]; ok {
			inst.Status = StatusStopped
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("startvm %q: %w\n%s", name, err, out)
	}

	fmt.Printf("[vm] waiting for SSH on %s (port %d)…\n", name, sshPort)
	if err := m.waitForSSH(sshPort, 180*time.Second); err != nil {
		// Mark as stopped if SSH wait fails
		m.mu.Lock()
		if inst, ok := m.instances[name]; ok {
			inst.Status = StatusStopped
		}
		m.mu.Unlock()
		return nil, err
	}

	m.mu.Lock()
	if current, ok := m.instances[name]; ok {
		current.Status = StatusRunning
		copyInst := *current
		m.cacheMu.Lock()
		m.cachedVMs = appendCachedInstance(m.cachedVMs, copyInst)
		m.cacheMu.Unlock()
	}
	changeFn = m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return inst, nil
}

// StopAndKeep powers off a VM but keeps it registered in VirtualBox.
func (m *Manager) StopAndKeep(name string) error {
	m.vbox("controlvm", name, "poweroff") //nolint
	time.Sleep(3 * time.Second)
	m.mu.Lock()
	if inst, ok := m.instances[name]; ok {
		inst.Status = StatusStopped
	}
	m.cacheMu.Lock()
	for i := range m.cachedVMs {
		if m.cachedVMs[i].Name == name {
			m.cachedVMs[i].Status = StatusStopped
		}
	}
	m.cacheMu.Unlock()
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return nil
}

// StopAndDestroy powers off a VM and removes it and its disk from disk.
func (m *Manager) StopAndDestroy(name string) error {
	if err := m.StopAndKeep(name); err != nil {
		return err
	}
	if out, err := m.vbox("unregistervm", name, "--delete"); err != nil {
		return fmt.Errorf("unregistervm %q: %w\n%s", name, err, out)
	}
	m.mu.Lock()
	delete(m.instances, name)
	m.cacheMu.Lock()
	m.cachedVMs = removeCachedInstance(m.cachedVMs, name)
	m.cacheMu.Unlock()
	changeFn := m.onChange
	m.mu.Unlock()
	if changeFn != nil {
		changeFn()
	}
	return nil
}

// RefreshStatus queries VirtualBox for the current VM state.
func (m *Manager) RefreshStatus(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[name]
	if !ok {
		return
	}
	inst.Status = m.queryStatus(name)
}

func (m *Manager) queryStatus(name string) Status {
	out, err := m.vbox("showvminfo", name, "--machinereadable")
	if err != nil {
		return StatusUnknown
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, `VMState="`) {
			state := strings.Trim(strings.TrimPrefix(line, `VMState=`), `"`)
			switch state {
			case "running":
				return StatusRunning
			case "saved":
				return StatusSaved
			case "poweroff", "aborted":
				return StatusStopped
			}
		}
	}
	return StatusUnknown
}

// ---------------------------------------------------------------------------
// SSH helpers
// ---------------------------------------------------------------------------

// SSHClient returns a ready-to-use SSH client for the named instance.
func (m *Manager) SSHClient(name string) (*sshutil.Client, error) {
	m.mu.RLock()
	inst, ok := m.instances[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("instance %q not found", name)
	}
	return sshutil.New("127.0.0.1", inst.SSHPort, m.sshUser, m.sshKeyPath)
}

// RunCommand executes a command on the named VM via SSH.
func (m *Manager) RunCommand(name, command string) (string, error) {
	c, err := m.SSHClient(name)
	if err != nil {
		return "", err
	}
	return c.Run(command)
}

func (m *Manager) waitForSSH(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := sshutil.New("127.0.0.1", port, m.sshUser, m.sshKeyPath)
		if err == nil {
			if _, err2 := c.Run("echo ok"); err2 == nil {
				fmt.Printf("[vm] SSH ready on port %d\n", port)
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Printf("[vm] SSH timeout on port %d (VM may still be booting)\n", port)
	return fmt.Errorf("ssh timeout on port %d", port)
}

// ---------------------------------------------------------------------------
// VBoxManage wrapper
// ---------------------------------------------------------------------------

func (m *Manager) vbox(args ...string) (string, error) {
	timeout := vboxTimeout(args...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.vboxPath, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return strings.TrimSpace(string(out)), fmt.Errorf("vbox timeout: %s %s", m.vboxPath, strings.Join(args, " "))
	}
	return strings.TrimSpace(string(out)), err
}

func vboxTimeout(args ...string) time.Duration {
	if len(args) == 0 {
		return 8 * time.Second
	}
	switch args[0] {
	case "clonevm":
		return 3 * time.Minute
	case "startvm":
		return 60 * time.Second
	case "unregistervm":
		return 45 * time.Second
	case "controlvm":
		return 20 * time.Second
	case "modifyvm":
		return 20 * time.Second
	case "showvminfo":
		return 12 * time.Second
	case "list":
		return 12 * time.Second
	default:
		return 20 * time.Second
	}
}

func (m *Manager) syncWithVirtualBox() []string {
	_, removed, _ := m.syncWithVBoxSnapshot()
	return removed
}

func (m *Manager) syncWithVBoxSnapshot() ([]string, []string, error) {
	vmsOut, err := m.vbox("list", "vms")
	if err != nil {
		return nil, nil, err
	}
	runningOut, _ := m.vbox("list", "runningvms")

	allNames := parseVBoxVMNames(vmsOut)
	runningNames := parseVBoxVMNameSet(runningOut)

	m.mu.Lock()
	removed := make([]string, 0)
	added := make([]string, 0)
	for name := range m.instances {
		if !containsName(allNames, name) {
			delete(m.instances, name)
			removed = append(removed, name)
		}
	}
	for _, name := range allNames {
		idx, ok := parseAppVMIndex(name)
		if !ok {
			continue
		}
		inst, exists := m.instances[name]
		if !exists {
			status := StatusStopped
			if runningNames[name] {
				status = StatusRunning
			}
			m.instances[name] = &Instance{
				Name:      name,
				IP:        fmt.Sprintf("10.0.2.%d", 10+idx),
				Port:      8000 + idx,
				SSHPort:   2200 + idx,
				Status:    status,
				CreatedAt: time.Now(),
			}
			added = append(added, name)
			continue
		}
		if inst.Status == StatusStarting {
			if inst.IP == "" || inst.IP == "-" {
				inst.IP = fmt.Sprintf("10.0.2.%d", 10+idx)
			}
			if inst.Port == 0 {
				inst.Port = 8000 + idx
			}
			if inst.SSHPort == 0 {
				inst.SSHPort = 2200 + idx
			}
			// Fall through to check if VM actually started
		}
		if inst.IP == "" || inst.IP == "-" {
			inst.IP = fmt.Sprintf("10.0.2.%d", 10+idx)
		}
		if inst.Port == 0 {
			inst.Port = 8000 + idx
		}
		if inst.SSHPort == 0 {
			inst.SSHPort = 2200 + idx
		}
		if runningNames[name] {
			inst.Status = StatusRunning
		} else {
			inst.Status = StatusStopped
		}
	}
	managedCopy := make([]Instance, 0, len(allNames))
	for _, name := range allNames {
		if inst, ok := m.instances[name]; ok {
			managedCopy = append(managedCopy, *inst)
			continue
		}
		st := StatusStopped
		if runningNames[name] {
			st = StatusRunning
		}
		managedCopy = append(managedCopy, Instance{Name: name, IP: "-", Port: 0, SSHPort: 0, Status: st})
	}
	changeFn := m.onChange
	m.mu.Unlock()

	m.cacheMu.Lock()
	m.cachedVMs = managedCopy
	m.cacheMu.Unlock()

	if len(added)+len(removed) > 0 && changeFn != nil {
		go changeFn()
	}
	return added, removed, nil
}

func containsName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func appendCachedInstance(items []Instance, inst Instance) []Instance {
	for i, item := range items {
		if item.Name == inst.Name {
			items[i] = inst
			return items
		}
	}
	return append(items, inst)
}

func removeCachedInstance(items []Instance, name string) []Instance {
	out := make([]Instance, 0, len(items))
	for _, item := range items {
		if item.Name != name {
			out = append(out, item)
		}
	}
	return out
}

func parseVBoxVMNames(output string) []string {
	names := make([]string, 0)
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"") {
			continue
		}
		rest := strings.TrimPrefix(line, "\"")
		idx := strings.Index(rest, "\" {")
		if idx <= 0 {
			continue
		}
		name := strings.ReplaceAll(rest[:idx], `\"`, `"`)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseVBoxVMNameSet(output string) map[string]bool {
	names := parseVBoxVMNames(output)
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

func parseAppVMIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "app-vm-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "app-vm-"))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func instancesFromMap(items map[string]Instance) []Instance {
	names := make([]string, 0, len(items))
	for n := range items {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Instance, 0, len(names))
	for _, n := range names {
		out = append(out, items[n])
	}
	return out
}

func isVBoxFileConflict(output string) bool {
	text := strings.ToLower(output)
	return strings.Contains(text, "vbox_e_file_error") ||
		(strings.Contains(text, "already exists") && strings.Contains(text, ".vbox"))
}

func (m *Manager) cleanupOrphanVMDir(name string) (bool, error) {
	if _, err := m.vbox("showvminfo", name, "--machinereadable"); err == nil {
		return false, nil
	}

	base, err := m.defaultMachineFolder()
	if err != nil {
		return false, err
	}

	dir := filepath.Join(base, name)
	if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
		return false, nil
	} else if statErr != nil {
		return false, statErr
	}

	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) defaultMachineFolder() (string, error) {
	out, err := m.vbox("list", "systemproperties")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Default machine folder:") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		folder := strings.TrimSpace(parts[1])
		if folder != "" {
			return folder, nil
		}
	}
	return "", fmt.Errorf("default machine folder not found")
}
