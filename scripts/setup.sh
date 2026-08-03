#!/usr/bin/env bash
# Deja un clon recien hecho listo para trabajar.  Se llama con: make setup
#
# Es idempotente: se puede repetir sin estropear nada, y no toca nada que ya
# este bien puesto. No instala nada en el sistema.
set -euo pipefail
cd "$(dirname "$0")/.."

ok()   { echo "  ok    $*"; }
info() { echo "  ..    $*"; }
warn() { echo "  aviso $*" >&2; }

fallos=0
err() { echo "  ERROR $*" >&2; fallos=$((fallos + 1)); }

echo "Preparando el clon de deckman"
echo

# --- 1. lo unico que hace falta tener instalado -----------------------------

# shellcheck source=contenedor.sh
. scripts/contenedor.sh
if detectar_runtime 2>/dev/null; then
	ok "runtime de contenedores encontrado: $RUNTIME"
else
	err "hace falta podman o docker. Es el unico requisito: Go se usa dentro
        del contenedor y no se instala en el sistema."
fi

# --- 2. datos de la Deck ----------------------------------------------------

if [ -f deck.local.env ]; then
	ok "deck.local.env ya existe (no lo toco)"
else
	cp deck.local.env.ejemplo deck.local.env
	info "creado deck.local.env a partir del ejemplo"
	info "rellena ahi la IP y la contrasena de tu Deck para 'make deck'"
fi

# --- 3. remotos -------------------------------------------------------------
#
# El proyecto vive en GitHub y en un Forgejo propio, y los dos tienen que ir
# siempre a la vez. Configurando las dos URL de push en 'origin', un 'git push'
# normal llega a ambos y no hay forma de dejarse uno por detras sin querer.
#
# Quien clone de fuera solo tendra GitHub, y eso esta bien: no se inventa nada.

if ! git rev-parse --git-dir >/dev/null 2>&1; then
	warn "esto no es un repositorio git; me salto los remotos"
else
	principal="$(git remote get-url origin 2>/dev/null || true)"
	if [ -z "$principal" ]; then
		warn "no hay remoto 'origin'; me salto la configuracion de push"
	else
		espejo="$(git remote get-url forgejo 2>/dev/null || true)"
		if [ -z "$espejo" ]; then
			ok "un solo remoto (origin); nada que sincronizar"
		else
			# Se rehace la lista entera para no acumular duplicados al repetir.
			git remote set-url --delete --push origin '.*' 2>/dev/null || true
			git remote set-url --add --push origin "$principal"
			git remote set-url --add --push origin "$espejo"
			ok "'git push' ira a la vez a origin y a forgejo"
		fi
	fi
fi

# --- 4. gancho de pre-push --------------------------------------------------
#
# Empujar algo que no compila o sin formatear obliga a un segundo commit de
# "arreglar el CI" que ensucia el historial. El gancho corre lo mismo que el
# CI, en local, antes de que salga. Con --no-verify se salta si hace falta.

hook=".git/hooks/pre-push"
if [ -d .git/hooks ]; then
	if [ -e "$hook" ] && ! grep -q "deckman-setup" "$hook" 2>/dev/null; then
		warn "ya tienes un $hook propio; no lo toco"
	else
		cat > "$hook" <<'HOOK'
#!/usr/bin/env bash
# deckman-setup: lo puso scripts/setup.sh. Para saltarlo: git push --no-verify
echo ">> comprobando antes de empujar (make check)"
if ! make check; then
	echo
	echo "No empujo: las comprobaciones fallan." >&2
	echo "Arreglalo, o empuja igualmente con: git push --no-verify" >&2
	exit 1
fi
HOOK
		chmod +x "$hook"
		ok "gancho de pre-push instalado (se salta con git push --no-verify)"
	fi
fi

echo
if [ "$fallos" -gt 0 ]; then
	echo "Faltan cosas por resolver (arriba). Cuando esten, repite: make setup" >&2
	exit 1
fi
echo "Listo. Sigue con:  make check"
