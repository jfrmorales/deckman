#!/usr/bin/env bash
# Crea el par de claves con el que se firman las releases. Una sola vez.
#
#   scripts/crear-clave-firma.sh
#
# Deja cosign.key (privada, 0600) y cosign.pub en la carpeta de configuracion
# de deckman. A partir de ahi, scripts/release.sh firma solo.
#
# cosign no se instala en el sistema: se ejecuta en contenedor, igual que Go.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
usuario_contenedor

DESTINO="${DECKMAN_COSIGN_KEY:-$HOME/.config/deckman/cosign.key}"
DIR="$(dirname "$DESTINO")"
PREFIJO="$(basename "${DESTINO%.key}")"

if [ -f "$DESTINO" ]; then
	echo "Ya hay una clave en $DESTINO."
	echo
	echo "No la sustituyo: quien haya guardado la publica de antes se encontraria"
	echo "una verificacion que falla sin saber por que. Si de verdad quieres"
	echo "cambiarla, borrala a mano, crea la nueva y DILO EN EL CHANGELOG."
	exit 1
fi

# Contrasena vacia si no se indica otra. Es una decision, no un descuido:
#
# La alternativa es tener que teclearla (o exportarla) en cada `make release`,
# y lo que pasa entonces es que acaba escrita en un script, que es exactamente
# donde no queria estar. La clave queda en 0600 en la carpeta de configuracion,
# al lado de la clave SSH de deckman, que ya vive asi por el mismo motivo:
# quien tenga tu usuario en este PC tiene las dos, y una contrasena guardada al
# lado no cambia eso.
#
# Si prefieres ponerle una, exporta COSIGN_PASSWORD antes de llamar a este
# script y acuerdate de exportarla tambien al publicar.
mkdir -p "$DIR"
chmod 700 "$DIR"

TMP="$(mktemp -d)"
chmod 700 "$TMP"
trap 'rm -rf "$TMP"' EXIT

echo ">> generando el par de claves"
$RUNTIME run --rm "${USUARIO_OPTS[@]}" -v "$TMP:/trabajo:Z" -w /trabajo \
	-e COSIGN_PASSWORD="${COSIGN_PASSWORD:-}" \
	"$COSIGN_IMAGE" generate-key-pair --output-key-prefix "$PREFIJO"

# Se mueve despues de generar, no antes: si algo falla a mitad, la carpeta de
# configuracion no se queda con media clave.
install -m 600 "$TMP/$PREFIJO.key" "$DIR/$PREFIJO.key"
install -m 644 "$TMP/$PREFIJO.pub" "$DIR/$PREFIJO.pub"

echo
echo "Listo:"
echo "  privada  $DIR/$PREFIJO.key   (0600, NO se versiona ni se comparte)"
echo "  publica  $DIR/$PREFIJO.pub   (se sube con cada release)"
echo
echo "Huella de la publica, por si quieres apuntarla en algun sitio:"
sha256sum "$DIR/$PREFIJO.pub" | cut -d' ' -f1
echo
echo "La proxima  make release  ya firmara."
