# Novedades de deckman

Lo que va cambiando en [deckman](https://github.com/jfrmorales/deckman/releases/latest), contado para quien lo usa. Lo más reciente, arriba.

<!--
Cómo se mantiene este fichero:

- ⚠️ **Empujar una entrada a `main` del Forgejo (`utils/deckman`) la publica
  en el hilo de Forocoches** (t=10767169, f=43): el bumper (repo
  `forocoches-bumper`) lee este fichero por la API del Forgejo
  (`changelog_repo: utils/deckman/NOVEDADES.md`) y convierte en mensaje cada
  entrada que no ha anunciado. El hilo está en modo «news»: no se reflota por
  posición, sólo se escribe en él cuando aquí aparece una entrada nueva.
- Una entrada por versión o bloque de novedades, encima de la anterior, con la
  cabecera `## dd/mm/aaaa · Título corto`. La cabecera es su identidad:
  cambiarla en una entrada ya publicada la anuncia otra vez; corregir el texto
  de debajo, no.
- Escrito para quien usa deckman: qué cambia en la práctica, no detalles
  internos de código. Tres apartados como mucho: «Novedades», «Mejoras» y
  «Arreglos». Sin emoji: el foro no los aguanta.
-->

## 15/11/2026 · Mover juegos a la microSD sin cerrar Steam, y versión para macOS y ARM

### Novedades

- **Mover juegos de Steam a la microSD con Steam abierto.** Hasta ahora hacía
  falta cerrar Steam para pasar un juego a la tarjeta, y en modo juego eso no
  se puede porque la Deck lo vuelve a levantar sola. Ahora, si Steam está
  abierto, deckman le pide a Steam que mueva el juego (igual que Ajustes >
  Almacenamiento > Mover) y sigue el progreso en directo. Probado de ida y
  vuelta contra una Steam Deck real.
- **Binarios para macOS y ARM.** Además de Linux y Windows en x86_64, cada
  versión publica ahora ejecutables para macOS (Intel y Apple Silicon) y para
  Linux y Windows en ARM64. En macOS abre ventana propia usando Chrome, Edge,
  Brave, Chromium o Vivaldi si están en `/Applications`, y enseña los discos de
  `/Volumes`.
- **Se apaga solo al cerrar la pestaña.** Cuando se abre en una pestaña del
  navegador, deckman se cierra solo al minuto de cerrarla si no hay ningún
  trabajo en marcha, para no quedarse corriendo en segundo plano.

### Mejoras

- **Carátulas en caliente al instalar desde la galería.** Si Steam está abierto
  con CEF disponible, las portadas se aplican al momento también en el flujo de
  instalación guiada.
- **Subidas más seguras.** Los ficheros se suben primero como `.deckman.part` y
  se renombran al terminar, evitando archivos truncados si se corta la conexión
  a mitad y reanudas después.

### Arreglos

- **Unidades desmontadas.** Si tienes una microSD sacada o sin montar, ya no
  aparece en las barras de almacenamiento ni como destino al mover un juego.
- **Juegos no-Steam y carpetas compartidas.** Reconoce carpetas `Juegos` además
  de `Games` en cualquier unidad, y cuando un acceso directo apunta fuera de su
  carpeta de inicio (como `EmulationStationDE` lanzado desde `~/Applications`),
  no le suma el tamaño de esa carpeta ni ofrece borrarla al desinstalar.

Descarga en [GitHub Releases](https://github.com/jfrmorales/deckman/releases/latest).
