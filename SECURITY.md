# Seguridad

## Informar de un fallo

Si has encontrado algo que compromete la seguridad de quien usa deckman,
escribe a **jfrmorales@outlook.com** con el asunto `deckman: seguridad`.

No lo abras como incidencia publica hasta que este corregido. Este es un
proyecto de una persona sin guardia de nadie: contesto en cuanto lo veo, y si
en dos semanas no has recibido respuesta, insiste — es que se ha perdido, no
que se este ignorando.

Cuenta que hace falta para reproducirlo. Si no estas seguro de si lo que has
visto cuenta como fallo de seguridad, mandalo igual.

## Que protege deckman y que no

Conviene saber donde estan los limites antes de decidir si algo es un fallo.

**El servidor solo escucha en 127.0.0.1.** No hay forma de abrirlo a la red, y
es deliberado: la interfaz puede borrar ficheros de la Deck y no lleva
contrasena. Ademas de escuchar solo en local, cada peticion de la API tiene que
llegar con la cabecera `X-Deckman` y con un `Host` que sea localhost. Lo
primero corta que una pagina cualquiera dispare ordenes desde otra pestaña; lo
segundo corta el *DNS rebinding*, que es la vuelta conocida a lo primero.

**La contraseña de la Deck no se guarda nunca.** Se usa una sola vez para
instalar una clave SSH, y a partir de ahi se conecta con la clave. La privada
vive en la carpeta de configuracion con permisos 0600.

**La clave de host de la Deck se verifica al estilo TOFU**: la primera vez se
acepta y se recuerda, y si despues cambia, deckman se planta y avisa en vez de
conectar. Reinstalar SteamOS cambia esa clave de forma legitima, asi que la
interfaz ofrece volver a confiar — pero es una decision de quien mira, no algo
que pase solo.

**Lo que deckman NO hace**: cifrar la carpeta de configuracion, aislar unas
Decks de otras (la clave SSH es una sola para todas, a proposito), ni protegerte
de otro programa que ya este corriendo con tu usuario en este PC. Quien tiene tu
usuario tiene tu clave; eso no es un fallo de deckman, es el modelo.

## Como se vigila

- `make audit` (y el CI en cada push) pasa **golangci-lint** con gosec y
  **govulncheck**, que avisa de las vulnerabilidades conocidas que el codigo
  realmente alcanza, no solo de las que hay en los modulos que arrastra.
- Las dependencias se revisan solas: ver `.forgejo/workflows/renovate.yml`.
- Los binarios de cada version llevan `SHA256SUMS` y firma **cosign** sin
  claves; como comprobarlos esta en el README.

## Versiones

Se corrige sobre la ultima version publicada. No hay ramas de soporte de
versiones viejas.
