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

### Añadido

- **Las releases van firmadas de verdad.** La 0.6.0 y la 0.6.1 anunciaban la
  firma pero salieron sin ella: faltaba la clave, y el script lo dijo cada vez.
  Ahora existe, la crea `scripts/crear-clave-firma.sh` y **cosign no se instala
  en el sistema** — va en contenedor, como Go. Se comprueba así, y tiene que
  decir `Verified OK`:

  ```sh
  cosign verify-blob --key cosign.pub --bundle SHA256SUMS.bundle SHA256SUMS
  ```

  El fichero es `SHA256SUMS.bundle` y no `SHA256SUMS.sig` porque cosign 3
  deprecó la firma suelta: con ella, verificar se va a buscar el registro de
  transparencia y falla. Se probó antes de publicar nada, incluido que un
  `SHA256SUMS` retocado **no** pasa la verificación.

### Corregido

- **`make release` esperaba al fichero equivocado.** Antes de subir el
  `.flatpak` esperaba a ver el `SHA256SUMS` en la release, creyendo que era lo
  último que sube el CI; es lo primero, porque los ficheros se suben en orden
  alfabético y la `S` va antes que la `d`. No llegó a romper nada —ese fichero
  se genera antes de subir ninguno, así que ya listaba todo— pero la espera no
  esperaba a lo que decía. Ahora espera al binario de Windows, que sí es el
  último.

### Eliminado

- **El workflow de Renovate de este repositorio**, que no podía funcionar:
  lanzaba Renovate en un contenedor del runner, y desde ahí no se llega a la
  vez a Forgejo y a internet. Ya existía un Renovate montado en el host por ese
  mismo motivo; este repositorio se ha apuntado a esa lista, que era lo que
  había que hacer desde el principio. La configuración (`renovate.json5`) se
  queda: Renovate la lee del propio repositorio.

## [0.6.1] — 2026-08-04

### Cambiado

- **Fuera del repositorio los datos personales de quien lo mantiene.** Es
  público: un correo en texto plano ahí es justo lo que rastrean los
  recolectores de spam, y el dominio propio apuntaba a infraestructura que no
  pinta nada aquí. Nada de esto cambia el programa — solo dónde escribir si
  encuentras un fallo.
  - `SECURITY.md` ya no da un correo: los fallos de seguridad se informan por
    los **avisos privados de GitHub**, que además mantienen el informe
    reservado hasta que hay arreglo sin depender de que nadie se acuerde de no
    publicarlo.
  - Los commits del bot de dependencias van con un dominio `.invalid`
    (RFC 2606), que es lo que git pide y nadie lee.
  - Los comentarios del CI ya no citan rutas de la máquina que lo hospeda.

  La v0.6.0 salió con el correo dentro, así que en su etiqueta sigue estando;
  quien lo necesite, esta versión.

## [0.6.0] — 2026-08-04

### Corregido

- **Se comprueba la clave SSH de la Deck.** Hasta ahora deckman aceptaba
  cualquiera (`InsecureIgnoreHostKey`), con el razonamiento de que esto es una
  LAN doméstica y exigir un `known_hosts` impediría conectarse tras reinstalar
  SteamOS. La segunda mitad era cierta, pero la conclusión no: aceptar
  cualquier clave significa que **la contraseña de la Deck viaja hacia lo que
  conteste en esa IP**, sea la Deck o no.

  Ahora es TOFU, como el `ssh` de toda la vida pero sin el susto tipográfico:
  la primera conexión acepta y recuerda sin preguntar nada, y si después esa
  dirección presenta otra clave, deckman se planta y enseña las dos huellas.
  Reinstalar SteamOS la cambia de forma legítima, así que la interfaz ofrece
  volver a confiar — con la huella delante, y solo si el usuario lo dice.
  Olvidar una Deck se lleva también su clave recordada.

  Una línea ilegible en ese fichero (una escritura a medias, alguien
  editándolo) tumbaba la conexión a **todas** las Decks con un
  «invalid curve point» del que no se salía desde la interfaz. Salió probando
  contra una Deck de verdad: ahora se apartan solo las líneas rotas y las
  demás conservan su verificación.
- **Actualizada `golang.org/x/crypto` de v0.44.0 a v0.54.0.** `govulncheck`
  señalaba **cinco vulnerabilidades que este código llamaba de verdad** (con
  fichero y línea), entre ellas un bypass de la interacción física en llaves
  FIDO/U2F. Ninguna se había detectado porque nada las miraba; eso también se
  arregla en esta versión (ver *Añadido*).
- **Un `config.json` ilegible ya no se pierde.** Antes salía de `Load()` como
  «no había ninguna Deck», y el primer guardado de la sesión lo machacaba: con
  él se iban las Decks recordadas y `keyPath` — y sin `keyPath`, la clave que
  deckman instaló en la Deck se queda ahí sin forma de retirarla desde la
  interfaz. Ahora se aparta como `config.json.roto` y se empieza de cero.
- Errores que se tragaba el código en silencio y ahora se cuentan: no poder
  guardar la configuración al conectar (el síntoma era que deckman «olvidaba»
  la Deck entre arranques) y no poder corregir `keyPath` al migrar al Flatpak.

### Añadido

- **`make audit`**: `golangci-lint` (con `gosec`) y `govulncheck`, en
  contenedor como todo lo demás. Va aparte de `make check` y **no** es puerta
  para publicar: compara contra una base de datos que cambia sola, así que
  puede ponerse en rojo sin que nadie haya tocado nada. El CI lo pasa en cada
  push (`.forgejo/workflows/auditoria.yml`), que es donde uno quiere enterarse.
  Cada exclusión de `.golangci.yml` lleva escrito su porqué.
- **Renovate** (`.forgejo/workflows/renovate.yml`): abre pull requests cuando
  una dependencia se queda atrás, los lunes, y sin esperar para los avisos de
  seguridad. Dependabot no valía: el CI de este proyecto vive en un Forgejo
  propio a propósito.
- **Las releases van firmadas.** `SHA256SUMS` dice que lo descargado no viene
  corrupto; la firma `cosign` dice además de quién viene. Se firma desde el PC
  que publica y no desde el CI, para que la clave que firma no viva donde vive
  el token que publica. Cómo comprobarlo, en el README.
- **SBOM** (`deckman-<versión>.cdx.json`) en cada release: el inventario de lo
  que lleva dentro el binario, para poder contestar en un segundo si un aviso
  de seguridad afecta a la versión que alguien tiene instalada.
- **`SECURITY.md`**: dónde informar de un fallo de seguridad, y qué protege
  deckman y qué no.
- Pruebas del borde HTTP (`internal/server`, del 6% al 35% de cobertura). La
  principal **saca la lista de rutas del propio código**, así que una ruta
  nueva queda cubierta sin que nadie se acuerde de venir a añadirla: es
  exactamente la regresión que se coló cuando la cabecera propia solo se
  exigía en POST.

## [0.5.0] — 2026-08-04

### Añadido

- **deckman habla castellano, inglés y francés.** Por defecto sigue al idioma
  del navegador; hay un selector en la barra superior y la elección se guarda.
  No es solo la interfaz: **los mensajes de error del servidor también se
  traducen**, así que no se mezcla una pantalla en francés con un fallo en
  castellano.

  La clave del catálogo es **el texto en castellano**, no un identificador
  inventado: el código sigue diciendo lo que pasa sin ir a buscarlo a otro
  fichero. Si falta una traducción sale el castellano, nunca una clave cruda.

  Traducidos los 163 mensajes del servidor y las ~260 cadenas de la interfaz.
  Añadir un idioma es añadir un bloque a
  `internal/i18n/catalogo.go` y otro a
  `internal/server/web/i18n-catalogo.js`; las contribuciones son bienvenidas.
- **README en inglés y francés** (`README.en.md`, `README.fr.md`), enlazados
  entre sí desde la cabecera de los tres.

## [0.4.3] — 2026-08-04

### Corregido

- **Publicar deja de ser una carrera, ahora sí.** La causa no era la latencia de
  la API de GitHub, como decían la 0.4.1 y la 0.4.2: era el **orden de empuje**.
  `git remote` devuelve los remotos alfabéticamente, así que se empujaba a
  Forgejo antes que a GitHub; Forgejo dispara el CI al recibir el tag y ese
  trabajo publica la release en GitHub, que todavía no tenía el tag. Ahora el
  remoto que dispara el CI va el último, con lo que la carrera desaparece en vez
  de tolerarse.
- **El apaño de la 0.4.2 podía dejar los dos remotos con tags distintos.**
  Mandar `target_commitish` hacía que GitHub creara el tag él mismo, y lo creaba
  *ligero*: al llegar después el push del tag anotado, el ref ya existía con
  otro objeto y lo rechazaba. Pasó al publicar la propia v0.4.2, cuyo ref hubo
  que reparar a mano. El tag lo crea siempre el push; el CI solo espera a verlo
  y, si no llega, lo dice en vez de inventárselo.

### Nota

- La **v0.4.2 se publicó sin su `.flatpak`**: el fallo anterior cortó la
  publicación justo antes de construirlo. Los binarios de Linux y Windows sí
  están. Quien use el Flatpak, a esta versión.

## [0.4.2] — 2026-08-04

### Corregido

- **La carrera con GitHub al publicar se ataja de raíz.** La 0.4.1 la trataba
  reintentando; ahora no hay nada que reintentar: la release se crea mandando
  `target_commitish`, así que si la API de GitHub todavía no ve el tag lo crea
  ella en ese commit, y si lo ve ignora el campo. Comprobado publicando una
  release con un tag que no existía en ningún sitio, que es exactamente lo que
  tumbó a la v0.4.0.

## [0.4.1] — 2026-08-04

### Corregido

- **La caja de buscar en archive.org se quedaba en un dedal.** El desplegable
  que acotaba la búsqueda al sistema heredaba el `width: 100%` de los campos del
  formulario y, en una fila, se llevaba todo el ancho. Ahora ese ajuste es una
  casilla («Buscar en todos los sistemas, no solo en *psx*», con el nombre del
  sistema puesto), la caja recupera su ancho y los desplegables que vayan en
  fila ocupan solo lo suyo.
- **Publicar fallaba de vez en cuando por una carrera con GitHub.** El CI
  arranca en cuanto Forgejo recibe el tag, y para entonces GitHub puede no
  haberlo registrado todavía: la API contesta *«Published releases must have a
  valid tag»*, que parece un error de datos y es cuestión de segundos. Le pasó
  a la v0.4.0, cuya release hubo que crear a mano. Ahora se reintenta con
  espera creciente, y solo ante ese error concreto.

## [0.4.0] — 2026-08-04

### Añadido

- **Buscar carátulas**, y esta vez de verdad. Baja carátula, pantalla de título
  y captura de cada ROM desde **libretro-thumbnails** y las deja donde ES-DE las
  busca (`Emulation/tools/downloaded_media/<sistema>/`, siguiendo el enlace
  `media` que EmuDeck deja en cada carpeta, así que da igual dónde tengas la
  instalación). Sin clave ni registro. Empareja por el nombre del fichero, que
  es como indexa libretro: con volcados estilo No-Intro/Redump acierta casi
  siempre. Al terminar dice qué juegos se quedaron sin nada, que es lo único
  accionable del resultado.

  No toca el `gamelist.xml`: ES-DE encuentra las imágenes solo por el nombre,
  así que no hay motivo para reescribir un fichero del que es dueño y en el que
  guarda tus partidas jugadas.

  Lo que **no** trae es el texto (descripción, año, género, jugadores): eso
  está en ScreenScraper, que exige credenciales de desarrollador concedidas por
  ellos a cada aplicación. Se añadirá cuando deckman las tenga.

### Cambiado

- **«Gestionar» solo lista los sistemas que tienen ROMs.** EmuDeck crea 181
  carpetas de sistema al instalarse y en una Deck real solo cuatro tenían
  juegos: elegir entre 181 para llegar a 4 no es una lista, es un obstáculo.
  Cada uno viene con su número de juegos, y los alias de EmuDeck se
  deduplican (`gamecube` es un enlace a `gc`, no un sistema aparte). *Enviar
  ROM* y *Descargar* siguen ofreciendo todos: la primera ROM de un sistema va
  a una carpeta que aún está vacía.
- **La búsqueda de ROMs se acota al sistema elegido.** Teniendo puesto arcade,
  los resultados de PSP solo estorban. Un desplegable al lado permite abrirla a
  todos los sistemas cuando lo que quieres es ver qué hay y elegir.
- **Buscar carátulas deja de ser una subpestaña suelta y vive dentro de
  «Gestionar»**, debajo de la lista de ROMs. Se hace sobre el sistema que estás
  mirando, así que tenerlo aparte obligaba a elegir el sistema dos veces en dos
  sitios distintos.

### Corregido

- **«Gestionar» listaba como ROMs los ficheros de EmuDeck**, con su botón de
  *Eliminar* al lado: `systeminfo.txt`, `metadata.txt` y el enlace `media` —
  que apunta a la carpeta de carátulas del sistema, así que borrarlo se las
  llevaba todas por delante. Ahora solo se listan ficheros de verdad. Lo que no
  parece una ROM (una descarga a medias, una extensión rara) **sí** sigue
  saliendo: es justo lo que uno viene a limpiar.

## [0.3.0] — 2026-08-04

### Añadido

- **Gestor de la colección de ROMs** (Emulación → Gestionar): lista lo que hay
  en cada sistema con su tamaño, y permite renombrar y borrar. El servidor solo
  acepta sistema y nombre, nunca una ruta: la ruta la arma él a partir de la
  carpeta de ROMs que descubre en la Deck.
- **Descargas directas** (Emulación → Descargar): se le da una URL y la ROM la
  baja **la Deck**, no este PC, así que el fichero no cruza dos veces la red.
  Va como trabajo largo, con barra de progreso y botón de cancelar como el
  resto. El fichero se baja a un `.parcial` y solo se renombra si la descarga
  termina bien; si falla, no queda basura en la carpeta de ROMs.
- **Buscador en archive.org** dentro de esa misma subpestaña: busca en la
  sección de software, se queda con el fichero descargable más prometedor de
  cada resultado y rellena la casilla de la URL al elegir uno.

### Cambiado

- **Rediseño visual completo**: superficies traslúcidas, modo oscuro y
  tipografía Inter.
- La subpestaña **Scrapear sigue siendo un hueco** marcado como «Próximamente»:
  no hay scraper detrás, y un botón que diga «listo» sin haber mirado ni una
  carátula es peor que no tener el botón.

### Corregido

- La interfaz **ya no pide la tipografía a Google Fonts**. Va embebida en el
  binario y tiene que verse igual sin internet; de paso, arrancar deckman ya no
  avisa a nadie de fuera. El CSS pide Inter y cae en la del sistema si no está.

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

[No publicado]: https://github.com/jfrmorales/deckman/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/jfrmorales/deckman/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/jfrmorales/deckman/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/jfrmorales/deckman/compare/v0.4.3...v0.5.0
[0.4.3]: https://github.com/jfrmorales/deckman/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/jfrmorales/deckman/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/jfrmorales/deckman/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/jfrmorales/deckman/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/jfrmorales/deckman/compare/v0.2.5...v0.3.0
[0.2.5]: https://github.com/jfrmorales/deckman/compare/v0.2.4...v0.2.5
[0.2.4]: https://github.com/jfrmorales/deckman/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/jfrmorales/deckman/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/jfrmorales/deckman/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/jfrmorales/deckman/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/jfrmorales/deckman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jfrmorales/deckman/releases/tag/v0.1.0
