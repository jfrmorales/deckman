# Trozo comun de build.sh y test.sh: se carga con `. scripts/contenedor.sh`.
# No es ejecutable a proposito: solo, no hace nada.
#
# Aqui vive todo lo que estaba copiado en tres sitios y se corregia en uno:
# que imagen de Go se usa, como se encuentra el runtime de contenedores y los
# volumenes de cache que hacen que recompilar sea rapido.

# La misma version que pide go.mod; el CI comprueba que no se separen
# (.forgejo/workflows/pruebas.yml, paso "version de go").
GO_IMAGE="docker.io/library/golang:1.26.5"

# Herramientas de auditoria (scripts/auditar.sh y el trabajo del mismo nombre
# en el CI). Las versiones viven aqui para que el PC y el CI analicen con lo
# mismo: un linter que dice cosas distintas en cada sitio no sirve de puerta.
#
# golangci-lint va clavado: una version nueva estrena reglas y convertiria un
# push cualquiera en una tarde de arreglar avisos que ayer no existian.
# govulncheck va suelto a proposito, que es justo lo contrario de lo que uno
# esperaria: lo que quiero de el es la base de vulnerabilidades de HOY.
GOLANGCI_VERSION="v2.12.2"
GOVULNCHECK_VERSION="latest"

# Dentro de distrobox, podman vive en el host.
detectar_runtime() {
	if command -v podman >/dev/null 2>&1; then
		RUNTIME="podman"
	elif command -v docker >/dev/null 2>&1; then
		RUNTIME="docker"
	elif command -v distrobox-host-exec >/dev/null 2>&1 \
	     && distrobox-host-exec podman --version >/dev/null 2>&1; then
		RUNTIME="distrobox-host-exec podman"
	else
		echo "error: hace falta podman o docker" >&2
		return 1
	fi
}

# Cache de modulos y de build persistente, para que recompilar sea rapido.
#
# deckman-gobin guarda las herramientas ya compiladas (golangci-lint,
# govulncheck). Sin el, cada `make audit` volvia a compilarlas enteras: son
# minutos, y hacen que nadie las ejecute.
preparar_volumenes() {
	$RUNTIME volume create deckman-gomod >/dev/null 2>&1 || true
	$RUNTIME volume create deckman-gocache >/dev/null 2>&1 || true
	$RUNTIME volume create deckman-gobin >/dev/null 2>&1 || true
}
