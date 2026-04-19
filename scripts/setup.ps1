# setup.ps1 – Prepara el entorno Windows para Elastic LB Manager
# Ejecutar como Administrador en PowerShell
#Requires -RunAsAdministrator

Write-Host @"
========================================================
  Elastic Load Balancer Manager – Setup Windows
  Universidad del Quindío – 2026-1
========================================================
"@ -ForegroundColor Cyan

# ── 1. Verificar VirtualBox ───────────────────────────────────────
Write-Host "`n[1/5] Verificando VirtualBox..." -ForegroundColor Yellow
$vbox = "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe"
if (-not (Test-Path $vbox)) {
    Write-Host "  ERROR: VBoxManage.exe no encontrado en $vbox" -ForegroundColor Red
    Write-Host "  Instala VirtualBox desde https://www.virtualbox.org/" -ForegroundColor Red
    exit 1
}
$ver = & $vbox --version
Write-Host "  OK: VirtualBox $ver" -ForegroundColor Green

# ── 2. Verificar Go ───────────────────────────────────────────────
Write-Host "`n[2/5] Verificando Go..." -ForegroundColor Yellow
try {
    $goVer = & go version 2>$null
    Write-Host "  OK: $goVer" -ForegroundColor Green
} catch {
    Write-Host "  ERROR: Go no está instalado." -ForegroundColor Red
    Write-Host "  Descarga desde https://go.dev/dl/" -ForegroundColor Yellow
    exit 1
}

# ── 3. Generar llave SSH ──────────────────────────────────────────
Write-Host "`n[3/5] Configurando llave SSH..." -ForegroundColor Yellow
$sshDir  = "$env:USERPROFILE\.ssh"
$keyPath = "$sshDir\id_ed25519_haproxy"
if (-not (Test-Path $sshDir)) { New-Item -ItemType Directory -Path $sshDir | Out-Null }
if (-not (Test-Path $keyPath)) {
  ssh-keygen -t ed25519 -N '""' -f $keyPath -C "elasticity-manager"
    Write-Host "  OK: Llave generada en $keyPath" -ForegroundColor Green
} else {
    Write-Host "  OK: Llave existente en $keyPath" -ForegroundColor Green
}
Write-Host "  IMPORTANTE: Copia la llave publica a todas las VMs:" -ForegroundColor Cyan
Get-Content "$keyPath.pub"

# ── 4. Compilar la aplicación ─────────────────────────────────────
Write-Host "`n[4/5] Compilando elasticity-manager.exe..." -ForegroundColor Yellow
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location (Join-Path $scriptDir "..")
& go mod tidy
& go build -ldflags="-s -w" -o bin\elasticity-manager.exe .\cmd\main.go
if ($LASTEXITCODE -ne 0) {
    Write-Host "  ERROR: Falló la compilación" -ForegroundColor Red
    exit 1
}
Write-Host "  OK: bin\elasticity-manager.exe" -ForegroundColor Green

# ── 5. Instrucciones finales ──────────────────────────────────────
Write-Host "`n[5/5] Preparación de VMs..." -ForegroundColor Yellow
$envPath = Join-Path (Split-Path -Parent $scriptDir) ".env"
@"
SSH_KEY=$keyPath
SSH_USER=debian
HAPROXY_HOST=127.0.0.1
HAPROXY_SSH_PORT=2200
BASE_VM=debian-base
BASE_SNAPSHOT=base-snapshot
"@ | Set-Content -Path $envPath -Encoding ASCII
Write-Host "  OK: .env generado en $envPath" -ForegroundColor Green
Write-Host @"

  PASOS PARA CONFIGURAR LAS VMs EN VIRTUALBOX:
  ─────────────────────────────────────────────
  1. Crea una VM Debian 13 CLI llamada 'debian-base'
     - Red: NAT
     - Configura reenvío de puertos: host 127.0.0.1:2200 → VM 22

  2. Dentro de la VM instala:
       sudo apt update && sudo apt install -y haproxy stress-ng openssh-server python3

     3. Copia tu llave pública a la VM:
       En Windows (PowerShell): type $env:USERPROFILE\.ssh\id_ed25519_haproxy.pub | ssh -p 2200 debian@127.0.0.1 "cat >> ~/.ssh/authorized_keys"

  4. Toma el snapshot base:
       VBoxManage snapshot debian-base take base-snapshot

  5. La primera VM (haproxy-vm) debe estar corriendo antes de iniciar
     la aplicación (es donde HAProxy se recargará via SSH).

  ─────────────────────────────────────────────
  VARIABLES DE ENTORNO (opcionales):
    SSH_KEY          Ruta a la llave privada     (default: auto-detect)
    SSH_USER         Usuario SSH en las VMs      (default: debian)
    HAPROXY_HOST     IP del host HAProxy         (default: 127.0.0.1)
    HAPROXY_SSH_PORT Puerto SSH de haproxy-vm    (default: 2200)
    BASE_VM          Nombre de la VM base        (default: debian-base)
    BASE_SNAPSHOT    Nombre del snapshot         (default: base-snapshot)
  ─────────────────────────────────────────────

  .env portable sugerido:
    SSH_USER=debian
    HAPROXY_HOST=127.0.0.1
    HAPROXY_SSH_PORT=2200
    BASE_VM=debian-base
    BASE_SNAPSHOT=base-snapshot

"@ -ForegroundColor White

Write-Host "========================================================" -ForegroundColor Cyan
Write-Host "  Setup completo. Para iniciar:" -ForegroundColor Green
Write-Host "    .\run.ps1" -ForegroundColor Yellow
Write-Host "  Panel web: http://localhost:8080" -ForegroundColor Cyan
Write-Host "========================================================" -ForegroundColor Cyan
