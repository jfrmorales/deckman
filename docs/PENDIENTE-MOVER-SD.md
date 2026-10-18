# Pendiente: mover juegos de Steam a la microSD con Steam abierto

Estado a 2026-10-18. Escrito para retomarlo sin tener que reconstruir nada.

## El problema

Un usuario avisó de que **no se podían pasar juegos a la SD**. La causa:
`moveSteamGame` exigía Steam cerrado, y en **modo juego** eso es imposible,
porque `steam-launcher.service` vuelve a levantar Steam en cuanto se cierra.
Copiar con Steam abierto tampoco vale: Steam reescribe los `.acf` al salir y
deshace el cambio (ver `HALLAZGOS-STEAM.md`). La función quedaba inservible para
casi todo el mundo.

## Lo que se ha hecho

- **Steam abierto → se lo pide a Steam** (`deck.MoveAppLive`, en
  `internal/deck/cef.go`), por `SteamClient.InstallFolder`, que es lo mismo que
  usa Ajustes > Almacenamiento > Mover:
  1. `GetInstallFolders()` para sacar el **índice** de la biblioteca de destino
     (la API no acepta rutas). El emparejamiento por ruta es
     `installFolderIndex`, con pruebas.
  2. `RegisterForMoveContentProgress` apunta el avance en
     `globalThis.__deckmanMove`.
  3. `MoveInstallFolderForApp(appid, índice)`, sin esperar a la promesa.
- `moveSteamGameLive` (`internal/deck/move.go`) sondea cada 2 s. **Manda el
  disco**: el juego está movido cuando `appmanifest_<id>.acf` está en el destino
  y ya no en el origen. Falla con mensaje claro si Steam informa de un error,
  si se pierde el contacto 5 veces seguidas, si dice "terminado" y el manifiesto
  no aparece en 30 s, o si pasa 15 min sin avanzar. Cancelar el trabajo llama a
  `CancelMove`.
- **Steam cerrado → como antes** (copia con rsync dentro de la Deck).
- Si la API no existe en la versión de Steam de la Deck, se dice y se pide
  cerrar Steam; no se toca nada.
- Interfaz: el aviso del diálogo distingue los dos casos, y `moveTarget` ya no
  ofrece una unidad sin tamaño (desmontada).

## Lo que falta

**Nada de esto se ha probado contra una Deck.** Las firmas de
`SteamClient.InstallFolder` salen de los tipos de la comunidad de Decky, no de
Valve. Antes de publicar:

1. Con Steam abierto (modo juego), mover un juego **pequeño** del interno a la
   microSD y de vuelta. Comprobar:
   - que el índice elegido es el de la tarjeta;
   - que la barra avanza (si `strBytesMoved` viene vacío, se estima con
     `flProgress`; si tampoco, se queda en 0 hasta el final);
   - que el `.acf` acaba solo en el destino y el juego arranca;
   - qué pasa con `compatdata` y `shadercache` (los mueve Steam, no nosotros).
2. Cancelar a medias desde deckman y ver que Steam para y el juego sigue entero.
3. Con Steam cerrado (modo escritorio), la vía de copia, que tampoco estaba
   probada.
4. Si algo no cuadra con la API real, corregir `MoveAppLive`/`moveState` y
   apuntar lo averiguado en `HALLAZGOS-STEAM.md` §4.

Después: `make release` y reinstalar el Flatpak.
