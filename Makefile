# Makefile – Elastic LB Manager
# Funciona en Windows con GNU Make (mingw/git bash) o en Linux/macOS

APP := elasticity-manager
SRC := ./cmd/main.go

.PHONY: all build build-win build-linux run tidy clean

all: build

## Compilar para el SO actual
build: tidy
	@mkdir -p bin
	go build -ldflags="-s -w" -o bin/$(APP) $(SRC)
	@echo "✓ bin/$(APP)"

## Compilar explícitamente para Windows (desde cualquier OS)
build-win: tidy
	@mkdir -p bin
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(APP).exe $(SRC)
	@echo "✓ bin/$(APP).exe"

## Compilar para Linux (para las VMs)
build-linux: tidy
	@mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(APP)-linux $(SRC)
	@echo "✓ bin/$(APP)-linux"

## Ejecutar en modo desarrollo
run: build
	./bin/$(APP)

## Descargar dependencias
tidy:
	go mod tidy

## Limpiar binarios
clean:
	rm -rf bin/

## Mostrar arquitectura detectada de VBoxManage
check-vbox:
	VBoxManage --version || VBoxManage.exe --version
