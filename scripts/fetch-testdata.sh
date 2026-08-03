#!/usr/bin/env bash
# Trae de una Steam Deck los ficheros de configuracion reales que usan las
# pruebas. No se versionan: llevan identificadores de la cuenta de Steam.
set -euo pipefail
HOST="${1:?uso: $0 <ip-de-la-deck> [usuario]}"
USER="${2:-deck}"
cd "$(dirname "$0")/.."
mkdir -p testdata
# Sin restos de una bajada anterior: el manifiesto llega con el nombre del
# primer juego de ESA Deck y luego se renombra; uno viejo confundiria el ls.
rm -f testdata/appmanifest_*.acf

# Todo en UNA sesion SSH que emite un tar, por dos motivos:
#
#   - antes eran cuatro conexiones (cuatro handshakes) para cuatro ficheros;
#   - el shortcuts.vdf se sacaba con `cat userdata/*/config/shortcuts.vdf`,
#     y con mas de una cuenta de Steam en la Deck eso CONCATENA varios VDF
#     binarios en uno corrupto, sin decir nada. La cuenta se elige ahora de
#     forma explicita y con mas de una se falla, que es lo honesto.
ssh "$USER@$HOST" '
	set -eu
	R="$HOME/.local/share/Steam"
	cuenta=""
	for d in "$R"/userdata/*/; do
		id="$(basename "$d")"
		[ "$id" = "0" ] && continue   # la carpeta 0 es de Steam, no una cuenta
		[ -f "$d/config/localconfig.vdf" ] || continue
		if [ -n "$cuenta" ]; then
			echo "error: hay varias cuentas en userdata/ ($cuenta y $id); elige a mano" >&2
			exit 1
		fi
		cuenta="$id"
	done
	[ -n "$cuenta" ] || { echo "error: ninguna cuenta de Steam en userdata/" >&2; exit 1; }
	acf="$(ls "$R"/steamapps/appmanifest_*.acf | head -1)"
	tar -cf - \
		-C "$R/userdata/$cuenta/config" shortcuts.vdf \
		-C "$R/config" config.vdf \
		-C "$R/steamapps" libraryfolders.vdf "$(basename "$acf")"
' | tar -xf - -C testdata

# Las pruebas esperan el manifiesto con este nombre fijo, sea cual sea el
# primer juego instalado en esa Deck.
acf_local="$(ls testdata/appmanifest_*.acf | head -1)"
if [ "$acf_local" != "testdata/appmanifest_252950.acf" ]; then
	mv "$acf_local" testdata/appmanifest_252950.acf
fi
ls -l testdata/
