#!/usr/bin/env bash
# Ejecuta las pruebas en contenedor. Con argumentos, incluye las de integracion
# contra una Steam Deck real:  ./test.sh 192.168.1.50 <contrasena>
set -euo pipefail
cd "$(dirname "$0")"

# La IP y la contrasena de la Deck de cada cual no se versionan: este repo es
# publico. Si existe deck.local.env (ver deck.local.env.ejemplo) se leen de ahi
# y basta con ./test.sh a secas; los argumentos mandan sobre el fichero.
#
# DECKMAN_SIN_DECK lo pone `make check` para quedarse solo en las pruebas
# locales. Sin eso, publicar una version exigiria tener la Deck encendida.
if [ -f deck.local.env ] && [ -z "${DECKMAN_SIN_DECK:-}" ]; then
	# shellcheck source=/dev/null
	. ./deck.local.env
fi

if command -v podman >/dev/null 2>&1; then RUNTIME="podman"
elif command -v docker >/dev/null 2>&1; then RUNTIME="docker"
elif command -v distrobox-host-exec >/dev/null 2>&1; then RUNTIME="distrobox-host-exec podman"
else echo "error: hace falta podman o docker" >&2; exit 1
fi

$RUNTIME volume create deckman-gomod >/dev/null 2>&1 || true
$RUNTIME volume create deckman-gocache >/dev/null 2>&1 || true

HOST="${1:-${DECKMAN_TEST_HOST:-}}"
PASS="${2:-${DECKMAN_TEST_PASS:-}}"

ENVS=()
if [ -n "$HOST" ]; then
	ENVS+=(-e "DECKMAN_TEST_HOST=$HOST")
	[ -n "$PASS" ] && ENVS+=(-e "DECKMAN_TEST_PASS=$PASS")
	echo ">> incluyendo pruebas de integracion contra $HOST"
fi
# La clave de SteamGridDB se toma del entorno; nunca del codigo.
if [ -n "${DECKMAN_TEST_GRIDKEY:-}" ]; then
	ENVS+=(-e "DECKMAN_TEST_GRIDKEY=$DECKMAN_TEST_GRIDKEY")
	echo ">> incluyendo pruebas contra SteamGridDB"
fi

$RUNTIME run --rm \
	-v "$PWD:/src:Z" \
	-v deckman-gomod:/go/pkg/mod \
	-v deckman-gocache:/root/.cache/go-build \
	-w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "${ENVS[@]}" \
	docker.io/library/golang:1.26.5 \
	sh -c 'test -z "$(gofmt -l ./cmd ./internal)" || { echo "sin formatear:"; gofmt -l ./cmd ./internal; exit 1; }
	       go vet ./... && go test ./... -timeout 15m "$@"' -- "${@:3}"
