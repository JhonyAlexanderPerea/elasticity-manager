@echo off
:: run.bat – Lanzador simple para Windows (doble clic o CMD)
title Elastic LB Manager

:: Variables de entorno (si ya estan definidas, no se sobreescriben)
if not defined SSH_USER set SSH_USER=debian
if not defined HAPROXY_HOST set HAPROXY_HOST=127.0.0.1
if not defined HAPROXY_SSH_PORT set HAPROXY_SSH_PORT=2200

echo.
echo  ============================================
echo   Elastic Load Balancer Manager
echo   Universidad del Quindio - 2026-1
echo  ============================================
echo.
echo  Panel web: http://localhost:8080
echo  Presiona Ctrl+C para detener
echo.

if not exist bin\elasticity-manager.exe (
    echo Compilando...
    go build -ldflags="-s -w" -o bin\elasticity-manager.exe .\cmd\main.go
    if errorlevel 1 (
        echo ERROR: Fallo la compilacion. Verifica que Go este instalado.
        pause
        exit /b 1
    )
)

bin\elasticity-manager.exe
pause
