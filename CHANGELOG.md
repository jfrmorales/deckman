# Registro de cambios

Formato [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/), versiones
[SemVer](https://semver.org/lang/es/).

Cada versión publicada es un tag `vX.Y.Z` en git. La versión que lleva un
binario sale de ahí (`deckman --version`), y la que muestra el Flatpak sale del
bloque `<releases>` del metainfo. Los tres los sincroniza `scripts/release.sh`.

## [No publicado]

<!-- Los cambios se van anotando aquí; scripts/release.sh los mueve a la
     versión nueva al publicar. Secciones: Añadido, Cambiado, Corregido,
     Eliminado. -->

## [0.2.5] — 2026-08-04

### Cambiado

- **La pestaña «Enviar ROM» pasa a ser «Emulación»**, con cuatro tarjetas que
  hacen de subpestaña: *Enviar ROM* (lo de antes, igual de funcional),
  *Gestionar*, *Descargar* y *Scrapear*. Las tres últimas son de momento un
  hueco marcado como «Próximamente»: el sitio donde irán, para no volver a
  mover la interfaz cuando lleguen.

## [0.2.4] — 2026-08-03

### Cambiado

- **Escanear la biblioteca es mucho más rápido**: los manifiestos de los juegos
  se leen todos en una sola orden remota (antes, un viaje SFTP por juego), el
  `df` de las unidades va en una sola sesión SSH y los ficheros remotos se bajan
  por la ruta concurrente de SFTP en vez de a trozos de 32 KB. En bibliotecas
  grandes por wifi la diferencia son segundos.
- **Mover o borrar un juego arranca al instante**: reutilizan el inventario que
  la interfaz acaba de cargar (válido un minuto) en vez de repetir el escaneo
  completo, que incluía medir todos los `compatdata` y `shadercache` de la
  Deck. Las comprobaciones de seguridad (Steam abierto, depurador accesible,
  qué se puede borrar) se siguen haciendo frescas en el momento.
- **Menos viajes por SSH en general**: la raíz de Steam, la cuenta de usuario y
  la pestaña del depurador de Steam se memoizan durante la sesión (instalar las
  cuatro carátulas de un juego abría ~12 sesiones SSH; ahora las justas), las
  retransmisiones de juegos comprueban lo ya copiado con un listado por carpeta
  en vez de una consulta por fichero, y las cuatro búsquedas de carátulas en
  SteamGridDB van en paralelo.
- **La conexión con la Deck se mantiene viva** con un keepalive periódico: antes
  una sesión parada un rato la cortaba el router sin aviso y el fallo aparecía a
  mitad de la siguiente operación.
- **Compilar y publicar es más rápido y robusto**: `build.sh` ya no ejecuta
  `go mod tidy` (no exige red ni ensucia el árbol), usa un solo contenedor con
  las dos compilaciones en paralelo, y `make flatpak` ya no compila el binario
  de Windows que no empaqueta. La receta de compilación vive en un único sitio
  (`scripts/compilar.sh`) compartido por el build local, el CI y la release; el
  CI usa cachés persistentes de Go (antes recompilaba la stdlib entera en cada
  push) y comprueba antes de publicar un tag.

### Corregido

- `make build` tras `make clean` fallaba: `go build` no crea `dist/`.
- Publicar podía dejar el `SHA256SUMS` de la release sin la línea del
  `.flatpak` si `release.sh` ganaba la carrera al CI: ahora espera al fichero,
  no a la release, y avisa si no llega.
- Dos carreras de datos en el servidor web (la lista de Decks compartida entre
  handlers y los contadores de progreso leídos sin su cerrojo) y una goroutine
  que quedaba colgada al reabrir la ventana en Windows.
- `fetch-testdata.sh` corrompía `testdata/shortcuts.vdf` si la Deck tenía más
  de una cuenta de Steam (concatenaba los VDF); ahora elige la cuenta y se
  niega si hay varias.
- La opción `MaxPacket` de SFTP pedía el valor por defecto creyendo que
  aceleraba: eliminada junto a su comentario engañoso.

## [0.2.3] — 2026-08-03

### Añadido

- **El Flatpak también se publica**: cada versión lleva ahora un fichero
  `deckman-X.Y.Z.flatpak` en las *Releases*, que se instala con
  `flatpak install deckman-X.Y.Z.flatpak`. Hasta ahora la única forma de tener
  el Flatpak era clonar el repositorio y compilarlo. Lo sube la misma máquina
  que publica, porque el runner del CI no puede construir Flatpaks, y su suma
  se añade al `SHA256SUMS` de la release.

## [0.2.2] — 2026-08-03

### Añadido

- **Binarios descargables**: al publicar una versión, el CI compila deckman
  para Linux y Windows y lo sube a las *Releases* de GitHub, con las notas del
  changelog y un `SHA256SUMS` para comprobar lo que te bajas. Ya no hace falta
  compilar para usarlo.

### Cambiado

- Las comprobaciones automáticas pasan de GitHub Actions a **Forgejo Actions**,
  en un runner propio. El CI deja de depender de un servicio de terceros; el
  repositorio de GitHub sigue siendo la cara pública, pero ya no ejecuta nada.

## [0.2.1] — 2026-08-03

### Corregido

- **La ventana ya tiene su icono en Wayland.** Salía un círculo naranja con una
  W en la barra de título y en la de tareas. En Wayland la ventana no lleva
  icono propio: se lo manda el navegador al escritorio por
  `xdg-toplevel-icon`, sacándolo del favicon — y el nuestro era solo SVG, que
  no le sirve como origen para el icono de ventana. Ahora se declaran también
  PNG de 32, 48, 128 y 256 px. El favicon de la pestaña lo sigue tomando del
  SVG, así que no se pierde nitidez.

## [0.2.0] — 2026-08-03

### Añadido

- **Varias Steam Decks**: las que se van usando quedan guardadas y se eligen de
  una lista en la pantalla de conexión, con nombre opcional para distinguirlas.
- **Olvidar una Deck**, que además de quitarla de la lista **le retira la clave
  SSH** que deckman le instaló. Hasta ahora no había forma de revocar ese
  acceso desde la aplicación: había que editar `authorized_keys` a mano. Si la
  Deck no se puede alcanzar para revocarla, se avisa y se explica qué línea
  borrar, en vez de dar por hecho que se cortó el acceso.

### Cambiado

- Las comprobaciones automáticas usan `actions/checkout@v5` y
  `actions/setup-go@v6`: las anteriores avisaban de Node.js 20 obsoleto en cada
  ejecución.
- La configuración guarda ahora una lista de Decks. La de una sola Deck se
  migra sola al arrancar, conservando la clave SSH y la de SteamGridDB.
- **Una sola entrada para todo**: `make` lista lo que se puede hacer y
  `make setup` deja un clon recién hecho listo para trabajar (comprueba
  requisitos, crea `deck.local.env`, configura los remotos y pone un gancho de
  pre-push). Publicar es ahora `make release V=X.Y.Z`, que además reinstala el
  Flatpak para que no se quede desfasado, y deshace todo lo hecho si algo falla
  antes de publicar.

### Corregido

- Al retirar la clave SSH de una Deck ya no queda un
  `authorized_keys.deckman.bak` con la clave recién revocada dentro, y encima
  legible por cualquiera. La copia de seguridad se borra en cuanto se confirma
  que el fichero bueno quedó bien; para los ficheros de Steam se conserva, que
  ahí sí es una red de seguridad.

## [0.1.0] — 2026-08-03

Primera versión con registro. Recoge el estado del proyecto tal y como estaba
al publicarlo en GitHub y Forgejo.

### Añadido

- **Biblioteca**: juegos de Steam y no-Steam con portada, tamaño real,
  ubicación, y lo que ocupan el prefijo de Proton y la caché de shaders.
- **Enviar juego**: copia una carpeta de juego de Windows a `~/Games` de la Deck
  y lo registra en Steam con la versión de Proton que se elija.
- **Autodetección**: al elegir carpeta deduce qué juego es, cuál de los `.exe`
  lo arranca y qué Proton conviene.
- **Carátulas**: galería de SteamGridDB para portada, fondo, logo e icono, con
  vista previa, aplicadas en caliente.
- **Enviar ROM**: copia ROMs a la carpeta de sistema que toca de EmuDeck.
- **Mover**: traslada juegos entre el SSD interno y la microSD sin volver a
  descargarlos, incluidos los no-Steam (se lleva la carpeta y actualiza el
  acceso directo).
- **Limpiar**: desinstala juegos y borra por separado el prefijo de Proton o la
  caché de shaders.
- Un único ejecutable para Linux y Windows, sin nada que instalar en la Deck
  más allá de SSH.
- Empaquetado **Flatpak** para Linux, con migración automática de la
  configuración desde `~/.config/deckman`.
- Licencia **GPL-3.0-or-later**, guía de contribución y comprobación automática
  de formato, `vet`, pruebas y compilación cruzada en cada pull request.
- Este registro de cambios y `scripts/release.sh`, que publica una versión
  sincronizando changelog, metainfo del Flatpak y tag de git, y comprueba que
  llega a los dos remotos.

### Notas

Quedan vías sin probar contra hardware real: mover un juego **de Steam** entre
interno y microSD, desinstalar, la ventana WebView2 en un Windows real,
`RemoveShortcutLive`, `SetCompatToolLive` y `RelocateShortcut`. Están recogidas
en `CLAUDE.md`. Por eso 0.1.0 y no 1.0.0.

[No publicado]: https://github.com/jfrmorales/deckman/compare/v0.2.5...HEAD
[0.2.5]: https://github.com/jfrmorales/deckman/compare/v0.2.4...v0.2.5
[0.2.4]: https://github.com/jfrmorales/deckman/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/jfrmorales/deckman/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/jfrmorales/deckman/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/jfrmorales/deckman/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jfrmorales/deckman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jfrmorales/deckman/releases/tag/v0.1.0
