#!/bin/sh
# Lo mismo que `make check` (gofmt + vet + pruebas locales), pero pensado para
# correr YA DENTRO del contenedor de scripts/dev: test.sh levanta su propio
# contenedor y aquí no hay podman dentro de podman.
#
# Añade lo que test.sh no mira: que el JavaScript de la interfaz al menos se
# pueda leer. Un paréntesis de más en un fichero embebido no lo ve `go test`, y
# se descubre abriendo la aplicación con la pantalla en blanco.
set -eu

sin="$(gofmt -l ./cmd ./internal)"
if [ -n "$sin" ]; then
	echo "sin formatear:"
	echo "$sin"
	exit 1
fi

for f in internal/server/web/*.js; do
	node --check "$f"
done

go test -vet=all ./... -timeout 15m "$@"
