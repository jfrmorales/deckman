# Arquitectura

Un solo binario Go que arranca un servidor web local y habla con la Steam Deck
por SSH. Sin dependencias en tiempo de ejecución: ni en el PC ni en la Deck.

```
  Navegador  ──HTTP──▶  deckman (tu PC)  ──SSH/SFTP──▶  Steam Deck
   (interfaz)            servidor local                  ~/.local/share/Steam
                              │                          ~/Games
                              └──HTTPS──▶  Steam Store · ProtonDB · SteamGridDB
```

Todo lo pesado lo hace el servidor: el navegador solo pinta. La interfaz va
**embebida en el binario** (`//go:embed web`), así que no hay ficheros sueltos
que instalar.

## Paquetes

```
cmd/deckman/          arranque, flags, abre el navegador
internal/config/      ajustes persistentes y clave SSH propia
internal/deck/        TODO lo que sabe de la Steam Deck y de Steam
internal/detect/      deducir qué juego hay en una carpeta (local, sin red)
internal/meta/        servicios externos (tienda, ProtonDB, SteamGridDB)
internal/server/      API HTTP + interfaz web embebida
```

Las dependencias van en un solo sentido: `server` → {`deck`, `detect`, `meta`,
`config`}. Los de abajo no se conocen entre sí. En particular **`deck` no
importa `meta`**: quien los junta es el servidor.

### `internal/deck` — el grueso

| Fichero | De qué se encarga |
|---|---|
| `client.go` | Conexión SSH+SFTP, ejecutar órdenes, escritura atómica con copia |
| `vdf.go` | KeyValues en texto (`libraryfolders.vdf`, `.acf`, `config.vdf`) |
| `shortcuts.go` | KeyValues **binario** (`shortcuts.vdf`) |
| `library.go` | Inventario: bibliotecas, juegos, Proton, espacio, ROMs |
| `transfer.go` | Subidas con progreso, alta de juegos no-Steam, Proton |
| `move.go` | Mover entre interno y microSD, desinstalar, reiniciar Steam |
| `artwork.go` | Carátulas como ficheros: nombres, listar, reemplazar, borrar |
| `cover.go` | Qué portada enseñar en la biblioteca, de las dos cachés de Steam |
| `cef.go` | Hablar con Steam **en caliente** por su depurador |

La convención de nombres de las carátulas vive **solo** en `artwork.go`. Se
duplicó una vez entre `deck` y `meta` y fue un error; `meta` decide qué imagen,
`deck` decide cómo se llama el fichero.

## Decisiones y por qué

**Go con la interfaz embebida.** El requisito era "lo menos complicado
posible" para Windows y Linux. Un binario sin dependencias lo cumple; una app
nativa habría añadido fricción de compilación cruzada, y cualquier cosa con
runtime habría obligado a instalar algo.

**SSH y SFTP en Go puro**, no llamando a `ssh`/`rsync`. Windows no trae
`rsync`, y depender del `ssh` del sistema habría hecho el comportamiento
distinto en cada máquina.

**Explorador de ficheros propio.** El selector del navegador nunca entrega la
ruta real de una carpeta, y para copiar un juego hace falta exactamente eso.

**Ventana de escritorio sin motor web propio.** En Windows la interfaz va en
una ventana WebView2 (`go-webview2`, Go puro, sin CGO: la compilación cruzada
del contenedor sigue funcionando; el runtime viene de serie en Windows 10/11).
En Linux un webview obligaría a CGO y a WebKitGTK, no garantizado en todas las
distros, así que la ventana es el **modo app** de cualquier navegador Chromium
(`--app=`, con perfil aparte para poder atar la vida de deckman a la de la
ventana; los navegadores Flatpak necesitan el perfil dentro de SU carpeta
privada o se quedan en un diálogo de error). Escalera completa en
`cmd/deckman/ui.go`: ventana nativa → modo app → pestaña. El servidor no sabe
nada de todo esto: la ventana es un cliente más.

**Escribir en la Deck es siempre reversible.** Antes de tocar un fichero de
configuración de Steam se deja una copia `.deckman.bak` al lado, y se escribe a
un temporal que se renombra encima (`PosixRename`). Un corte a mitad no deja a
Steam con un fichero incompleto.

**Con Steam abierto, se le pide a Steam.** Ver
[HALLAZGOS-STEAM.md](HALLAZGOS-STEAM.md) §1 y §4. Editar sus ficheros mientras
corre pierde datos.

## Operaciones largas

Copiar o mover un juego tarda minutos. El patrón:

1. El endpoint arranca un *job* y responde al momento con su id.
2. El trabajo corre en segundo plano y va emitiendo `deck.Progress`.
3. El navegador los recibe por **SSE** (`/api/events`).
4. `progressTracker` recuerda el último parte para que el mensaje final
   conserve los contadores.

Solo se admite **un trabajo a la vez**: dos transferencias por el mismo canal
SSH se estorban más que se ayudan.

## API HTTP

Escucha **solo en `127.0.0.1`**. Los `POST` exigen la cabecera `X-Deckman`, que
una web abierta en otra pestaña no puede mandar sin un *preflight* CORS que no
concedemos.

| Ruta | Para qué |
|---|---|
| `/api/state` | Estado: conexión, claves, trabajo en curso |
| `/api/connect`, `/api/disconnect` | Conectar (instala la clave SSH) |
| `/api/inventory` | Foto completa de la Deck |
| `/api/browse`, `/api/places` | Explorador de ficheros del PC |
| `/api/detect` | Qué juego hay en una carpeta |
| `/api/settings` | Clave de SteamGridDB |
| `/api/artwork/{games,list,current,apply,remove,size}` | Galería de carátulas |
| `/api/cover` | Miniatura de la portada de un juego, cacheada en memoria |
| `/api/send-game`, `/api/send-rom` | Enviar a la Deck |
| `/api/move`, `/api/delete`, `/api/compat` | Gestionar lo instalado |
| `/api/restart-steam`, `/api/cancel` | Acciones sueltas |
| `/api/events` | Progreso en vivo (SSE) |

Las descargas de imágenes solo se aceptan desde dominios de SteamGridDB: sin
ese filtro el servidor sería un proxy abierto hacia cualquier sitio.

## Pruebas

53 pruebas en tres niveles:

- **Locales**: parsers, nombres, detección. Usan muestras reales en `testdata/`
  (no versionadas: llevan identificadores de la cuenta de Steam). Sin ellas se
  saltan solas.
- **Contra una Deck** (`DECKMAN_TEST_HOST`): montan un árbol de Steam falso en
  `~/deckman-selftest` y lo borran al terminar. **No tocan la configuración
  real**, salvo las que por fuerza tienen que hacerlo (arte en caliente), que
  restauran lo que había.
- **Contra SteamGridDB** (`DECKMAN_TEST_GRIDKEY`): comprueban que la forma de
  las respuestas sigue siendo la esperada.

La prueba que más vale es `TestShortcutsRoundTrip`: lee un `shortcuts.vdf` real
y comprueba que se reescribe **byte a byte**. Si eso falla, cualquier otra cosa
que hagamos corrompe la configuración del usuario.

## Compilar

`./build.sh` compila para Linux y Windows dentro de un contenedor
(`golang:1.26.5`). **No hace falta Go instalado**, solo podman o docker. Fue una
petición explícita: no ensuciar el sistema.

`flatpak/build.sh` empaqueta el binario de Linux como Flatpak
(`io.github.jfrmorales.deckman`) usando `org.flatpak.Builder`, que es a su vez
un Flatpak: la misma regla de no instalar nada. El manifiesto no compila nada
(su `sdk` es la propia Platform de freedesktop); solo copia el binario, el
icono y los ficheros de escritorio. El sandbox tiene red y el disco en solo
lectura; la única escritura local de deckman es su configuración, que dentro
del sandbox va a `~/.var/app/<id>/config/deckman` (con migración automática
desde `~/.config/deckman` la primera vez, para conservar la clave SSH ya
instalada en la Deck).
