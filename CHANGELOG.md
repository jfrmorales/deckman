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

[No publicado]: https://github.com/jfrmorales/deckman/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/jfrmorales/deckman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jfrmorales/deckman/releases/tag/v0.1.0
