# Entrada unica de deckman. `make` a secas dice lo que hay.
#
# Aqui no vive la logica: esta en los scripts de siempre (build.sh, test.sh,
# scripts/release.sh, flatpak/build.sh). Este fichero solo les pone nombre y
# orden, para que nadie tenga que aprenderselo ni acordarse de cual va antes.
#
# Requisitos: podman o docker. Go NO se instala en el sistema.

.DEFAULT_GOAL := help
.PHONY: help setup check audit build deck flatpak release clean

help:
	@echo 'deckman — gestiona una Steam Deck desde el PC'
	@echo
	@echo '  make setup        deja el clon listo: comprueba requisitos, crea'
	@echo '                    deck.local.env y configura los remotos'
	@echo '  make check        gofmt + vet + pruebas locales (lo mismo que el CI)'
	@echo '  make audit        analisis estatico y vulnerabilidades conocidas'
	@echo '  make build        binarios de Linux y Windows en dist/'
	@echo '  make deck         check + pruebas contra tu Steam Deck de verdad'
	@echo '  make flatpak      empaqueta e instala el Flatpak (usuario)'
	@echo '  make clean        borra dist/ y lo que deja el empaquetado'
	@echo
	@echo '  make release V=0.2.0'
	@echo '                    publica: comprueba, versiona, etiqueta, empuja a'
	@echo '                    los dos remotos y reinstala el Flatpak. Si algo'
	@echo '                    falla antes de publicar, lo deshace todo.'
	@echo
	@echo 'Empieza por  make setup  y sigue con  make check.'

setup:
	@scripts/setup.sh

check:
	@DECKMAN_SIN_DECK=1 ./test.sh

# Aparte de check a proposito: mira contra una base de datos que cambia sola,
# asi que puede ponerse en rojo sin que nadie haya tocado nada. El porque
# esta en audit.sh.
audit:
	@./audit.sh

build:
	@./build.sh

# Las de integracion necesitan una Deck con SSH: los datos salen de
# deck.local.env (make setup lo crea a partir del ejemplo).
deck:
	@if [ ! -f deck.local.env ]; then \
		echo 'error: falta deck.local.env — ejecuta primero: make setup' >&2; \
		exit 1; \
	fi
	@./test.sh

flatpak:
	@flatpak/build.sh

# V es obligatorio y se comprueba aqui para poder decirlo con un ejemplo, en
# vez de soltar el "uso:" pelado del script.
release:
	@if [ -z '$(V)' ]; then \
		echo 'error: falta la version.  Ejemplo:  make release V=0.2.0' >&2; \
		exit 1; \
	fi
	@scripts/release.sh '$(V)'

clean:
	@rm -rf dist flatpak/build-dir flatpak/.flatpak-builder
	@echo 'borrado dist/ y lo del empaquetado'
