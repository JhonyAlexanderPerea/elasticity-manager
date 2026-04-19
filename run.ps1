# run.ps1 – Inicia Elastic LB Manager en Windows
# Uso: .\run.ps1  [o con variables de entorno personalizadas]

param(
    [string]$SshKey      = "",
    [string]$SshUser     = "",
    [string]$HaproxyHost = "127.0.0.1",
    [int]$HaproxyPort    = 2200,
    [string]$BaseVM      = "",
    [string]$BaseSnap    = ""
)

if ($SshKey) { $env:SSH_KEY = $SshKey }
if ($SshUser) { $env:SSH_USER = $SshUser }
if ($HaproxyHost) { $env:HAPROXY_HOST = $HaproxyHost }
if ($HaproxyPort) { $env:HAPROXY_SSH_PORT = $HaproxyPort }
if ($BaseVM) { $env:BASE_VM = $BaseVM }
if ($BaseSnap) { $env:BASE_SNAPSHOT = $BaseSnap }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $scriptDir "bin\elasticity-manager.exe"

if (-not (Test-Path $exe)) {
    Write-Host "Compilando primero..." -ForegroundColor Yellow
    Set-Location $scriptDir
    & go build -ldflags="-s -w" -o bin\elasticity-manager.exe .\cmd\main.go
}

Write-Host "Iniciando Elastic LB Manager..." -ForegroundColor Cyan
Write-Host "Panel web: http://localhost:8080" -ForegroundColor Green
Write-Host "Ctrl+C para detener`n" -ForegroundColor Yellow

$listener = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($listener) {
    $ownerId = $listener.OwningProcess
    $ownerProcess = Get-Process -Id $ownerId -ErrorAction SilentlyContinue
    if ($ownerProcess) {
        Write-Host "Liberando puerto 8080 (PID $ownerId - $($ownerProcess.ProcessName))..." -ForegroundColor Yellow
    } else {
        Write-Host "Liberando puerto 8080 (PID $ownerId)..." -ForegroundColor Yellow
    }
    Stop-Process -Id $ownerId -Force -ErrorAction SilentlyContinue
}

& $exe
