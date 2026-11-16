# Entorno de desarrollo: el UM780 y un contenedor reutilizable

**Regla:** nada de desarrollo se ejecuta en el portátil. Pruebas, compilaciones y comprobaciones se
lanzan en el **UM780**, dentro de la imagen `deckman-dev`. Todo pasa por un único script:

```bash
scripts/dev/um780.sh <comando>
```

`make check`, `make build` y compañía siguen valiendo donde haya podman a mano (ver el `CLAUDE.md`
de la raíz); esto es lo mismo, pero ejecutado en el UM780.

## Uso

| Para | Comando |
|---|---|
| Formato, sintaxis del JavaScript, `vet` y pruebas | `scripts/dev/um780.sh scripts/dev/comprobar.sh` |
| Una prueba suelta | `scripts/dev/um780.sh go test ./internal/server -run TestCatalogoWeb -v` |
| Compilar el binario de Linux y traerlo | `scripts/dev/um780.sh --traer dist/deckman scripts/compilar.sh linux dist/deckman dev` |
| Mirar algo a mano | `scripts/dev/um780.sh --shell` |
| Actualizar la imagen base | `scripts/dev/um780.sh --rebuild <comando>` |

Para ver la interfaz con un binario recién compilado, sin tocar la configuración de verdad:

```bash
XDG_CONFIG_HOME=$(mktemp -d) dist/deckman -no-browser -port 8779
```

## Qué hace el script

1. **Sincroniza** el árbol de trabajo del portátil, con los cambios sin commit, en `~/dev/<carpeta>`
   del UM780 (`rsync --delete`). No viajan `dist/`, `testdata/` ni **`deck.local.env`**.
   - Esa carpeta es **una copia desechable**: se pisa en cada ejecución. Allí no se edita ni se hace
     commit.
2. **Construye la imagen** si hace falta. La etiqueta es el hash de `Containerfile` más la imagen de
   Go de `scripts/contenedor.sh`: si cambia cualquiera de los dos, se reconstruye sola.
3. **Ejecuta** el comando con `podman run --rm`, con el repositorio en `/w` y las cachés de Go en los
   volúmenes `deckman-gomod` y `deckman-gocache`.
4. **Trae de vuelta** `.dev-out/` (aunque el comando falle) y las rutas de `--traer` (solo si sale
   bien).

El código de salida es el del comando, así que sirve en scripts y en `&&`.

## La imagen (`Containerfile`)

- Base: la imagen de Go de `scripts/contenedor.sh`. La versión **no se repite** en el
  `Containerfile`; la pasa `um780.sh` como `--build-arg`, para que siga en un único sitio (el CI
  comprueba que coincide con `go.mod`).
- Lleva además `nodejs`, solo para `node --check`: la aplicación no usa Node.

## Trampas conocidas

- **`./test.sh` y `./build.sh` no sirven aquí dentro.** Levantan su propio contenedor, y dentro del
  de desarrollo no hay podman. Por eso existe `comprobar.sh`, que hace lo mismo que `make check` ya
  dentro, y por eso se compila con `scripts/compilar.sh` directamente.
- **Las pruebas de integración contra la Deck no corren aquí**: `deck.local.env` no se copia y el
  contenedor lleva `DECKMAN_SIN_DECK=1`. Esas siguen siendo `make deck`.
- `--traer` no borra nada en el portátil.
- En un worktree, la carpeta remota lleva el nombre del worktree, no `deckman`.
