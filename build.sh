#!/usr/bin/env bash
# Compila deckman para Linux y Windows dentro de un contenedor.
# No requiere Go instalado en el sistema: solo podman (o docker).
#
# DECKMAN_OBJETIVOS elige que se compila: "linux windows" por defecto, o
# cualquier lista de objetivos de scripts/compilar.sh (linux-arm64,
# darwin-arm64...), o "todos". Lo usa flatpak/build.sh, que solo necesita el
# binario de Linux y pagaba el cruce a Windows para nada en cada `make flatpak`
# y `make release`; y `make build-all`, que saca todo lo que se publica.
set -euo pipefail
cd "$(dirname "$0")"
SRC="$PWD"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OBJETIVOS="${DECKMAN_OBJETIVOS:-linux windows}"
# La misma lista que publica scripts/publicar-release.sh.
[ "$OBJETIVOS" = "todos" ] &&
	OBJETIVOS="linux linux-arm64 windows windows-arm64 darwin-amd64 darwin-arm64"

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
preparar_volumenes

# Donde deja cada objetivo su binario. linux y windows conservan los nombres
# de siempre (los usan flatpak/build.sh y quien ya tuviera un acceso directo);
# el resto lleva el objetivo en el nombre para que no se pisen.
salida_de() {
	case "$1" in
		linux) echo dist/deckman ;;
		windows) echo dist/deckman.exe ;;
		windows-*) echo "dist/deckman-$1.exe" ;;
		linux-* | darwin-*) echo "dist/deckman-$1" ;;
		*) return 1 ;;
	esac
}

SALIDAS=()
PARES=""
for obj in $OBJETIVOS; do
	s="$(salida_de "$obj")" ||
		{ echo "error: objetivo desconocido '$obj' (vale: linux, windows, linux-arm64, windows-arm64, darwin-amd64, darwin-arm64 o todos)" >&2; exit 1; }
	SALIDAS+=("$s")
	PARES="$PARES $obj:$s"
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
	-e PARES="$PARES" \
	"$GO_IMAGE" sh -c '
		pids=""
		for par in $PARES; do
			scripts/compilar.sh "${par%%:*}" "${par#*:}" "$VERSION" & pids="$pids $!"
		done
		rc=0
		for p in $pids; do wait "$p" || rc=1; done
		exit "$rc"
	'

echo
echo "Listo:"
ls -lh "${SALIDAS[@]}"
