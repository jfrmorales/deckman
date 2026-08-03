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

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
preparar_volumenes

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

# -vet=all dentro de `go test`: corre el mismo analisis que un `go vet ./...`
# suelto pero compartiendo la compilacion, en vez de pagarla dos veces.
$RUNTIME run --rm \
	-v "$PWD:/src:Z" \
	-v deckman-gomod:/go/pkg/mod \
	-v deckman-gocache:/root/.cache/go-build \
	-w /src -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false "${ENVS[@]}" \
	"$GO_IMAGE" \
	sh -c 'sin="$(gofmt -l ./cmd ./internal)"
	       if [ -n "$sin" ]; then echo "sin formatear:"; echo "$sin"; exit 1; fi
	       go test -vet=all ./... -timeout 15m "$@"' -- "${@:3}"
