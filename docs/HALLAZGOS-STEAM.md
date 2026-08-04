# Cómo funciona Steam por dentro (lo que hubo que averiguar)

Nada de esto está documentado por Valve. Todo se comprobó a mano contra una
Steam Deck real (SteamOS 3.7, Steam de julio de 2026) y está respaldado por
pruebas en el repositorio. Se recoge aquí porque son las trampas que hicieron
fallar cosas, y sin ellas el código parece arbitrario.

---

## 1. `shortcuts.vdf`: Steam manda, y es peligroso

**Dónde**: `~/.local/share/Steam/userdata/<usuario>/config/shortcuts.vdf`

Es VDF **binario**, no texto. Gramática:

```
0x00 <clave NUL> ...hijos... 0x08   mapa
0x01 <clave NUL> <valor NUL>        cadena
0x02 <clave NUL> <int32 LE>         entero
```

El fichero entero es un mapa `shortcuts` cuyos hijos son `"0"`, `"1"`, `"2"`…
y termina con dos `0x08` (cierra el mapa y cierra la raíz).

### El appid de los juegos no-Steam es ALEATORIO

Se suele decir que es `CRC32(exe+nombre) | 0x80000000`. **Es falso para los que
crea Steam.** Contrastado contra 7 accesos directos reales: solo coincidía uno,
el que había creado EmuDeck. Los que añade Steam llevan un identificador al
azar.

Consecuencia: **el appid guardado manda siempre**. Recalcularlo hace que el
mapeo de Proton y las carátulas apunten a otro juego. Para emparejar un juego
que ya existe hay que comparar por **ejecutable**, no por appid.

Para los que creamos nosotros sí usamos el CRC, porque interesa que sea
determinista (reenviar el mismo juego actualiza en vez de duplicar). Steam
respeta el identificador que encuentre escrito.

### NUNCA escribir el fichero con Steam abierto

Steam mantiene la lista **en memoria** y reescribe `shortcuts.vdf` al salir.
Editarlo por detrás mientras Steam corre:

1. se pierde, porque Steam lo sobrescribe con su copia; y
2. puede dejar el fichero **con menos juegos de los que había**.

Pasó de verdad durante el desarrollo: se añadió un juego, Steam se reinició y
el fichero quedó con **1 entrada de 7**. Los juegos seguían en disco, pero
desaparecieron de la biblioteca.

**La vía correcta con Steam abierto es pedírselo a Steam** (ver §4). Con Steam
cerrado, el fichero es nuestro.

> Código: `deck.AddNonSteamGame` (fichero, solo con Steam cerrado),
> `deck.AddShortcutLive` (API), `server.registerShortcut` (elige),
> `deck.checkNoShortcutsLost` (red de seguridad).

---

## 2. Las carátulas se reconocen por la EXTENSIÓN del nombre

**Dónde**: `~/.local/share/Steam/userdata/<usuario>/config/grid/`

| Fichero | Qué es |
|---|---|
| `<appid>p.png` | portada vertical (la de la cuadrícula) |
| `<appid>.png` | portada horizontal ("reproducidos recientemente") |
| `<appid>_hero.png` | fondo de la ficha |
| `<appid>_logo.png` | logo sobre el fondo |
| `<appid>_icon.png` | icono pequeño |

Steam decide si un fichero es arte **por la extensión**, no por su contenido.
Un `.webp` lo ignora, aunque después sepa decodificarlo sin problema.

La prueba fue concluyente: el mismo fichero animado guardado como `.webp` no
aparecía; el plugin de SteamGridDB para Decky guardaba **los mismos bytes**
(MD5 idéntico) con nombre `.png` y sí se veía. De 175 ficheros de arte en esa
Deck, **ninguno** escrito por Steam o por el plugin era `.webp`.

Por eso todo se guarda como `.png`, `.jpg` o `.ico`, sin tocar el contenido.

**Cuidado con los duplicados**: si conviven `<appid>p.jpg` y `<appid>p.png`,
Steam elige uno de forma impredecible. Al cambiar un arte hay que borrar
**todos** los de ese tipo, sea cual sea su extensión.

**Y cuidado con lo que no es arte**: el plugin de Decky guarda ficheros
`<appid>.json` en esa misma carpeta. Comparten nombre con la portada
horizontal (que no lleva sufijo), así que un filtro ingenuo los toma por arte y
se los carga. Hay que mirar solo extensiones de imagen.

> Código: `deck.artSuffix`, `deck.steamExt`, `deck.isArtExt`, `deck.artworkFiles`.

---

## 2 bis. La portada que ya tiene un juego está en DOS sitios, y con dos formas

Para enseñar una miniatura en la biblioteca no basta con `grid/`: ahí solo está
lo que ha elegido el usuario. Lo que Steam se baja de la tienda vive aparte, y
ha cambiado de formato entre versiones. En la misma Deck conviven las dos:

```
appcache/librarycache/<appid>/library_600x900.jpg          173 juegos
appcache/librarycache/<appid>/<hash>/library_capsule.jpg    21 juegos
```

Los nombres de la variante nueva son `library_capsule.jpg` (vertical),
`library_header.jpg` y `header.jpg` (horizontales), colgando de una carpeta con
un hash. El mapa appid → hash está en `assetcache.vdf`, pero **no hace falta
leerlo**: basta con buscar a las dos profundidades.

Sin la variante nueva, juegos tan comunes como Rocket League o Tomb Raider
salían sin portada aunque Steam sí se la enseña.

**Y la caché no está completa**: solo guarda los juegos cuya ficha se ha abierto
alguna vez. Para el resto queda la portada de la tienda
(`cdn.cloudflare.steamstatic.com/steam/apps/<appid>/library_600x900.jpg`), que
la pide el navegador directamente. Los no-Steam no están en la tienda: ahí, si
no hay arte local, no hay portada.

> Código: `internal/deck/cover.go`, `steamCoverURL` en `app.js`.

---

## 2 ter. `StartDir` NO es la carpeta del juego

En un acceso directo, `StartDir` es la carpeta del **ejecutable**, que puede
estar varios niveles por debajo de la del juego. Comprobado en la Deck del
usuario:

| Juego | `StartDir` | Carpeta real |
|---|---|---|
| The Chronicles of Riddick | `…/Riddick/System/Win32_x86` (16 MB) | `…/Riddick` (11 GB) |
| Bills Must Be Paid | `…/Bills Must Be Paid/game` | `…/Bills Must Be Paid` |

Confundirlas cuesta caro: mover solo `System/Win32_x86` rompe el juego y deja
11 GB tirados, y borrarlo dice "liberados 16 MB" mientras el resto sigue
ocupando disco. El propio deckman guarda ahí la carpeta del `.exe` al enviar un
juego, así que se lo hace a sí mismo.

La única frontera fiable es el `Games` de cada unidad: si el juego cuelga de
uno, el **primer tramo** es el juego. Lo que no está ahí (emuladores, plugins,
lanzadores de Heroic) no se sabe dónde empieza, así que no se ofrece moverlo.

> Código: `deck.gameRootFor`, `deck.nonSteamDir`.

---

## 3. Steam lleva su propio registro del arte personalizado

**Dónde**: `~/.local/share/Steam/userdata/<usuario>/config/librarycache/<appid>.json`

Contiene una clave `customimage`. Escribir el fichero de imagen por detrás se
salta ese registro, y Steam puede acabar mostrando otra cosa o revirtiendo el
cambio. Usando su API lo escribe él, lo apunta y refresca la biblioteca.

---

## 4. Se le puede hablar a Steam en caliente (CEF)

La interfaz de Steam es Chromium empotrado. En la Deck arranca con
`--remote-debugging-port=8080` **escuchando solo en su localhost**, así que hay
que llegar por un túnel SSH.

```
GET http://127.0.0.1:8080/json   → lista de pestañas
```

La pestaña que interesa se titula **`SharedJSContext`**. Es la única con la API
completa: las demás (menús, avisos) llevan un `SteamClient` recortado sin
`SteamClient.Apps`. Desde ahí, por WebSocket y `Runtime.evaluate`:

### Carátulas

```js
SteamClient.Apps.SetCustomArtworkForApp(appid, base64, "png"|"jpg", assetType)
SteamClient.Apps.ClearCustomArtworkForApp(appid, assetType)
```

Los códigos de `assetType` **no están documentados**. Se sacaron llamando uno
por uno y mirando qué fichero aparecía:

| assetType | Fichero | Tipo |
|---|---|---|
| `0` | `<appid>p.png` | portada vertical |
| `1` | `<appid>_hero.png` | fondo |
| `2` | `<appid>_logo.png` | logo |
| `3` | `<appid>.png` | portada horizontal |
| `4` | — | **no hace nada** |

**Los iconos no pasan por esta API.** Ese sí hay que escribirlo como fichero, y
solo se ve tras reiniciar Steam.

Aguanta ficheros grandes: un fondo animado de 17 MB tarda ~3,6 s en base64.

### Accesos directos

```js
const appid = await SteamClient.Apps.AddShortcut(nombre, exe, dir, opciones)
await SteamClient.Apps.SetShortcutName(appid, nombre)
await SteamClient.Apps.SetShortcutStartDir(appid, dir)
await SteamClient.Apps.SetShortcutLaunchOptions(appid, opciones)
await SteamClient.Apps.SpecifyCompatTool(appid, "proton_experimental")
await SteamClient.Apps.RemoveShortcut(appid)
```

`AddShortcut` devuelve el appid (aleatorio) y **Steam persiste el fichero al
momento**, sin reiniciar.

`SetShortcutExe` + `SetShortcutStartDir` valen para **mover un juego no-Steam de
unidad con Steam abierto**: comprobado el 2026-08-03 llevando "Bills Must Be
Paid" al microSD y de vuelta. Steam escribió `shortcuts.vdf` al instante, mantuvo
el appid (y con él las carátulas y el Proton) y no perdió ninguno de los 9
accesos directos. Detalle: Steam guarda ahí el `Exe` **sin comillas**, al revés
que cuando lo escribe él solo; hay que aceptar las dos formas al leer.

Otros métodos vistos: `SetShortcutIcon`, `SetShortcutIsVR`,
`SetShortcutSortAs`, `GetShortcutDataForPath`, `CreateDesktopShortcutForApp`,
`InstallFlatpakAppAndCreateShortcut`, `GetAvailableCompatTools`.

> Código: `internal/deck/cef.go`.

---

## 5. Las versiones de Proton

Los Proton **oficiales** no tienen `compatibilitytool.vdf` — solo los
instalados a mano (GE-Proton, ULWGL). Se reconocen porque su carpeta en
`steamapps/common/` lleva un **`toolmanifest.vdf`**, igual que los
Steam Linux Runtime. Eso sirve además para no listarlos como si fueran juegos.

El nombre interno que hay que escribir en `config.vdf` se deriva del nombre
visible: se quita `"Proton "` y de la versión se elimina el punto, salvo que la
*minor* sea 0, en cuyo caso queda solo la *major*.

| Nombre visible | Identificador |
|---|---|
| Proton Experimental | `proton_experimental` |
| Proton Hotfix | `proton_hotfix` |
| Proton 4.2 | `proton_42` |
| Proton 6.3 | `proton_63` |
| Proton 9.0 (Beta) | `proton_9` |
| Proton 10.0 | `proton_10` |

Regla verificada contra un `config.vdf` real donde Steam tenía escritos
`proton_42`, `proton_8`, `proton_9` y `proton_experimental`.

**El prefijo de un juego no-Steam vive siempre en la biblioteca principal.**
Comprobado en una Deck con microSD dada de alta como biblioteca: los prefijos de
los siete juegos no-Steam estaban todos en
`~/.local/share/Steam/steamapps/compatdata`, aunque los juegos no. Por eso al
mover uno de unidad el prefijo **no** viaja: llevárselo dejaría las partidas
guardadas donde Steam no las busca.

El mapeo vive en `config.vdf`, en
`InstallConfigStore → Software → Valve → Steam → CompatToolMapping`.
Ojo: Steam no es consistente con las mayúsculas de esas claves (`Valve` /
`valve`), así que hay que buscarlas sin distinguirlas.

> Código: `deck.protonInternalName`, `deck.toolInstallDirs`, `deck.annotateCompat`.

---

## 6. Reiniciar Steam

En **modo juego** Steam corre bajo la unidad de usuario
`steam-launcher.service`. Su `ExecStop` mata al hijo del envoltorio, porque el
envoltorio no reenvía señales; sin eso el cierre se cuelga. Reiniciar la unidad
es limpio y Steam vuelve solo en unos 6 segundos:

```sh
systemctl --user restart steam-launcher.service
```

En **modo escritorio** esa unidad no existe y solo cabe `steam -shutdown`; hay
que reabrirlo a mano. Se distinguen con `systemctl --user is-active`.

> Código: `deck.RestartSteam`, `deck.SteamRestartMode`.

---

## 7. Identificar un juego desde su carpeta

Muchos repacks dejan el appid real dentro. Es la vía más fiable, es exacta y no
necesita red:

- `steam_appid.txt` — el número pelado
- `steam_api.ini`, `steam_api64.ini`, `steam_emu.ini`, `ColdClientLoader.ini` —
  línea `AppId=…` (un `AppId=0` es el valor por defecto, no vale)

Comprobado: Resident Evil 4 → `2050650`, Devil May Cry 5 → `601150`.

**Elegir el ejecutable** necesita lista negra, no basta con el mayor: en
Resident Evil 4 el segundo `.exe` más grande es `CrashReport.exe`, con 151 MB.
Fuera van desinstaladores, `vc_redist`, `dxwebsetup`, informes de fallos,
trainers, selectores de idioma y prerrequisitos de Unreal; y carpetas enteras
como `_Redist`, `_CommonRedist` o `_Bonus Content`.

> Código: `internal/detect/detect.go`.

---

## 8. Servicios externos: qué funciona hoy

| Servicio | Estado |
|---|---|
| `store.steampowered.com/api/storesearch` | Funciona, sin clave |
| `store.steampowered.com/api/appdetails` | Funciona, sin clave |
| `protondb.com/api/v1/reports/summaries/<appid>.json` | Funciona, sin clave |
| `steamgriddb.com/api/v2/…` | Funciona, **clave gratuita** |
| `api.steampowered.com/ISteamApps/GetAppList` | **Desaparecido** (404) |
| `steamcommunity.com/actions/SearchApps` | Funciona pero quisquilloso: devuelve vacío con nombres sucios |
| **steamdb.info** | **No se usa.** Sin API pública y sus términos no permiten rascarla |

**SteamGridDB y las animadas**: se sirven como `.webp`, pero su **miniatura es
un vídeo `.webm`**. Un `<img>` no puede pintarla — hay que usar `<video>`. Y
pesan: un fondo animado de 3840×1240 ronda los 45 MB, así que el límite de
descarga tiene que dejarlas pasar **y** hay que comprobar que llegan completas
(un webp truncado conserva la cabecera y parece válido).

Ojo con los títulos repetidos: "Resident Evil 4" son dos juegos distintos en
SteamGridDB (2005 y el remake de 2023), y la búsqueda devuelve antes el viejo.

---

## 9. Rendimiento: la trampa de `pkg/sftp`

`File.ReadFrom` solo usa su ruta de escritura concurrente si puede averiguar de
antemano el tamaño del origen, y para eso busca un `Len()`, un `Size()` o un
`Stat()` en el lector. Un envoltorio que los esconda lo deja en la ruta
secuencial.

Medido contra una Deck real, subiendo 40 MB:

| Variante | Velocidad |
|---|---|
| Envoltorio sin `Stat()` | 5,3 MB/s |
| Envoltorio **con `Stat()`** | 94,6 MB/s |
| `*os.File` directo | 78–101 MB/s |
| `io.Copy` | 41–65 MB/s |

**18 veces más lento** por esconder un método. Para un juego de 58 GB, 3 horas
frente a 10 minutos.

> Código: `deck.ctxReader` en `internal/deck/transfer.go`.

---

## 10. La biblioteca enseña los juegos no-Steam de TUS OTROS EQUIPOS

Steam sincroniza los accesos directos entre los ordenadores de la misma cuenta.
En la Deck aparecen también los del PC, y **no están en el `shortcuts.vdf` de la
Deck**: viven solo en la memoria de Steam, llegan de la nube.

Esto despista mucho: se envía un juego con deckman y en la Deck sale *dos veces*
(el que se acaba de copiar y el que ya estaba dado de alta en el PC), o aparece
uno que nadie ha creado ahí.

Se distinguen por `per_client_data` del `appStore`, comprobado en una Deck real:

```js
appStore.GetAppOverviewByAppID(2615821107).per_client_data
// [{clientid: "1043189624999430412", client_name: "bazzite", installed: true}]
```

- `clientid: "0"` (y `client_name` vacío) → el acceso directo es **de la Deck**,
  y está en su `shortcuts.vdf`.
- Un `clientid` con `client_name` → es de **otro equipo**. No se puede quitar
  desde la Deck; hay que borrarlo en el Steam de ese ordenador.

Cazado el 2026-08-03: los duplicados eran accesos directos que **Heroic Games
Launcher** había dado de alta en el PC (`Exe = "flatpak"`, `LaunchOptions =
run com.heroicgameslauncher.hgl ... heroic://launch?...&runner=sideload`). Uno
se llamaba literalmente `Title`: es Meltopia, que en Heroic tiene su título
bien puesto (`sideload_apps/library.json`), pero al que Heroic dio de alta en
Steam con su marcador de posición. Para saber qué juego es hay que casar el
`appName=` de las opciones de lanzamiento con el `app_name` de esa biblioteca.

> deckman no interviene: lee el `shortcuts.vdf` de la Deck, donde estas entradas
> no están. Si alguna vez hace falta listar lo que Steam tiene en memoria, el
> filtro es `app_type === 1073741824`.

### Ocultarlos: `collectionStore.SetAppsAsHidden`, y toma APPIDS

No hay ajuste por dispositivo (`settingsStore` no tiene ninguna clave de
biblioteca para esto). Lo único que hay es la colección `hidden`, y **se
sincroniza por la nube**: lo que ocultas en la Deck se oculta también en el PC.

```js
collectionStore.SetAppsAsHidden([2615821107, 2481320450], true);  // ocultar
collectionStore.BIsHidden(2615821107);                            // comprobar
```

Le entran **appids**, no las fichas del `appStore`. Pasarle las fichas revienta
con `TypeError: Cannot read properties of null (reading 'appid')`, porque por
dentro hace `e.map(id => GetAppOverviewByAppID(id))` y luego filtra por
`undefined`, no por `null`: el `null` que devuelve la búsqueda fallida se cuela
hasta `AddApps`. El mismo error sale si se le pasa un appid que no existe.

Queda guardado en `userdata/<id>/config/localconfig.vdf`, dentro de la entrada
`user-collections`; Steam lo vuelca a disco en unos segundos.

---

## 11. EmuDeck y ES-DE (esto no es Steam, pero pica igual)

Comprobado el 2026-08-04 contra una Deck real con EmuDeck instalado en la
microSD (`/run/media/deck/USD00/Emulation`).

### EmuDeck crea 181 carpetas de sistema, y ninguna está vacía

Al instalarse deja una carpeta por cada sistema que soporta —**181** en esa
Deck— tengas ese sistema o no. Y en cada una mete tres cosas:

```
roms/vic20/
├── media -> ../../tools/downloaded_media/vic20     (enlace)
├── metadata.txt
└── systeminfo.txt
```

Consecuencias, las dos pagadas:

- **Contar ficheros no sirve** para saber si un sistema tiene juegos: todos
  tienen dos. Hay que contar por **extensión de ROM**. Eso además descarta solo
  tres carpetas que sí tienen ficheros pero no son consolas: `emulators`
  (guiones `.sh`), `model2` y `xbox360` (el propio emulador: `.exe`, `.toml`,
  `.lua`; sus ROMs cuelgan de un `roms/` interno).
- **`media` es un enlace y `ReadDir` no lo llama directorio.** Se colaba en la
  lista de ROMs como si fuera un fichero, con su botón de *Eliminar* al lado —
  y borrarlo se lleva todas las carátulas del sistema. Filtrar por
  `Mode().IsRegular()`, no por `IsDir()`.

### Hay alias: `gamecube` es un enlace a `gc`

No son dos sistemas, es el mismo con dos nombres (mismo inodo). Sin saltarse
los enlaces, la misma carpeta sale dos veces y el usuario cree tener duplicados.

### Las carátulas NO van a `~/ES-DE/downloaded_media`

Van a `Emulation/tools/downloaded_media/<sistema>/`, y desde cada carpeta de
ROMs apunta ahí el enlace `media`. Lo fiable es **seguir el enlace**: si la
instalación está en la microSD, o el usuario la movió, el enlace lo sabe y una
ruta reconstruida a mano no.

Dentro hay una subcarpeta por tipo: `covers`, `titlescreens`, `screenshots`,
`marquees`, `miximages`, `3dboxes`, `backcovers`, `fanart`, `videos`, `wheel`…
El fichero se llama **igual que la ROM sin extensión**, con `.png`.

### Para poner una carátula NO hace falta tocar el `gamelist.xml`

ES-DE empareja las imágenes **por nombre de fichero**. El `gamelist.xml` (en
`~/ES-DE/gamelists/<sistema>/gamelist.xml`, ES-DE 3.x) es solo para el texto —
y ahí guarda además `playcount` y `lastplayed`, así que reescribirlo sin
conservarlos borra el historial de partidas. Si solo se bajan imágenes, no se
toca: menos superficie y ningún riesgo.

### libretro-thumbnails: carátulas sin clave ni registro

```
https://thumbnails.libretro.com/<Sistema largo>/Named_Boxarts/<Nombre>.png
                                                 Named_Titles/
                                                 Named_Snaps/
```

`<Sistema largo>` es el nombre con fabricante (`Nintendo - Nintendo 64`,
`Sony - PlayStation`), no el de EmuDeck. Indexa por el nombre exacto del
volcado No-Intro/Redump: sobre las 7 ROMs reales de la Deck de pruebas acertó
las 7, incluidos sufijos como `(EDC)` y `(Spain)`. Acepta el escapado de
`url.PathEscape` de Go (paréntesis como `%28`/`%29`).

### ScreenScraper exige credenciales de la aplicación, no del usuario

La alternativa con texto (descripción, año, género) es ScreenScraper, que usa
ES-DE. Su API pide **dos** juegos de credenciales, y sin las primeras no se
puede ni empezar:

```
sin devid        → "Erreur de login : Vérifier vos identifiants développeur !"
devid falso      → "Erreur de login : Vérifier les identifiants utilisateurs !"
```

Las de desarrollador las concede su equipo por el foro, aplicación por
aplicación, y solo a software gratuito. La cuenta de usuario es gratis (20 000
scrapes al día, 1 hilo). Es decir: el bloqueo no es dinero, es un trámite
humano que hay que hacer antes de escribir una línea de ese código.
