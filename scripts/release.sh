#!/usr/bin/env bash
# Publica una version de principio a fin.  Se llama con: make release V=0.2.0
#
# La version vive en tres sitios y a mano se desincronizan siempre:
#
#   - el tag de git       -> lo que acaba dentro del binario (deckman --version),
#                            que build.sh saca con `git describe --tags`
#   - CHANGELOG.md        -> lo que se lee una persona
#   - el metainfo Flatpak -> lo que muestra `flatpak list` (sin <releases> la
#                            columna sale vacia)
#
# Todo lo que pasa antes de empujar es reversible: si falla algo, se deshacen
# el commit y el tag y el repositorio queda como estaba. A partir del push ya
# no se puede deshacer sin reescribir historia publicada, asi que ahi se
# comprueba y se informa en vez de tocar nada.
set -euo pipefail
cd "$(dirname "$0")/.."

CHANGELOG="CHANGELOG.md"
METAINFO="flatpak/io.github.jfrmorales.deckman.metainfo.xml"
REPO="https://github.com/jfrmorales/deckman"

# De aqui salen $RUNTIME y $COSIGN_IMAGE, que hacen falta para firmar. Se
# detecta el runtime al principio y no al llegar a la firma: si no hay podman
# ni docker, esto ya no iba a poder construir el Flatpak tampoco, y vale mas
# saberlo antes de empezar a mover tags.
# shellcheck source=scripts/contenedor.sh
. scripts/contenedor.sh
detectar_runtime
usuario_contenedor

err() { echo "error: $*" >&2; exit 1; }
paso() { echo; echo ">> $*"; }

VERSION="${1:-}"
[ -n "$VERSION" ] || err "uso: $0 <X.Y.Z>   (o: make release V=0.2.0)"
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

ANTERIOR="$(grep -m1 -oP '^## \[\K[0-9]+\.[0-9]+\.[0-9]+' "$CHANGELOG" || true)"

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

echo "Publicando $TAG ($FECHA). Cambios:"
echo "$PENDIENTE" | sed 's/^/   /'

# --- red de seguridad -------------------------------------------------------
#
# Desde aqui se toca el repositorio. ANTES es el punto exacto al que volver.

ANTES="$(git rev-parse HEAD)"
NUEVO=""
PUBLICADO=0

# Todas las URL a las que se empuja, de todos los remotos. Un mismo remoto
# puede tener varias (asi es como origin llega a GitHub y a Forgejo a la vez).
pushurls() {
	local r
	for r in $(git remote); do
		git remote get-url --push --all "$r" 2>/dev/null || true
	done | sort -u
}

deshacer() {
	local codigo=$?
	trap - ERR EXIT INT TERM
	# El trap tambien esta en EXIT, que salta al terminar bien: ahi no hay nada
	# que deshacer. Hace falta EXIT porque los errores propios salen con `exit`
	# (cancelar en la confirmacion, por ejemplo) y eso NO dispara ERR: sin esto,
	# decir que no dejaba un commit y un tag de version huerfanos.
	[ "$codigo" -eq 0 ] && return 0

	# Deshacer en local no sirve de nada si esto ya salio a algun remoto: se
	# quedaria el remoto por delante y el clon aparentemente limpio, que es
	# peor que el fallo original porque no se ve. Se comprueba de verdad.
	local parcial=0 url remoto
	if [ -n "$NUEVO" ]; then
		for url in $(pushurls); do
			remoto="$(git ls-remote "$url" refs/heads/main 2>/dev/null | cut -f1)"
			[ "$remoto" = "$NUEVO" ] && parcial=1
		done
	fi

	if [ "$PUBLICADO" -eq 1 ] || [ "$parcial" -eq 1 ]; then
		echo >&2
		echo "ATENCION: la version $TAG ya ha salido a algun remoto, asi que NO" >&2
		echo "deshago nada: reescribir historia publicada seria peor." >&2
		echo >&2
		echo "Estado de cada remoto:" >&2
		for url in $(pushurls); do
			remoto="$(git ls-remote "$url" refs/heads/main 2>/dev/null | cut -c1-7)"
			echo "   ${remoto:-inalcanzable}  $url" >&2
		done
		echo >&2
		echo "Termina de publicar a mano cuando puedas:" >&2
		echo "   git push <remoto> main && git push <remoto> $TAG" >&2
		exit "$codigo"
	fi

	echo >&2
	echo ">> algo ha fallado; dejando el repositorio como estaba" >&2
	git tag -d "$TAG" >/dev/null 2>&1 || true
	git reset --hard "$ANTES" >/dev/null 2>&1 || true
	echo "   vuelto a $(git rev-parse --short HEAD), sin tag $TAG" >&2
	exit "$codigo"
}
trap deshacer EXIT ERR INT TERM

# Antes de tocar nada, comprobar que se puede hablar con todos los remotos.
# La causa habitual de que el push falle a mitad es que uno no esta accesible
# (VPN caida, servidor propio apagado), y eso se sabe ya: mejor no llegar a
# crear el commit que tener que decidir despues como se deshace.
paso "comprobando que los remotos responden"
for url in $(pushurls); do
	if git ls-remote "$url" >/dev/null 2>&1; then
		echo "   ok     $url"
	else
		err "no se puede hablar con $url
Publicar dejaria los remotos desparejos. Arregla el acceso y repite."
	fi
done

# --- las pruebas, antes de versionar nada -----------------------------------
#
# Sin Deck a proposito: publicar no puede depender de tener el aparato
# encendido. Las de integracion se corren aparte con `make deck`.

paso "comprobando (gofmt, vet y pruebas locales)"
DECKMAN_SIN_DECK=1 ./test.sh

# --- CHANGELOG --------------------------------------------------------------

paso "actualizando $CHANGELOG"
awk -v ver="$VERSION" -v fecha="$FECHA" '
	function volcar() {
		print "## [" ver "] — " fecha
		print ""
		for (i = 1; i <= n; i++) print cuerpo[i]
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

paso "actualizando $METAINFO"
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
	|| err "no se pudo insertar la version en $METAINFO"

# --- commit y tag -----------------------------------------------------------

paso "commit y tag"
git add "$CHANGELOG" "$METAINFO"
git commit -q -m "Version $VERSION"
NUEVO="$(git rev-parse HEAD)"
git tag -a "$TAG" -m "deckman $VERSION"
echo "   $TAG creado en local"

# --- confirmar --------------------------------------------------------------

# El orden de empuje importa, y costo la v0.4.2. `git remote` los devuelve
# alfabeticamente, o sea forgejo antes que origin. Forgejo es quien dispara el
# CI al recibir el tag, y ese job publica la release en GitHub: empujando
# forgejo primero, el CI arrancaba cuando GitHub todavia no tenia el tag y la
# API respondia «Published releases must have a valid tag».
#
# Asi que el que dispara el CI va el ULTIMO. `origin` lleva las dos pushurl
# (GitHub y Forgejo, en ese orden), asi que empujarlo primero deja el tag en
# GitHub antes de que Forgejo se entere. Es la carrera entera, resuelta con el
# orden en vez de con reintentos.
REMOTOS=()
for r in $(git remote); do [ "$r" = forgejo ] || REMOTOS+=("$r"); done
for r in $(git remote); do [ "$r" = forgejo ] && REMOTOS+=("$r"); done
[ ${#REMOTOS[@]} -gt 0 ] || err "no hay remotos configurados"

echo
echo "Todo listo. Falta empujar main y $TAG a: ${REMOTOS[*]}"
echo "(a partir de aqui ya no se puede deshacer solo)"
if [ -z "${DECKMAN_RELEASE_SI:-}" ]; then
	read -r -p "¿Publico? [s/N] " ok
	[[ "$ok" =~ ^[sSyY]$ ]] || err "cancelado a peticion tuya"
fi

# --- publicar ---------------------------------------------------------------

paso "empujando"
for r in "${REMOTOS[@]}"; do
	git push "$r" main
	git push "$r" "$TAG"
done
PUBLICADO=1

# Comprobar de verdad que ha llegado, no fiarse del codigo de salida: con
# varias pushurl en un mismo remoto, un fallo parcial pasa desapercibido.
paso "comprobando que esta en todos los remotos"
FALLO=0
for r in "${REMOTOS[@]}"; do
	for url in $(git remote get-url --push --all "$r"); do
		if git ls-remote --tags "$url" "refs/tags/$TAG" 2>/dev/null | grep -q "$TAG"; then
			echo "   ok     $url"
		else
			echo "   FALTA  $url" >&2
			FALLO=1
		fi
	done
done
if [ "$FALLO" -ne 0 ]; then
	trap - ERR EXIT INT TERM
	echo >&2
	echo "Algun remoto se ha quedado atras. Como el tag ya esta publicado en" >&2
	echo "otro, no lo deshago: empuja el que falte a mano y comprueba." >&2
	exit 1
fi

# --- Flatpak ----------------------------------------------------------------
#
# Reinstalar aqui es el motivo de que esto sea una sola orden: tener el Flatpak
# desfasado respecto al codigo era justo el problema que habia. Ya publicado,
# un fallo aqui no invalida la version: se avisa y se sigue.

trap - EXIT ERR INT TERM
BUNDLE=""
if command -v flatpak >/dev/null 2>&1; then
	paso "reinstalando el Flatpak"
	# Absoluta a proposito: flatpak/build.sh hace cd a su propio directorio, y
	# con una ruta relativa el bundle acababa en flatpak/dist/ mientras aqui se
	# buscaba en dist/.
	BUNDLE="$PWD/dist/deckman-$VERSION.flatpak"
	# Fuera bundles de versiones anteriores: nadie los limpiaba y un `ls dist/`
	# acababa enseñando .flatpak viejos como si fueran el actual.
	rm -f "$PWD"/dist/deckman-*.flatpak
	if ! DECKMAN_BUNDLE="$BUNDLE" flatpak/build.sh; then
		echo "aviso: la version esta publicada, pero el Flatpak no se pudo" >&2
		echo "       reinstalar. Reintenta con:  make flatpak" >&2
		BUNDLE=""
	fi
else
	echo
	echo "(sin flatpak en este sistema; me salto la reinstalacion)"
fi

# --- el bundle, a la release ------------------------------------------------
#
# Los binarios los sube el CI al ver el tag, pero el .flatpak no puede: el
# runner no construye Flatpaks (necesita espacios de nombres de usuario y
# bwrap, y bajarse el runtime entero). Asi que lo sube esta maquina, que es
# donde acaba de construirse.
#
# Hay que esperar a que el CI haya creado la release: se corre en paralelo y
# aqui se llega antes. Si no aparece, no se falla —la version ya esta
# publicada— y se deja dicho el comando para subirlo luego.

if [ -n "$BUNDLE" ] && [ ! -f "$BUNDLE" ]; then
	# Saltarselo en silencio era peor que el fallo: la version salia publicada
	# sin Flatpak y sin que nada lo dijera.
	echo "aviso: no encuentro el bundle en $BUNDLE; la release se queda sin .flatpak" >&2
	BUNDLE=""
fi
if [ -n "$BUNDLE" ]; then
	if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
		# Se espera a que el CI haya subido TODO, no solo a que exista la
		# release: la crea primero y sube los ficheros despues, y colarse en
		# ese hueco dejaba el SHA256SUMS publicado sin la linea del bundle.
		#
		# Y se espera al ULTIMO fichero, que es el .exe. Antes se esperaba al
		# SHA256SUMS creyendo que era el ultimo, y resulta que es el primero:
		# publicar-release.sh sube lo que hay en la carpeta y el glob va en
		# orden alfabetico, donde la S mayuscula va antes que la d minuscula.
		# No llego a romper nada —el SHA256SUMS se genera antes de subir nada,
		# asi que ya listaba todo— pero la espera no esperaba a lo que decia,
		# y eso solo se sostiene mientras nadie toque el orden.
		ULTIMO="deckman-$VERSION-windows-amd64.exe"
		paso "esperando a que el CI termine de subir ($ULTIMO)"
		encontrada=0
		espera=2
		for _ in $(seq 1 14); do   # ~5 minutos en total
			if gh release view "$TAG" --json assets --jq '.assets[].name' 2>/dev/null \
				| grep -qxF "$ULTIMO"; then
				encontrada=1
				break
			fi
			sleep "$espera"
			[ "$espera" -lt 30 ] && espera=$((espera * 2))
		done
		if [ "$encontrada" -eq 1 ] && gh release upload "$TAG" "$BUNDLE" --clobber >/dev/null 2>&1; then
			echo "   subido $(basename "$BUNDLE")"
			# El SHA256SUMS lo genera el CI antes de que este bundle exista, asi
			# que hay que anadirle su linea: si no, el fichero que la gente usa
			# para comprobar lo que se baja no cubre justo el del Flatpak.
			tmp="$(mktemp -d)"
			if gh release download "$TAG" -p SHA256SUMS -D "$tmp" >/dev/null 2>&1; then
				( cd "$(dirname "$BUNDLE")" && sha256sum "$(basename "$BUNDLE")" ) >> "$tmp/SHA256SUMS"
				gh release upload "$TAG" "$tmp/SHA256SUMS" --clobber >/dev/null 2>&1 \
					&& echo "   SHA256SUMS actualizado con el bundle" \
					|| echo "aviso: no se pudo actualizar SHA256SUMS" >&2
			else
				echo "aviso: no se pudo bajar SHA256SUMS; se queda sin la linea del bundle." >&2
				echo "       Anadela a mano: sha256sum $(basename "$BUNDLE") >> SHA256SUMS" >&2
			fi
			rm -rf "$tmp"
		else
			echo "aviso: no se pudo subir el bundle (¿el CI aun no ha terminado?)." >&2
			echo "       Cuando la release y su SHA256SUMS existan:" >&2
			echo "       gh release upload $TAG $BUNDLE --clobber" >&2
			echo "       y anade su linea al SHA256SUMS de la release." >&2
		fi
	else
		echo
		echo "(sin gh autenticado; el bundle se queda en $BUNDLE)"
	fi
fi

# --- firma ------------------------------------------------------------------
#
# SHA256SUMS dice que lo descargado no viene corrupto; la firma dice ademas de
# quien viene. Sin ella, cualquiera con el token de la release podria cambiar
# los binarios Y el SHA256SUMS a juego, y quien comprueba no notaria nada.
#
# Se firma AQUI y no en el CI por dos razones. La primera es de orden: el
# SHA256SUMS definitivo no existe hasta que este PC le añade la linea del
# .flatpak, que el runner no puede construir. La segunda es de confianza: la
# clave que firma no tiene por que estar en el mismo sitio que el token que
# publica, porque entonces quien se lleve uno se lleva los dos.
#
# Se firma cosign keyless? No: sin claves va contra Fulcio, que solo confia en
# unos cuantos emisores OIDC publicos (GitHub, GitLab, Google...). Este CI es un
# Forgejo propio y no esta en esa lista, asi que aqui toca clave.
#
# Es opcional mientras no haya clave: crear una es una decision del que publica
# y no algo que deba imponer un script. En cuanto exista, un fallo al firmar se
# cuenta a gritos — a estas alturas la version ya esta publicada y no se puede
# deshacer, asi que lo unico util es decir exactamente que falta por hacer.
#
# cosign no se instala: se ejecuta en contenedor, como Go. La clave se copia al
# directorio temporal (que es 0700) en vez de montar ~/.config/deckman, porque
# montar esa carpeta con :Z le cambiaria la etiqueta de SELinux — y ahi dentro
# esta tambien la configuracion que usa el deckman de verdad.

CLAVE_FIRMA="${DECKMAN_COSIGN_KEY:-$HOME/.config/deckman/cosign.key}"
if [ -n "$TAG" ] && command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
	if [ ! -f "$CLAVE_FIRMA" ]; then
		echo
		echo "(sin firmar: no hay clave en $CLAVE_FIRMA)"
		echo " Para firmar las proximas versiones:  scripts/crear-clave-firma.sh"
	else
		paso "firmando SHA256SUMS"
		tmpf="$(mktemp -d)"
		chmod 700 "$tmpf"
		firmado=0
		if gh release download "$TAG" -p SHA256SUMS -D "$tmpf" >/dev/null 2>&1; then
			cp "$CLAVE_FIRMA" "$tmpf/cosign.key"
			# COSIGN_PASSWORD vacio si no se ha puesto: sin la variable, cosign
			# se queda esperando a que alguien teclee la contrasena y `make
			# release` se cuelga sin decir por que.
			#
			# --bundle y no --output-signature: cosign 3 deprecio la firma
			# suelta y con ella verify-blob se va a buscar el registro de
			# transparencia y falla. El bundle lleva firma y registro juntos, y
			# se comprueba con una orden sola. Probado antes de ponerlo aqui,
			# incluido que un SHA256SUMS retocado NO pasa la verificacion.
			if $RUNTIME run --rm "${USUARIO_OPTS[@]}" -v "$tmpf:/trabajo:Z" -w /trabajo \
				-e COSIGN_PASSWORD="${COSIGN_PASSWORD:-}" \
				"$COSIGN_IMAGE" sign-blob --key cosign.key --yes \
				--bundle SHA256SUMS.bundle SHA256SUMS >/dev/null 2>&1; then
				firmado=1
			fi
			rm -f "$tmpf/cosign.key"
		fi
		if [ "$firmado" -eq 1 ]; then
			# La publica va con cada release a proposito, aunque sea siempre la
			# misma: quien se baja los binarios tiene ahi mismo con que
			# comprobarlos, sin buscarla en ningun otro sitio.
			cp "${CLAVE_FIRMA%.key}.pub" "$tmpf/cosign.pub" 2>/dev/null || true
			if gh release upload "$TAG" "$tmpf/SHA256SUMS.bundle" --clobber >/dev/null 2>&1; then
				[ -f "$tmpf/cosign.pub" ] && gh release upload "$TAG" "$tmpf/cosign.pub" --clobber >/dev/null 2>&1
				echo "   firmado y subido SHA256SUMS.bundle"
			else
				echo "aviso: no se pudo subir la firma de $TAG." >&2
			fi
		else
			echo "aviso: no se pudo firmar $TAG. Hazlo a mano:" >&2
			echo "       gh release download $TAG -p SHA256SUMS" >&2
			echo "       $RUNTIME run --rm -v \"\$PWD:/t:Z\" -w /t -e COSIGN_PASSWORD $COSIGN_IMAGE \\" >&2
			echo "         sign-blob --key $CLAVE_FIRMA --yes --bundle SHA256SUMS.bundle SHA256SUMS" >&2
			echo "       gh release upload $TAG SHA256SUMS.bundle --clobber" >&2
		fi
		rm -rf "$tmpf"
	fi
fi

echo
echo "Publicada $TAG."
echo "Releases:  https://github.com/jfrmorales/deckman/releases/tag/$TAG"
