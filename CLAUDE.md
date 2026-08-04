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
make audit            # golangci-lint + govulncheck
make deck             # + las de integración contra la Deck
make build            # linux + windows en dist/
make flatpak          # empaqueta e instala el Flatpak (usuario)
make release V=0.2.0  # publica de principio a fin
```

`make audit` va **aparte** de `check` y **no** es puerta para publicar: mira
contra una base de datos que cambia sola, así que puede ponerse en rojo sin que
nadie haya tocado nada. El CI lo pasa igual en cada push
(`.forgejo/workflows/auditoria.yml`). El porqué largo, en `audit.sh`.

Cada exclusión de `.golangci.yml` lleva escrito su motivo. Si añades una, di
por qué; una exclusión sin motivo es la que nadie se atreve a quitar después.

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

**La clave SSH de la Deck se verifica (TOFU) y eso no se relaja.** La primera
conexión acepta y recuerda; un cambio posterior se planta con un
`*deck.ClaveDeHostCambiada` y solo lo salta `ConfiarClaveNueva`, que pone el
usuario con la huella delante. `deck.Connect` **falla** si no se le da
`KnownHostsPath`: aceptar cualquier clave con la ruta vacía sería el viejo
`InsecureIgnoreHostKey` pero invisible. El porqué está en
`internal/deck/hostkey.go`.

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

  La **verificación TOFU de la clave de host** (2026-08-04) se probó entera sin
  la Deck, y la forma merece quedar apuntada porque se reutiliza: un servidor
  SSH de mentira en `127.0.0.1:2222` (30 líneas con `x/crypto/ssh`, acepta
  cualquier contraseña y rechaza las sesiones) sirve de sobra, porque el
  apretón de manos —que es donde se verifica la clave— ocurre **antes** de
  autenticar. Arrancarlo con una semilla y luego con otra imita exactamente lo
  que hace reinstalar SteamOS. Comprobado así: primera conexión recuerda la
  clave, la misma clave no molesta, otra clave se planta con las dos huellas
  (en los tres idiomas), cancelar deja `known_hosts` intacto y confiar deja una
  sola línea con la nueva. Y con Playwright, el diálogo de la interfaz de
  verdad, sin errores de JavaScript.

  Repetido **contra la Deck del usuario** el mismo día: primera conexión
  apunta su clave ECDSA, la segunda no molesta, sustituirla en `known_hosts`
  por otra válida dispara el aviso con las dos huellas, y confiar deja una
  sola línea con la real. La prueba se hizo con `XDG_CONFIG_HOME` a un
  directorio desechable y se limpió con «olvidar Deck» (`revocada: true`); la
  clave del usuario siguió funcionando después. De ahí salió el fallo de la
  línea ilegible: una entrada corrupta tumbaba `knownhosts.New` entero.

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

## Idiomas

Desde la 0.5.0 la aplicación habla **castellano, inglés y francés**, y el
castellano es **el original**: se escribe primero y hace de **clave** del
catálogo. Nada de identificadores tipo `err.roms.delete_failed` — con ellos el
código deja de decir lo que pasa y hay que ir al catálogo para leerlo.

```go
return i18n.Errorf("no se pudo borrar %q de %s: %w", nombre, sistema, err)
```

Dos catálogos, misma idea en los dos:

- `internal/i18n/catalogo.go` — los mensajes del servidor. `i18n.Errorf`
  sustituye a `fmt.Errorf` en todo lo que pueda acabar delante de una persona;
  el error guarda el formato y sus argumentos **sin juntarlos**, y se traduce en
  el borde HTTP (`writeErr`), cuando ya se sabe qué idioma quiere quien mira.
- `internal/server/web/i18n-catalogo.js` — la interfaz. El HTML **no lleva
  marcas**: `i18n.js` recorre el DOM y traduce cada nodo buscándolo por su
  texto. Lo que pinta JavaScript va con `t('...', arg)`, con huecos `{0}`.

Al añadir texto nuevo: escríbelo en castellano y añade las dos traducciones.
Si falta una, sale el castellano — se entiende menos, pero nunca aparece una
clave cruda. Hay pruebas que comprueban que los verbos de formato cuadran, que
inglés y francés cubren lo mismo y que no quedan claves huérfanas.

Si repintas la interfaz desde JavaScript, acuérdate de que `aplicarIdioma`
tiene que volver a llamar a esa función: lo generado después del recorrido del
DOM no lo alcanza el traductor y se queda en el idioma anterior.

## Estilo

- **Comentarios y documentación interna, en castellano.** Lo que ve el usuario
  va en los tres idiomas (arriba); el README también (`README.en.md`,
  `README.fr.md`).
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

Las comprobaciones automáticas corren en **Forgejo Actions**
(`.forgejo/workflows/pruebas.yml`), no en GitHub: el CI no depende de un
servicio de terceros. Dos trampas de ese runner, ya pagadas:

- La imagen del label `docker` va con node y **sin Go**, y el runner
  **no puede levantar contenedores dentro del job**. Por eso el job declara
  `container: golang:...` y por eso ahí no se usa `./test.sh`, que monta un
  contenedor: ese script existe para no instalar Go en el PC de nadie, problema
  que en el CI no se plantea.
- `actions/checkout` es una acción de **JavaScript** y necesita `node` dentro
  del contenedor del job. En la imagen de Go no está, y falla con
  `node: executable file not found`. Se clona a mano con `git`, que sí viene.

El **`.flatpak` no lo sube el CI**, lo sube `scripts/release.sh` desde este PC:
el runner no puede construir Flatpaks (necesita espacios de nombres de usuario
y `bwrap`, y bajarse el runtime entero). Espera a que el CI cree la release,
sube el bundle y le añade su línea al `SHA256SUMS`, que el CI genera antes de
que ese fichero exista.

Al llegar un tag, `publicar.yml` sube los binarios a las **Releases de GitHub**
usando el secreto `GH_RELEASE_TOKEN` de Forgejo (no puede llamarse `GITHUB_*`:
Forgejo reserva el prefijo). El trabajo lo hace `scripts/publicar-release.sh`,
que está aparte del workflow **para poder ejecutarlo a mano**: la lógica que
solo corre en CI es la que nadie prueba hasta que falla. Se prueba con
`GH_TOKEN=... scripts/publicar-release.sh v9.9.9` y luego
`gh release delete v9.9.9 --cleanup-tag`. Ojo: compila con `go` suelto porque
está pensado para correr dentro del contenedor del job, así que a mano va
lanzado igual (`podman run ... $GO_IMAGE sh -c 'scripts/publicar-release.sh …'`).

Ahí hay una tercera trampa ya pagada, y **el orden de los remotos es parte de
la lógica, no un detalle**. `git remote` los devuelve alfabéticamente: `forgejo`
antes que `origin`. Pero Forgejo es quien dispara el CI al recibir el tag, y ese
trabajo publica la release **en GitHub**. Empujando a Forgejo primero, el job
arrancaba cuando GitHub aún no tenía el tag y la API contestaba 422
`Published releases must have a valid tag`. Se comió la v0.4.0.

Por eso `release.sh` empuja **el que dispara el CI el último**. `origin` lleva
las dos *pushurl* (GitHub y Forgejo, en ese orden), así que empujarlo primero
deja el tag en GitHub antes de que Forgejo se entere. Si algún día se añade otro
remoto que dispare CI, va detrás igual.

Y el atajo que parece obvio está minado: mandar **`target_commitish`** al crear
la release hace que GitHub cree el tag él mismo — pero lo crea **ligero**, y
cuando llega el push del tag anotado el ref ya existe con otro objeto y lo
rechaza, dejando los dos remotos con tags distintos para la misma versión. Pasó
en la v0.4.2 y hubo que reparar el ref a mano con un push forzado. El tag lo
crea siempre el push; `publicar-release.sh` solo **espera** a verlo y, si no
llega, falla diciéndolo.

El repositorio está en **dos remotos y los dos van siempre a la vez**:
`origin` (GitHub, público) y `forgejo`. `origin` tiene las dos URL de push
configuradas, así que un `git push` normal llega a ambos; `release.sh` además
comprueba que el tag ha aterrizado en los dos y falla si no.

**El Flatpak instalado se deja siempre al día.** No es algo que el usuario
tenga que pedir ni que haya que ofrecerle: si un cambio llega a `main`, se
publica y se reinstala, y se dice qué versión queda puesta. Tener el paquete
instalado por detrás del código ya le hizo perder tiempo una vez. `make release`
lo hace en la misma orden; comprobar al final con
`flatpak run io.github.jfrmorales.deckman --version`.

## No commitear sin permiso

Preferencia del usuario. Salvo lo que pida explícitamente, no se commitea ni se
empuja por iniciativa propia.
