#!/usr/bin/env bash
# Publica una version: scripts/release.sh 0.2.0
#
# La version vive en tres sitios y aqui es donde se sincronizan, porque a mano
# se desincronizan siempre:
#
#   - el tag de git       -> lo que acaba dentro del binario (deckman --version),
#                            que build.sh saca con `git describe --tags`
#   - CHANGELOG.md        -> lo que se lee una persona
#   - el metainfo Flatpak -> lo que muestra `flatpak list` (sin <releases> la
#                            columna sale vacia)
#
# Deja el trabajo hecho pero NO publica sin avisar: al final pide confirmacion
# antes de empujar, y comprueba que el tag ha llegado a los dos remotos.
set -euo pipefail
cd "$(dirname "$0")/.."

CHANGELOG="CHANGELOG.md"
METAINFO="flatpak/io.github.jfrmorales.deckman.metainfo.xml"

err() { echo "error: $*" >&2; exit 1; }

VERSION="${1:-}"
[ -n "$VERSION" ] || err "uso: $0 <X.Y.Z>   (p. ej. $0 0.2.0)"
VERSION="${VERSION#v}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
	|| err "la version tiene que ser X.Y.Z, no '$VERSION'"

TAG="v$VERSION"
FECHA="$(date +%F)"

# --- comprobaciones antes de tocar nada -------------------------------------

RAMA="$(git rev-parse --abbrev-ref HEAD)"
[ "$RAMA" = "main" ] || err "estas en la rama '$RAMA'; las versiones se publican desde main"

[ -z "$(git status --porcelain)" ] \
	|| err "hay cambios sin commitear. Commitealos o guardalos antes de publicar"

git rev-parse -q --verify "refs/tags/$TAG" >/dev/null \
	&& err "el tag $TAG ya existe"

grep -q '^## \[No publicado\]' "$CHANGELOG" \
	|| err "no encuentro la seccion '## [No publicado]' en $CHANGELOG"

# Version anterior: la primera cabecera de version del changelog. Sirve para el
# enlace de comparacion del final del fichero.
ANTERIOR="$(grep -m1 -oP '^## \[\K[0-9]+\.[0-9]+\.[0-9]+' "$CHANGELOG" || true)"

# ¿Hay algo que publicar? Lo que haya bajo "No publicado" sin contar el
# comentario de plantilla ni las lineas en blanco.
PENDIENTE="$(awk '
	/^## \[No publicado\]/ { dentro=1; next }
	dentro && /^## \[/     { exit }
	dentro && /<!--/       { com=1 }
	dentro && !com && NF   { print }
	dentro && /-->/        { com=0 }
' "$CHANGELOG")"

if [ -z "$PENDIENTE" ]; then
	err "no hay nada anotado bajo '## [No publicado]' en $CHANGELOG.
Escribe primero que cambia en esta version: para eso esta el registro."
fi

echo ">> publicando $TAG ($FECHA)"
echo "$PENDIENTE" | sed 's/^/   /'
echo

# --- CHANGELOG --------------------------------------------------------------
# Mueve lo pendiente a una seccion nueva y deja "No publicado" vacia, con su
# comentario de plantilla intacto.

awk -v ver="$VERSION" -v fecha="$FECHA" '
	function volcar() {
		print "## [" ver "] — " fecha
		print ""
		for (i = 1; i <= n; i++) print cuerpo[i]
		volcado = 1
	}
	/^## \[No publicado\]/ && !dentro { print; print ""; dentro = 1; next }
	dentro && /^## \[/ { volcar(); dentro = 0; print; next }
	dentro {
		if ($0 ~ /<!--/) com = 1
		if (com) { print }                    # el comentario se queda arriba
		else if (NF || n) { cuerpo[++n] = $0 } # lo demas baja a la version nueva
		if ($0 ~ /-->/) { com = 0; print "" }
		next
	}
	{ print }
	END { if (dentro) volcar() }
' "$CHANGELOG" > "$CHANGELOG.tmp"

# Enlaces del final: "No publicado" pasa a comparar contra el tag nuevo, y se
# anade la linea de esta version.
REPO="https://github.com/jfrmorales/deckman"
if [ -n "$ANTERIOR" ]; then
	NUEVO_ENLACE="[$VERSION]: $REPO/compare/v$ANTERIOR...$TAG"
else
	NUEVO_ENLACE="[$VERSION]: $REPO/releases/tag/$TAG"
fi
awk -v nuevo="$NUEVO_ENLACE" -v repo="$REPO" -v tag="$TAG" '
	/^\[No publicado\]:/ {
		print "[No publicado]: " repo "/compare/" tag "...HEAD"
		print nuevo
		next
	}
	{ print }
' "$CHANGELOG.tmp" > "$CHANGELOG"
rm -f "$CHANGELOG.tmp"

# --- metainfo ---------------------------------------------------------------
# La entrada nueva va justo debajo de <releases> para que la mas reciente quede
# la primera, que es como AppStream espera encontrarlas.

awk -v ver="$VERSION" -v fecha="$FECHA" -v repo="$REPO" '
	{ print }
	/^  <releases>/ && !hecho {
		print "    <release version=\"" ver "\" date=\"" fecha "\">"
		print "      <description>"
		print "        <p>Ver el registro de cambios.</p>"
		print "      </description>"
		print "      <url type=\"details\">" repo "/blob/main/CHANGELOG.md</url>"
		print "    </release>"
		hecho = 1
	}
' "$METAINFO" > "$METAINFO.tmp" && mv "$METAINFO.tmp" "$METAINFO"

grep -q "version=\"$VERSION\"" "$METAINFO" \
	|| err "no se pudo insertar la version en $METAINFO; revisalo a mano"

# --- commit y tag -----------------------------------------------------------

git add "$CHANGELOG" "$METAINFO"
git commit -q -m "Version $VERSION"
git tag -a "$TAG" -m "deckman $VERSION"
echo ">> commit y tag $TAG creados en local"

# --- publicar en los dos remotos --------------------------------------------

REMOTOS=()
for r in $(git remote); do REMOTOS+=("$r"); done
[ ${#REMOTOS[@]} -gt 0 ] || err "no hay remotos configurados"

echo
echo "Se va a empujar main y $TAG a: ${REMOTOS[*]}"
read -r -p "¿Seguimos? [s/N] " ok
if [[ ! "$ok" =~ ^[sSyY]$ ]]; then
	echo "Cancelado. El commit y el tag siguen en local:"
	echo "  git tag -d $TAG && git reset --hard HEAD~1   # para deshacer"
	exit 1
fi

for r in "${REMOTOS[@]}"; do
	echo ">> empujando a $r"
	git push "$r" main
	git push "$r" "$TAG"
done

# Comprobar de verdad que el tag esta en los dos, no fiarse del codigo de
# salida: con varias pushurl en un mismo remoto, un fallo parcial pasa
# desapercibido.
echo
FALLO=0
for r in "${REMOTOS[@]}"; do
	for url in $(git remote get-url --push --all "$r"); do
		if git ls-remote --tags "$url" "refs/tags/$TAG" 2>/dev/null | grep -q "$TAG"; then
			echo "   ok   $TAG en $url"
		else
			echo "   FALTA $TAG en $url" >&2
			FALLO=1
		fi
	done
done
[ "$FALLO" -eq 0 ] || err "algun remoto se ha quedado atras; empuja a mano antes de seguir"

echo
echo "Publicada $TAG. Para que el Flatpak instalado lleve esta version:"
echo "  flatpak/build.sh"
