# Elastic Load Balancer Manager

---

## Arquitectura (Windows + VirtualBox)

```
 ┌─────────────────────────────────────────────────────┐
 │                  WINDOWS HOST                       │
 │                                                     │
 │  ┌──────────────────────────────────────────────┐   │
 │  │      elasticity-manager.exe (Go)             │   │
 │  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐ │   │
 │  │  │ HAProxy  │ │   VM     │ │ CPU Monitor  │ │   │
 │  │  │ Manager  │ │ Manager  │ │  + Scaler    │ │   │
 │  │  └────┬─────┘ └────┬─────┘ └──────┬───────┘ │   │
 │  └───────┼────────────┼──────────────┼──────────┘   │
 │          │SSH         │VBoxManage    │SSH            │
 │          │            │.exe          │               │
 │  ┌───────▼────┐  ┌────▼──────────────▼────────────┐ │
 │  │ NAT :2200  │  │         VirtualBox              │ │
 │  └───────┬────┘  │  ┌──────────┐  ┌────────────┐  │ │
 │          │       │  │haproxy-vm│  │ app-vm-1…N │  │ │
 │          │       │  │(HAProxy) │  │ (clones)   │  │ │
 │          │       │  └────┬─────┘  └────┬───────┘  │ │
 │          └───────┼───────┘             │          │ │
 │  SSH key-based   │  NAT :2201,:2202…   │          │ │
 │                  └─────────────────────┘          │ │
 │                  └────────────────────────────────┘ │
 └─────────────────────────────────────────────────────┘
```

**Puntos clave:**
- **HAProxy NO corre en Windows** → corre dentro de `haproxy-vm` (Debian 13)
- **VBoxManage.exe** se invoca directamente desde Windows para crear/destruir VMs
- **SSH** se usa para escribir la config de HAProxy, recargar el servicio y medir CPU
- Todo el acceso SSH es via **NAT port-forwarding** (`127.0.0.1:22XX → VM:22`)

---

## Requisitos Windows

| Componente | Descarga |
|---|---|
| **Go 1.22+** | https://go.dev/dl/ |
| **VirtualBox 7.x** | https://www.virtualbox.org/ |
| **OpenSSH Client** | Incluido en Windows 10/11 (Configuración → Apps → Características) |

---

## Instalación paso a paso

### Paso 1 – Preparar el código
```powershell
# Descomprimir el ZIP o clonar
cd elasticity-manager

# Instalar dependencias Go
go mod tidy
```

### Paso 2 – Crear la VM base en VirtualBox

1. Crea una VM llamada **`debian-base`** con Debian 13 CLI
2. Tipo de red: **NAT**
3. Agrega estos reenvíos de puertos en VirtualBox (Configuración → Red → Avanzado → Reenvío de puertos):

   | Nombre | Protocolo | IP Host | Puerto Host | IP Invitado | Puerto Invitado |
   |---|---|---|---|---|---|
   | SSH-haproxy | TCP | 127.0.0.1 | **2200** | — | 22 |

4. Inicia la VM e instala los paquetes necesarios:
   ```bash
  sudo apt update && sudo apt install -y haproxy stress-ng openssh-server python3 wrk
   sudo systemctl enable haproxy ssh
   ```

5. **Copia tu llave SSH pública a la VM** (desde PowerShell Windows):
   ```powershell
   # Genera la llave si no la tienes
   ssh-keygen -t rsa -b 4096

   # Copia la llave a la VM
   type $env:USERPROFILE\.ssh\id_rsa.pub | ssh -p 2200 debian@127.0.0.1 "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
   ```

6. **Configura sudo sin contraseña para haproxy** (en la VM):
   ```bash
   echo "debian ALL=(ALL) NOPASSWD: /usr/bin/tee, /usr/bin/systemctl reload haproxy, /usr/bin/systemctl restart haproxy" | sudo tee /etc/sudoers.d/elasticity
   ```

7. Toma el snapshot base:
   ```powershell
   # En PowerShell Windows (con VirtualBox en PATH o ruta completa)
   & "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe" snapshot debian-base take base-snapshot
   ```

### Paso 3 – Ejecutar el setup automático
```powershell
# Como Administrador
.\scripts\setup.ps1
```

### Paso 4 – Iniciar la aplicación
```powershell
# Opción A: PowerShell
.\run.ps1

# Opción B: doble clic en run.bat

# Opción C: manual con variables de entorno
$env:HAPROXY_SSH_PORT=2200
go run .\cmd\main.go
```

### Paso 5 – Abrir el panel web
```
http://localhost:8080
```

---

## Variables de Entorno

| Variable | Default | Descripción |
|---|---|---|
| `SSH_KEY` | `auto (~/.ssh/id_ed25519, id_ecdsa, id_rsa)` | Ruta a la llave privada SSH |
| `SSH_USER` | `debian` | Usuario SSH en las VMs |
| `HAPROXY_HOST` | `127.0.0.1` | IP del host HAProxy (siempre 127.0.0.1 con NAT) |
| `HAPROXY_SSH_PORT` | `2200` | Puerto NAT mapeado al SSH de haproxy-vm |
| `BASE_VM` | `debian-base` | Nombre de la VM base para clonar |
| `BASE_SNAPSHOT` | `base-snapshot` | Snapshot de la VM base |

La aplicación busca `.env` tanto en el directorio actual como junto al ejecutable.
Si no defines `SSH_KEY`, intenta descubrir automáticamente llaves en `~/.ssh`
con este orden: `id_ed25519_haproxy`, `id_ed25519`, `id_ecdsa`, `id_rsa`.
El script `scripts/setup.ps1` también genera un `.env` local con valores base.

## Persistencia local

La aplicación guarda su estado en `data/state.json` dentro de la raíz detectada del proyecto.

Se persiste automáticamente:
- configuración del auto-scaler
- backends y servidores de HAProxy
- registro de instancias administradas por la app
- estado activado/pausado del auto-scaler
- historial de eventos del auto-scaler
- contador de instancias creadas por el autoscaler

Si borras ese archivo, la app vuelve a los valores por defecto en el siguiente arranque.

## Replicar en otra PC

1. Copia el proyecto completo a la nueva máquina.
2. Ejecuta `scripts/setup.ps1` para generar la llave SSH y el archivo `.env` base.
3. Crea en VirtualBox una VM base y snapshot con los nombres que hayas definido en `.env`.
4. Arranca con `run.ps1` o `run.bat`.

---

## API REST

### Configuración

```http
GET  /api/config
PUT  /api/config
```
```json
{
  "upper_threshold": 80,
  "lower_threshold": 20,
  "sample_interval": 10,
  "evaluation_window": 60,
  "max_instances": 5,
  "min_instances": 1
}
```

### Backends

```http
GET    /api/backends
POST   /api/backends          {"name":"app-backend","algorithm":"roundrobin"}
PUT    /api/backends/{name}   {"algorithm":"leastconn"}
DELETE /api/backends/{name}
```

### Servidores en un Backend

```http
GET    /api/backends/{name}/servers
POST   /api/backends/{name}/servers   {"name":"vm1","ip":"10.0.2.10","port":8000,"weight":1}
PUT    /api/backends/{name}/servers/{server}
DELETE /api/backends/{name}/servers/{server}
```

### Monitoreo y control

```http
GET  /api/status           # Estado general
GET  /api/vms              # Instancias activas
GET  /api/events           # Log del auto-scaler
GET  /api/haproxy/config   # Config HAProxy generada
POST /api/autoscaler/enable
POST /api/autoscaler/disable
```

### Simulación de carga

```http
POST /api/simulate
POST /api/simulate/cancel
POST /api/simulate/wrk
POST /api/simulate/wrk/cancel
```
```json
// Modo 1: Inyección directa (sin SSH, para demo)
{
  "instance_name": "simulated-vm",
  "cpu_percent": 85,
  "duration": 60,
  "use_ssh": false
}

// Modo 2: stress-ng real en la VM via SSH
{
  "instance_name": "app-vm-1",
  "cpu_percent": 80,
  "duration": 60,
  "use_ssh": true
}

// Modo 3: carga HTTP masiva con wrk vía la VM de HAProxy
{
  "threads": 4,
  "connections": 100,
  "duration": 30,
  "path": "/"
}
```

### Prueba de balanceo masiva

La pestaña **Simulación** del panel ahora incluye un bloque para `wrk` que ejecuta la carga desde la VM de HAProxy contra la ruta objetivo. También puedes usar el script de carga larga para mantener presión sobre el sistema por varios minutos.

```powershell
.
scripts\test-balancing.ps1 -SshUser user -HaproxySshPort 2200 -MaxVMs 2 -Concurrency 25 -DurationSeconds 600
```

Parámetros útiles:
- `-Requests`: total de requests cuando no usas modo por duración.
- `-Concurrency`: cantidad de workers concurrentes.
- `-DurationSeconds`: si es mayor que cero, mantiene la carga durante ese tiempo.
- `-MaxVMs`: limita cuántas VMs estables toma para la prueba.

---

## Lógica de Auto-Scaling

```
Cada 10 segundos el scaler evalúa:

  avg = promedio de CPU en los últimos `evaluation_window` segundos

  si (avg > upper_threshold) AND (instancias < max_instances):
    1. VBoxManage clonevm debian-base --snapshot base-snapshot --options link
    2. VBoxManage modifyvm <nueva-vm> --natpf1 "SSH-<vm>,tcp,127.0.0.1,<port>,,22"
    3. VBoxManage startvm <nueva-vm> --type headless
    4. Esperar SSH disponible (~30s)
    5. SSH → agregar servidor en haproxy.cfg → systemctl reload haproxy

  si (avg < lower_threshold) AND (instancias > min_instances):
    1. SSH → quitar servidor de haproxy.cfg → systemctl reload haproxy
    2. VBoxManage controlvm <vm> poweroff
    3. VBoxManage unregistervm <vm> --delete
```

---

## Pruebas con stress-ng

```bash
# Desde la VM via SSH (puerto 2200 en Windows)
ssh -p 2200 debian@127.0.0.1

# Generar carga alta → activar scale-out
stress-ng --cpu 4 --timeout 90s &

# Verificar CPU
watch -n 1 "grep 'cpu ' /proc/stat | awk '{usage=(\$2+\$4)*100/(\$2+\$3+\$4+\$5)} END {print usage\"%\"}'"
```

O usa la pestaña **Simulación** del panel web sin necesidad de SSH.

## Pruebas con wrk

`wrk` se usa para generar peticiones HTTP masivas contra HAProxy y verificar el balanceo real entre VMs.

Ejemplo por consola:

```bash
wrk -t4 -c100 -d30s http://IP_HAPROXY/
```

Desde el panel web, usa la tarjeta **wrk vía HAProxy SSH** e ingresa:
- `Threads (-t)`: cantidad de hilos de `wrk`
- `Conexiones (-c)`: conexiones simultáneas
- `Duración (seg)`: cuánto tiempo mantener la carga
- `Ruta objetivo`: por ejemplo `/` o `/heavy` si tu backend la expone

Si quieres cancelar la prueba en curso, usa el botón **Cancelar** de la misma tarjeta.

---

## Estructura del Proyecto

```
elasticity-manager/
├── cmd/main.go                        # Entrada, configuración Windows
├── internal/
│   ├── sshutil/client.go              # Cliente SSH reutilizable
│   ├── haproxy/manager.go             # CRUD backends, escribe config via SSH
│   ├── vm/manager.go                  # VBoxManage.exe + detección Windows
│   ├── monitor/cpu.go                 # Polling CPU via SSH (/proc/stat)
│   ├── autoscaler/scaler.go           # Lógica umbral scale-out/in
│   └── api/
│       ├── store.go                   # Config thread-safe
│       ├── router.go                  # 21 endpoints REST
│       └── dashboard.go               # SPA HTML embebida
├── scripts/
│   └── setup.ps1                      # Setup automático Windows
├── run.ps1                            # Lanzador PowerShell
├── run.bat                            # Lanzador CMD/doble clic
├── Makefile
└── go.mod
```

---

## Problemas y Mejoras

### Problemas encontrados
1. **HAProxy no disponible en Windows nativamente** → se resuelve corriendo HAProxy en la VM y gestionándolo via SSH.
2. **Tiempo de arranque de VMs** (~30-60s) puede causar que HAProxy registre servidores no listos → se implementó espera de SSH con timeout.
3. **VBoxManage path variable** en Windows → se implementó detección automática de la ruta de instalación.
4. **Escritura remota de archivos** → se usa `echo '...' | sudo tee` para escribir `/etc/haproxy/haproxy.cfg` sin transferencia de archivos (no se requiere `scp`).
5. **Pruebas de carga** → `stress-ng` estresa CPU y `wrk` estresa tráfico HTTP masivo desde la VM de HAProxy.

### Mejoras futuras
- Persistencia de estado en SQLite (sobrevivir reinicios de la app)
- Instalar como servicio Windows con `sc.exe` o NSSM
- Soporte para múltiples backends con escalado independiente
- Exportar métricas a Prometheus/Grafana
- Autenticación JWT en el panel web
- Notificaciones (email/Slack) en eventos de scale
