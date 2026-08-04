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
make audit    # análisis estático (golangci-lint) y vulnerabilidades conocidas
make build    # binarios de Linux y Windows en dist/
```

`make audit` va aparte de `make check` a propósito: compara contra una base de
datos de vulnerabilidades que cambia sola, así que puede ponerse en rojo sin
que nadie haya tocado nada. El CI la pasa en cada push (`auditoria.yml`), pero
no es puerta para publicar — el porqué largo está en `audit.sh`.

Lo que dice el linter está en `.golangci.yml`, con **una explicación por cada
exclusión**. Si algo te sale marcado y crees que no debería, cámbialo ahí y
cuenta por qué; una exclusión sin motivo escrito es la que nadie se atreve a
quitar después.

`make` a secas lista las órdenes con lo que hace cada una; no hay que
aprenderse ningún orden. Por debajo siguen estando los scripts de siempre
(`build.sh`, `test.sh`, `scripts/release.sh`, `flatpak/build.sh`), que se pueden
usar sueltos si hace falta.

`make setup` también instala un gancho de **pre-push** que corre `make check`
antes de empujar, para no tener que arreglar el CI en un segundo commit. Si
alguna vez estorba: `git push --no-verify`.

**Un aviso sobre los pull requests de GitHub:** las comprobaciones automáticas
corren en un Forgejo propio, no en GitHub Actions, así que en tu pull request no
verás la marca verde. No es que no se compruebe: quien mantiene el proyecto pasa
`make check` sobre el cambio. Antes de enviarlo, córrelo tú también — es
exactamente lo mismo que corre el CI.

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

Al llegar el tag a Forgejo, su CI compila los binarios de Linux y Windows y los
sube a las **Releases de GitHub** con las notas del changelog y un `SHA256SUMS`
(`.forgejo/workflows/publicar.yml`).

### El token de las releases (una sola vez)

Para que el CI pueda escribir en GitHub hace falta un secreto en el repositorio
de Forgejo:

1. En GitHub, **Settings → Developer settings → Personal access tokens →
   Fine-grained tokens → Generate new token**. Que solo alcance a
   `jfrmorales/deckman`, y en *Repository permissions* dale **Contents:
   Read and write**. Nada más.
2. En Forgejo, en el repositorio: **Settings → Actions → Secrets → Add secret**,
   con nombre **`GH_RELEASE_TOKEN`** y el token como valor.

No puede llamarse `GITHUB_...`: Forgejo reserva ese prefijo y rechaza el
secreto. Si el token falta, el job de publicar falla diciéndolo, pero el tag y
el código ya están publicados: se arregla poniendo el secreto y relanzando el
job, que reemplaza los ficheros en vez de duplicarlos.

Para probarlo sin publicar una versión de verdad, el script se puede ejecutar
suelto:

```sh
GH_TOKEN=... scripts/publicar-release.sh v9.9.9
```

### La clave de firma (una sola vez)

`SHA256SUMS` dice que lo descargado no viene corrupto. La firma dice además de
quién viene: sin ella, cualquiera con el token de arriba podría cambiar los
binarios **y** el `SHA256SUMS` a juego, y quien comprueba no notaría nada.

La firma la pone `scripts/release.sh` desde el PC que publica, no el CI. Son
dos motivos: el `SHA256SUMS` definitivo no existe hasta que esa máquina le añade
la línea del `.flatpak` (que el runner no puede construir), y la clave que firma
no tiene por qué vivir donde vive el token que publica — si no, quien se lleve
uno se lleva los dos.

```sh
scripts/crear-clave-firma.sh
```

Deja `cosign.key` (0600) y `cosign.pub` en la carpeta de configuración de
deckman, y a partir de ahí `release.sh` firma solo. **cosign no se instala**:
va en contenedor, como Go.

La clave se crea **sin contraseña** salvo que exportes `COSIGN_PASSWORD` antes.
Es una decisión, no un descuido: la alternativa es teclearla en cada
`make release`, y lo que pasa entonces es que acaba escrita en un script, que
es justo donde no querías tenerla. Queda en 0600 al lado de la clave SSH de
deckman, que ya vive así por lo mismo — quien tenga tu usuario en este PC tiene
las dos.

La pública se sube con cada release para que quien descarga tenga con qué
comprobar sin buscarla en ningún otro sitio. Mientras no exista la clave, se
publica sin firmar y el script lo dice.

Al firmar, cosign anota la firma en el **registro público de transparencia** de
Sigstore. Es lo que le pone fecha, y lo que se publica ahí es el resumen del
fichero y la clave pública — las dos cosas van ya dentro de la release.

**Nada de esto se versiona.** Si cambias de clave, dilo en el changelog: quien
guardara la anterior se encontraría una verificación que falla y no sabría si
es un problema suyo.

### El bot de dependencias

**Renovate** abre pull requests cuando una dependencia se queda atrás. Las
reglas de este repositorio están en `renovate.json5` (en `.json5` para poder
comentarlas), pero **aquí no hay ningún workflow que lo lance**, y eso es
deliberado.

Renovate necesita dos cosas a la vez: llegar a Forgejo por su red interna y
salir a internet para consultar versiones. Un contenedor del runner no tiene
las dos, así que corre en el host, disparado desde el repositorio de la
infraestructura, que ya lo hacía para sus propias imágenes. Este repositorio
solo está apuntado en su lista.

Si contribuyes, esto no te afecta: los PR de dependencias aparecen solos y se
revisan como cualquier otro. Si mantienes tu propio fork y quieres algo
parecido, monta Renovate donde tenga esas dos vías; no copies un
`container: renovate` en el runner, que es lo que no funciona.

## Licencia

deckman está bajo **GPL-3.0-or-later**. Al enviar un parche aceptas que se
distribuya bajo esa misma licencia.
