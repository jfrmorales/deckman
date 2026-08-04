#!/usr/bin/env bash
# Pasa el analisis estatico y busca vulnerabilidades conocidas, en contenedor.
# No requiere Go instalado en el sistema: solo podman (o docker).
#
# Va aparte de test.sh y NO entra en `make check` a proposito. Las pruebas
# dicen si el codigo hace lo suyo y tienen que dar el mismo resultado hoy que
# dentro de un año; la auditoria compara contra una base de datos que cambia
# sola, asi que puede ponerse en rojo sin que nadie haya tocado nada. Eso esta
# bien para enterarse, pero seria pesimo como puerta de publicar: dejaria
# `make release` a merced de que hoy aparezca un aviso sin arreglo publicado
# todavia. El CI la corre en cada push, que es donde uno quiere enterarse.
set -euo pipefail
cd "$(dirname "$0")"

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
preparar_volumenes

$RUNTIME run --rm \
	-v "$PWD:/src:Z" \
	-v deckman-gomod:/go/pkg/mod \
	-v deckman-gocache:/root/.cache/go-build \
	-v deckman-gobin:/go/bin \
	-w /src -e GOFLAGS=-buildvcs=false \
	"$GO_IMAGE" scripts/auditar.sh
