param(
    [string]$ApiBase = "http://127.0.0.1:8080",
    [string]$SshUser = "user",
    [string]$SshKey = ""
)

$ErrorActionPreference = "Stop"
if ($null -ne (Get-Variable -Name PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue)) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Resolve-SshKey {
    param([string]$ExplicitKey)

    if ($ExplicitKey -and (Test-Path $ExplicitKey)) { return $ExplicitKey }
    if ($env:SSH_KEY -and (Test-Path $env:SSH_KEY)) { return $env:SSH_KEY }

    $candidates = @(
        "$env:USERPROFILE\\.ssh\\id_rsa",
        "$env:USERPROFILE\\.ssh\\id_ed25519",
        "$env:USERPROFILE\\.ssh\\id_ecdsa"
    )
    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) { return $candidate }
    }
    throw "No se encontró llave SSH. Usa -SshKey o define SSH_KEY."
}

function Invoke-Ssh {
    param(
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
        "$User@127.0.0.1",
        $Command
    )

    $output = & ssh @sshArgs 2>&1
    if ($LASTEXITCODE -ne 0) {
        $details = ($output | Out-String).Trim()
        if (-not $details) { $details = "sin detalle" }
        throw "SSH falló en 127.0.0.1:$Port -> $details"
    }

    return $output
}

function Get-TargetsFromApi {
    try {
        $vms = Invoke-RestMethod -Method GET -Uri "$ApiBase/api/vms"
        return @($vms | Where-Object { $_.name -like "app-vm-*" -and $_.status -eq "running" -and $_.ssh_port -gt 0 } | Sort-Object name | ForEach-Object {
            [pscustomobject]@{ name = $_.name; ssh_port = [int]$_.ssh_port }
        })
    } catch {
        return @()
    }
}

function Get-TargetsFromVBox {
    $targets = @()
    $running = @(VBoxManage list runningvms 2>$null)
    foreach ($line in $running) {
        if ($line -notmatch '"(?<name>.+?)"') { continue }
        $name = $Matches['name']
        if ($name -notlike 'app-vm-*') { continue }
        $info = VBoxManage showvminfo $name --machinereadable 2>$null
        $sshRule = $info | Where-Object { $_ -match 'Forwarding\(\d+\)="SSH-.*?,tcp,127\.0\.0\.1,(\d+),,22"' } | Select-Object -First 1
        if (-not $sshRule) { continue }
        $port = [int]([regex]::Match($sshRule, '127\.0\.0\.1,(\d+),,22').Groups[1].Value)
        if ($port -gt 0) {
            $targets += [pscustomobject]@{ name = $name; ssh_port = $port }
        }
    }
    return @($targets | Sort-Object name)
}

$key = Resolve-SshKey -ExplicitKey $SshKey
$targets = Get-TargetsFromApi
if ($targets.Count -eq 0) {
    $targets = Get-TargetsFromVBox
}
if ($targets.Count -eq 0) {
    throw "No hay app-vm-* corriendo con SSH disponible (API ni VirtualBox)."
}

$remotePayload = @'
sudo systemctl stop servidorimagenes.service || true
sudo systemctl disable servidorimagenes.service || true
if [ -f /etc/systemd/system/servidorimagenes.service ]; then
    sudo mv /etc/systemd/system/servidorimagenes.service /etc/systemd/system/servidorimagenes.service.disabled || true
    sudo systemctl daemon-reload || true
fi
sudo systemctl mask servidorimagenes.service || true
sudo pkill -f "servidorimagenes|image|uvicorn|gunicorn|flask" || true
echo ENABLED:$(systemctl is-enabled servidorimagenes.service 2>/dev/null || echo unknown)
echo ACTIVE:$(systemctl is-active servidorimagenes.service 2>/dev/null || echo unknown)
ss -ltnp 2>/dev/null | grep ':8080' || true
exit 0
'@
$encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remotePayload))
$remoteCmd = "echo '$encoded' | base64 -d | bash"

foreach ($vm in $targets) {
    Write-Host "[VM $($vm.name)] deshabilitando servicio de imagenes en 127.0.0.1:$($vm.ssh_port)..." -ForegroundColor Cyan
    try {
        $result = Invoke-Ssh -Port ([int]$vm.ssh_port) -User $SshUser -Key $key -Command $remoteCmd
        $result | ForEach-Object { Write-Host "  $_" }
        Write-Host "  OK" -ForegroundColor Green
    } catch {
        $msg = $_.Exception.Message
        if ($msg -match 'is masked, ignoring') {
            Write-Host "  Ya estaba deshabilitado/masked (OK)" -ForegroundColor Green
        } else {
            Write-Warning "  Error en $($vm.name): $msg"
        }
    }
}

Write-Host "Limpieza completada." -ForegroundColor Green
