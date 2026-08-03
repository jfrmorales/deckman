# Contribuir a deckman

Se agradecen los informes de fallo y los parches. Esto es lo que conviene saber
antes de empezar.

## Antes de tocar código

Lee **[docs/HALLAZGOS-STEAM.md](docs/HALLAZGOS-STEAM.md)**. Recoge cómo funciona
Steam por dentro: nada de eso está documentado por Valve, se averiguó a mano
contra una Deck real, y son exactamente las trampas que han hecho fallar cosas.
Si vas a tocar carátulas, accesos directos o Proton, está ahí.

Dos reglas que no se saltan, porque saltárselas destruye datos de la gente:

- **Nunca escribas `shortcuts.vdf` con Steam abierto.** Steam lo tiene en
  memoria y lo reescribe al salir; hacerlo por detrás ya borró seis juegos
  no-Steam de un usuario. Con Steam abierto se usa su API
  (`deck.AddShortcutLive`); con Steam cerrado se puede editar el fichero. Lo
  decide `server.registerShortcut`.
- **Antes de escribir cualquier fichero de configuración de Steam**, deja copia
  (`WriteFileAtomic` ya lo hace) y comprueba que no pierdes datos
  (`checkNoShortcutsLost`).

[docs/ARQUITECTURA.md](docs/ARQUITECTURA.md) explica cómo está organizado el
código y por qué.

## Compilar y probar

Hace falta **podman o docker**, y nada más: el proyecto no instala Go en el
sistema, todo va en contenedor.

```sh
./build.sh    # binarios de Linux y Windows en dist/
./test.sh     # pruebas locales
```

Para las pruebas de integración necesitas una Steam Deck con SSH activado.
Copia `deck.local.env.ejemplo` a `deck.local.env`, rellena la IP y la
contraseña, y `./test.sh` ya las incluye. Ese fichero no se versiona.

Las de integración **no tocan la configuración real** de tu Deck: montan un
árbol de Steam de mentira en `~/deckman-selftest` y lo borran al terminar. Las
pocas que por fuerza tienen que tocar Steam (arte en caliente) restauran lo que
había.

Si tu cambio afecta a algo que solo se puede comprobar contra hardware real y
no has podido probarlo, **dilo en el pull request**. Vale más un «esto no lo he
probado» que un «debería funcionar».

## Estilo

- **Todo en castellano**: interfaz, comentarios, mensajes de error y
  documentación.
- Los comentarios explican **por qué**, no qué. Si algo parece arbitrario, es
  que hay una trampa detrás: cuéntala y di cómo se comprobó.
- Los mensajes de error se los lee una persona: que digan qué pasó y qué hacer.
- El código pasa por `gofmt` y `go vet` — `./test.sh` falla si no.

## Cambios y versiones

Anota lo que cambias en **[CHANGELOG.md](CHANGELOG.md)**, bajo `## [No
publicado]`, en la sección que toque (Añadido, Cambiado, Corregido, Eliminado).
Las versiones las publica quien mantiene el proyecto con `scripts/release.sh`.

## Licencia

deckman está bajo **GPL-3.0-or-later**. Al enviar un parche aceptas que se
distribuya bajo esa misma licencia.
