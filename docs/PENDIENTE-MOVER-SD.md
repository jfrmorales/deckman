# Mover juegos de Steam a la microSD con Steam abierto

Estado a 2026-11-15: **probado y corregido contra una Steam Deck real en modo juego**.

## El problema

Un usuario avisó de que **no se podían pasar juegos a la SD**. La causa:
`moveSteamGame` exigía Steam cerrado, y en **modo juego** eso es imposible,
porque `steam-launcher.service` vuelve a levantar Steam en cuanto se cierra.
Copiar con Steam abierto tampoco vale: Steam reescribe los `.acf` al salir y
deshace el cambio (ver `HALLAZGOS-STEAM.md`). La función quedaba inservible para
casi todo el mundo.

## Lo que se ha hecho y verificado

- **Steam abierto → se lo pide a Steam** (`deck.MoveAppLive`, en
  `internal/deck/cef.go`), por `SteamClient.InstallFolder`, que es lo mismo que
  usa Ajustes > Almacenamiento > Mover:
  1. `GetInstallFolders()` filtrando `bIsMounted !== false` para sacar el **índice**
     de la biblioteca de destino (la API no acepta rutas). El emparejamiento por
     ruta es `installFolderIndex`, con pruebas.
  2. `RegisterForMoveContentProgress` apunta el avance en
     `globalThis.__deckmanMove`, ignorando eventos previos de otros `appid` y
     entendiendo `eError === 20` como «en curso» y `eError === 0` (tras iniciar)
     como «terminado» (verificado en `chunk~2dcc5aaf7.js` y `sp.js` de Steam).
  3. `MoveInstallFolderForApp(appid, índice)`, sabiendo que su promesa resuelve
     al empezar y no al terminar.
- `moveSteamGameLive` (`internal/deck/move.go`) sondea cada 2 s. **Manda el
  disco**: el juego está movido cuando `appmanifest_<id>.acf` está en el destino
  y ya no en el origen. Falla con mensaje claro si Steam informa de un error
  (`isMoveError`: distinto de `0` y `20`), si se pierde el contacto 5 veces
  seguidas, si indica `finished` sin mover el manifiesto en 30 s, o si pasa
  15 min sin avanzar. Cancelar el trabajo llama a `CancelMove`.
- Verificado en una Deck real (`192.168.1.179`, modo juego, 2026-11-15) con
  `TestIntegrationMoveAppLiveRoundTrip`: traslado de ida a `/run/media/deck/LX512`
  y vuelta a `/home/deck/.local/share/Steam` con `Kao the Kangaroo (2000 re-release)`
  (`2360020`, 257 MB), además de restauración de `SteamWorld Dig` (`252410`).

## Lo que sigue pendiente de probar en hardware

1. Cancelar a medias un traslado en curso (`CancelMoveLive`) desde deckman y
   comprobar que Steam se detiene y el juego queda íntegro.
2. Con Steam cerrado (modo escritorio), la vía de copia propia (`rsync`).
3. Nota observada con `SteamWorld Dig` (`252410`): los juegos con script de
   desinstalación Proton (`iscriptevaluator.exe`) que tienen `compatdata`
   pueden recibir `eError: 39` de Steam al volver desde microSD al interno porque
   el evaluador escribe `config_info` en la biblioteca principal antes de mover
   la carpeta `compatdata` (documentado en `HALLAZGOS-STEAM.md` §4).
