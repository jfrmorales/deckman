#!/usr/bin/env bash
# Analisis estatico y vulnerabilidades conocidas. Necesita `go` en el PATH:
# esta pensado para correr DENTRO del contenedor, igual que compilar.sh.
# Desde el PC se llama con ../audit.sh, que pone el contenedor; en el CI se
# llama directo, porque el trabajo ya vive en la imagen de Go.
#
# Esta aparte del workflow por lo mismo que publicar-release.sh: la logica que
# solo corre en CI es la que nadie prueba hasta que falla.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh

export GOBIN="${GOBIN:-/go/bin}"
export PATH="$GOBIN:$PATH"

# Se instalan solo si faltan: con el volumen deckman-gobin puesto, la segunda
# vez esto no cuesta nada. En el CI el volumen es el mismo que el de las
# pruebas, asi que tampoco se recompilan en cada push.
instalar_si_falta() {
	local binario="$1" paquete="$2"
	if ! command -v "$binario" >/dev/null 2>&1; then
		echo ">> instalando $binario"
		go install "$paquete"
	fi
}

fallos=0

echo ">> golangci-lint"
instalar_si_falta golangci-lint \
	"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION"
golangci-lint run ./... || fallos=1

echo
echo ">> govulncheck"
instalar_si_falta govulncheck "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION"
# ./... y no ./cmd/...: interesa lo que ALCANZA el codigo, y las trazas salen
# con fichero y linea, que es lo que dice si una vulnerabilidad va conmigo o
# solo esta en un modulo que arrastro sin llamar.
govulncheck ./... || fallos=1

echo
if [ "$fallos" -ne 0 ]; then
	echo "la auditoria ha encontrado cosas (arriba)." >&2
	exit 1
fi
echo "auditoria limpia."
