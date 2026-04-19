package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
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
	if cfg.LowerThreshold >= cfg.UpperThreshold {
		badRequest(w, "lower_threshold debe ser menor que upper_threshold")
		return
	}
	if cfg.SampleInterval < 1 {
		cfg.SampleInterval = 5
	}
	if cfg.EvaluationWindow < cfg.SampleInterval {
		cfg.EvaluationWindow = cfg.SampleInterval * 6
	}
	h.store.SetConfig(cfg)
	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("Configuración actualizada: upper=%.0f%% lower=%.0f%%", cfg.UpperThreshold, cfg.LowerThreshold))
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
}

func (h *handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.store.GetConfig()
	managed := h.vm.ListInstances()
	runningCount := 0
	instanceNames := make([]string, 0, len(managed))
	latest := h.mon.Latest()
	for _, inst := range managed {
		instanceNames = append(instanceNames, inst.Name)
		if inst.Status == vm.StatusRunning {
			runningCount++
		}
		if _, ok := latest[inst.Name]; !ok {
			latest[inst.Name] = 0
		}
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
	if err := h.hap.RemoveServer(r.PathValue("name"), r.PathValue("server")); err != nil {
		serverError(w, err.Error())
		return
	}
	respond(w, http.StatusOK, map[string]string{"removed": r.PathValue("server")})
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
		checkCmd := "command -v stress-ng >/dev/null 2>&1"
		if _, err := h.vm.RunCommand(body.InstanceName, checkCmd); err != nil {
			serverError(w, fmt.Sprintf("stress-ng no está instalado o no está en PATH en %s: %v", body.InstanceName, err))
			return
		}
		cmd := fmt.Sprintf("setsid sh -c 'exec stress-ng --cpu 0 --cpu-load %d --timeout %ds >/dev/null 2>&1 < /dev/null' >/dev/null 2>&1 < /dev/null &", cpuLoad, duration)
		if _, err := h.vm.RunCommand(body.InstanceName, cmd); err != nil {
			serverError(w, fmt.Sprintf("stress-ng SSH: %v", err))
			return
		}
		h.scaler.AddEvent(autoscaler.EventInfo,
			fmt.Sprintf("stress-ng iniciado en %s: load=%d%% por %ds", body.InstanceName, cpuLoad, duration))
	} else {
		// Simulated injection (no SSH needed – great for demos)
		name := body.InstanceName
		if name == "" {
			name = "simulated-vm"
		}
		duration := body.Duration
		if duration < 1 {
			duration = 60
		}
		h.mon.StartSimulatedLoad(name, body.CPUPercent, time.Duration(duration)*time.Second)
		go func() {
			time.Sleep(time.Duration(duration) * time.Second)
			h.scaler.AddEvent(autoscaler.EventInfo,
				fmt.Sprintf("CPU simulada finalizada para '%s'", name))
		}()
		h.scaler.AddEvent(autoscaler.EventInfo,
			fmt.Sprintf("CPU simulada %.0f%% inyectada en '%s' (%ds)", body.CPUPercent, name, duration))
	}
	respond(w, http.StatusOK, map[string]string{"status": "simulación iniciada"})
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

	if body.UseSSH {
		if body.InstanceName == "" {
			badRequest(w, "instance_name es requerido para cancelar stress-ng vía SSH")
			return
		}
		cmd := "pkill -f stress-ng || true"
		if _, err := h.vm.RunCommand(body.InstanceName, cmd); err != nil {
			serverError(w, fmt.Sprintf("cancelar stress-ng SSH: %v", err))
			return
		}
		h.scaler.AddEvent(autoscaler.EventInfo,
			fmt.Sprintf("stress-ng cancelado en %s", body.InstanceName))
		respond(w, http.StatusOK, map[string]string{"status": "stress-ng cancelado"})
		return
	}

	name := body.InstanceName
	if name == "" {
		name = "simulated-vm"
	}
	h.mon.RemoveInstanceData(name)
	h.scaler.AddEvent(autoscaler.EventInfo,
		fmt.Sprintf("CPU simulada cancelada para '%s'", name))
	respond(w, http.StatusOK, map[string]string{"status": "simulación cancelada"})
}

func (h *handlers) simulateWrk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Threads     int    `json:"threads"`
		Connections int    `json:"connections"`
		Duration    int    `json:"duration"`
		Path        string `json:"path"`
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
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.ContainsAny(path, " '\"`;$&|<>") {
		badRequest(w, "path contiene caracteres no permitidos")
		return
	}

	if _, err := h.hap.RunCommand("command -v wrk >/dev/null 2>&1"); err != nil {
		serverError(w, fmt.Sprintf("wrk no está instalado en la VM de HAProxy: %v", err))
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

func (h *handlers) cancelWrk(w http.ResponseWriter, r *http.Request) {
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
