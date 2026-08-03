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
make setup    # una vez: comprueba requisitos y deja el clon listo
make          # dice todo lo que se puede hacer
make check    # gofmt + vet + pruebas locales (lo mismo que el CI)
make build    # binarios de Linux y Windows en dist/
```

`make` a secas lista las órdenes con lo que hace cada una; no hay que
aprenderse ningún orden. Por debajo siguen estando los scripts de siempre
(`build.sh`, `test.sh`, `scripts/release.sh`, `flatpak/build.sh`), que se pueden
usar sueltos si hace falta.

`make setup` también instala un gancho de **pre-push** que corre `make check`
antes de empujar, para no tener que arreglar el CI en un segundo commit. Si
alguna vez estorba: `git push --no-verify`.

Para las pruebas de integración necesitas una Steam Deck con SSH activado.
Rellena la IP y la contraseña en `deck.local.env` (lo crea `make setup`) y
corre `make deck`. Ese fichero no se versiona.

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
No es un trámite: `make release` se niega a publicar si esa sección está vacía.

Publicar es cosa de quien mantiene el proyecto, y es una sola orden:

```sh
make release V=0.2.0
```

Comprueba, mueve el changelog, actualiza el metainfo del Flatpak, etiqueta,
empuja a todos los remotos verificando que llega a cada uno, y reinstala el
Flatpak. **Si algo falla antes de publicar, lo deshace todo** y te deja como
estabas. Si el fallo ocurre cuando ya ha salido a algún remoto, no reescribe
historia publicada: te dice el estado de cada uno y qué comando falta.

## Licencia

deckman está bajo **GPL-3.0-or-later**. Al enviar un parche aceptas que se
distribuya bajo esa misma licencia.
