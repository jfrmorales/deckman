#!/usr/bin/env bash
# Compila los binarios de una version y los publica como Release de GitHub.
#
#   scripts/publicar-release.sh v0.2.2
#
# Lo llama el CI de Forgejo al llegar un tag (.forgejo/workflows/publicar.yml),
# pero funciona suelto con GH_TOKEN puesto, que es como se prueba sin esperar a
# publicar una version de verdad.
#
# Va en un script y no dentro del workflow para poder ejecutarlo a mano: la
# logica que solo corre en CI es la que nadie prueba hasta que falla.
#
# Requisitos: go, git, curl y python3. Estan todos en la imagen oficial de Go,
# que es donde corre el job. jq no hace falta a proposito: no esta en esa imagen
# y python3 si.
set -euo pipefail
cd "$(dirname "$0")/.."

err() { echo "error: $*" >&2; exit 1; }
paso() { echo; echo ">> $*"; }

TAG="${1:-}"
[ -n "$TAG" ] || err "uso: $0 <vX.Y.Z>"
[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || err "el tag tiene que ser vX.Y.Z, no '$TAG'"
VERSION="${TAG#v}"

[ -n "${GH_TOKEN:-}" ] || err "falta GH_TOKEN (token de GitHub con permiso de escritura en Contents)"
REPO="${GH_REPO:-jfrmorales/deckman}"

# Un sitio aparte para no pisar el dist/ del PC de quien lo ejecute a mano.
SALIDA="dist/release"
rm -rf "$SALIDA"
mkdir -p "$SALIDA"

# --- binarios ---------------------------------------------------------------
#
# La version se toma del tag y no de `git describe`: en el CI se clona en
# superficial y puede no haber ni tags ni historia con la que describir. Asi
# ademas queda garantizado que el binario dice exactamente lo que dice el tag.

paso "compilando $TAG"
LDFLAGS="-s -w -X main.version=$TAG"
LIN="deckman-$VERSION-linux-amd64"
WIN="deckman-$VERSION-windows-amd64.exe"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -trimpath -ldflags "$LDFLAGS" -o "$SALIDA/$LIN" ./cmd/deckman
# -H windowsgui: sin ventana de consola al hacer doble clic, igual que build.sh.
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	go build -trimpath -ldflags "$LDFLAGS -H windowsgui" -o "$SALIDA/$WIN" ./cmd/deckman

( cd "$SALIDA" && sha256sum "$LIN" "$WIN" > SHA256SUMS )
ls -lh "$SALIDA"

# --- notas ------------------------------------------------------------------
#
# Salen del changelog: si la version esta anotada ahi, no hay que escribirla
# dos veces. Si no aparece, se publica igual con un texto minimo en vez de
# fallar: los binarios ya estan compilados y son lo que importa.

paso "sacando las notas de CHANGELOG.md"
# Se recortan solo los blancos del principio y del final, no todos: las lineas
# en blanco separan las secciones y sin ellas Markdown pega los parrafos.
NOTAS="$(awk -v ver="$VERSION" '
	$0 ~ ("^## \\[" ver "\\]") { dentro = 1; next }
	dentro && /^## \[/ { exit }
	dentro {
		if (NF == 0) { pendientes++; next }   # se guardan por si vienen mas lineas
		while (pendientes > 0 && n > 0) { linea[++n] = ""; pendientes-- }
		pendientes = 0
		linea[++n] = $0
	}
	END { for (i = 1; i <= n; i++) print linea[i] }
' CHANGELOG.md)"

if [ -z "$(printf '%s' "$NOTAS" | tr -d '[:space:]')" ]; then
	echo "   aviso: $VERSION no aparece en CHANGELOG.md; publico sin notas" >&2
	NOTAS="Ver el registro de cambios: https://github.com/$REPO/blob/main/CHANGELOG.md"
fi
printf '%s\n' "$NOTAS" | head -5

# --- release ----------------------------------------------------------------

API="https://api.github.com/repos/$REPO"
CAB=(-H "Authorization: Bearer $GH_TOKEN" -H "Accept: application/vnd.github+json"
     -H "X-GitHub-Api-Version: 2022-11-28")

# El id de la release se saca con python3, no con grep: en la respuesta hay
# muchos campos "id" (autor, assets...) y quedarse con el primero que aparezca
# es una cerilla esperando a encenderse.
sacar_id() { python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("id","") or "")'; }

paso "creando la release en $REPO"
CUERPO="$(python3 -c '
import json, sys
tag = sys.argv[1]
print(json.dumps({
    "tag_name": tag,
    "name": tag,
    "body": sys.stdin.read(),
    "draft": False,
    "prerelease": False,
}))' "$TAG" <<< "$NOTAS")"

RESP="$(curl -sS -X POST "${CAB[@]}" "$API/releases" -d "$CUERPO")"
ID="$(printf '%s' "$RESP" | sacar_id || true)"

if [ -z "$ID" ]; then
	# Lo normal aqui es que la release ya exista (segundo intento del job, o se
	# creo a mano). Se reutiliza en vez de fallar.
	ID="$(curl -sS "${CAB[@]}" "$API/releases/tags/$TAG" | sacar_id || true)"
	[ -n "$ID" ] || err "no se pudo crear ni encontrar la release $TAG. Respuesta de GitHub:
$RESP"
	echo "   la release ya existia (id $ID); reutilizo"
else
	echo "   creada (id $ID)"
fi

# --- ficheros ---------------------------------------------------------------
#
# Se borra el fichero homonimo antes de subir: GitHub rechaza un asset con el
# nombre de otro que ya esta, y sin esto reintentar el job fallaba entero.

paso "subiendo los ficheros"
YA="$(curl -sS "${CAB[@]}" "$API/releases/$ID/assets")"
for f in "$SALIDA"/*; do
	nombre="$(basename "$f")"
	viejo="$(printf '%s' "$YA" | python3 -c '
import sys, json
nombre = sys.argv[1]
for a in json.load(sys.stdin):
    if a.get("name") == nombre:
        print(a["id"]); break
' "$nombre" || true)"
	if [ -n "$viejo" ]; then
		curl -sS -X DELETE "${CAB[@]}" "$API/releases/assets/$viejo" >/dev/null
		echo "   reemplazo $nombre"
	fi
	curl -sS -X POST "${CAB[@]}" \
		-H "Content-Type: application/octet-stream" \
		--data-binary @"$f" \
		"https://uploads.github.com/repos/$REPO/releases/$ID/assets?name=$nombre" >/dev/null
	echo "   ok $nombre"
done

echo
echo "Publicada: https://github.com/$REPO/releases/tag/$TAG"
