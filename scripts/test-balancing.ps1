param(
    [string]$ApiBase = "http://127.0.0.1:8080",
    [string]$SshUser = "user",
    [string]$SshKey = "",
    [string]$HaproxyHost = "127.0.0.1",
    [int]$HaproxySshPort = 2200,
    [int]$Requests = 30,
    [int]$MaxVMs = 2,
    [int]$Concurrency = 10,
    [int]$DurationSeconds = 0
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
        "$env:USERPROFILE\\.ssh\\id_ed25519_haproxy",
        "$env:USERPROFILE\\.ssh\\id_ed25519",
        "$env:USERPROFILE\\.ssh\\id_ecdsa",
        "$env:USERPROFILE\\.ssh\\id_rsa"
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

    $sshArgs = @(
        "-i", $Key,
        "-o", "LogLevel=ERROR",
        "-o", "StrictHostKeyChecking=no",
        "-o", "UserKnownHostsFile=NUL",
        "-o", "ConnectTimeout=6",
        "-p", "$Port",
        "$User@$TargetHost",
        $Command
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

Write-Host "[3/4] Levantando servidor demo en cada VM (puerto 8000, /health)..." -ForegroundColor Cyan

$readyVms = @()
foreach ($vm in $runningAppVms) {
    Write-Host "  -> $($vm.name) (ssh 127.0.0.1:$($vm.ssh_port))"
    try {
        $vmTag = $vm.name
        $serverBootstrap = "mkdir -p /tmp/em_demo_www; echo served_by=$vmTag > /tmp/em_demo_www/index.html; echo ok > /tmp/em_demo_www/health; nohup python3 -m http.server 8000 --bind 0.0.0.0 --directory /tmp/em_demo_www >/tmp/em_demo_server.log 2>&1 < /dev/null & disown || true"
        Invoke-Ssh -TargetHost "127.0.0.1" -Port ([int]$vm.ssh_port) -User $SshUser -Key $resolvedKey -Command $serverBootstrap | Out-Null
        $readyVms += $vm
    } catch {
        Write-Warning "No se pudo preparar $($vm.name): $($_.Exception.Message)"
    }
}

if ($readyVms.Count -lt 1) {
    throw "Se requiere al menos 1 app-vm accesible por SSH para probar carga. Preparadas: $($readyVms.Count)."
}

Write-Host "[4/4] Ejecutando requests via HAProxy para verificar distribución..." -ForegroundColor Cyan
$workerCount = [Math]::Max(1, $Concurrency)
$balanceProbe = if ($DurationSeconds -gt 0) {
    'if command -v curl >/dev/null 2>&1; then CLIENT=''curl -s --max-time 3 http://127.0.0.1/''; elif command -v wget >/dev/null 2>&1; then CLIENT=''wget -qO- --timeout=3 http://127.0.0.1/''; else echo ERROR_NO_HTTP_CLIENT; exit 2; fi; tmp=$(mktemp); end=$(( $(date +%s) + ' + [string]$DurationSeconds + ' )); w=1; while [ "$w" -le ' + [string]$workerCount + ' ]; do ( while [ "$(date +%s)" -lt "$end" ]; do sh -c "$CLIENT" >> "$tmp" || echo request_failed >> "$tmp"; done ) & w=$((w+1)); done; wait; sort "$tmp" | uniq -c; rm -f "$tmp"'
} else {
    $requestsPerWorker = [Math]::Ceiling($Requests / $workerCount)
    'if command -v curl >/dev/null 2>&1; then CLIENT=''curl -s --max-time 3 http://127.0.0.1/''; elif command -v wget >/dev/null 2>&1; then CLIENT=''wget -qO- --timeout=3 http://127.0.0.1/''; else echo ERROR_NO_HTTP_CLIENT; exit 2; fi; tmp=$(mktemp); w=1; while [ "$w" -le ' + [string]$workerCount + ' ]; do ( i=1; while [ "$i" -le ' + [string]$requestsPerWorker + ' ]; do sh -c "$CLIENT" >> "$tmp" || echo request_failed >> "$tmp"; i=$((i+1)); done ) & w=$((w+1)); done; wait; sort "$tmp" | uniq -c; rm -f "$tmp"'
}
Invoke-Ssh -TargetHost $HaproxyHost -Port $HaproxySshPort -User $SshUser -Key $resolvedKey -Command $balanceProbe

Write-Host "\nPrueba completada." -ForegroundColor Green
Write-Host "Si ves múltiples valores de 'served_by=...', el balanceo está funcionando." -ForegroundColor Green
