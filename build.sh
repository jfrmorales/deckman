#!/usr/bin/env bash
# Compila deckman para Linux y Windows dentro de un contenedor.
# No requiere Go instalado en el sistema: solo podman (o docker).
#
# DECKMAN_OBJETIVOS elige que se compila ("linux", "windows" o ambos, que es
# el defecto). Lo usa flatpak/build.sh, que solo necesita el binario de Linux
# y pagaba el cruce a Windows para nada en cada `make flatpak` y `make release`.
set -euo pipefail
cd "$(dirname "$0")"
SRC="$PWD"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OBJETIVOS="${DECKMAN_OBJETIVOS:-linux windows}"

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
preparar_volumenes

SALIDAS=()
for os in $OBJETIVOS; do
	case "$os" in
		linux) SALIDAS+=(dist/deckman) ;;
		windows) SALIDAS+=(dist/deckman.exe) ;;
		*) echo "error: objetivo desconocido '$os' (vale: linux windows)" >&2; exit 1 ;;
	esac
done

echo ">> compilando: $OBJETIVOS"

# Un solo `run` para todo: arrancar el contenedor (montajes, reetiquetado
# SELinux) cuesta mas que compilar en caliente, y antes se pagaba tres veces.
# Dentro, los objetivos van en paralelo; los codigos de salida se recogen uno
# a uno porque un `wait` a secas devuelve 0 aunque un hijo haya fallado.
#
# Aqui ya no hay `go mod tidy`: exigia red aunque la cache tuviera todo y
# podia reescribir go.mod/go.sum, dejando el arbol sucio justo antes de que
# release.sh se negara a publicar por "cambios sin commitear". Lo que falte
# lo descarga `go build`; la coherencia del go.mod la vigila el CI.
$RUNTIME run --rm \
	-v "$SRC:/src:Z" \
	-v deckman-gomod:/go/pkg/mod \
	-v deckman-gocache:/root/.cache/go-build \
	-w /src \
	-e GOFLAGS=-buildvcs=false \
	-e VERSION="$VERSION" \
	-e OBJETIVOS="$OBJETIVOS" \
	"$GO_IMAGE" sh -c '
		pids=""
		for os in $OBJETIVOS; do
			case "$os" in
				linux) salida=dist/deckman ;;
				windows) salida=dist/deckman.exe ;;
			esac
			scripts/compilar.sh "$os" "$salida" "$VERSION" & pids="$pids $!"
		done
		rc=0
		for p in $pids; do wait "$p" || rc=1; done
		exit "$rc"
	'

echo
echo "Listo:"
ls -lh "${SALIDAS[@]}"
