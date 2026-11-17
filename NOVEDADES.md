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

## 17/11/2026 · Recordar el ejecutable elegido por carpeta (v0.8.1)

### Mejoras

- **deckman recuerda el ejecutable que elegiste a mano para cada juego**: si en una carpeta que trae versión de Windows y nativa de Linux (o varios ejecutables) cambias al binario de Linux al enviarlo, en los envíos siguientes para actualizar ese juego volverá a proponerlo directamente (con Proton en «Ninguno» si es nativo).

[IMG]https://raw.githubusercontent.com/jfrmorales/deckman/main/docs/capturas/enviar-ejecutable-recordado.webp[/IMG]

## 16/11/2026 · Enviar juegos nativos de Linux

### Novedades

- **«Enviar juego» ya no es solo para juegos de Windows**: deckman reconoce los ejecutables nativos de Linux aunque no lleven extensión (binarios, AppImage y scripts de arranque), como los de las recompilaciones que se publican con versión de Linux, y propone lanzarlos sin Proton. Antes, con una carpeta así, respondía que no encontraba ningún `.exe`.
- **El selector de ejecutable distingue Windows y Linux**: cada ejecutable lleva su etiqueta y, al elegir uno, la versión de Proton se ajusta sola: «Ninguno» para un nativo y Proton para un `.exe`.
- **Carpetas con las dos versiones**: se propone la de Windows con Proton, que no depende de las bibliotecas que traiga SteamOS, y se avisa de que la nativa está ahí por si prefieres usarla.

### Arreglos

- **Los ejecutables nativos llegaban sin permiso de ejecución** y Steam no conseguía arrancarlos. Ahora quedan listos para lanzar, también cuando la carpeta se envía desde un PC con Windows. Si ya habías enviado un juego así, basta con volver a enviarlo: no se copia de nuevo, solo se corrige el permiso.
- **Se copian los enlaces simbólicos de dentro de la carpeta del juego**, que es como muchos juegos nativos traen sus bibliotecas. Antes se saltaban y el juego no encontraba lo que necesitaba.

## 16/11/2026 · Vista previa de cambios, soporte para SteamOS y guía de conexión

### Novedades

- **Vista previa de cambios antes de tocar el equipo**: antes de enviar un juego, cambiar Proton, aplicar o quitar una carátula, mover un juego o gestionar ROMs, deckman muestra un resumen con los ficheros y carpetas exactos que se van a modificar (`shortcuts.vdf`, `config.vdf`, `grid/`, `~/Games/`, etc.), indicando qué se conserva y la copia de seguridad `.deckman.bak`.

[IMG]https://i.ibb.co/QvkdTJZG/preview-modal-es.png[/IMG]

### Mejoras

- **Textos adaptados a SteamOS**: cambiadas las referencias exclusivas a Steam Deck por **SteamOS** para quienes usan deckman también en Steam Machine u otros equipos con SteamOS.
- **Guía inicial más clara**: la pantalla de conexión y el README explican ahora paso a paso cómo asignar contraseña al usuario `deck` (desde Ajustes del sistema o `passwd` en Konsole) y dónde encontrar la dirección IP en los ajustes de red tanto en modo juego como en modo escritorio.

[IMG]https://i.ibb.co/SXDkqDFL/connect-es.png[/IMG]

## 15/11/2026 · Cola de movimientos e historial permanente de operaciones

### Novedades

- **Historial permanente de operaciones**: cada movimiento entre el almacenamiento interno y la microSD, envío de juego, envío/descarga de ROM o descarga de carátulas queda guardado en disco. Aunque cierres deckman o se corte la conexión y vuelvas otro día, el panel sigue ahí con un resumen claro de qué terminó, qué falló, qué quedó pendiente y en qué unidad está ahora cada juego.
- **Cola automática de movimientos (FIFO)**: ya puedes pulsar **Mover** en varios juegos seguidos; el primero empieza al instante y los demás quedan en espera (**En cola**) para ejecutarse automáticamente uno detrás de otro (con botón **Quitar** por si cambias de idea antes de que empiece).
- **Recuperación inteligente tras cortes**: al abrir deckman después de una interrupción, los movimientos sin terminar quedan marcados como **interrumpido**. En cuanto cargas la biblioteca de la Deck, comprueba automáticamente si Steam terminó alguno por su cuenta para marcarlo como completado, te indica dónde está cada juego y te deja relanzar todo lo pendiente con un clic (**Reanudar pendientes**) o fila a fila (**Reintentar**).

### Mejoras

- Comprobación de espacio libre en el destino también al poner juegos en cola y contador de pendientes visible en el modal de progreso.
- Botón **Limpiar historial** para despejar operaciones terminadas cuando ya no necesites consultarlas.

[IMG]https://raw.githubusercontent.com/jfrmorales/deckman/main/docs/capturas/historial-operaciones.webp[/IMG]

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
