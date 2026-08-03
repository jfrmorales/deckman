#!/usr/bin/env bash
# Empaqueta deckman como Flatpak y lo instala para el usuario actual.
#
# No instala nada en el sistema: el binario lo compila ../build.sh en un
# contenedor, y el empaquetado lo hace org.flatpak.Builder, que es a su vez un
# Flatpak. Requisitos: podman (o docker) y flatpak con el remoto flathub.
set -euo pipefail
cd "$(dirname "$0")"

APPID="io.github.jfrmorales.deckman"
METAINFO="$APPID.metainfo.xml"

# Que version se va a empaquetar, y con cual se compara.
#
# El binario lleva la que salga de `git describe` (lo hace ../build.sh) y el
# Flatpak muestra la del bloque <releases> del metainfo. Si no coinciden es que
# se publico un tag sin pasar por scripts/release.sh, y acabas sin saber que
# tienes instalado: por eso se avisa aqui, antes de construir.
TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
META_VER="$(grep -m1 -oP '<release version="\K[^"]+' "$METAINFO" || true)"
INSTALADA="$(flatpak info --user "$APPID" 2>/dev/null | grep -oP '^\s+Version:\s+\K.*' || true)"

if [ -n "$TAG" ] && [ -n "$META_VER" ] && [ "${TAG#v}" != "$META_VER" ]; then
	echo "aviso: el ultimo tag es $TAG pero el metainfo dice $META_VER." >&2
	echo "       Publica con scripts/release.sh para que no se separen." >&2
fi

if ! flatpak info --user org.flatpak.Builder >/dev/null 2>&1 \
   && ! flatpak info org.flatpak.Builder >/dev/null 2>&1; then
	echo ">> instalando org.flatpak.Builder (una sola vez)"
	flatpak install --user -y flathub org.flatpak.Builder
fi

echo ">> compilando el binario"
# Solo Linux: el Flatpak no empaqueta el .exe y el cruce a Windows se pagaba
# entero (stdlib incluida) en cada `make flatpak` y `make release` para nada.
DECKMAN_OBJETIVOS=linux ../build.sh

echo ">> construyendo e instalando el Flatpak (usuario)"
# --install-deps-from: en una maquina limpia el runtime de Freedesktop no esta
# y flatpak-builder abortaba a mitad con "runtime not installed". Instalado ya,
# es un no-op.
flatpak run org.flatpak.Builder \
	--user --install --force-clean \
	--install-deps-from=flathub \
	--state-dir=.flatpak-builder \
	build-dir "$APPID.yml"

AHORA="$(flatpak info --user "$APPID" 2>/dev/null | grep -oP '^\s+Version:\s+\K.*' || true)"

# Bundle de un solo fichero para publicarlo en las releases, con
# DECKMAN_BUNDLE=<ruta>. Lo usa scripts/release.sh; en el uso normal no se
# genera porque solo hace falta al publicar.
#
# Se exporta el build-dir que acaba de quedar, en vez de volver a construir:
# es el mismo arbol que se ha instalado, asi que el fichero que se publica y lo
# que tiene el que publica son exactamente lo mismo.
#
# El bundle no va firmado. Al instalarlo, flatpak avisa de que viene de un
# origen sin firmar, que es lo esperable en un binario descargado a mano; para
# comprobarlo esta el SHA256SUMS de la release.
if [ -n "${DECKMAN_BUNDLE:-}" ]; then
	echo ">> empaquetando el bundle"
	repo="$(mktemp -d)"
	trap 'rm -rf "$repo"' EXIT
	flatpak build-export "$repo" build-dir master >/dev/null
	mkdir -p "$(dirname "$DECKMAN_BUNDLE")"
	flatpak build-bundle "$repo" "$DECKMAN_BUNDLE" "$APPID" master
	echo "   $DECKMAN_BUNDLE ($(du -h "$DECKMAN_BUNDLE" | cut -f1))"
fi

echo
echo "Listo. Arrancar con:  flatpak run $APPID"
echo "O desde el menú de aplicaciones: deckman"
echo
# Decir siempre que ha quedado instalado: la duda de "¿tengo el Flatpak al
# dia?" no se resuelve mirando fechas de ficheros.
echo "Instalado:  ${AHORA:-sin version en el metainfo}"
if [ -n "$INSTALADA" ] && [ "$INSTALADA" != "$AHORA" ]; then
	echo "Antes era:  $INSTALADA"
fi
echo "Binario:    $(git describe --tags --always --dirty 2>/dev/null || echo dev)"
echo
echo "Comprobar:  flatpak run $APPID --version"
