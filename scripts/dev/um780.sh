#!/usr/bin/env bash
# Ejecuta un comando del repositorio en el UM780, dentro del contenedor de desarrollo.
#
#   scripts/dev/um780.sh go test ./...
#   scripts/dev/um780.sh scripts/dev/comprobar.sh        # gofmt + vet + pruebas + sintaxis JS
#   scripts/dev/um780.sh --traer dist/deckman scripts/compilar.sh linux dist/deckman dev
#   scripts/dev/um780.sh --shell              # bash dentro del contenedor
#   scripts/dev/um780.sh --rebuild <cmd>      # fuerza a reconstruir la imagen
#
# Qué hace, en orden:
#   1. rsync del árbol de trabajo del portátil (con cambios sin commit) a ~/dev/<repo> del UM780.
#      Es una copia de trabajo desechable, no un clon: allí no se hace commit ni se edita.
#   2. Construye la imagen de scripts/dev/Containerfile si no existe ya con ese hash.
#   3. Ejecuta el comando en el contenedor con el repo montado en /w.
#   4. Trae de vuelta .dev-out/ (capturas, informes) al portátil, también si el comando falla, y las
#      rutas pedidas con --traer solo si el comando sale bien.
# Detalles y por qué así: scripts/dev/README.md.
set -euo pipefail

HOST=${UM780_HOST:-um780}
REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
REMOTO="dev/$(basename "$REPO")"
IMAGEN=deckman-dev
SALIDA=.dev-out

# La versión de Go no se repite aquí: sale de scripts/contenedor.sh, que es
# donde la vigila el CI. Con un tercer sitio donde escribirla, acabaría
# probándose en el UM780 con un Go distinto del que publica.
# shellcheck source=scripts/contenedor.sh
. "$REPO/scripts/contenedor.sh"

rebuild=0
shell=0
traer=()
while [[ ${1:-} == --* ]]; do
    case $1 in
        --rebuild) rebuild=1 ;;
        --shell) shell=1 ;;
        --traer) traer+=("${2:?--traer necesita una ruta del repo}"); shift ;;
        *) echo "Opción desconocida: $1" >&2; exit 2 ;;
    esac
    shift
done
if [[ $shell == 0 && $# == 0 ]]; then
    sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
fi

ssh "$HOST" "mkdir -p $REMOTO"
# deck.local.env no viaja: lleva la IP y la contraseña de la Deck, y lo que se
# prueba en el UM780 son las pruebas locales, no las de integración.
rsync -az --delete \
    --exclude dist/ --exclude testdata/ --exclude flatpak/.flatpak-builder/ --exclude flatpak/build-dir/ \
    --exclude deck.local.env --exclude "$SALIDA/" \
    "$REPO/" "$HOST:$REMOTO/"

# La etiqueta cubre el Containerfile y la imagen base: cambiar cualquiera de
# los dos reconstruye sola.
TAG=$( { cat "$REPO/scripts/dev/Containerfile"; echo "$GO_IMAGE"; } | sha256sum | cut -c1-12)
BUILD="podman build --build-arg GO_IMAGE=$GO_IMAGE -t $IMAGEN:$TAG -f $REMOTO/scripts/dev/Containerfile $REMOTO/scripts/dev"
if [[ $rebuild == 1 ]]; then
    ssh "$HOST" "${BUILD/podman build/podman build --pull}"
else
    ssh "$HOST" "podman image exists $IMAGEN:$TAG || $BUILD"
fi

if [[ $shell == 1 ]]; then
    cmd=bash
    tty=(-t)
    ti=-it
else
    cmd=$(printf '%q ' "$@")
    tty=()
    ti=
fi

# Los volúmenes son la caché de módulos y de compilación: sin ellos cada
# ejecución vuelve a bajar y compilar todas las dependencias.
estado=0
ssh "${tty[@]}" "$HOST" "cd $REMOTO && mkdir -p $SALIDA && podman run --rm $ti --init \
    -v \"\$PWD\":/w:Z -w /w \
    -v deckman-gomod:/go/pkg/mod -v deckman-gocache:/root/.cache/go-build \
    -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false -e DECKMAN_SIN_DECK=1 \
    $IMAGEN:$TAG $cmd" || estado=$?

mkdir -p "$REPO/$SALIDA"
rsync -az "$HOST:$REMOTO/$SALIDA/" "$REPO/$SALIDA/" || true
if [[ $estado == 0 ]]; then
    for ruta in "${traer[@]}"; do
        ruta=${ruta%/}
        mkdir -p "$REPO/$(dirname "$ruta")"
        rsync -az "$HOST:$REMOTO/$ruta" "$REPO/$(dirname "$ruta")/"   # vale para fichero o carpeta
        echo "↩ traído: $ruta"
    done
fi
exit $estado
