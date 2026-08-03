#!/usr/bin/env bash
# Compila deckman para Linux y Windows dentro de un contenedor.
# No requiere Go instalado en el sistema: solo podman (o docker).
set -euo pipefail

cd "$(dirname "$0")"
SRC="$PWD"
GO_IMAGE="docker.io/library/golang:1.26.5"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"

# Dentro de distrobox, podman vive en el host.
if command -v podman >/dev/null 2>&1; then
	RUNTIME="podman"
elif command -v docker >/dev/null 2>&1; then
	RUNTIME="docker"
elif command -v distrobox-host-exec >/dev/null 2>&1; then
	RUNTIME="distrobox-host-exec podman"
else
	echo "error: hace falta podman o docker para compilar" >&2
	exit 1
fi

# Cache de modulos y de build persistente, para que recompilar sea rapido.
$RUNTIME volume create deckman-gomod >/dev/null 2>&1 || true
$RUNTIME volume create deckman-gocache >/dev/null 2>&1 || true

run_in_container() {
	$RUNTIME run --rm \
		-v "$SRC:/src:Z" \
		-v deckman-gomod:/go/pkg/mod \
		-v deckman-gocache:/root/.cache/go-build \
		-w /src \
		-e CGO_ENABLED=0 \
		-e GOFLAGS=-buildvcs=false \
		"$GO_IMAGE" "$@"
}

echo ">> resolviendo dependencias"
run_in_container go mod tidy

LDFLAGS="-s -w -X main.version=$VERSION"

echo ">> compilando linux/amd64"
run_in_container env GOOS=linux GOARCH=amd64 \
	go build -trimpath -ldflags "$LDFLAGS" -o dist/deckman ./cmd/deckman

# -H windowsgui: sin ventana de consola al hacer doble clic. La contrapartida
# es que los flags tipo -version no imprimen nada ni desde cmd.exe; la vida
# del proceso la gobiernan la ventana y el boton Salir de la interfaz.
echo ">> compilando windows/amd64"
run_in_container env GOOS=windows GOARCH=amd64 \
	go build -trimpath -ldflags "$LDFLAGS -H windowsgui" -o dist/deckman.exe ./cmd/deckman

echo
echo "Listo:"
ls -lh dist/deckman dist/deckman.exe
