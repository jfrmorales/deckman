# deckman — notas para trabajar en este proyecto

Gestor de una Steam Deck desde el PC. Go, un binario por sistema, interfaz web
embebida. Ver `README.md` para el uso y `docs/` para el detalle.

## Antes de tocar nada

Lee **`docs/HALLAZGOS-STEAM.md`**. Son las trampas de Steam que ya costaron
fallos reales, ninguna documentada por Valve. Si vas a tocar carátulas,
accesos directos o Proton, están todas ahí.

## Compilar y probar: siempre en contenedor

**No instales Go en el sistema.** Petición explícita del usuario: el entorno se
mantiene limpio y todo va por podman/docker.

Entrada única: **`make`** a secas lista todo. La lógica sigue en los scripts
(`build.sh`, `test.sh`, `scripts/release.sh`, `flatpak/build.sh`); el Makefile
solo les pone nombre y orden, así que no dupliques nada ahí.

```sh
make setup            # deja el clon listo (requisitos, deck.local.env, remotos, gancho)
make check            # gofmt + vet + pruebas locales, SIN la Deck
make deck             # + las de integración contra la Deck
make build            # linux + windows en dist/
make flatpak          # empaqueta e instala el Flatpak (usuario)
make release V=0.2.0  # publica de principio a fin
```

`make check` pone `DECKMAN_SIN_DECK=1` para que `test.sh` ignore
`deck.local.env`: publicar no puede depender de tener la Deck encendida.

El Flatpak usa `org.flatpak.Builder` (otro Flatpak, nada en el sistema) y
empaqueta el binario que sale de `./build.sh`; el manifiesto y sus porqués
están en `flatpak/`. En el sandbox la config va a
`~/.var/app/io.github.jfrmorales.deckman/config/deckman` (con migración
automática desde `~/.config/deckman` la primera vez).

La IP y la contraseña de la Deck del usuario están en **`deck.local.env`**, que
no se versiona: este repositorio es público. `./test.sh` lo lee solo. Si hace
falta la IP para un `ssh` a mano, sácala de ahí — nunca la escribas en un
fichero versionado.

## Reglas que no se saltan

**Nunca escribas `shortcuts.vdf` con Steam abierto.** Steam lo tiene en memoria
y lo reescribe al salir; hacerlo por detrás ya borró seis juegos no-Steam de un
usuario. Con Steam abierto se usa su API (`deck.AddShortcutLive`); con Steam
cerrado se puede editar el fichero. Lo decide `server.registerShortcut`.

**Las carátulas se guardan como `.png`/`.jpg`/`.ico`, nunca `.webp`.** Steam
identifica el arte por la extensión del nombre, no por el contenido.

**Los ficheros `<appid>.json` de la carpeta `grid/` son del plugin de Decky**,
no son arte. Comparten nombre con la portada horizontal: filtrar por extensión
de imagen o se los borra.

**Antes de escribir un fichero de configuración de Steam**, deja copia
(`WriteFileAtomic` ya lo hace) y comprueba que no pierdes datos
(`checkNoShortcutsLost`).

## Verificar de verdad

El usuario espera comprobación real, no "debería funcionar":

- Contra su Deck, con datos reales, y **restaurando** lo que se toque.
- La interfaz se mira con Playwright: hay un venv en
  `~/repositories/webwright-workspace/.venv`. Comprobar que no hay errores de
  JavaScript y **mirar la captura**, no solo contar elementos.
- Si algo queda sin probar, **decirlo explícitamente**. Sigue pendiente:
  **mover un juego de Steam** entre interno y microSD (necesita Steam cerrado),
  **desinstalar**, la **ventana WebView2 en un Windows real** (compila, pero
  aquí no hay Windows donde abrirla), las vías en caliente de la revisión
  2026-08-03 (`RemoveShortcutLive`, `SetCompatToolLive`) y `RelocateShortcut`
  (mover un no-Steam con Steam **cerrado**: solo cubierto por pruebas unitarias).

  Sí probado contra la Deck el 2026-08-03: **mover un juego no-Steam** con Steam
  abierto (`SetShortcutPathLive`), ida y vuelta, con los 9 accesos directos
  intactos. Y **olvidar una Deck** (`RemovePublicKey`): instalada una segunda
  clave con el **mismo comentario** que la real y distinto material, se retiró
  solo la de prueba y `authorized_keys` quedó idéntico byte a byte al original.
  La forma de probarlo sin arriesgar la configuración del usuario es lanzar el
  binario con `XDG_CONFIG_HOME` apuntando a un directorio desechable: se genera
  su propia clave y no toca la de verdad.

Cuidado al lanzar el binario en segundo plano: a veces el proceso acaba en el
host, fuera del contenedor de distrobox, y `pkill` desde dentro no lo ve. Se
localiza con `distrobox-host-exec ss -ltnp | grep 8777`.

## Estilo

- Todo en castellano: interfaz, comentarios, mensajes de error, documentación.
- Los comentarios explican **por qué**, no qué. Si algo parece arbitrario, es
  que hay una trampa detrás: cuéntala y di cómo se comprobó.
- Los mensajes de error se los lee una persona: que digan qué pasó y qué hacer.

## Versiones y remotos

Cada cambio se anota en **`CHANGELOG.md`**, bajo `## [No publicado]`. No es
opcional: `scripts/release.sh` se niega a publicar si esa sección está vacía.

Publicar es `make release V=X.Y.Z`. Es el único sitio donde se toca la
versión: sincroniza el changelog, el `<releases>` del metainfo del Flatpak (de
ahí sale lo que muestra `flatpak list`) y el tag de git (de ahí sale lo que
lleva el binario, vía `git describe` en `build.sh`). A mano se desincronizan.

Todo lo anterior al push es reversible y hay un trap que lo deshace. Dos
trampas que ya costaron un fallo y por eso están cubiertas:

- El trap tiene que estar en **`EXIT`** además de en `ERR`. Los errores propios
  salen con `exit`, y eso no dispara `ERR`: sin `EXIT`, cancelar en la
  confirmación dejaba un commit y un tag de versión huérfanos.
- Con varias *pushurl* en un mismo remoto, el push puede llegar a uno y fallar
  en otro. Deshacer en local entonces deja el remoto **por delante** sin que se
  note. Por eso hay un preflight (`git ls-remote` a cada URL antes de tocar
  nada) y, si aun así pasa, se detecta y **no** se deshace.

El repositorio está en **dos remotos y los dos van siempre a la vez**:
`origin` (GitHub, público) y `forgejo`. `origin` tiene las dos URL de push
configuradas, así que un `git push` normal llega a ambos; `release.sh` además
comprueba que el tag ha aterrizado en los dos y falla si no.

Tras publicar, `flatpak/build.sh` reinstala el Flatpak y dice qué versión deja
instalada y cuál había antes.

## No commitear sin permiso

Preferencia del usuario. Salvo lo que pida explícitamente, no se commitea ni se
empuja por iniciativa propia.
