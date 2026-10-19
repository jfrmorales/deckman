#!/usr/bin/env bash
# La receta de compilacion, en un unico sitio:
#
#   scripts/compilar.sh <objetivo> <salida> <version>
#
# El objetivo es <sistema>[-<arquitectura>]: linux, linux-arm64, windows,
# windows-arm64, darwin-amd64, darwin-arm64... Sin arquitectura es amd64, que
# es lo que significaban "linux" y "windows" antes de que hubiera mas.
#
# La usan build.sh (dentro del contenedor), publicar-release.sh (en el CI) y
# pruebas.yml (para validar el cruce a Windows). Antes estaba copiada en los
# tres y el CI compilaba SIN los flags reales de release: un fallo de enlazado
# por -H windowsgui no lo habria visto nadie hasta publicar.
set -euo pipefail

OBJETIVO="${1:?uso: $0 <sistema>[-<arquitectura>] <salida> <version>}"
SALIDA="${2:?falta la ruta de salida}"
VERSION="${3:?falta la version}"

GOOS="${OBJETIVO%%-*}"
GOARCH=amd64
[ "$OBJETIVO" != "$GOOS" ] && GOARCH="${OBJETIVO#*-}"
case "$GOOS/$GOARCH" in
	linux/amd64 | linux/arm64 | windows/amd64 | windows/arm64 | darwin/amd64 | darwin/arm64) ;;
	*) echo "error: objetivo no soportado '$OBJETIVO'" >&2; exit 1 ;;
esac

LDFLAGS="-s -w -X main.version=$VERSION"
# -H windowsgui: sin ventana de consola al hacer doble clic. La contrapartida
# es que los flags tipo -version no imprimen nada ni desde cmd.exe; la vida
# del proceso la gobiernan la ventana y el boton Salir de la interfaz.
[ "$GOOS" = "windows" ] && LDFLAGS="$LDFLAGS -H windowsgui"

# go build no crea directorios intermedios: sin esto, compilar tras
# `make clean` fallaba con "no such file or directory".
case "$SALIDA" in
	/dev/*) ;;
	*) mkdir -p "$(dirname "$SALIDA")" ;;
esac

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
	go build -trimpath -ldflags "$LDFLAGS" -o "$SALIDA" ./cmd/deckman
