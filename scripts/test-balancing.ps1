param(
    [string]$ApiBase = "http://127.0.0.1:8080",
    [string]$SshUser = "user",
    [string]$SshKey = "",
    [string]$HaproxyHost = "127.0.0.1",
    [int]$HaproxySshPort = 2200,
    [int]$Requests = 30,
    [int]$MaxVMs = 2,
    [int]$Concurrency = 10,
    [int]$DurationSeconds = 0,
    [ValidateSet("stable-demo", "none")]
    [string]$AutoscalerProfile = "stable-demo",
    [int]$UpperThreshold = 65,
    [int]$LowerThreshold = 20,
    [int]$PeakThreshold = 90,
    [int]$SampleInterval = 10,
    [int]$EvaluationWindow = 120,
    [int]$MinInstances = 2,
    [int]$MaxInstances = 4
)

$ErrorActionPreference = "Stop"
if ($null -ne (Get-Variable -Name PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue)) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Resolve-SshKey {
    param([string]$ExplicitKey)

    if ($ExplicitKey -and (Test-Path $ExplicitKey)) {
        return $ExplicitKey
    }

    if ($env:SSH_KEY -and (Test-Path $env:SSH_KEY)) {
        return $env:SSH_KEY
    }

    $candidates = @(
        "$env:USERPROFILE\\.ssh\\id_rsa",
        "$env:USERPROFILE\\.ssh\\id_ed25519_haproxy",
        "$env:USERPROFILE\\.ssh\\id_ed25519",
        "$env:USERPROFILE\\.ssh\\id_ecdsa"
    )

    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) {
            return $candidate
        }
    }

    throw "No se encontró llave SSH. Usa -SshKey o define SSH_KEY."
}

function Invoke-Ssh {
    param(
        [string]$TargetHost,
        [int]$Port,
        [string]$User,
        [string]$Key,
        [string]$Command
    )

    $normalizedCommand = $Command -replace "`r`n", "`n" -replace "`r", "`n"

    $sshArgs = @(
        "-i", $Key,
        "-o", "LogLevel=ERROR",
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=NUL",
        "-o", "ConnectTimeout=6",
        "-p", "$Port",
        "$User@$TargetHost",
        $normalizedCommand
    )

    $output = & ssh @sshArgs 2>&1
    if ($LASTEXITCODE -ne 0) {
        $details = ($output | Out-String).Trim()
        if (-not $details) {
            $details = "sin detalle"
        }
        throw "SSH falló en $TargetHost`:$Port -> $details"
    }

    return $output
}

Write-Host "[1/4] Resolviendo llave SSH..." -ForegroundColor Cyan
$resolvedKey = Resolve-SshKey -ExplicitKey $SshKey
Write-Host "Usando llave: $resolvedKey"

Write-Host "[2/4] Obteniendo VMs desde API..." -ForegroundColor Cyan
$vms = Invoke-RestMethod -Method GET -Uri "$ApiBase/api/vms"
$runningAppVms = @($vms | Where-Object {
    $_.name -like "app-vm-*" -and $_.status -eq "running" -and $_.ssh_port -gt 0
})

$runningAppVms = @($runningAppVms | Sort-Object name)
if ($MaxVMs -gt 0 -and $runningAppVms.Count -gt $MaxVMs) {
    $runningAppVms = @($runningAppVms | Select-Object -First $MaxVMs)
}

if ($runningAppVms.Count -lt 1) {
    throw "Se requiere al menos 1 app-vm corriendo para probar carga. Actuales: $($runningAppVms.Count)."
}

Write-Host "VMs activas: $($runningAppVms.name -join ', ')"

Write-Host "[3/4] Levantando servidor demo en cada VM (puerto 8000, /health + /heavy)..." -ForegroundColor Cyan

$readyVms = @()
foreach ($vm in $runningAppVms) {
    Write-Host "  -> $($vm.name) (ssh 127.0.0.1:$($vm.ssh_port))"
    try {
        $vmTag = $vm.name
        $serverBootstrapPayload = @'
cat > /tmp/em_demo_server.py <<'PY'
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
import hashlib
import time

VMTAG = '__VMTAG__'

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
PY

pkill -f 'python3 /tmp/em_demo_server.py' || true
pkill -f 'python3 -m http.server 8000' || true
pkill -f 'ThreadingHTTPServer' || true
if command -v fuser >/dev/null 2>&1; then
    fuser -k 8000/tcp >/dev/null 2>&1 || true
else
    pid_on_8000=$(ss -ltnp 2>/dev/null | awk 'match($0, /pid=[0-9]+/) && $0 ~ /:8000/ { print substr($0, RSTART+4, RLENGTH-4); exit }')
    if [ -n "$pid_on_8000" ]; then
        kill "$pid_on_8000" >/dev/null 2>&1 || true
    fi
fi

nohup python3 -u /tmp/em_demo_server.py >/tmp/em_demo_server.log 2>&1 < /dev/null &
sleep 1
if command -v curl >/dev/null 2>&1; then
    if ! curl -fsS --max-time 3 http://127.0.0.1:8000/health | tr -d '\r\n' | grep -qx 'ok'; then
        echo BAD_HEALTH
        curl -s --max-time 3 http://127.0.0.1:8000/health 2>/dev/null | head -c 120 || true
        echo
        tail -n 30 /tmp/em_demo_server.log 2>/dev/null || true
        exit 3
    fi
elif command -v wget >/dev/null 2>&1; then
    if ! wget -qO- --timeout=3 http://127.0.0.1:8000/health | tr -d '\r\n' | grep -qx 'ok'; then
        echo BAD_HEALTH
        wget -qO- --timeout=3 http://127.0.0.1:8000/health 2>/dev/null | head -c 120 || true
        echo
        tail -n 30 /tmp/em_demo_server.log 2>/dev/null || true
        exit 3
    fi
else
    echo ERROR_NO_HTTP_CLIENT
    exit 2
fi
'@
     $serverBootstrapPayload = $serverBootstrapPayload.Replace("__VMTAG__", $vmTag)
     $serverBootstrapPayload = $serverBootstrapPayload -replace "`r`n", "`n" -replace "`r", "`n"
     $encodedBootstrap = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($serverBootstrapPayload))
        $serverBootstrap = "echo '$encodedBootstrap' | base64 -d | bash"
        Invoke-Ssh -TargetHost "127.0.0.1" -Port ([int]$vm.ssh_port) -User $SshUser -Key $resolvedKey -Command $serverBootstrap | Out-Null
        $readyVms += $vm
    } catch {
        Write-Warning "No se pudo preparar $($vm.name): $($_.Exception.Message)"
    }
}

if ($readyVms.Count -lt 1) {
    throw "Se requiere al menos 1 app-vm accesible por SSH para probar carga. Preparadas: $($readyVms.Count)."
}

Write-Host "[4/4] Activando auto-scaler..." -ForegroundColor Cyan
try {
    if ($AutoscalerProfile -eq "stable-demo") {
        if ($MaxInstances -lt $MinInstances) {
            throw "MaxInstances ($MaxInstances) no puede ser menor que MinInstances ($MinInstances)."
        }

        $configPayload = @{
            upper_threshold   = $UpperThreshold
            lower_threshold   = $LowerThreshold
            peak_threshold    = $PeakThreshold
            sample_interval   = $SampleInterval
            evaluation_window = $EvaluationWindow
            max_instances     = $MaxInstances
            min_instances     = $MinInstances
        } | ConvertTo-Json

        Invoke-RestMethod -Method PUT -Uri "$ApiBase/api/config" -ContentType "application/json" -Body $configPayload | Out-Null
        Write-Host ("Autoscaler profile applied ({0}): upper={1}, lower={2}, peak={3}, min={4}, max={5}, window={6}s" -f $AutoscalerProfile, $UpperThreshold, $LowerThreshold, $PeakThreshold, $MinInstances, $MaxInstances, $EvaluationWindow)
    }

    $serviceStatus = Invoke-RestMethod -Method GET -Uri "$ApiBase/api/status"
    if ($serviceStatus.scaler_enabled) {
        Write-Host "Auto-scaler already enabled"
    } else {
        Invoke-RestMethod -Method POST -Uri "$ApiBase/api/autoscaler/enable" | Out-Null
        Write-Host "Auto-scaler enabled"
        Start-Sleep -Seconds 2
    }
} catch {
    Write-Warning "No se pudo verificar/activar auto-scaler: $_"
}

Write-Host "[5/5] Ejecutando requests via HAProxy para verificar distribución..." -ForegroundColor Cyan
$workerCount = [Math]::Max(1, $Concurrency)
$balanceProbe = if ($DurationSeconds -gt 0) {
        (@'
request_once() {
    if command -v curl >/dev/null 2>&1; then
        curl -s --max-time 65 http://127.0.0.1/heavy?ms=3000
        return $?
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO- --timeout=65 http://127.0.0.1/heavy?ms=3000
        return $?
    fi
    echo ERROR_NO_HTTP_CLIENT
    return 2
}
tmp=$(mktemp)
end=$(( $(date +%s) + __DURATION__ ))
w=1
while [ "$w" -le __WORKERS__ ]; do
    (
        while [ "$(date +%s)" -lt "$end" ]; do
            request_once >> "$tmp" || echo request_failed >> "$tmp"
        done
    ) &
    w=$((w+1))
done
wait
sed -n 's/^served_by=//p' "$tmp" | sed '/^$/d' | sort | uniq -c
rm -f "$tmp"
'@).Replace("__DURATION__", [string]$DurationSeconds).Replace("__WORKERS__", [string]$workerCount)
} else {
    $requestsPerWorker = [Math]::Ceiling($Requests / $workerCount)
        (@'
request_once() {
    if command -v curl >/dev/null 2>&1; then
        curl -s --max-time 65 http://127.0.0.1/heavy?ms=3000
        return $?
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO- --timeout=65 http://127.0.0.1/heavy?ms=3000
        return $?
    fi
    echo ERROR_NO_HTTP_CLIENT
    return 2
}
tmp=$(mktemp)
w=1
while [ "$w" -le __WORKERS__ ]; do
    (
        i=1
        while [ "$i" -le __REQ_PER_WORKER__ ]; do
            request_once >> "$tmp" || echo request_failed >> "$tmp"
            i=$((i+1))
        done
    ) &
    w=$((w+1))
done
wait
sed -n 's/^served_by=//p' "$tmp" | sed '/^$/d' | sort | uniq -c
rm -f "$tmp"
'@).Replace("__WORKERS__", [string]$workerCount).Replace("__REQ_PER_WORKER__", [string]$requestsPerWorker)
}
Invoke-Ssh -TargetHost $HaproxyHost -Port $HaproxySshPort -User $SshUser -Key $resolvedKey -Command $balanceProbe

Write-Host "\nPrueba completada." -ForegroundColor Green
Write-Host "If you see multiple 'served_by' values, balancing is working." -ForegroundColor Green
