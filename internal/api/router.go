package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"elasticity-manager/internal/autoscaler"
	"elasticity-manager/internal/haproxy"
	"elasticity-manager/internal/monitor"
	"elasticity-manager/internal/vm"
)

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

// NewRouter wires all HTTP routes and returns the handler.
func NewRouter(
	store *Store,
	hap *haproxy.Manager,
	vmMgr *vm.Manager,
	mon *monitor.CPUMonitor,
	scaler *autoscaler.AutoScaler,
) http.Handler {
	h := &handlers{store: store, hap: hap, vm: vmMgr, mon: mon, scaler: scaler}
	mux := http.NewServeMux()

	// Dashboard SPA
	mux.HandleFunc("GET /", h.ui)

	// Config
	mux.HandleFunc("GET /api/config", h.getConfig)
	mux.HandleFunc("PUT /api/config", h.putConfig)

	// Status
	mux.HandleFunc("GET /api/status", h.getStatus)

	// Backends
	mux.HandleFunc("GET /api/backends", h.listBackends)
	mux.HandleFunc("POST /api/backends", h.createBackend)
	mux.HandleFunc("PUT /api/backends/{name}", h.updateBackend)
	mux.HandleFunc("DELETE /api/backends/{name}", h.deleteBackend)

	// Servers inside a backend
	mux.HandleFunc("GET /api/backends/{name}/servers", h.listServers)
	mux.HandleFunc("POST /api/backends/{name}/servers", h.addServer)
	mux.HandleFunc("PUT /api/backends/{name}/servers/{server}", h.updateServer)
	mux.HandleFunc("DELETE /api/backends/{name}/servers/{server}", h.removeServer)

	// VMs
	mux.HandleFunc("GET /api/vms", h.listVMs)

	// Events
	mux.HandleFunc("GET /api/events", h.listEvents)
	mux.HandleFunc("GET /api/events/stream", h.streamEvents)

	// HAProxy raw config
	mux.HandleFunc("GET /api/haproxy/config", h.haproxyConfig)

	// Simulation
	mux.HandleFunc("POST /api/simulate", h.simulate)
	mux.HandleFunc("POST /api/simulate/cancel", h.cancelSimulate)
	mux.HandleFunc("POST /api/simulate/wrk", h.simulateWrk)
	mux.HandleFunc("POST /api/simulate/wrk/cancel", h.cancelWrk)

	// Scaler toggle
	mux.HandleFunc("POST /api/autoscaler/enable", h.enableScaler)
	mux.HandleFunc("POST /api/autoscaler/disable", h.disableScaler)

	return mux
}

// ---------------------------------------------------------------------------
// Handlers struct
// ---------------------------------------------------------------------------

type handlers struct {
	store  *Store
	hap    *haproxy.Manager
	vm     *vm.Manager
	mon    *monitor.CPUMonitor
	scaler *autoscaler.AutoScaler

	wrkMu     sync.Mutex
	wrkCancel context.CancelFunc
	wrkMode   string
}

func (h *handlers) startLocalWrk(threads, connections, duration int, targetURL, targetLabel string) {
	h.wrkMu.Lock()
	if h.wrkCancel != nil {
		h.wrkCancel()
		h.wrkCancel = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.wrkCancel = cancel
	h.wrkMode = targetLabel
	h.wrkMu.Unlock()

	go h.runLocalWrk(ctx, threads, connections, duration, targetURL, targetLabel)
}

func (h *handlers) runLocalWrk(ctx context.Context, threads, connections, duration int, targetURL, targetLabel string) {
	// Fallback local: impacta un objetivo HTTP desde el host local.
	target := targetURL
	deadline := time.Now().Add(time.Duration(duration) * time.Second)
	workers := connections
	if workers < 1 {
		workers = 1
	}
	if workers > 2000 {
		workers = 2000
	}

	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		MaxIdleConns:        workers,
		MaxIdleConnsPerHost: workers,
		MaxConnsPerHost:     workers,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: transport}

	var okCount atomic.Int64
	var failCount atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if time.Now().After(deadline) {
					return
				}
				select {
				case <-ctx.Done():
					return
				default:
				}

				resp, err := client.Get(target)
				if err != nil {
					failCount.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 500 {
					okCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	h.wrkMu.Lock()
	h.wrkCancel = nil
	h.wrkMode = ""
	h.wrkMu.Unlock()

	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("wrk %s finalizado: t=%d c=%d d=%ds target=%s ok=%d fail=%d", targetLabel, threads, connections, duration, target, okCount.Load(), failCount.Load()))
}

// ── UI ──────────────────────────────────────────────────────────────────────
func (h *handlers) ui(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

// ── Config ──────────────────────────────────────────────────────────────────
func (h *handlers) getConfig(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, h.store.GetConfig())
}

func (h *handlers) putConfig(w http.ResponseWriter, r *http.Request) {
	var cfg AutoScalerConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		badRequest(w, err.Error())
		return
	}
	if cfg.MinInstances < 2 {
		badRequest(w, "min_instances debe ser >= 2 para garantizar balanceo entre VMs")
		return
	}
	if cfg.MaxInstances < 2 {
		badRequest(w, "max_instances debe ser >= 2")
		return
	}
	if cfg.MinInstances > cfg.MaxInstances {
		badRequest(w, "min_instances no puede ser mayor que max_instances")
		return
	}
	if cfg.LowerThreshold >= cfg.UpperThreshold {
		badRequest(w, "lower_threshold debe ser menor que upper_threshold")
		return
	}
	if cfg.PeakThreshold <= 0 {
		cfg.PeakThreshold = 90
	}
	if cfg.PeakThreshold > 100 {
		cfg.PeakThreshold = 100
	}
	if cfg.SampleInterval < 1 {
		cfg.SampleInterval = 5
	}
	if cfg.EvaluationWindow < cfg.SampleInterval {
		cfg.EvaluationWindow = cfg.SampleInterval * 6
	}
	h.store.SetConfig(cfg)
	go h.scaler.EnsureMinInstances(cfg.MinInstances)
	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("Configuración actualizada: upper=%.0f%% lower=%.0f%% peak=%.0f%%", cfg.UpperThreshold, cfg.LowerThreshold, cfg.PeakThreshold))
	respond(w, http.StatusOK, cfg)
}

// ── Status ──────────────────────────────────────────────────────────────────
var startTime = time.Now()

type statusResponse struct {
	Instances     int                `json:"instances"`
	InstanceNames []string           `json:"instance_names"`
	AvgCPU        float64            `json:"avg_cpu"`
	LatestCPU     map[string]float64 `json:"latest_cpu"`
	Backends      int                `json:"backends"`
	ScalerOn      bool               `json:"scaler_enabled"`
	Uptime        string             `json:"uptime"`
	ServerTime    string             `json:"server_time"`
	StartUnix     int64              `json:"start_unix"`
	StartUnixNano int64              `json:"start_unix_nano"`
}

func (h *handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.GetConfig()
	managed := h.vm.ListInstances()
	runningCount := 0
	instanceNames := make([]string, 0, len(managed))
	rawLatest := h.mon.Latest()
	latest := make(map[string]float64, len(managed))
	knownManaged := make(map[string]struct{}, len(managed))
	for _, inst := range managed {
		knownManaged[inst.Name] = struct{}{}
		if inst.Status == vm.StatusRunning {
			instanceNames = append(instanceNames, inst.Name)
			runningCount++
			if cpu, ok := rawLatest[inst.Name]; ok {
				latest[inst.Name] = cpu
			} else {
				latest[inst.Name] = 0
			}
		}
	}
	for name, cpu := range rawLatest {
		if _, ok := knownManaged[name]; ok {
			continue
		}
		// Keep simulated/injected series visible in dashboard gauges.
		latest[name] = cpu
		instanceNames = append(instanceNames, name)
	}
	respond(w, http.StatusOK, statusResponse{
		Instances:     runningCount,
		InstanceNames: instanceNames,
		AvgCPU:        h.mon.AverageCPU(cfg.EvaluationWindow),
		LatestCPU:     latest,
		Backends:      len(h.hap.ListBackends()),
		ScalerOn:      h.scaler.IsEnabled(),
		Uptime:        time.Since(startTime).Round(time.Second).String(),
		ServerTime:    time.Now().Format(time.RFC3339),
		StartUnix:     startTime.Unix(),
		StartUnixNano: startTime.UnixNano(),
	})
}

// ── Backends ─────────────────────────────────────────────────────────────────
func (h *handlers) listBackends(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, h.hap.ListBackends())
}

func (h *handlers) createBackend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}
	if body.Name == "" {
		badRequest(w, "name es requerido")
		return
	}
	if err := h.hap.CreateBackend(body.Name, body.Algorithm); err != nil {
		serverError(w, err.Error())
		return
	}
	b, _ := h.hap.GetBackend(body.Name)
	respond(w, http.StatusCreated, b)
}

func (h *handlers) updateBackend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Algorithm string `json:"algorithm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := h.hap.UpdateBackend(name, body.Algorithm); err != nil {
		serverError(w, err.Error())
		return
	}
	b, _ := h.hap.GetBackend(name)
	respond(w, http.StatusOK, b)
}

func (h *handlers) deleteBackend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "app-backend" {
		badRequest(w, "app-backend es obligatorio para enrutar tráfico por HAProxy")
		return
	}
	if err := h.hap.DeleteBackend(name); err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusOK, map[string]string{"deleted": name})
}

// ── Servers ──────────────────────────────────────────────────────────────────
func (h *handlers) listServers(w http.ResponseWriter, r *http.Request) {
	b, ok := h.hap.GetBackend(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	respond(w, http.StatusOK, b.Servers)
}

func (h *handlers) addServer(w http.ResponseWriter, r *http.Request) {
	backendName := r.PathValue("name")
	var srv haproxy.Server
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		badRequest(w, err.Error())
		return
	}
	if srv.Name == "" || srv.IP == "" || srv.Port == 0 {
		badRequest(w, "name, ip y port son requeridos")
		return
	}
	if err := h.hap.AddServer(backendName, srv); err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusCreated, srv)
}

func (h *handlers) updateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IP     string `json:"ip"`
		Port   int    `json:"port"`
		Weight int    `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}
	if err := h.hap.UpdateServer(r.PathValue("name"), r.PathValue("server"), body.IP, body.Port, body.Weight); err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusOK, map[string]string{"updated": r.PathValue("server")})
}

func (h *handlers) removeServer(w http.ResponseWriter, r *http.Request) {
	backendName := r.PathValue("name")
	serverName := r.PathValue("server")
	if backendName == "app-backend" {
		b, ok := h.hap.GetBackend(backendName)
		if ok {
			appVMCount := 0
			for _, srv := range b.Servers {
				if strings.HasPrefix(srv.Name, "app-vm-") {
					appVMCount++
				}
			}
			if strings.HasPrefix(serverName, "app-vm-") && appVMCount <= 2 {
				badRequest(w, "no se puede remover: app-backend debe mantener al menos 2 app-vm para balanceo")
				return
			}
		}
	}

	if err := h.hap.RemoveServer(backendName, serverName); err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusOK, map[string]string{"removed": serverName})
}

// ── VMs ───────────────────────────────────────────────────────────────────────
func (h *handlers) listVMs(w http.ResponseWriter, r *http.Request) {
	vms := h.vm.ListVirtualBoxVMs()
	latest := h.mon.Latest()
	for i := range vms {
		if cpu, ok := latest[vms[i].Name]; ok {
			vms[i].CPU = cpu
		}
	}
	respond(w, http.StatusOK, vms)
}

// ── Events ────────────────────────────────────────────────────────────────────
func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, h.scaler.Events())
}

func (h *handlers) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		serverError(w, "streaming no soportado")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeEvent := func(ev autoscaler.Event) bool {
		payload, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	events := h.scaler.Events()
	maxInit := 40
	end := maxInit - 1
	if end >= len(events) {
		end = len(events) - 1
	}
	for i := end; i >= 0; i-- {
		if !writeEvent(events[i]) {
			return
		}
	}

	lastKey := ""
	if len(events) > 0 {
		last := events[0]
		lastKey = fmt.Sprintf("%s|%s|%s", last.Timestamp.Format(time.RFC3339Nano), last.Kind, last.Message)
	}

	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			curr := h.scaler.Events()
			if len(curr) == 0 {
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
				continue
			}

			newest := curr[0]
			newestKey := fmt.Sprintf("%s|%s|%s", newest.Timestamp.Format(time.RFC3339Nano), newest.Kind, newest.Message)
			if newestKey == lastKey {
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
				continue
			}

			idxLast := -1
			for i, ev := range curr {
				k := fmt.Sprintf("%s|%s|%s", ev.Timestamp.Format(time.RFC3339Nano), ev.Kind, ev.Message)
				if k == lastKey {
					idxLast = i
					break
				}
			}

			limit := len(curr)
			if idxLast >= 0 {
				limit = idxLast
			}
			for i := limit - 1; i >= 0; i-- {
				if !writeEvent(curr[i]) {
					return
				}
			}
			lastKey = newestKey
		}
	}
}

// ── HAProxy config ────────────────────────────────────────────────────────────
func (h *handlers) haproxyConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.hap.GetConfig()
	if err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusOK, map[string]string{"config": cfg})
}

// ── Simulation ────────────────────────────────────────────────────────────────
func (h *handlers) simulate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceName string  `json:"instance_name"`
		CPUPercent   float64 `json:"cpu_percent"`
		Duration     int     `json:"duration"` // seconds
		UseSSH       bool    `json:"use_ssh"`  // if true: run stress-ng on remote VM
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}

	if body.UseSSH && body.InstanceName != "" {
		badRequest(w, "instance_name no aplica para stress-ng via SSH global. Déjalo vacío para ejecutar en todas las app-vm running")
		return
	}
	if !body.UseSSH {
		badRequest(w, "simulación inyectada deshabilitada: usa stress-ng via SSH global")
		return
	}

	cpuLoad := int(body.CPUPercent)
	if cpuLoad <= 0 {
		cpuLoad = 100
	}
	if cpuLoad > 100 {
		cpuLoad = 100
	}
	duration := body.Duration
	if duration < 1 {
		duration = 60
	}

	targets := h.runningAppVMNames()
	if len(targets) == 0 {
		serverError(w, "no hay app-vm running para ejecutar stress-ng")
		return
	}

	targetsCopy := append([]string(nil), targets...)
	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("Iniciando stress-ng en app-vm running (%d nodos): load=%d%% por %ds", len(targetsCopy), cpuLoad, duration))

	go func(targets []string, load, secs int) {
		mode, err := h.startStressOnTargets(targets, load, secs)
		if err != nil {
			h.scaler.AddEvent(autoscaler.EventWarning, fmt.Sprintf("stress-ng SSH: %v", err))
			return
		}
		h.scaler.AddEvent(autoscaler.EventInfo,
			fmt.Sprintf("stress-ng iniciado en app-vm running (%d nodos): load=%d%% por %ds (%s)", len(targets), load, secs, mode))
	}(targetsCopy, cpuLoad, duration)

	respond(w, http.StatusOK, map[string]string{"status": "simulación en inicio"})
}

func (h *handlers) cancelSimulate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceName string `json:"instance_name"`
		UseSSH       bool   `json:"use_ssh"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}

	if !body.UseSSH {
		badRequest(w, "simulación inyectada deshabilitada: usa cancelación stress-ng via SSH global")
		return
	}

	if body.InstanceName != "" {
		badRequest(w, "instance_name no aplica para cancelar stress-ng via SSH global. Déjalo vacío")
		return
	}

	targets := h.runningAppVMNames()
	if len(targets) == 0 {
		respond(w, http.StatusOK, map[string]string{"status": "sin app-vm running para cancelar"})
		return
	}

	cmd := "pkill -x stress-ng >/dev/null 2>&1 || true; pkill -f '^python3(\\s+.*)?\\s+/tmp/em_cpu_burn.py(\\s+.*)?$' >/dev/null 2>&1 || true"
	if err := h.runCommandOnTargets(targets, cmd); err != nil {
		serverError(w, fmt.Sprintf("cancelar stress-ng SSH: %v", err))
		return
	}
	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("stress-ng cancelado en app-vm running (%d nodos)", len(targets)))
	respond(w, http.StatusOK, map[string]string{"status": "stress-ng cancelado"})
}

func (h *handlers) simulateWrk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Threads     int    `json:"threads"`
		Connections int    `json:"connections"`
		Duration    int    `json:"duration"`
		Path        string `json:"path"`
		TargetVM    string `json:"target_vm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		badRequest(w, err.Error())
		return
	}

	threads := body.Threads
	if threads < 1 {
		threads = 4
	}
	if threads > 64 {
		threads = 64
	}

	connections := body.Connections
	if connections < 1 {
		connections = 100
	}
	if connections > 2000 {
		connections = 2000
	}

	duration := body.Duration
	if duration < 1 {
		duration = 30
	}
	if duration > 86400 {
		duration = 86400
	}

	path := strings.TrimSpace(body.Path)
	if path == "" {
		path = "/heavy?ms=6000"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path == "/" {
		path = "/heavy?ms=6000"
	}
	if strings.ContainsAny(path, " '\"`;$&|<>") {
		badRequest(w, "path contiene caracteres no permitidos")
		return
	}

	if err := h.ensureDemoServersForWrk(); err != nil {
		h.scaler.AddEvent(autoscaler.EventWarning, fmt.Sprintf("No se pudo preparar servidor de carga en VMs: %v", err))
	}

	targetVM := strings.TrimSpace(body.TargetVM)
	if targetVM != "" {
		badRequest(w, "target_vm bypassa HAProxy y no sirve para validar balanceo. Déjalo vacío para correr wrk vía HAProxy")
		return
	}

	if _, err := h.hap.RunCommand("command -v wrk >/dev/null 2>&1"); err != nil {
		serverError(w, fmt.Sprintf("wrk remoto no disponible en HAProxy VM (%v). Instala wrk o restablece SSH hacia HAProxy", err))
		return
	}

	cmd := fmt.Sprintf("setsid sh -c 'exec wrk -t%d -c%d -d%ds http://127.0.0.1%s >/tmp/wrk-load.log 2>&1 < /dev/null' >/dev/null 2>&1 < /dev/null &", threads, connections, duration, path)
	if _, err := h.hap.RunCommand(cmd); err != nil {
		serverError(w, fmt.Sprintf("wrk ssh: %v", err))
		return
	}

	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("wrk iniciado vía HAProxy: t=%d c=%d d=%ds path=%s", threads, connections, duration, path))
	respond(w, http.StatusOK, map[string]string{"status": "wrk iniciado"})
}

func (h *handlers) ensureDemoServersForWrk() error {
	instances := h.vm.ListInstances()
	ready := 0
	var lastErr error

	for _, inst := range instances {
		if inst.Status != vm.StatusRunning {
			continue
		}
		if !strings.HasPrefix(inst.Name, "app-vm-") {
			continue
		}

		cmd := fmt.Sprintf("cat >/tmp/em_demo_server.py <<'PY'\nfrom http.server import BaseHTTPRequestHandler, ThreadingHTTPServer\nfrom urllib.parse import parse_qs, urlparse\nimport hashlib\nimport time\nVMTAG='%s'\nclass H(BaseHTTPRequestHandler):\n    def log_message(self, fmt, *args):\n        return\n    def do_GET(self):\n        p = urlparse(self.path)\n        if p.path == '/health':\n            body = b'ok\\n'\n        elif p.path == '/heavy':\n            q = parse_qs(p.query)\n            ms = 6000\n            if 'ms' in q and q['ms']:\n                try:\n                    ms = max(100, min(6000, int(q['ms'][0])))\n                except ValueError:\n                    ms = 6000\n            end = time.perf_counter() + (ms / 1000.0)\n            total = 0\n            while time.perf_counter() < end:\n                hashlib.pbkdf2_hmac('sha256', b'password', b'salt', 100000)\n                total += 1\n            body = f\"served_by=%s\\nload={total}\\n\".encode()\n        else:\n            body = f\"served_by=%s\\n\".encode()\n        self.send_response(200)\n        self.send_header('Content-Type', 'text/plain; charset=utf-8')\n        self.send_header('Content-Length', str(len(body)))\n        self.end_headers()\n        self.wfile.write(body)\nThreadingHTTPServer(('0.0.0.0', 8000), H).serve_forever()\nPY\nif command -v curl >/dev/null 2>&1; then\n    if curl -fsS --max-time 2 http://127.0.0.1:8000/health >/dev/null 2>&1; then\n        exit 0\n    fi\nelif command -v wget >/dev/null 2>&1; then\n    if wget -qO- --timeout=2 http://127.0.0.1:8000/health >/dev/null 2>&1; then\n        exit 0\n    fi\nfi\nnohup python3 -u /tmp/em_demo_server.py >/tmp/em_demo_server.log 2>&1 < /dev/null &\nfor i in 1 2 3 4 5; do\n    if command -v curl >/dev/null 2>&1; then\n        if curl -fsS --max-time 3 http://127.0.0.1:8000/health >/dev/null 2>&1; then\n            exit 0\n        fi\n    elif command -v wget >/dev/null 2>&1; then\n        if wget -qO- --timeout=3 http://127.0.0.1:8000/health >/dev/null 2>&1; then\n            exit 0\n        fi\n    fi\n    sleep 1\ndone\nexit 1", inst.Name, inst.Name, inst.Name, inst.Name)

		if _, err := h.vm.RunCommand(inst.Name, cmd); err != nil {
			lastErr = err
			continue
		}
		ready++
	}

	if ready == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

func (h *handlers) runningAppVMNames() []string {
	instances := h.vm.ListInstances()
	names := make([]string, 0, len(instances))
	for _, inst := range instances {
		if inst.Status != vm.StatusRunning {
			continue
		}
		if !strings.HasPrefix(inst.Name, "app-vm-") {
			continue
		}
		names = append(names, inst.Name)
	}
	sort.Strings(names)
	return names
}

func (h *handlers) ensureStressNGAvailable(targets []string) error {
	checkCmd := "command -v stress-ng >/dev/null 2>&1"
	for _, name := range targets {
		if _, err := h.vm.RunCommand(name, checkCmd); err != nil {
			return fmt.Errorf("stress-ng no está instalado o no está en PATH en %s: %v", name, err)
		}
	}
	return nil
}

func (h *handlers) runCommandOnTargets(targets []string, cmd string) error {
	for _, name := range targets {
		if _, err := h.vm.RunCommand(name, cmd); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func (h *handlers) startStressOnTargets(targets []string, cpuLoad, duration int) (string, error) {
	fallbackUsed := false
	for _, name := range targets {
		cmd := fmt.Sprintf("workers=$(nproc 2>/dev/null || echo 2)\n"+
			"if [ -z \"$workers\" ] || [ \"$workers\" -lt 1 ]; then workers=1; fi\n"+
			"pkill -x stress-ng >/dev/null 2>&1 || true\n"+
			"pkill -f '^python3(\\s+.*)?\\s+/tmp/em_cpu_burn.py(\\s+.*)?$' >/dev/null 2>&1 || true\n"+
			"if command -v stress-ng >/dev/null 2>&1; then\n"+
			"  nohup sh -c \"exec stress-ng --cpu $workers --cpu-load %d --timeout %ds --metrics-brief\" >/tmp/stress-ng-load.log 2>&1 < /dev/null &\n"+
			"else\n"+
			"  echo 'stress-ng no encontrado' > /tmp/stress-ng-load.log\n"+
			"fi\n"+
			"sleep 1\n"+
			"if pgrep -f '^stress-ng ' >/dev/null 2>&1; then\n"+
			"  echo STARTED_STRESS\n"+
			"  exit 0\n"+
			"fi\n"+
			"if ! command -v python3 >/dev/null 2>&1; then\n"+
			"  echo START_FAILED_NO_PYTHON\n"+
			"  tail -n 40 /tmp/stress-ng-load.log 2>/dev/null || true\n"+
			"  exit 1\n"+
			"fi\n"+
			"cat >/tmp/em_cpu_burn.py <<'PY'\n"+
			"import hashlib\n"+
			"import multiprocessing\n"+
			"import time\n"+
			"SECONDS = %d\n"+
			"WORKERS = 2\n"+
			"def burn():\n"+
			"    end = time.time() + max(1, SECONDS)\n"+
			"    while time.time() < end:\n"+
			"        hashlib.pbkdf2_hmac('sha256', b'password', b'salt', 60000)\n"+
			"if __name__ == '__main__':\n"+
			"    ps = []\n"+
			"    for _ in range(max(1, WORKERS)):\n"+
			"        p = multiprocessing.Process(target=burn)\n"+
			"        p.start()\n"+
			"        ps.append(p)\n"+
			"    for p in ps:\n"+
			"        p.join()\n"+
			"PY\n"+
			"nohup python3 -u /tmp/em_cpu_burn.py >/tmp/stress-ng-load.log 2>&1 < /dev/null &\n"+
			"sleep 1\n"+
			"if pgrep -f '/tmp/em_cpu_burn.py' >/dev/null 2>&1; then\n"+
			"  echo STARTED_FALLBACK\n"+
			"  exit 0\n"+
			"fi\n"+
			"echo START_FAILED\n"+
			"tail -n 40 /tmp/stress-ng-load.log 2>/dev/null || true\n"+
			"exit 1", cpuLoad, duration, duration)

		out, err := h.vm.RunCommand(name, cmd)
		if err != nil {
			return "", fmt.Errorf("%s: %v (output: %s)", name, err, strings.TrimSpace(out))
		}
		if strings.Contains(out, "STARTED_FALLBACK") {
			fallbackUsed = true
			continue
		}
		if strings.Contains(out, "STARTED_STRESS") {
			continue
		}
		return "", fmt.Errorf("%s: stress-ng no quedó activo (detalle: %s)", name, strings.TrimSpace(out))
	}
	if fallbackUsed {
		return "fallback-python", nil
	}
	return "stress-ng", nil
}

func (h *handlers) cancelWrk(w http.ResponseWriter, r *http.Request) {
	h.wrkMu.Lock()
	if h.wrkCancel != nil {
		h.wrkCancel()
		h.wrkCancel = nil
		mode := h.wrkMode
		h.wrkMode = ""
		h.wrkMu.Unlock()
		h.scaler.AddEvent(autoscaler.EventInfo, fmt.Sprintf("wrk cancelado (%s)", mode))
		respond(w, http.StatusOK, map[string]string{"status": "wrk cancelado"})
		return
	}
	h.wrkMu.Unlock()

	if _, err := h.hap.RunCommand("pkill -f '^wrk ' || true"); err != nil {
		serverError(w, fmt.Sprintf("cancelar wrk ssh: %v", err))
		return
	}
	h.scaler.AddEvent(autoscaler.EventInfo, "wrk cancelado en VM de HAProxy")
	respond(w, http.StatusOK, map[string]string{"status": "wrk cancelado"})
}

// ── Scaler toggle ─────────────────────────────────────────────────────────────
func (h *handlers) enableScaler(w http.ResponseWriter, r *http.Request) {
	h.scaler.SetEnabled(true)
	go h.scaler.EnsureMinInstances(h.store.GetConfig().MinInstances)
	h.scaler.AddEvent(autoscaler.EventInfo, "Auto-scaler ACTIVADO desde el panel web")
	respond(w, http.StatusOK, map[string]bool{"enabled": true})
}

func (h *handlers) disableScaler(w http.ResponseWriter, r *http.Request) {
	h.scaler.SetEnabled(false)
	h.scaler.AddEvent(autoscaler.EventInfo, "Auto-scaler PAUSADO desde el panel web")
	respond(w, http.StatusOK, map[string]bool{"enabled": false})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func respond(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encode: %v", err)
	}
}

func badRequest(w http.ResponseWriter, msg string) {
	respond(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, msg string) {
	respond(w, http.StatusInternalServerError, map[string]string{"error": msg})
}
